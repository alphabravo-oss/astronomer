// Package identity adapts the shared relying party to product-owned accounts.
package identity

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/alphabravo-oss/identity/pkg/rp"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

var (
	ErrDisabled        = errors.New("optional SSO is disabled")
	ErrUnavailable     = errors.New("optional SSO is temporarily unavailable")
	ErrUnauthenticated = errors.New("external identity is not authorized")
)

type AccountStore interface {
	ResolveSharedIdentityUser(context.Context, sqlc.ResolveSharedIdentityUserParams) (sqlc.User, error)
}

// Service never performs network I/O at construction. An issuer outage cannot
// prevent the server starting or local authentication being available.
type Service struct {
	settings      config.IdentitySettings
	store         AccountStore
	client        *http.Client
	mu            sync.Mutex
	verifier      *rp.Verifier
	web           *rp.Web
	webRetryAfter time.Time
	retryAfter    time.Time
	initializing  singleflight.Group
}

func New(settings config.IdentitySettings, store AccountStore, client *http.Client) *Service {
	return &Service{settings: settings, store: store, client: client}
}

// Authenticate resolves every request against the current product account and
// binding. Deactivation and unlinking take effect without waiting for JWT expiry.
// Upstream roles never implicitly grant product administrator privileges.
func (s *Service) Authenticate(ctx context.Context, raw string) (sqlc.User, error) {
	if s.settings.Mode != "both" {
		return sqlc.User{}, ErrDisabled
	}
	if raw == "" || len(raw) > 32768 || s.store == nil {
		return sqlc.User{}, ErrUnauthenticated
	}
	v, err := s.getVerifier(ctx)
	if err != nil {
		return sqlc.User{}, err
	}
	external, err := v.Verify(raw)
	if err != nil || len(external.Subject()) > 255 {
		return sqlc.User{}, ErrUnauthenticated
	}
	user, err := s.store.ResolveSharedIdentityUser(ctx, sqlc.ResolveSharedIdentityUserParams{Issuer: s.settings.Issuer, Subject: external.Subject()})
	if err != nil || !user.IsActive || user.IsService {
		return sqlc.User{}, ErrUnauthenticated
	}
	if user.TokensInvalidatedAt.Valid {
		// Parse metadata only after the shared verifier has authenticated these
		// exact bytes. No unverified claim is used to select a key or account.
		var claims jwt.RegisteredClaims
		if _, _, err := jwt.NewParser().ParseUnverified(raw, &claims); err != nil || claims.IssuedAt == nil || !claims.IssuedAt.After(user.TokensInvalidatedAt.Time) {
			return sqlc.User{}, ErrUnauthenticated
		}
	}
	return user, nil
}

func (s *Service) getVerifier(ctx context.Context) (*rp.Verifier, error) {
	s.mu.Lock()
	v, retryAfter := s.verifier, s.retryAfter
	s.mu.Unlock()
	if v != nil {
		return v, nil
	}
	if time.Now().Before(retryAfter) {
		return nil, ErrUnavailable
	}
	result := s.initializing.DoChan("verifier", func() (any, error) {
		s.mu.Lock()
		v, retryAfter := s.verifier, s.retryAfter
		s.mu.Unlock()
		if v != nil {
			return v, nil
		}
		if time.Now().Before(retryAfter) {
			return nil, ErrUnavailable
		}
		// Initialization is shared but bounded independently of any one browser's
		// cancellation. Waiting callers can still cancel immediately below.
		initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v, err := rp.New(initCtx, rp.Config{Issuer: s.settings.Issuer, Audience: s.settings.ClientID, JWKSURL: s.settings.JWKSURL, RolesClaim: s.settings.RolesClaim, Product: "astronomer", HTTPClient: s.client})
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.retryAfter = time.Now().Add(5 * time.Second)
			return nil, ErrUnavailable
		}
		s.verifier = v
		return v, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-result:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*rp.Verifier), nil
	}
}
