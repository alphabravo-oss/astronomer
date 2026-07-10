package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/argosecurity"
)

// Local aliases so the body of isExpiredJWT reads cleanly without aliasing the
// stdlib packages everywhere; keeps the function self-contained.
var (
	base64URLDecode = base64.RawURLEncoding.DecodeString
	jsonUnmarshal   = json.Unmarshal
)

// ArgoCDUIProxy reverse-proxies browser traffic to the in-cluster ArgoCD
// server. It is mounted at the *top-level* `/argocd/*` path (NOT under
// `/api/v1`) so the upstream SPA's relative asset URLs resolve under the
// same prefix without any path rewriting.
//
// Authentication is handled by middleware sitting in front of this handler:
// callers must present either an `Authorization: Bearer <jwt>` header (used
// by XHRs that ArgoCD's bundle issues against `/argocd/api/v1/...` once the
// SPA boots) or an `astronomer_session` cookie (used for the very first
// browser navigation, which can't carry a custom Authorization header).
//
// Path forwarding: the upstream argocd-server is expected to be configured
// with `server.rootpath: /argocd`. When that's set, ArgoCD's SPA emits
// asset and API URLs under `/argocd/...` and ArgoCD itself routes them
// based on that prefix. So the proxy forwards the *full* path unchanged
// — no prefix stripping.
// SessionTokenSource returns the upstream ArgoCD session JWT (the value of
// the `argocd.token` cookie). Implementations look up the local cluster's
// ArgoCD instance row and decrypt its stored auth_token. Returning empty
// string + nil means "no instance configured yet"; the proxy passes the
// request through unchanged in that case and ArgoCD shows its own login.
type SessionTokenSource interface {
	UpstreamSessionToken(ctx context.Context) (string, error)
}

// ArgoCDUIProxy reverse-proxies browser traffic to the in-cluster ArgoCD
// server with optional upstream session-cookie injection.
type ArgoCDUIProxy struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
	log    *slog.Logger

	mu        sync.RWMutex
	tokens    SessionTokenSource
	audit     any
	cached    string
	cachedExp time.Time
}

// argocdTokenCacheTTL caps how long a decrypted upstream session token is
// reused across requests. Short enough that a token rotation propagates
// within a minute; long enough that a busy dashboard isn't decrypting on
// every navigation.
const argocdTokenCacheTTL = 60 * time.Second

// SetSessionTokenSource registers a token source. Calling with nil disables
// auto-injection (the proxy then forwards exactly what the browser sent).
// Safe to call from server boot.
func (p *ArgoCDUIProxy) SetSessionTokenSource(src SessionTokenSource) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.tokens = src
	p.cached = ""
	p.cachedExp = time.Time{}
	p.mu.Unlock()
}

func (p *ArgoCDUIProxy) SetAuditWriter(audit any) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.audit = audit
	p.mu.Unlock()
}

