package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
)

const explicitSessionTTL = 10 * time.Minute

func newExplicitSessionJWT() *auth.JWTManager {
	manager := auth.MustNewJWTManager("session-timeout-path-test-secret", sessionpolicy.DefaultMinutes)
	manager.SetAccessTokenTTLProvider(func(ctx context.Context) time.Duration {
		if minutes, ok := sessionpolicy.MinutesFromContext(ctx); ok {
			return time.Duration(minutes) * time.Minute
		}
		return explicitSessionTTL
	})
	return manager
}

func requireSessionCookieTTL(t *testing.T, manager *auth.JWTManager, response *http.Response) {
	t.Helper()
	var access string
	for _, cookie := range response.Cookies() {
		if cookie.Name == auth.SessionCookieName {
			access = cookie.Value
			break
		}
	}
	if access == "" {
		t.Fatalf("response did not set %s", auth.SessionCookieName)
	}
	claims, err := manager.ValidateToken(access)
	if err != nil {
		t.Fatalf("ValidateToken(session cookie): %v", err)
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != explicitSessionTTL {
		t.Fatalf("access token absolute TTL = %s, want %s", got, explicitSessionTTL)
	}
}

func TestSessionTimeoutExplicitValueAcrossActualMintPaths(t *testing.T) {
	t.Run("password", func(t *testing.T) {
		manager := newExplicitSessionJWT()
		user := makeTestUser(t, true)
		users := newMockQuerier(user)
		h := NewAuthHandler(users, manager)
		wireAuthTestMutationTx(h, &authTestMutationTx{users: users})
		h.SetSessionTimeoutPolicy(func(context.Context) int { return 10 })
		body, _ := json.Marshal(LoginRequest{Email: user.Email, Password: "testpassword"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.Login(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Login status = %d; body=%s", w.Code, w.Body.String())
		}
		requireSessionCookieTTL(t, manager, w.Result())
	})

	t.Run("refresh", func(t *testing.T) {
		manager := newExplicitSessionJWT()
		user := makeTestUser(t, true)
		users := newMockQuerier(user)
		h := NewAuthHandler(users, manager)
		wireAuthTestMutationTx(h, &authTestMutationTx{users: users})
		h.SetSessionTimeoutPolicy(func(context.Context) int { return 10 })
		refresh, err := manager.GenerateRefreshToken(user.ID)
		if err != nil {
			t.Fatalf("GenerateRefreshToken: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh/", nil)
		req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: refresh})
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
		req.Header.Set("X-CSRF-Token", "csrf-token")
		w := httptest.NewRecorder()

		h.Refresh(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Refresh status = %d; body=%s", w.Code, w.Body.String())
		}
		requireSessionCookieTTL(t, manager, w.Result())
	})

	t.Run("sso callback", func(t *testing.T) {
		manager := newExplicitSessionJWT()
		user := makeTestUser(t, true)
		flow := &fakeTTLSSOFlow{info: &auth.SSOUserInfo{Email: user.Email, Username: user.Username, ConnectorID: "test"}}
		queries := &fakeTTLSSOQueries{user: user}
		h := &SSOHandler{
			manager:  flow,
			jwt:      manager,
			frontend: "/",
			now:      time.Now,
			states:   make(map[string]ssoState),
		}
		h.SetRunTx(func(ctx context.Context, fn func(SSOCallbackTx) error) error { return fn(queries) })
		h.SetEncryptor(newTestEncryptor(t))
		h.SetRBACCacheInvalidator(&ssoTestInvalidator{})
		state := "ttl-state"
		cookie, err := h.signStateCookie("test", state)
		if err != nil {
			t.Fatalf("signStateCookie: %v", err)
		}
		req := withProvider(httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/test/?code=ok&state="+state, nil), "test")
		req.AddCookie(&http.Cookie{Name: "astro_sso_state", Value: cookie})
		w := httptest.NewRecorder()

		h.Callback(w, req)

		if w.Code != http.StatusFound {
			t.Fatalf("Callback status = %d; body=%s", w.Code, w.Body.String())
		}
		requireSessionCookieTTL(t, manager, w.Result())
	})

	t.Run("totp completion", func(t *testing.T) {
		manager := newExplicitSessionJWT()
		user := makeTestUser(t, true)
		store := newFakeTOTPStore()
		encryptor := mustEncryptor(t)
		secret, _, err := auth.GenerateSecret(user.Username, "Astronomer")
		if err != nil {
			t.Fatalf("GenerateSecret: %v", err)
		}
		encrypted, err := encryptor.Encrypt(secret)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
			UserID:          user.ID,
			SecretEncrypted: encrypted,
			ConfirmedAt:     time.Now(),
		})
		codes, hashes, err := auth.GenerateRecoveryCodes(1)
		if err != nil {
			t.Fatalf("GenerateRecoveryCodes: %v", err)
		}
		_ = store.InsertRecoveryCode(context.Background(), sqlc.InsertRecoveryCodeParams{UserID: user.ID, CodeHash: hashes[0]})
		h := newTestTOTPHandler(store, newMockQuerier(user), encryptor, manager)
		challenge, err := manager.GeneratePurposeToken(user.ID, auth.PurposeTOTPChallenge, auth.TOTPChallengeTTL)
		if err != nil {
			t.Fatalf("GeneratePurposeToken: %v", err)
		}
		body, _ := json.Marshal(map[string]any{"challenge_token": challenge, "code": codes[0], "use_recovery": true})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/verify/", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.Verify(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Verify status = %d; body=%s", w.Code, w.Body.String())
		}
		requireSessionCookieTTL(t, manager, w.Result())
	})
}

