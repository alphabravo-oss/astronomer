package handler

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

type recordingAuthAuditWriter struct {
	rows []sqlc.CreateAuditLogV1Params
}

func (w *recordingAuthAuditWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	w.rows = append(w.rows, arg)
	return nil
}

// authTestMutationTx is the transaction-bound credential store used by the
// auth HTTP tests. Keeping state and audit delegates on one object mirrors the
// production transaction contract without weakening the fail-closed handler.
type authTestMutationTx struct {
	users          UserQuerier
	tokens         TokenQuerier
	revocations    RevocationQuerier
	passwordResets PasswordResetStore
	audit          *recordingAuthAuditWriter
	audits         []sqlc.UpsertAuditOutboxParams
}

func (tx *authTestMutationTx) GetUserByIDForUpdate(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	if tx.users == nil {
		return sqlc.User{}, fmt.Errorf("auth test transaction: user store is not configured")
	}
	return tx.users.GetUserByID(ctx, id)
}

func (tx *authTestMutationTx) RecordFailedLoginAttempt(ctx context.Context, arg sqlc.RecordFailedLoginAttemptParams) (sqlc.User, error) {
	if recorder, ok := tx.users.(interface {
		RecordFailedLoginAttempt(context.Context, sqlc.RecordFailedLoginAttemptParams) (sqlc.User, error)
	}); ok {
		return recorder.RecordFailedLoginAttempt(ctx, arg)
	}
	if tx.users == nil {
		return sqlc.User{}, fmt.Errorf("auth test transaction: failed-login store is not configured")
	}
	user, err := tx.users.GetUserByID(ctx, arg.ID)
	if err != nil {
		return sqlc.User{}, err
	}
	user.FailedLoginCount++
	user.FailedLoginAt = arg.FailedLoginAt
	if user.FailedLoginCount >= arg.LockoutThreshold {
		user.LockedUntil = arg.LockedUntil
		user.LockedReason = arg.LockedReason
	}
	return user, nil
}

func (tx *authTestMutationTx) UpdateUserPasswordHash(context.Context, sqlc.UpdateUserPasswordHashParams) error {
	return fmt.Errorf("auth test transaction: password store is not configured")
}

func (tx *authTestMutationTx) ClearMustChangePassword(context.Context, uuid.UUID) error {
	return fmt.Errorf("auth test transaction: password store is not configured")
}

func (tx *authTestMutationTx) RevokeJWT(ctx context.Context, arg sqlc.RevokeJWTParams) error {
	if tx.revocations == nil {
		return fmt.Errorf("auth test transaction: revocation store is not configured")
	}
	return tx.revocations.RevokeJWT(ctx, arg)
}

func (tx *authTestMutationTx) InvalidateAllTokens(ctx context.Context, arg sqlc.InvalidateAllTokensParams) error {
	if tx.revocations == nil {
		return fmt.Errorf("auth test transaction: revocation store is not configured")
	}
	return tx.revocations.InvalidateAllTokens(ctx, arg)
}

func (tx *authTestMutationTx) ConsumePasswordResetToken(ctx context.Context, arg sqlc.ConsumePasswordResetTokenParams) (int64, error) {
	if tx.passwordResets == nil {
		return 0, fmt.Errorf("auth test transaction: password reset store is not configured")
	}
	return tx.passwordResets.ConsumePasswordResetToken(ctx, arg)
}

func (tx *authTestMutationTx) DeletePasswordResetTokensForUser(ctx context.Context, userID uuid.UUID) error {
	if tx.passwordResets == nil {
		return fmt.Errorf("auth test transaction: password reset store is not configured")
	}
	return tx.passwordResets.DeletePasswordResetTokensForUser(ctx, userID)
}

func (tx *authTestMutationTx) UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) error {
	if tx.passwordResets == nil {
		return fmt.Errorf("auth test transaction: password reset store is not configured")
	}
	return tx.passwordResets.UpdateUserPassword(ctx, arg)
}