// upstreamToken returns the cached token, or asks the source for a fresh
// one when the cache has expired. A nil source is treated as "no token";
// errors from the source are logged once and swallowed — the proxy must
// still forward the request, just without the cookie injection.
func (p *ArgoCDUIProxy) upstreamToken(ctx context.Context) string {
	p.mu.RLock()
	if p.cached != "" && time.Now().Before(p.cachedExp) {
		t := p.cached
		p.mu.RUnlock()
		return t
	}
	src := p.tokens
	p.mu.RUnlock()
	if src == nil {
		return ""
	}
	tok, err := src.UpstreamSessionToken(ctx)
	if err != nil {
		// Surface this loudly. Decrypt failures and DB lookup errors here are
		// the most common reason "Open ArgoCD UI" silently lands on the
		// upstream login page — operators need to see them. Once-per-cache-
		// window cadence (cleared each TTL) keeps this from spamming logs.
		p.log.Warn("argocd UI proxy: token source error",
			"error", argosecurity.SanitizeString(err.Error()),
			"hint", "check ASTRONOMER_ENCRYPTION_KEY hasn't rotated and that the argocd_instances row holds a valid encrypted token")
		return ""
	}
	if tok == "" {
		p.log.Debug("argocd UI proxy: token source returned empty (no instance configured for local cluster)")
		return ""
	}
	if expired, exp := isExpiredJWT(tok); expired {
		// A session JWT was stored instead of a non-expiring API token —
		// session JWTs roll off after 24h and break SSO until re-minted.
		// Surface this clearly so the operator can swap the row's value
		// for a token issued via `argocd account generate-token --account
		// astronomer --expires-in 0` (see migration 024).
		p.log.Warn("argocd UI proxy: stored upstream token is expired",
			"exp", exp.UTC().Format(time.RFC3339),
			"hint", "store a non-expiring API token: enable accounts.astronomer apiKey in argocd-cm and PUT a fresh token to /api/v1/argocd/instances/{id}/")
		return ""
	}
	p.mu.Lock()
	p.cached = tok
	p.cachedExp = time.Now().Add(argocdTokenCacheTTL)
	p.mu.Unlock()
	return tok
}