type fakeTTLSSOFlow struct {
	info *auth.SSOUserInfo
}

func (f *fakeTTLSSOFlow) HasProvider(string) bool { return true }
func (f *fakeTTLSSOFlow) GetAuthorizationURL(string) (string, string, error) {
	return "https://idp.example.test", "state", nil
}
func (f *fakeTTLSSOFlow) HandleCallback(context.Context, string, string, string) (*auth.SSOUserInfo, error) {
	return f.info, nil
}

type fakeTTLSSOQueries struct {
	user      sqlc.User
	claimUser sqlc.User
	claimErr  error
}

func (f *fakeTTLSSOQueries) GetUserByEmail(context.Context, string) (sqlc.User, error) {
	return f.user, nil
}
func (f *fakeTTLSSOQueries) GetUserByUsername(context.Context, string) (sqlc.User, error) {
	return f.user, nil
}
func (f *fakeTTLSSOQueries) CreateUser(context.Context, sqlc.CreateUserParams) (sqlc.User, error) {
	return sqlc.User{}, errors.New("unexpected CreateUser")
}
func (f *fakeTTLSSOQueries) UpdateUserLastLogin(context.Context, uuid.UUID) error { return nil }
func (f *fakeTTLSSOQueries) GetDexConnectorByName(context.Context, string) (sqlc.DexConnector, error) {
	return sqlc.DexConnector{ID: uuid.New(), Enabled: true}, nil
}
func (f *fakeTTLSSOQueries) ClaimExternalPrincipal(context.Context, sqlc.ClaimExternalPrincipalParams) (sqlc.User, error) {
	if f.claimErr != nil {
		return sqlc.User{}, f.claimErr
	}
	if f.claimUser.ID != uuid.Nil {
		return f.claimUser, nil
	}
	return sqlc.User{}, pgx.ErrNoRows
}
func (f *fakeTTLSSOQueries) UpsertUserIDPGroups(context.Context, sqlc.UpsertUserIDPGroupsParams) (sqlc.UserIdpGroup, error) {
	return sqlc.UserIdpGroup{}, nil
}
func (f *fakeTTLSSOQueries) GetUserIDPGroups(context.Context, uuid.UUID) (sqlc.UserIdpGroup, error) {
	return sqlc.UserIdpGroup{}, nil
}
func (f *fakeTTLSSOQueries) ListGroupMappingsForConnector(context.Context, pgtype.UUID) ([]sqlc.IdentityGroupMapping, error) {
	return nil, nil
}
func (f *fakeTTLSSOQueries) ListGroupSyncGlobalBindingsForConnector(context.Context, sqlc.ListGroupSyncGlobalBindingsForConnectorParams) ([]sqlc.GlobalRoleBinding, error) {
	return nil, nil
}
func (f *fakeTTLSSOQueries) ListGroupSyncClusterBindingsForConnector(context.Context, sqlc.ListGroupSyncClusterBindingsForConnectorParams) ([]sqlc.ClusterRoleBinding, error) {
	return nil, nil
}
func (f *fakeTTLSSOQueries) ListGroupSyncProjectBindingsForConnector(context.Context, sqlc.ListGroupSyncProjectBindingsForConnectorParams) ([]sqlc.ProjectRoleBinding, error) {
	return nil, nil
}
func (f *fakeTTLSSOQueries) CreateGroupSyncGlobalBindingForConnector(context.Context, sqlc.CreateGroupSyncGlobalBindingForConnectorParams) (sqlc.GlobalRoleBinding, error) {
	return sqlc.GlobalRoleBinding{}, nil
}
func (f *fakeTTLSSOQueries) CreateGroupSyncClusterBindingForConnector(context.Context, sqlc.CreateGroupSyncClusterBindingForConnectorParams) (sqlc.ClusterRoleBinding, error) {
	return sqlc.ClusterRoleBinding{}, nil
}
func (f *fakeTTLSSOQueries) CreateGroupSyncProjectBindingForConnector(context.Context, sqlc.CreateGroupSyncProjectBindingForConnectorParams) (sqlc.ProjectRoleBinding, error) {
	return sqlc.ProjectRoleBinding{}, nil
}
func (f *fakeTTLSSOQueries) DeleteGroupSyncGlobalBinding(context.Context, uuid.UUID) error {
	return nil
}
func (f *fakeTTLSSOQueries) DeleteGroupSyncClusterBinding(context.Context, uuid.UUID) error {
	return nil
}
func (f *fakeTTLSSOQueries) DeleteGroupSyncProjectBinding(context.Context, uuid.UUID) error {
	return nil
}

var _ SSOQuerier = (*fakeTTLSSOQueries)(nil)
