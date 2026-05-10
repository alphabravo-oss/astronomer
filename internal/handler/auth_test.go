package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type recordingAuthAuditWriter struct {
	rows []sqlc.CreateAuditLogV1Params
}

func (w *recordingAuthAuditWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	w.rows = append(w.rows, arg)
	return nil
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

func TestLogin(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)

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
			name:       "valid login with username",
			body:       LoginRequest{Username: "testuser", Password: "testpassword"},
			users:      []sqlc.User{makeTestUser(t, true)},
			wantStatus: http.StatusOK,
		},
		{
			name:       "wrong password",
			body:       LoginRequest{Email: "test@example.com", Password: "wrongpassword"},
			users:      []sqlc.User{makeTestUser(t, true)},
			wantStatus: http.StatusUnauthorized,
			wantError:  "invalid_credentials",
		},
		{
			name:       "user not found",
			body:       LoginRequest{Email: "nobody@example.com", Password: "testpassword"},
			users:      []sqlc.User{},
			wantStatus: http.StatusUnauthorized,
			wantError:  "invalid_credentials",
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
			wantError:  "missing_credentials",
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
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
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
		ID:        uuid.New(),
		UserID:    arg.UserID,
		Name:      arg.Name,
		TokenHash: arg.TokenHash,
		Prefix:    arg.Prefix,
		ExpiresAt: arg.ExpiresAt,
		IsRevoked: false,
		Scopes:    arg.Scopes,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
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

// setAuthUser returns a request with middleware.AuthenticatedUser in context.
func setAuthUser(r *http.Request, userID string) *http.Request {
	user := &middleware.AuthenticatedUser{
		ID:         userID,
		AuthMethod: "jwt",
	}
	ctx := middleware.SetAuthenticatedUserForTest(r.Context(), user)
	return r.WithContext(ctx)
}

func TestCreateToken(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()

	t.Run("successful creation", func(t *testing.T) {
		tokenQ := newMockTokenQuerier()
		handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)
		auditWriter := &recordingAuthAuditWriter{}
		handler.SetAuditWriter(auditWriter)

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
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
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

		count, ok := resp["count"].(float64)
		if !ok || count != 1 {
			t.Fatalf("expected count=1, got %v", resp["count"])
		}
	})
}

func TestRevokeToken(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
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