func (tx *authTestMutationTx) CreateAPIToken(ctx context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error) {
	if tx.tokens == nil {
		return sqlc.ApiToken{}, fmt.Errorf("auth test transaction: token store is not configured")
	}
	return tx.tokens.CreateAPIToken(ctx, arg)
}

func (tx *authTestMutationTx) RevokeAPIToken(ctx context.Context, id uuid.UUID) error {
	if tx.tokens == nil {
		return fmt.Errorf("auth test transaction: token store is not configured")
	}
	return tx.tokens.RevokeAPIToken(ctx, id)
}

func (tx *authTestMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	tx.audits = append(tx.audits, arg)
	if tx.audit != nil {
		tx.audit.rows = append(tx.audit.rows, auditLogParamsFromOutbox(arg))
	}
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func wireAuthTestMutationTx(h *AuthHandler, tx *authTestMutationTx) {
	if tx.audit == nil {
		tx.audit = &recordingAuthAuditWriter{}
	}
	h.SetAuditWriter(tx.audit)
	h.SetRunTx(func(_ context.Context, fn func(AuthMutationTx) error) error {
		return fn(tx)
	})
}

// mockUserQuerier implements UserQuerier for testing.
type mockUserQuerier struct {
	users map[string]sqlc.User // keyed by email and username
}

func newMockQuerier(users ...sqlc.User) *mockUserQuerier {
	m := &mockUserQuerier{users: make(map[string]sqlc.User)}
	for _, u := range users {
		m.users["email:"+u.Email] = u
		m.users["username:"+u.Username] = u
	}
	return m
}

func (m *mockUserQuerier) GetUserByEmail(_ context.Context, email string) (sqlc.User, error) {
	u, ok := m.users["email:"+email]
	if !ok {
		return sqlc.User{}, fmt.Errorf("no rows in result set")
	}
	return u, nil
}

func (m *mockUserQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return sqlc.User{}, fmt.Errorf("no rows in result set")
}

func (m *mockUserQuerier) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	u, ok := m.users["username:"+username]
	if !ok {
		return sqlc.User{}, fmt.Errorf("no rows in result set")
	}
	return u, nil
}

func (m *mockUserQuerier) UpdateUserLastLogin(_ context.Context, _ uuid.UUID) error {
	return nil
}

func mustHashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	return string(hash)
}