// NewArgoCDUIProxy builds the reverse proxy. `targetURL` should be the
// in-cluster service URL (e.g. `http://argocd-server.argocd.svc.cluster.local:80`).
//
// `httputil.NewSingleHostReverseProxy` already handles HTTP/1.1 Upgrade
// (WebSocket) round-trips through Go's default `Transport` when the upstream
// is plain HTTP — `http.Transport` natively splices the connection on
// Upgrade. We override `Director` only to set Host/X-Forwarded-* headers and
// preserve the original full request path/query.
func NewArgoCDUIProxy(targetURL string, log *slog.Logger) (*ArgoCDUIProxy, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}

	// Construct the wrapper first so the Director closure can capture it for
	// upstream-token lookups. The reverse proxy is owned by the wrapper.
	p := &ArgoCDUIProxy{target: target, log: log}

	rp := httputil.NewSingleHostReverseProxy(target)
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		// Default director rewrites Scheme/Host and joins paths. We let it
		// run, then override the Host header — argocd-server keys on it for
		// some routing decisions and dislikes the literal Astronomer
		// hostname appearing in `Host`.
		origDirector(req)
		req.Host = target.Host
		// X-Forwarded-* — these let the upstream (and any downstream
		// middleware) generate correct absolute URLs. The default
		// ReverseProxy already appends X-Forwarded-For, but X-Forwarded-Proto
		// and X-Forwarded-Host are not set automatically.
		if req.Header.Get("X-Forwarded-Proto") == "" {
			proto := "http"
			if req.TLS != nil {
				proto = "https"
			}
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		// Use the *original* host the browser hit so the upstream sees the
		// public hostname, not the in-cluster service DNS name.
		if req.Header.Get("X-Forwarded-Host") == "" && req.Header.Get("X-Original-Host") != "" {
			req.Header.Set("X-Forwarded-Host", req.Header.Get("X-Original-Host"))
		}
		// Strip Accept-Encoding for HTML navigations so the upstream
		// returns plaintext we can rewrite (`<base href>` substitution
		// + SPA index fallback for 404s). Asset / API responses keep
		// the client's encoding preferences and pass through unchanged.
		if wantsHTMLNav(req) {
			req.Header.Del("Accept-Encoding")
		}

		// Single sign-on: inject the upstream argocd.token cookie unless the
		// client already presented one. Without this the user lands on
		// ArgoCD's own login screen on first visit; with it they're logged
		// in as the admin user the instance row was provisioned with.
		// Astronomer auth has already gated this request — we're decrypting
		// a token we own to a backend we own.
		if !hasArgoCDCookie(req) {
			if tok := p.upstreamToken(req.Context()); tok != "" {
				appendCookie(req, "argocd.token="+tok)
				p.log.Debug("argocd UI proxy: injected upstream session cookie",
					"path", safeArgoCDProxyPath(req.URL.Path))
			} else {
				p.log.Debug("argocd UI proxy: no upstream token injected",
					"path", safeArgoCDProxyPath(req.URL.Path))
			}
		}

		// Strip the Authorization header before forwarding. The auth middleware
		// in front of this proxy parses Astronomer's JWT (either from the
		// header or the astronomer_session cookie) and stamps a
		// `Authorization: Bearer <astronomer-jwt>` on the inbound request so
		// downstream handlers can read it. Forwarding it to ArgoCD upstream
		// breaks auth because ArgoCD validates the Bearer JWT against its own
		// signing key, finds Astronomer's, and rejects the entire request
		// even when our injected `argocd.token` cookie would have worked.
		// Also strip the astronomer_session cookie — irrelevant to ArgoCD,
		// just adds noise (and arguably leaks the session JWT to upstream).
		req.Header.Del("Authorization")
		stripCookie(req, "astronomer_session")
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Warn("argocd UI proxy upstream error", "path", safeArgoCDProxyPath(r.URL.Path), "error", argosecurity.SanitizeString(err.Error()))
		http.Error(w, "argocd upstream unavailable", http.StatusBadGateway)
	}

	// SPA deep-link fallback: ArgoCD's argocd-server serves index.html only
	// at the literal `/argocd/` path. Sub-paths like `/argocd/applications`
	// are intended to be handled by the SPA's client-side router after the
	// app has booted — but a fresh navigation lands on argocd-server first
	// and gets a 404. Rewrite text/html 404s under /argocd/* to the SPA
	// index so the SPA can pick up the path from window.location and
	// route internally. We also rewrite the `<base href="/">` ArgoCD ships
	// to `<base href="/argocd/">` so all relative asset URLs resolve under
	// our public prefix.
	rp.ModifyResponse = func(resp *http.Response) error {
		// Only intercept SPA HTML responses. JSON / WS / asset responses
		// pass through unchanged.
		ct := resp.Header.Get("Content-Type")
		isHTML := strings.Contains(ct, "text/html")
		path := resp.Request.URL.Path
		// Don't touch responses to API calls — only navigations under the
		// /argocd/ prefix that aren't /argocd/api/*.
		isAPI := strings.HasPrefix(path, "/argocd/api/")
		if isAPI && resp.StatusCode == http.StatusSwitchingProtocols {
			return fmt.Errorf("Argo API protocol upgrades are not admitted by the redaction policy")
		}
		if isAPI && isJSONMediaType(ct) {
			if err := sanitizeArgoCDAPIJSONResponse(resp); err != nil {
				return err
			}
			sanitizeArgoCDUIResponseHeaders(resp)
			return nil
		}
		if isAPI && resp.Request.Method != http.MethodHead && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified && resp.ContentLength != 0 {
			// Application watches, NDJSON event feeds and log streams can carry
			// the same source/manifests or legacy credential lines as JSON
			// responses. Buffering an unbounded stream is unsafe, while opaque
			// pass-through is a redaction bypass. Keep them disabled until a
			// bounded format-aware streaming sanitizer is implemented.
			return fmt.Errorf("non-JSON Argo API response is not admitted by the redaction policy")
		}

		// Case 1: 404 on a text/html navigation under /argocd/* — the SPA
		// deep-link case. Re-fetch /argocd/ and substitute it in.
		if resp.StatusCode == http.StatusNotFound && !isAPI && wantsHTMLNav(resp.Request) {
			// Build a request for `/argocd/` against the upstream and copy
			// the body in. Reuse the proxy's transport so we don't pay
			// connection setup cost twice.
			indexURL := *target
			indexURL.Path = "/argocd/"
			req, _ := http.NewRequestWithContext(resp.Request.Context(), http.MethodGet, indexURL.String(), nil)
			req.Header.Set("Accept", "text/html")
			tr := rp.Transport
			if tr == nil {
				tr = http.DefaultTransport
			}
			indexResp, err := tr.RoundTrip(req)
			if err != nil {
				return nil // fall through with original 404
			}
			defer func() {
				_ = indexResp.Body.Close()
			}()
			body, err := io.ReadAll(indexResp.Body)
			if err != nil {
				return nil
			}
			body = rewriteBaseHref(body, "/argocd/")

			// Replace the original response payload + status. Drop the
			// original Content-Length so net/http recomputes it.
			resp.StatusCode = http.StatusOK
			resp.Status = "200 OK"
			resp.Body = io.NopCloser(bytes.NewReader(body))
			resp.ContentLength = int64(len(body))
			resp.Header = indexResp.Header.Clone()
			resp.Header.Set("Content-Type", "text/html; charset=utf-8")
			resp.Header.Del("Content-Length")
			resp.Header.Del("Content-Encoding") // we re-emitted plaintext
			sanitizeArgoCDUIResponseHeaders(resp)
			return nil
		}

		// Case 2: a successful HTML response on /argocd/ itself —
		// rewrite <base href="/"> → <base href="/argocd/"> so the
		// browser resolves all relative URLs (assets + xhrs) under
		// our prefix.
		if isHTML && resp.StatusCode == http.StatusOK && !isAPI {
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				return err
			}
			body = rewriteBaseHref(body, "/argocd/")
			resp.Body = io.NopCloser(bytes.NewReader(body))
			resp.ContentLength = int64(len(body))
			resp.Header.Del("Content-Length")
			resp.Header.Del("Content-Encoding")
		}
		sanitizeArgoCDUIResponseHeaders(resp)
		return nil
	}

	p.proxy = rp
	return p, nil
}

