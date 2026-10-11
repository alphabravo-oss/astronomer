package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

// HTTPS cookies are host-bound by the browser, preventing a sibling tool UI
// from injecting a Domain cookie into the control-plane session. Unprefixed
// names are used only by explicitly insecure local development servers.
func BrowserCookieName(r *http.Request, name string) string {
	if reqctx.RequestIsHTTPS(r) {
		return "__Host-" + name
	}
	return name
}

func BrowserCookie(r *http.Request, name string) (*http.Cookie, error) {
	return r.Cookie(BrowserCookieName(r, name))
}

const (
	SessionCookieName = "astronomer_session"
	RefreshCookieName = "astronomer_refresh"
	CSRFCookieName    = "astronomer_csrf"
)

// ValidateCSRF verifies the browser double-submit token used by unsafe
// cookie-authenticated requests.
func ValidateCSRF(request *http.Request) bool {
	if request == nil {
		return false
	}
	header := strings.TrimSpace(request.Header.Get("X-CSRF-Token"))
	if header == "" {
		return false
	}
	cookie, err := BrowserCookie(request, CSRFCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" || len(header) != len(cookie.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) == 1
}