func makeTestUser(t *testing.T, active bool) sqlc.User {
	t.Helper()
	return sqlc.User{
		ID:          uuid.New(),
		Email:       "test@example.com",
		Username:    "testuser",
		FirstName:   "Test",
		LastName:    "User",
		Password:    mustHashPassword(t, "testpassword"),
		IsActive:    active,
		IsStaff:     false,
		IsSuperuser: false,
		LastLogin:   pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
		DateJoined:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

type recordingCurrentUserRoles struct{ calls int }

func (r *recordingCurrentUserRoles) ListUserBindingsWithRoles(context.Context, pgtype.UUID) ([]sqlc.ListUserBindingsWithRolesRow, error) {
	r.calls++
	return nil, nil
}

func TestCurrentUserUsesResolvedSuperuserIdentityWithoutDatabaseReads(t *testing.T) {
	joined := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	lastLogin := joined.Add(time.Hour)
	userID := uuid.New()
	roles := &recordingCurrentUserRoles{}
	handler := NewAuthHandler(nil, auth.MustNewJWTManager("current-user-cache-test-secret", 60))
	handler.SetRoleBindings(roles)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me/", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{
		ID: userID.String(), Email: "admin@example.com", Username: "admin", AuthMethod: "jwt",
		FirstName: "Ada", LastName: "Lovelace", IsActive: true, IsStaff: true, IsSuperuser: true,
		MustChangePassword: false, DateJoined: joined, LastLogin: lastLogin, HasLastLogin: true, Resolved: true,
	}))
	recorder := httptest.NewRecorder()

	handler.CurrentUser(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if roles.calls != 0 {
		t.Fatalf("superuser role queries = %d, want 0", roles.calls)
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := response["data"].(map[string]any)
	if !ok || data["email"] != "admin@example.com" || data["is_superuser"] != true {
		t.Fatalf("resolved response = %#v", response)
	}
}

func TestLogin(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("test-secret-key-for-testing", 60)

	tests := []struct {
		name       string
		body       any
		users      []sqlc.User
		wantStatus int
		wantError  string // if non-empty, expect error response with this code
	}{
		{
			name:       "valid login with email",
			body:       LoginRequest{Email: "test@example.com", Password: "testpassword"},
			users:      []sqlc.User{makeTestUser(t, true)},
			wantStatus: http.StatusOK,
		},
		{
			name:       "rejects username login",
			body:       map[string]string{"username": "testuser", "password": "testpassword"},
			users:      []sqlc.User{makeTestUser(t, true)},
			wantStatus: http.StatusBadRequest,
			// A 400 "Email is required" is field validation, not an auth
			// challenge: legacy "missing_credentials" maps to ValidationError.
			wantError: apierror.ValidationError,
		},
		{
			name:       "rejects malformed email",
			body:       LoginRequest{Email: "testuser", Password: "testpassword"},
			users:      []sqlc.User{makeTestUser(t, true)},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid_email",
		},
		{
			name:       "wrong password",
			body:       LoginRequest{Email: "test@example.com", Password: "wrongpassword"},
			users:      []sqlc.User{makeTestUser(t, true)},
			wantStatus: http.StatusUnauthorized,
			// "invalid_credentials" was canonicalized to apierror.AuthenticationRequired.
			wantError: apierror.AuthenticationRequired,
		},
		{
			name:       "user not found",
			body:       LoginRequest{Email: "nobody@example.com", Password: "testpassword"},
			users:      []sqlc.User{},
			wantStatus: http.StatusUnauthorized,
			// "invalid_credentials" was canonicalized to apierror.AuthenticationRequired.
			wantError: apierror.AuthenticationRequired,
		},
		{
			name:       "inactive user",
			body:       LoginRequest{Email: "test@example.com", Password: "testpassword"},
			users:      []sqlc.User{makeTestUser(t, false)},
			wantStatus: http.StatusForbidden,
			wantError:  "account_disabled",
		},
		{
			name:       "missing credentials",
			body:       LoginRequest{Password: "testpassword"},
			users:      []sqlc.User{},
			wantStatus: http.StatusBadRequest,
			// A 400 "Email is required" is field validation, not an auth
			// challenge: legacy "missing_credentials" maps to ValidationError.
			wantError: apierror.ValidationError,
		},
		{
			name:       "invalid JSON body",
			body:       "not json{{{",
			users:      []sqlc.User{},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid_body",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockQuerier(tc.users...)
			handler := NewAuthHandler(mock, jwtMgr)
			wireAuthTestMutationTx(handler, &authTestMutationTx{users: mock})

			var bodyBytes []byte
			switch v := tc.body.(type) {
			case string:
				bodyBytes = []byte(v)
			default:
				var err error
				bodyBytes, err = json.Marshal(v)
				if err != nil {
					t.Fatalf("failed to marshal request body: %v", err)
				}
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.Login(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d; body: %s", tc.wantStatus, w.Code, w.Body.String())
			}

			if tc.wantError != "" {
				var body map[string]any
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				errObj, ok := body["error"].(map[string]any)
				if !ok {
					t.Fatalf("expected error response, got: %v", body)
				}
				if errObj["code"] != tc.wantError {
					t.Fatalf("expected error code %q, got %q", tc.wantError, errObj["code"])
				}
				return
			}

			// Success case: validate response structure
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			data, ok := body["data"].(map[string]any)
			if !ok {
				t.Fatalf("expected 'data' wrapper, got: %v", body)
			}

			if data["token"] == nil || data["token"] == "" {
				t.Fatal("expected non-empty token")
			}
			if data["refresh"] == nil || data["refresh"] == "" {
				t.Fatal("expected non-empty refresh token")
			}
			if !responseHasCookie(w.Result(), auth.SessionCookieName, true) {
				t.Fatalf("expected HttpOnly %s cookie", auth.SessionCookieName)
			}
			if !responseHasCookie(w.Result(), auth.RefreshCookieName, true) {
				t.Fatalf("expected HttpOnly %s cookie", auth.RefreshCookieName)
			}
			if !responseHasCookie(w.Result(), auth.CSRFCookieName, false) {
				t.Fatalf("expected readable %s cookie", auth.CSRFCookieName)
			}

			user, ok := data["user"].(map[string]any)
			if !ok {
				t.Fatalf("expected 'user' object in response, got: %v", data)
			}
			if user["email"] != "test@example.com" {
				t.Fatalf("expected email=test@example.com, got %v", user["email"])
			}
			if user["username"] != "testuser" {
				t.Fatalf("expected username=testuser, got %v", user["username"])
			}
			if user["first_name"] != "Test" {
				t.Fatalf("expected first_name=Test, got %v", user["first_name"])
			}
			if user["is_active"] != true {
				t.Fatalf("expected is_active=true, got %v", user["is_active"])
			}
		})
	}
}

func TestRefresh(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("test-secret-key-for-testing", 60)
	user := makeTestUser(t, true)

	t.Run("successful refresh writes audit", func(t *testing.T) {
		accessToken, refreshToken, err := jwtMgr.GenerateTokenPair(user.ID)
		if err != nil {
			t.Fatalf("generate token pair: %v", err)
		}
		if accessToken == "" || refreshToken == "" {
			t.Fatal("expected non-empty token pair")
		}

		handler := NewAuthHandler(newMockQuerier(user), jwtMgr)
		auditWriter := &recordingAuthAuditWriter{}
		handler.SetAuditWriter(auditWriter)

		body := fmt.Sprintf(`{"refresh":%q}`, refreshToken)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		handler.Refresh(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
		}
		if len(auditWriter.rows) != 1 {
			t.Fatalf("expected 1 audit row, got %d", len(auditWriter.rows))
		}
		if auditWriter.rows[0].Action != "auth.refresh" {
			t.Fatalf("action = %q, want auth.refresh", auditWriter.rows[0].Action)
		}
		if auditWriter.rows[0].ResourceID != user.ID.String() {
			t.Fatalf("resource_id = %q, want %q", auditWriter.rows[0].ResourceID, user.ID.String())
		}
		if !responseHasCookie(w.Result(), auth.SessionCookieName, true) {
			t.Fatalf("expected refreshed %s cookie", auth.SessionCookieName)
		}
		if !responseHasCookie(w.Result(), auth.RefreshCookieName, true) {
			t.Fatalf("expected refreshed %s cookie", auth.RefreshCookieName)
		}
	})

	t.Run("successful refresh can use HttpOnly refresh cookie", func(t *testing.T) {
		_, refreshToken, err := jwtMgr.GenerateTokenPair(user.ID)
		if err != nil {
			t.Fatalf("generate token pair: %v", err)
		}

		handler := NewAuthHandler(newMockQuerier(user), jwtMgr)
		handler.SetAuditWriter(&recordingAuthAuditWriter{})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh/", nil)
		req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: refreshToken})
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
		req.Header.Set("X-CSRF-Token", "csrf-token")

		w := httptest.NewRecorder()
		handler.Refresh(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
		}
		if !responseHasCookie(w.Result(), auth.SessionCookieName, true) {
			t.Fatalf("expected refreshed %s cookie", auth.SessionCookieName)
		}
	})

	t.Run("refresh cookie requires csrf", func(t *testing.T) {
		_, refreshToken, err := jwtMgr.GenerateTokenPair(user.ID)
		if err != nil {
			t.Fatalf("generate token pair: %v", err)
		}

		handler := NewAuthHandler(newMockQuerier(user), jwtMgr)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh/", nil)
		req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: refreshToken})

		w := httptest.NewRecorder()
		handler.Refresh(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d; body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid refresh token writes failure audit", func(t *testing.T) {
		handler := NewAuthHandler(newMockQuerier(user), jwtMgr)
		auditWriter := &recordingAuthAuditWriter{}
		handler.SetAuditWriter(auditWriter)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh/", strings.NewReader(`{"refresh":"bad-token"}`))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		handler.Refresh(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d; body: %s", w.Code, w.Body.String())
		}
		if len(auditWriter.rows) != 1 {
			t.Fatalf("expected 1 audit row, got %d", len(auditWriter.rows))
		}
		if auditWriter.rows[0].Action != "auth.refresh_failed" {
			t.Fatalf("action = %q, want auth.refresh_failed", auditWriter.rows[0].Action)
		}
	})
}

