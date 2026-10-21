package astrocli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func TestPasswordLoginRotatesOwnedTokenBeforeQuotaCheck(t *testing.T) {
	t.Parallel()
	rotated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login/":
			http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookieName, Value: "csrf", Path: "/"})
			_, _ = w.Write([]byte(`{"data":{"user":{"username":"admin","email":"admin@example.test"}}}`))
		case "/api/v1/auth/tokens/old/":
			if r.Method != http.MethodDelete || r.Header.Get("X-CSRF-Token") != "csrf" {
				t.Error("rotation did not use authenticated CSRF session")
			}
			rotated = true
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/auth/tokens/":
			if !rotated {
				t.Error("creation evaluated quota before rotating the previous token")
			}
			var body struct {
				ExpiresInDays int `json:"expires_in_days"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ExpiresInDays != 30 {
				t.Error("CLI token must expire")
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"new","token":"astro_new"}}`))
		case "/api/v1/auth/logout/":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	issued, err := IssuePasswordAPIToken(t.Context(), server.URL, "admin@example.test", "password", "astro-cli-session", 30, "old")
	if err != nil {
		t.Fatal(err)
	}
	if issued.ID != "new" || !rotated {
		t.Fatal("CLI credential was not rotated")
	}
}
