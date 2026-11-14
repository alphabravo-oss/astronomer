package identity

import (
	"context"
	"time"

	"github.com/alphabravo-oss/identity/pkg/rp"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const BrowserCookie = "__Host-astronomer_oidc"

// Browser constructs the shared PKCE/state handler only when SSO is requested.
func (s *Service) Browser(ctx context.Context) (*rp.Web, error) {
	if s.settings.Mode != "both" {
		return nil, ErrDisabled
	}
	result := s.initializing.DoChan("browser", func() (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.web != nil {
			return s.web, nil
		}
		if time.Now().Before(s.webRetryAfter) {
			return nil, ErrUnavailable
		}
		initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		web, err := rp.NewWeb(initCtx, rp.WebConfig{Config: rp.Config{Issuer: s.settings.Issuer, Audience: s.settings.ClientID, JWKSURL: s.settings.JWKSURL, RolesClaim: s.settings.RolesClaim, Product: "astronomer", HTTPClient: s.client}, ClientSecret: s.settings.ClientSecret, RedirectURI: s.settings.RedirectURI, AuthorizationURL: s.settings.AuthorizationURL, TokenURL: s.settings.TokenURL, CookieName: BrowserCookie, SuccessPath: "/dashboard/", LoginPath: "/auth/login"})
		if err != nil {
			s.webRetryAfter = time.Now().Add(5 * time.Second)
			return nil, ErrUnavailable
		}
		s.web = web
		return web, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(*rp.Web), nil
	}
}

func (s *Service) Enabled() bool  { return s != nil && s.settings.Mode == "both" }
func (s *Service) Issuer() string { return s.settings.Issuer }

// ValidateSession bypasses JWT positive caches: a removed binding is immediately
// ineffective, including for streams that validate Astronomer JWTs directly.
func (s *Service) ValidateSession(ctx context.Context, userID uuid.UUID, issuer, subject string, issuedAt time.Time) error {
	if !s.Enabled() || issuer != s.settings.Issuer {
		return ErrUnauthenticated
	}
	user, err := s.store.ResolveSharedIdentityUser(ctx, sqlc.ResolveSharedIdentityUserParams{Issuer: issuer, Subject: subject})
	if err != nil || user.ID != userID || !user.IsActive || user.IsService || (user.TokensInvalidatedAt.Valid && !issuedAt.After(user.TokensInvalidatedAt.Time)) {
		return ErrUnauthenticated
	}
	return nil
}