func TestBrowserSessionCookieAttributes(t *testing.T) {
	cases := []struct {
		name  string
		https bool
	}{
		{name: "plain http", https: false},
		{name: "https", https: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", nil)
			if tc.https {
				req.TLS = &tls.ConnectionState{}
			}
			rec := httptest.NewRecorder()
			setBrowserSessionCookies(rec, req, "access-token", "refresh-token")
			resp := rec.Result()

			session := cookieByName(t, resp, auth.SessionCookieName)
			assertCookieSecurity(t, session, cookieSecurityWant{
				value:    "access-token",
				path:     "/",
				httpOnly: true,
				secure:   tc.https,
				sameSite: http.SameSiteLaxMode,
			})
			if session.MaxAge != 0 {
				t.Fatalf("session MaxAge = %d, want session cookie MaxAge 0", session.MaxAge)
			}
			if session.Domain != "" {
				t.Fatalf("astronomer_session Domain = %q, want empty (host-only; do not widen to grafana.*)", session.Domain)
			}

			refresh := cookieByName(t, resp, auth.RefreshCookieName)
			assertCookieSecurity(t, refresh, cookieSecurityWant{
				value:    "refresh-token",
				path:     "/",
				httpOnly: true,
				secure:   tc.https,
				sameSite: http.SameSiteLaxMode,
				maxAge:   browserRefreshCookieMaxAge,
			})

			csrf := cookieByName(t, resp, auth.CSRFCookieName)
			assertCookieSecurity(t, csrf, cookieSecurityWant{
				value:    csrf.Value,
				path:     "/",
				httpOnly: false,
				secure:   tc.https,
				sameSite: http.SameSiteLaxMode,
			})
			if csrf.Value == "" {
				t.Fatal("csrf cookie value is empty")
			}
		})
	}
}

