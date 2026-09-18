package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

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
	cookie, err := request.Cookie(CSRFCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" || len(header) != len(cookie.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(cookie.Value)) == 1
}