// isExpiredJWT inspects a JWT's `exp` claim WITHOUT verifying its signature
// (the upstream owns the signing key, not us). A token without an `exp`
// claim is treated as never-expiring — the canonical case for ArgoCD API
// tokens generated with `--expires-in 0`. Returns the parsed expiry on the
// expired branch so the caller can include it in the log line.
func isExpiredJWT(token string) (bool, time.Time) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return false, time.Time{}
	}
	// JWT claim segments are base64url with optional padding.
	seg := parts[1]
	if pad := len(seg) % 4; pad != 0 {
		seg += strings.Repeat("=", 4-pad)
	}
	// Decode using base64.URLEncoding to match JWT's url-safe alphabet.
	var raw []byte
	if b, err := base64URLDecode(seg); err == nil {
		raw = b
	}
	if len(raw) == 0 {
		return false, time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := jsonUnmarshal(raw, &claims); err != nil {
		return false, time.Time{}
	}
	if claims.Exp == 0 {
		// No expiry claim — never-expiring API key. Treat as valid.
		return false, time.Time{}
	}
	exp := time.Unix(claims.Exp, 0)
	return time.Now().After(exp), exp
}

// hasArgoCDCookie reports whether the inbound request already carries an
// `argocd.token` cookie. When it does, we leave the upstream auth state to
// the browser — usually because the user authenticated against ArgoCD
// directly via the standalone host, or because we already injected the
// cookie on a prior request and ArgoCD has since rotated it.
func hasArgoCDCookie(r *http.Request) bool {
	c, err := r.Cookie("argocd.token")
	return err == nil && c != nil && c.Value != ""
}

func sanitizeArgoCDUIResponseHeaders(resp *http.Response) {
	if resp == nil {
		return
	}
	cookies := resp.Cookies()
	for key := range resp.Header {
		if !argoCDUIResponseHeaderAllowed(key) {
			resp.Header.Del(key)
		}
	}
	resp.Header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if !argoCDUIResponseCookieAllowed(cookie.Name) {
			continue
		}
		sanitized := sanitizeArgoCDUIResponseCookie(cookie, resp)
		resp.Header.Add("Set-Cookie", sanitized.String())
	}
}

