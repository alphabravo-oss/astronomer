package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func TestHTTPSCookieDoesNotFallBackToSiblingInjectableName(t *testing.T) {
	for _, address := range []string{"https://console.example.test/", "http://internal-server/"} {
		r := httptest.NewRequest("POST", address, nil)
		r = r.WithContext(reqctx.WithSecureCookiesRequired(r.Context(), true))
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "sibling-domain-session"})
		r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "sibling-domain-csrf"})
		r.Header.Set("X-CSRF-Token", "sibling-domain-csrf")
		if _, err := BrowserCookie(r, SessionCookieName); err == nil || ValidateCSRF(r) {
			t.Fatal("accepted sibling-injectable cookie")
		}
		r.AddCookie(&http.Cookie{Name: "__Host-" + SessionCookieName, Value: "host-session"})
		r.AddCookie(&http.Cookie{Name: "__Host-" + CSRFCookieName, Value: "host-csrf"})
		r.Header.Set("X-CSRF-Token", "host-csrf")
		cookie, err := BrowserCookie(r, SessionCookieName)
		if err != nil || cookie.Value != "host-session" || !ValidateCSRF(r) {
			t.Fatal("host cookie failed")
		}
	}
}