func TestClearBrowserSessionCookiesClearsAllSessionCookies(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	clearBrowserSessionCookies(rec, req)
	resp := rec.Result()

	for _, name := range []string{auth.SessionCookieName, auth.RefreshCookieName, auth.CSRFCookieName} {
		cookie := cookieByName(t, resp, name)
		assertCookieSecurity(t, cookie, cookieSecurityWant{
			value:    "",
			path:     "/",
			httpOnly: true,
			secure:   true,
			sameSite: http.SameSiteLaxMode,
			maxAge:   -1,
		})
	}
}

func responseHasCookie(resp *http.Response, name string, httpOnly bool) bool {
	for _, c := range resp.Cookies() {
		if c.Name == name && c.Value != "" && c.HttpOnly == httpOnly {
			return true
		}
	}
	return false
}

type cookieSecurityWant struct {
	value    string
	path     string
	httpOnly bool
	secure   bool
	sameSite http.SameSite
	maxAge   int
}

func cookieByName(t *testing.T, resp *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %s not found in %v", name, resp.Cookies())
	return nil
}

func assertCookieSecurity(t *testing.T, c *http.Cookie, want cookieSecurityWant) {
	t.Helper()
	if c.Value != want.value {
		t.Fatalf("%s value = %q, want %q", c.Name, c.Value, want.value)
	}
	if c.Path != want.path {
		t.Fatalf("%s path = %q, want %q", c.Name, c.Path, want.path)
	}
	if c.HttpOnly != want.httpOnly {
		t.Fatalf("%s HttpOnly = %v, want %v", c.Name, c.HttpOnly, want.httpOnly)
	}
	if c.Secure != want.secure {
		t.Fatalf("%s Secure = %v, want %v", c.Name, c.Secure, want.secure)
	}
	if c.SameSite != want.sameSite {
		t.Fatalf("%s SameSite = %v, want %v", c.Name, c.SameSite, want.sameSite)
	}
	if c.MaxAge != want.maxAge {
		t.Fatalf("%s MaxAge = %d, want %d", c.Name, c.MaxAge, want.maxAge)
	}
}