func argoCDUIResponseHeaderAllowed(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "":
		return false
	case "authorization", "clear-site-data", "content-length", "cookie", "proxy-authenticate", "proxy-authorization", "set-cookie", "set-cookie2", "www-authenticate":
		return false
	case "connection", "keep-alive", "te", "trailer", "trailers", "transfer-encoding", "upgrade":
		return false
	default:
		return true
	}
}

func argoCDUIResponseCookieAllowed(name string) bool {
	return name == "argocd.token"
}

func sanitizeArgoCDUIResponseCookie(cookie *http.Cookie, resp *http.Response) *http.Cookie {
	sanitized := *cookie
	sanitized.Domain = ""
	sanitized.Path = "/argocd"
	sanitized.HttpOnly = true
	sanitized.SameSite = http.SameSiteLaxMode
	if argoCDUIExternalHTTPS(resp) {
		sanitized.Secure = true
	}
	return &sanitized
}

func argoCDUIExternalHTTPS(resp *http.Response) bool {
	if resp == nil || resp.Request == nil {
		return false
	}
	return strings.EqualFold(resp.Request.Header.Get("X-Forwarded-Proto"), "https")
}

// appendCookie appends `value` (a single `name=value` pair) to the request's
// outgoing Cookie header, preserving any cookies the client already sent.
// We don't use http.Request.AddCookie because it doesn't merge into an
// existing Cookie header consistently across Go versions.
func appendCookie(r *http.Request, value string) {
	existing := r.Header.Get("Cookie")
	if existing == "" {
		r.Header.Set("Cookie", value)
		return
	}
	r.Header.Set("Cookie", existing+"; "+value)
}

// stripCookie removes a single named cookie from the request's outgoing
// Cookie header. Used to keep the Astronomer session cookie out of upstream
// requests (ArgoCD doesn't need it and shouldn't see the JWT).
func stripCookie(r *http.Request, name string) {
	existing := r.Header.Get("Cookie")
	if existing == "" {
		return
	}
	parts := strings.Split(existing, ";")
	out := make([]string, 0, len(parts))
	prefix := name + "="
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if strings.HasPrefix(t, prefix) {
			continue
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		r.Header.Del("Cookie")
		return
	}
	r.Header.Set("Cookie", strings.Join(out, "; "))
}

