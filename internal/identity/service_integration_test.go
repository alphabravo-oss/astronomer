//go:build integration

package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestSharedIdentityTrustAndBinding(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := testdb.Pool(t)
	q := sqlc.New(pool)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	issuer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "key-1", Algorithm: "RS256", Use: "sig"}}}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(issuer.Close)
	settings := config.IdentitySettings{Mode: "both", Issuer: issuer.URL, ClientID: "astronomer", JWKSURL: issuer.URL + "/keys"}
	s := New(settings, q, issuer.Client())
	if fetches.Load() != 0 {
		t.Fatal("construction contacted issuer")
	}
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: "local@example.test", Username: "local", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	bind := sqlc.CreateSharedIdentityBindingParams{Issuer: issuer.URL, Subject: "external-subject", UserID: user.ID, LinkedBy: user.ID}
	if _, err := q.CreateSharedIdentityBinding(ctx, bind); err != nil {
		t.Fatal(err)
	}
	sign := identityTokenSigner(t, key, user.Email)
	expires := time.Now().Add(time.Minute)
	raw := sign(issuer.URL, "astronomer", bind.Subject, expires, jwt.SigningMethodRS256)
	assertConcurrentAuthentication(t, s, raw, user.ID)
	if fetches.Load() != 1 {
		t.Fatalf("concurrent initialization fetched %d times", fetches.Load())
	}
	assertRejectedTokens(t, s, issuer.URL, bind.Subject, sign)
	other, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: "other@example.test", Username: "other", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	conflict := bind
	conflict.UserID = other.ID
	if _, err := q.CreateSharedIdentityBinding(ctx, conflict); err == nil {
		t.Fatal("binding silently transferred")
	}
	// The same subject in another issuer is a distinct identity.
	conflict.Issuer += "/other-tenant"
	if _, err := q.CreateSharedIdentityBinding(ctx, conflict); err != nil {
		t.Fatal(err)
	}
	issuer.Close()
	if got, err := s.Authenticate(ctx, raw); err != nil || got.ID != user.ID {
		t.Fatalf("cached keys during issuer outage: %v", err)
	}
	assertAccountRevocation(t, pool, q, s, raw, bind)
	local := New(config.IdentitySettings{Mode: "local"}, q, issuer.Client())
	if _, err := local.Authenticate(ctx, raw); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	cold := New(settings, q, issuer.Client())
	if _, err := cold.Authenticate(ctx, raw); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cold outage: %v", err)
	}
	// Local account access remains independent of the failed optional issuer.
	if got, err := q.GetUserByID(context.Background(), user.ID); err != nil || !got.IsActive {
		t.Fatalf("local account unavailable: %v", err)
	}
}

func assertRejectedTokens(t *testing.T, s *Service, issuerURL, subject string, sign func(string, string, string, time.Time, jwt.SigningMethod) string) {
	t.Helper()
	ctx := t.Context()
	expires := time.Now().Add(time.Minute)
	for _, tc := range []struct {
		name, iss, aud, sub string
		expiry              time.Time
		method              jwt.SigningMethod
	}{
		{"issuer", issuerURL + "/other-tenant", "astronomer", subject, expires, jwt.SigningMethodRS256},
		{"audience", issuerURL, "other-product", subject, expires, jwt.SigningMethodRS256},
		{"email-is-not-link", issuerURL, "astronomer", "unknown-subject", expires, jwt.SigningMethodRS256},
		{"expired", issuerURL, "astronomer", subject, time.Now().Add(-time.Minute), jwt.SigningMethodRS256},
		{"algorithm", issuerURL, "astronomer", subject, expires, jwt.SigningMethodHS256},
	} {
		if _, err := s.Authenticate(ctx, sign(tc.iss, tc.aud, tc.sub, tc.expiry, tc.method)); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func assertAccountRevocation(t *testing.T, pool *pgxpool.Pool, q *sqlc.Queries, s *Service, raw string, bind sqlc.CreateSharedIdentityBindingParams) {
	t.Helper()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, "UPDATE users SET tokens_invalidated_at=now() WHERE id=$1", bind.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("force logout ignored: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET tokens_invalidated_at=NULL WHERE id=$1", bind.UserID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		column string
		value  bool
	}{{"is_active", false}, {"is_service", true}} {
		// Identifiers are fixed test cases; values remain bound parameters.
		if _, err := pool.Exec(ctx, "UPDATE users SET "+tc.column+"=$1 WHERE id=$2", tc.value, bind.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authenticate(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%s accepted: %v", tc.column, err)
		}
		if _, err := pool.Exec(ctx, "UPDATE users SET "+tc.column+"=$1 WHERE id=$2", !tc.value, bind.UserID); err != nil {
			t.Fatal(err)
		}
	}
	count, err := q.DeleteSharedIdentityBinding(ctx, sqlc.DeleteSharedIdentityBindingParams{Issuer: bind.Issuer, Subject: bind.Subject, UserID: uuid.New()})
	if err != nil || count != 0 {
		t.Fatalf("wrong account unlink: %d %v", count, err)
	}
	count, err = q.DeleteSharedIdentityBinding(ctx, sqlc.DeleteSharedIdentityBindingParams{Issuer: bind.Issuer, Subject: bind.Subject, UserID: bind.UserID})
	if err != nil || count != 1 {
		t.Fatalf("unlink: %d %v", count, err)
	}
	if _, err := s.Authenticate(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unlinked identity accepted: %v", err)
	}
}

func identityTokenSigner(t *testing.T, key *rsa.PrivateKey, email string) func(string, string, string, time.Time, jwt.SigningMethod) string {
	t.Helper()
	return func(iss, aud, subject string, exp time.Time, method jwt.SigningMethod) string {
		t.Helper()
		token := jwt.NewWithClaims(method, jwt.MapClaims{"iss": iss, "aud": aud, "sub": subject, "exp": exp.Unix(), "iat": time.Now().Add(-time.Second).Unix(), "email": email, "alphabravo_roles": map[string]any{"astronomer": []string{"admin"}}})
		token.Header["kid"] = "key-1"
		var signingKey any = key
		if method == jwt.SigningMethodHS256 {
			signingKey = []byte("not-a-trusted-key")
		}
		raw, err := token.SignedString(signingKey)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
}

func assertConcurrentAuthentication(t *testing.T, s *Service, raw string, userID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			got, err := s.Authenticate(ctx, raw)
			if err != nil || got.ID != userID || got.IsSuperuser || got.IsStaff {
				t.Errorf("authenticate: id=%s admin=%v err=%v", got.ID, got.IsSuperuser, err)
			}
		})
	}
	wg.Wait()
}
