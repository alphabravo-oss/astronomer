package middleware

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// SessionCookieName is the cookie carrying the same JWT that XHR clients
// send via `Authorization: Bearer ...`. It exists because top-level browser
// navigation (an `<a target="_blank">` click) cannot attach a custom
// Authorization header — the only credential the browser will spontaneously
// send is a cookie. The frontend mirrors localStorage into this cookie at
// login / refresh / logout.
const SessionCookieName = "astronomer_session"

// AuthBrowserOrBearer authenticates a request using either:
//
//  1. `Authorization: Bearer <jwt>` (or `Bearer astro_<api-token>`), the same
//     way the regular API does. This path is taken for XHRs originated by
//     the ArgoCD SPA bundle once it has rendered.
//  2. The `astronomer_session` cookie, fallback used for the very first
//     browser navigation (an `<a target="_blank">` click on the dashboard).
//
// On failure, requests for HTML (Accept includes text/html and method is
// GET/HEAD) are redirected to the frontend login page with a `returnTo`
// query — that's the right UX for browser nav. Non-HTML requests get a JSON
// 401 like the rest of the API. This split matters because XHRs and
// WebSocket upgrade handshakes must NOT receive a 302 to the login page;
// the JS code expects a structured 401 it can handle.
//
// The wrapped handler observes the same `AuthenticatedUser` in context that
// the standard `RequireAuthWithQueries` provides, so downstream code (e.g.
// audit, RBAC) sees a uniform shape.
func AuthBrowserOrBearer(jwt *auth.JWTManager, queries TokenUserQuerier, loginPath string) func(http.Handler) http.Handler {
	if loginPath == "" {
		loginPath = "/auth/login"
	}
	// Reuse the existing AuthWithQueries pipeline by transplanting the
	// cookie's value into the Authorization header before invoking it. This
	// keeps token / API-key parsing and DB lookup logic in exactly one
	// place.
	inner := AuthWithQueries(jwt, queries)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
					// Synthesise a Bearer header. Don't mutate the original
					// request — clone so downstream handlers can still read
					// the unmodified cookie if they want.
					r2 := r.Clone(r.Context())
					r2.Header.Set("Authorization", "Bearer "+c.Value)
					r = r2
				}
			}

			// Wrap next with a status-aware writer so we can convert the
			// inner middleware's hardcoded 401 (JSON body) into a 302
			// for browser navigation only.
			cw := &authConvertingWriter{
				ResponseWriter: w,
				redirectURL:    redirectURL(r, loginPath),
				redirect:       wantsHTMLRedirect(r),
			}

			inner(next).ServeHTTP(cw, r)
			cw.flush()
		})
	}
}

// wantsHTMLRedirect returns true when the failure-mode UX should be a 302 to
// the login page rather than a JSON 401. This happens for browser top-level
// navigation: GET/HEAD with an Accept that includes text/html, and not an
// XHR (no Sec-Fetch-Mode: cors, no X-Requested-With) and not a WebSocket
// upgrade.
func wantsHTMLRedirect(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	if r.Header.Get("X-Requested-With") != "" {
		return false
	}
	mode := r.Header.Get("Sec-Fetch-Mode")
	if mode == "cors" || mode == "websocket" {
		return false
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html") || accept == ""
}

func redirectURL(r *http.Request, loginPath string) string {
	rt := r.URL.Path
	if r.URL.RawQuery != "" {
		rt += "?" + r.URL.RawQuery
	}
	return loginPath + "?returnTo=" + url.QueryEscape(rt)
}

// authConvertingWriter buffers the AuthWithQueries response when we know the
// caller is a browser navigation, so we can turn the canonical 401-with-JSON
// into a 302 to the login page instead. For non-redirect callers (XHR, WS)
// it writes through unchanged.
type authConvertingWriter struct {
	http.ResponseWriter
	redirectURL string
	redirect    bool

	statusCaptured bool
	intercept      bool
	headersSent    bool
}

func (w *authConvertingWriter) WriteHeader(code int) {
	if !w.statusCaptured {
		w.statusCaptured = true
		if w.redirect && code == http.StatusUnauthorized {
			w.intercept = true
			w.Header().Set("Location", w.redirectURL)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.ResponseWriter.WriteHeader(http.StatusFound)
			w.headersSent = true
			return
		}
	}
	if !w.intercept {
		w.ResponseWriter.WriteHeader(code)
		w.headersSent = true
	}
}

func (w *authConvertingWriter) Write(b []byte) (int, error) {
	if w.intercept {
		// Silently drop the JSON 401 body — we already wrote a 302. Pretend
		// we wrote it so the inner handler doesn't get an error.
		return len(b), nil
	}
	if !w.headersSent {
		w.ResponseWriter.WriteHeader(http.StatusOK)
		w.headersSent = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *authConvertingWriter) flush() {
	// Nothing to flush today — placeholder so callers always pair the
	// wrapper with a flush() call. Kept so future changes that buffer body
	// bytes have a hook here.
	_ = context.TODO()
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController can
// reach the original Hijacker for WebSocket upgrades.
func (w *authConvertingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