// wantsHTMLNav returns true when the upstream request is a top-level
// browser navigation (i.e. the user pasted /argocd/applications into the
// address bar or clicked an in-page anchor) rather than an XHR / asset
// fetch. We use this to decide whether to substitute index.html on a 404.
func wantsHTMLNav(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		return true
	}
	if r.Header.Get("Sec-Fetch-Dest") != "" {
		return false
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// rewriteBaseHref replaces `<base href="/">` with `<base href="<prefix>">`
// in a small HTML body. ArgoCD ships a tiny static index, so a single byte
// substitution is enough — we don't need a full HTML parser. If the tag
// is missing (e.g. ArgoCD changes its template in a later release) the
// body is returned unchanged.
//
// Also injects a tiny localStorage-cleanup script after the rewritten
// `<base>` tag. ArgoCD shares an origin with the Astronomer dashboard
// (we mount the SPA under /argocd/* on the same hostname), and Astronomer's
// next-themes integration historically wrote a literal string to
// localStorage["theme"]. ArgoCD's SPA reads the same key as JSON and
// `JSON.parse("dark")` throws — the React tree never mounts and the page
// renders blank. The Astronomer frontend now writes under
// "astronomer-theme", but already-installed browsers still have the bad
// value sitting in localStorage. The injected script removes any non-JSON
// "theme" entry on every load, runs in a try/catch, and is no-op once a
// well-formed value is present.
func rewriteBaseHref(body []byte, prefix string) []byte {
	want := `<base href="` + prefix + `">`
	purgeScript := `<script>(function(){try{var t=localStorage.getItem("theme");if(t!==null&&t.length>0){var c=t.charAt(0);if(c!=='"'&&c!=='{'&&c!=='['){localStorage.removeItem("theme");}}}catch(e){}})();</script>`
	// Upstream may already emit the correct base href (when ArgoCD is
	// configured with server.basehref) or the legacy `/`. Cover both;
	// either way, splice the localStorage purge script right after.
	if bytes.Contains(body, []byte(want)) {
		return bytes.Replace(body, []byte(want), []byte(want+purgeScript), 1)
	}
	old := []byte(`<base href="/">`)
	if !bytes.Contains(body, old) {
		return body
	}
	return bytes.Replace(body, old, []byte(want+purgeScript), 1)
}

// ServeHTTP proxies the request to the upstream ArgoCD server. It runs after
// auth middleware so reaching this point means the caller is allowed.
func (p *ArgoCDUIProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if status, err := validateArgoCDProxyMutationRequest(r); err != nil {
		http.Error(w, http.StatusText(status), status)
		p.recordProxyAudit(r, status)
		p.log.Warn("argocd UI proxy rejected unsafe mutation", "method", r.Method, "path", safeArgoCDProxyPath(r.URL.Path), "reason", argosecurity.SanitizeString(err.Error()))
		return
	}
	// Stash the original public host so the Director can stamp it on
	// X-Forwarded-Host before clobbering r.Host.
	if r.Header.Get("X-Original-Host") == "" && r.Host != "" {
		r.Header.Set("X-Original-Host", r.Host)
	}

	// Capture status by wrapping the writer. Cheap; only used for the debug
	// log line, which is rate-bounded by slog level filtering.
	sw := &statusRecorder{ResponseWriter: w, status: 0}
	p.proxy.ServeHTTP(sw, r)
	p.recordProxyAudit(r, sw.status)
	p.log.Debug("argocd UI proxy",
		"method", r.Method,
		"path", safeArgoCDProxyPath(r.URL.Path),
		"status", sw.status,
		"upstream", p.target.String(),
		"upgrade", strings.EqualFold(r.Header.Get("Connection"), "upgrade"),
	)
}

func (p *ArgoCDUIProxy) recordProxyAudit(r *http.Request, status int) {
	action, ok := argoCDUIProxyAuditAction(r)
	if !ok {
		return
	}
	p.mu.RLock()
	audit := p.audit
	p.mu.RUnlock()
	if audit == nil {
		return
	}
	safePath := safeArgoCDProxyPath(r.URL.Path)
	recordAudit(r, audit, action, "argocd_proxy", "", safePath, map[string]any{
		"path":        safePath,
		"status_code": status,
		"is_api":      strings.HasPrefix(r.URL.Path, "/argocd/api/"),
		"upgrade":     strings.EqualFold(r.Header.Get("Connection"), "upgrade"),
	})
}

func argoCDUIProxyAuditAction(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if wantsHTMLNav(r) {
			return "argocd.ui_proxy.opened", true
		}
		return "", false
	default:
		return "argocd.ui_proxy.forwarded", true
	}
}

// statusRecorder is a minimal http.ResponseWriter wrapper that captures the
// status code for the debug log. It deliberately implements http.Hijacker
// passthrough so WebSocket upgrades still work — the default ResponseWriter
// passed in by chi is an *http.response which exposes Hijack via interface
// assertion in httputil.ReverseProxy.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying ResponseWriter so the standard library's
// response-controller (used by httputil.ReverseProxy for hijack/flush on
// upgrade) can find the hijacker on the inner writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

const maxArgoCDProxyJSONBytes = 16 << 20

