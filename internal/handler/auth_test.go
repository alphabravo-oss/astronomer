package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

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