// --- Token CRUD Tests ---

// mockTokenQuerier implements TokenQuerier for testing.
type mockTokenQuerier struct {
	tokens    map[uuid.UUID]sqlc.ApiToken
	createErr error
	listErr   error
	countErr  error
	getErr    error
	revokeErr error
}

func newMockTokenQuerier() *mockTokenQuerier {
	return &mockTokenQuerier{
		tokens: make(map[uuid.UUID]sqlc.ApiToken),
	}
}

func (m *mockTokenQuerier) CreateAPIToken(_ context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error) {
	if m.createErr != nil {
		return sqlc.ApiToken{}, m.createErr
	}
	t := sqlc.ApiToken{
		ID:           uuid.New(),
		UserID:       arg.UserID,
		Name:         arg.Name,
		TokenHash:    arg.TokenHash,
		Prefix:       arg.Prefix,
		ExpiresAt:    arg.ExpiresAt,
		IsRevoked:    false,
		Scopes:       arg.Scopes,
		AllowedCidrs: arg.AllowedCidrs,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	m.tokens[t.ID] = t
	return t, nil
}

func (m *mockTokenQuerier) ListTokensByUser(_ context.Context, arg sqlc.ListTokensByUserParams) ([]sqlc.ApiToken, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []sqlc.ApiToken
	for _, t := range m.tokens {
		if t.UserID == arg.UserID && !t.IsRevoked {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *mockTokenQuerier) CountTokensByUser(_ context.Context, userID uuid.UUID) (int64, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	var count int64
	for _, t := range m.tokens {
		if t.UserID == userID && !t.IsRevoked {
			count++
		}
	}
	return count, nil
}

func (m *mockTokenQuerier) GetAPITokenByID(_ context.Context, id uuid.UUID) (sqlc.ApiToken, error) {
	if m.getErr != nil {
		return sqlc.ApiToken{}, m.getErr
	}
	t, ok := m.tokens[id]
	if !ok {
		return sqlc.ApiToken{}, fmt.Errorf("no rows in result set")
	}
	return t, nil
}

func (m *mockTokenQuerier) RevokeAPIToken(_ context.Context, id uuid.UUID) error {
	if m.revokeErr != nil {
		return m.revokeErr
	}
	t, ok := m.tokens[id]
	if !ok {
		return fmt.Errorf("no rows in result set")
	}
	t.IsRevoked = true
	m.tokens[id] = t
	return nil
}

// setAuthUser returns a request with reqctx.User in context.
func setAuthUser(r *http.Request, userID string) *http.Request {
	user := &reqctx.User{
		ID:         userID,
		AuthMethod: "jwt",
	}
	ctx := reqctx.WithUser(r.Context(), user)
	return r.WithContext(ctx)
}

func TestCreateToken(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()

	t.Run("successful creation", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)
		auditWriter := &recordingAuthAuditWriter{}
		handler.SetAuditWriter(auditWriter)
		wireAuthTestMutationTx(handler, &authTestMutationTx{tokens: tokenQ, audit: auditWriter})

		body := `{"name": "My Token", "expires_in_days": 90, "scopes": ["read"]}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = setAuthUser(req, userID.String())

		w := httptest.NewRecorder()
		handler.CreateToken(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected status 201, got %d; body: %s", w.Code, w.Body.String())
		}

		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		data, ok := resp["data"].(map[string]any)
		if !ok {
			t.Fatalf("expected 'data' wrapper, got: %v", resp)
		}

		token, _ := data["token"].(string)
		if !strings.HasPrefix(token, "astro_") {
			t.Fatalf("expected token to start with 'astro_', got %q", token)
		}

		if data["name"] != "My Token" {
			t.Fatalf("expected name 'My Token', got %v", data["name"])
		}

		prefix, _ := data["prefix"].(string)
		if len(prefix) != 12 {
			t.Fatalf("expected prefix length 12, got %d (%q)", len(prefix), prefix)
		}

		if data["expires_at"] == nil {
			t.Fatal("expected non-nil expires_at")
		}

		if data["id"] == nil || data["id"] == "" {
			t.Fatal("expected non-empty id")
		}
		if len(auditWriter.rows) != 1 {
			t.Fatalf("expected 1 audit row, got %d", len(auditWriter.rows))
		}
		if auditWriter.rows[0].Action != "auth.token.create" {
			t.Fatalf("action = %q, want auth.token.create", auditWriter.rows[0].Action)
		}
		if auditWriter.rows[0].ResourceType != "api_token" {
			t.Fatalf("resource_type = %q, want api_token", auditWriter.rows[0].ResourceType)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)

		body := `{"expires_in_days": 90}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = setAuthUser(req, userID.String())

		w := httptest.NewRecorder()
		handler.CreateToken(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", w.Code)
		}
	})

	t.Run("no auth", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)

		body := `{"name": "My Token"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		handler.CreateToken(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected status 401, got %d", w.Code)
		}
	})
}

func TestListTokens(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()

	t.Run("returns paginated list", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		// Pre-populate a token.
		tokenQ.tokens[uuid.New()] = sqlc.ApiToken{
			ID:        uuid.New(),
			UserID:    userID,
			Name:      "Test Token",
			Prefix:    "astro_abcdef",
			IsRevoked: false,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}

		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/tokens/", nil)
		req = setAuthUser(req, userID.String())

		w := httptest.NewRecorder()
		handler.ListTokens(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
		}

		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		data, ok := resp["data"].([]any)
		if !ok {
			t.Fatalf("expected 'data' array, got: %v", resp)
		}

		if len(data) != 1 {
			t.Fatalf("expected 1 token, got %d", len(data))
		}

		item := data[0].(map[string]any)
		if item["name"] != "Test Token" {
			t.Fatalf("expected name 'Test Token', got %v", item["name"])
		}

		// Verify plaintext token is NOT returned.
		if _, exists := item["token"]; exists {
			t.Fatal("plaintext token should not be in list response")
		}

		metadata, ok := resp["pagination"].(map[string]any)
		if !ok {
			t.Fatalf("missing pagination: %v", resp)
		}
		count, ok := metadata["total"].(float64)
		if !ok || count != 1 {
			t.Fatalf("expected pagination.total=1, got %v", metadata["total"])
		}
	})
}

func TestRevokeToken(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()

	t.Run("successful revoke", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		tokenID := uuid.New()
		tokenQ.tokens[tokenID] = sqlc.ApiToken{
			ID:        tokenID,
			UserID:    userID,
			Name:      "Test Token",
			IsRevoked: false,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}

		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)
		auditWriter := &recordingAuthAuditWriter{}
		handler.SetAuditWriter(auditWriter)
		wireAuthTestMutationTx(handler, &authTestMutationTx{tokens: tokenQ, audit: auditWriter})

		// Use chi router to inject URL params.
		r := chi.NewRouter()
		r.Delete("/api/v1/auth/tokens/{id}/", handler.RevokeToken)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/tokens/"+tokenID.String()+"/", nil)
		req = setAuthUser(req, userID.String())

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected status 204, got %d; body: %s", w.Code, w.Body.String())
		}

		// Verify the token is now revoked.
		if !tokenQ.tokens[tokenID].IsRevoked {
			t.Fatal("expected token to be revoked")
		}
		if len(auditWriter.rows) != 1 {
			t.Fatalf("expected 1 audit row, got %d", len(auditWriter.rows))
		}
		if auditWriter.rows[0].Action != "auth.token.revoke" {
			t.Fatalf("action = %q, want auth.token.revoke", auditWriter.rows[0].Action)
		}
		if auditWriter.rows[0].ResourceID != tokenID.String() {
			t.Fatalf("resource_id = %q, want %q", auditWriter.rows[0].ResourceID, tokenID.String())
		}
	})

	t.Run("cannot revoke other user token", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		tokenID := uuid.New()
		otherUserID := uuid.New()
		tokenQ.tokens[tokenID] = sqlc.ApiToken{
			ID:        tokenID,
			UserID:    otherUserID,
			Name:      "Other Token",
			IsRevoked: false,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}

		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)

		r := chi.NewRouter()
		r.Delete("/api/v1/auth/tokens/{id}/", handler.RevokeToken)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/tokens/"+tokenID.String()+"/", nil)
		req = setAuthUser(req, userID.String())

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d; body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid token ID", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)

		r := chi.NewRouter()
		r.Delete("/api/v1/auth/tokens/{id}/", handler.RevokeToken)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/tokens/not-a-uuid/", nil)
		req = setAuthUser(req, userID.String())

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d; body: %s", w.Code, w.Body.String())
		}
	})
}

func TestGenerateAPIToken(t *testing.T) {
	plaintext, hash, prefix, err := generateAPIToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(plaintext, "astro_") {
		t.Fatalf("expected plaintext to start with 'astro_', got %q", plaintext)
	}

	if len(hash) != 64 { // SHA-256 hex = 64 chars
		t.Fatalf("expected hash length 64, got %d", len(hash))
	}

	if prefix != plaintext[:12] {
		t.Fatalf("expected prefix %q, got %q", plaintext[:12], prefix)
	}

	// Verify uniqueness.
	plaintext2, _, _, err := generateAPIToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plaintext == plaintext2 {
		t.Fatal("expected unique tokens")
	}
}

// TestCreateToken_PersistsScopesAndCidrs covers the migration-044
// CreateToken path: the request's `scopes` and `allowed_cidrs` MUST
// land in the stored row + come back in the response so the CRUD UI
// can render them immediately.
func TestCreateToken_PersistsScopesAndCidrs(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()

	tokenQ := newMockTokenQuerier()
	handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)
	wireAuthTestMutationTx(handler, &authTestMutationTx{tokens: tokenQ})

	body := `{
		"name": "ci-deployer",
		"expires_in_days": 30,
		"scopes": ["clusters:write","read"],
		"allowed_cidrs": "10.0.0.0/8,192.168.1.5"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUser(req, userID.String())

	w := httptest.NewRecorder()
	handler.CreateToken(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d (body=%s)", w.Code, w.Body.String())
	}

	var envelope struct {
		Data CreateTokenResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	resp := envelope.Data
	if resp.AllowedCIDRs != "10.0.0.0/8,192.168.1.5" {
		t.Errorf("allowed_cidrs = %q, want round-trip of input", resp.AllowedCIDRs)
	}
	if len(resp.Scopes) != 2 || resp.Scopes[0] != "clusters:write" || resp.Scopes[1] != "read" {
		t.Errorf("scopes = %v, want [clusters:write read]", resp.Scopes)
	}

	// Check the stored row carries the same fields.
	if len(tokenQ.tokens) != 1 {
		t.Fatalf("expected exactly one stored token, got %d", len(tokenQ.tokens))
	}
	for _, stored := range tokenQ.tokens {
		if stored.AllowedCidrs != "10.0.0.0/8,192.168.1.5" {
			t.Errorf("stored allowed_cidrs = %q", stored.AllowedCidrs)
		}
		var scopes []string
		if err := json.Unmarshal(stored.Scopes, &scopes); err != nil {
			t.Fatalf("unmarshal stored scopes: %v", err)
		}
		if len(scopes) != 2 {
			t.Errorf("stored scopes = %v, want 2 entries", scopes)
		}
	}
}

// TestCreateToken_RejectsInvalidCIDR ensures a typo in allowed_cidrs
// fails the CREATE with a 400 rather than silently allowing every IP.
func TestCreateToken_RejectsInvalidCIDR(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()
	tokenQ := newMockTokenQuerier()
	handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)

	body := `{"name": "bad", "allowed_cidrs": "not-a-cidr"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUser(req, userID.String())

	w := httptest.NewRecorder()
	handler.CreateToken(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if len(tokenQ.tokens) != 0 {
		t.Errorf("invalid CIDR should not have persisted a row")
	}
}