func validateArgoCDProxyMutationRequest(r *http.Request) (int, error) {
	if r == nil || !strings.HasPrefix(r.URL.Path, "/argocd/api/") {
		return 0, nil
	}
	if r.Header.Get("Upgrade") != "" || headerContainsToken(r.Header.Values("Connection"), "upgrade") {
		return http.StatusForbidden, fmt.Errorf("Argo API protocol upgrades are not admitted")
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return 0, nil
	}
	if r.Body == nil || r.Body == http.NoBody {
		return 0, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxArgoCDProxyJSONBytes+1))
	if err != nil {
		return http.StatusBadRequest, fmt.Errorf("read mutation body")
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if len(raw) > maxArgoCDProxyJSONBytes {
		return http.StatusRequestEntityTooLarge, fmt.Errorf("mutation body exceeds inspection limit")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0, nil
	}
	if !isJSONMediaType(r.Header.Get("Content-Type")) {
		return http.StatusUnsupportedMediaType, fmt.Errorf("non-JSON Argo mutation body")
	}
	decoded, err := decodeArgoJSONBody(raw, r.Header.Get("Content-Encoding"))
	if err != nil {
		return http.StatusBadRequest, err
	}
	if err := argosecurity.ValidateMutationJSON(decoded); err != nil {
		return http.StatusBadRequest, err
	}
	return 0, nil
}

func sanitizeArgoCDAPIJSONResponse(resp *http.Response) error {
	if resp == nil || resp.Body == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxArgoCDProxyJSONBytes+1))
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("read Argo JSON response: %w", err)
	}
	if len(raw) > maxArgoCDProxyJSONBytes {
		return fmt.Errorf("Argo JSON response exceeds inspection limit")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		resp.ContentLength = 0
		resp.Header.Del("Content-Length")
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("ETag")
		resp.Header.Del("Content-MD5")
		resp.Header.Del("Digest")
		return nil
	}
	decoded, err := decodeArgoJSONBody(raw, resp.Header.Get("Content-Encoding"))
	if err != nil {
		return err
	}
	sanitized, err := argosecurity.SanitizeJSON(decoded)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(sanitized))
	resp.ContentLength = int64(len(sanitized))
	resp.Header.Del("Content-Length")
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("ETag")
	resp.Header.Del("Content-MD5")
	resp.Header.Del("Digest")
	return nil
}

func headerContainsToken(values []string, want string) bool {
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				return true
			}
		}
	}
	return false
}

func decodeArgoJSONBody(raw []byte, contentEncoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(contentEncoding)) {
	case "":
		return raw, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode gzip Argo JSON: %w", err)
		}
		defer reader.Close()
		decoded, err := io.ReadAll(io.LimitReader(reader, maxArgoCDProxyJSONBytes+1))
		if err != nil {
			return nil, fmt.Errorf("decode gzip Argo JSON: %w", err)
		}
		if len(decoded) > maxArgoCDProxyJSONBytes {
			return nil, fmt.Errorf("decoded Argo JSON exceeds inspection limit")
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unsupported Argo JSON content encoding")
	}
}

func isJSONMediaType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func safeArgoCDProxyPath(path string) string {
	if !strings.HasPrefix(path, "/argocd/api/") {
		return path
	}
	for _, collection := range []string{"applications", "applicationsets", "projects", "repositories", "clusters", "accounts", "certificates", "repocreds"} {
		marker := "/" + collection + "/"
		if index := strings.Index(path, marker); index >= 0 {
			safe := path[:index+len(marker)] + "*"
			for _, action := range []string{"sync", "refresh", "revisions", "manifests", "validate", "logs"} {
				if strings.HasSuffix(path, "/"+action) {
					return safe + "/" + action
				}
			}
			return safe
		}
	}
	for _, collection := range []string{"applications", "applicationsets", "projects", "repositories", "clusters", "accounts", "certificates", "repocreds", "version"} {
		if strings.HasSuffix(path, "/"+collection) {
			return path
		}
	}
	return "/argocd/api/*"
}
