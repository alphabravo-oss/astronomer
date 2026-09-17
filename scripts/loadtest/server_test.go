package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func TestBootstrapAdminAPITokenUsesCookieSessionAndRevokes(t *testing.T) {
	const (
		wantEmail    = "admin@example.com"
		wantPassword = "local-password"
		wantToken    = "astro_ephemeral-loadtest-token"
		wantSession  = "signed-session"
		wantCSRF     = "csrf-value"
	)
	tokenID := uuid.New()
	var mu sync.Mutex
	revokeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login/":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["email"] != wantEmail || body["password"] != wantPassword {
				t.Errorf("login body was not the supplied credential")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: auth.SessionCookieName, Value: wantSession, Path: "/", HttpOnly: true})
			http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookieName, Value: wantCSRF, Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"user":{"email":"admin@example.com"}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/tokens/":
			session, err := r.Cookie(auth.SessionCookieName)
			if err != nil || session.Value != wantSession || r.Header.Get("X-CSRF-Token") != wantCSRF {
				t.Error("token exchange did not use the CSRF-bound session cookie")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body struct {
				ExpiresInDays int      `json:"expires_in_days"`
				Scopes        []string `json:"scopes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpiresInDays != 1 || len(body.Scopes) != 1 || body.Scopes[0] != auth.ScopeAdmin {
				t.Errorf("ephemeral token request = %+v", body)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": tokenID.String(), "token": wantToken}})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/auth/tokens/"+tokenID.String()+"/":
			if r.Header.Get("Authorization") != "Bearer "+wantToken {
				t.Error("ephemeral token revocation did not authenticate with the minted token")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			mu.Lock()
			revokeCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	token, revoke, err := bootstrapAdminAPIToken(t.Context(), server.URL, wantEmail, wantPassword)
	if err != nil {
		t.Fatal(err)
	}
	if token != wantToken {
		t.Fatal("bootstrap returned an unexpected token")
	}
	revoke()
	revoke()
	mu.Lock()
	defer mu.Unlock()
	if revokeCalls != 1 {
		t.Fatalf("revocation calls = %d, want 1", revokeCalls)
	}
}

func TestBootstrapAdminAPITokenRejectsFailedLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	token, revoke, err := bootstrapAdminAPIToken(t.Context(), server.URL, "admin@example.com", "wrong")
	if err == nil || token != "" || revoke != nil {
		t.Fatalf("failed login result = token_present:%t revoke_present:%t err:%v", token != "", revoke != nil, err)
	}
}
