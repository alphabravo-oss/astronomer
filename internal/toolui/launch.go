package toolui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

var launchPage = template.Must(template.New("launch").Parse(`<!doctype html><meta charset="utf-8"><title>Opening tool</title><form method="post" action="{{.Action}}"><input type="hidden" name="grant" value="{{.Grant}}"><button type="submit">Open tool</button></form><script nonce="{{.Nonce}}">document.forms[0].submit()</script>`))

// Launch is called behind the existing authenticated, authorized UI route. It
// emits only gateway-owned HTML; no downstream bytes ever reach the main host.
func (g *Gateway) Launch(w http.ResponseWriter, r *http.Request, target Target, returnPath string) {
	if g == nil || g.jwt == nil || g.cipher == nil || g.grants == nil || g.proxy == nil {
		http.Error(w, "Configure tool UI DNS and TLS to open this tool", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Use the isolated tool origin", http.StatusGone)
		return
	}
	if r.Host != g.main.Host || !safeLaunchSource(r, g.main.String()) {
		http.Error(w, "Cross-origin tool launch denied", http.StatusForbidden)
		return
	}
	cookie, err := auth.BrowserCookie(r, auth.SessionCookieName)
	if err != nil {
		http.Error(w, "Browser session required", http.StatusUnauthorized)
		return
	}
	claims, err := g.jwt.ValidateTokenContext(r.Context(), cookie.Value)
	if err != nil || claims.TokenType != auth.AccessToken || !claims.BrowserSession || claims.ExpiresAt == nil {
		http.Error(w, "Browser session required", http.StatusUnauthorized)
		return
	}
	if !safeReturnPath(returnPath) {
		http.Error(w, "Invalid tool path", 400)
		return
	}
	s := session{Purpose: sessionPurpose, Target: target, Host: g.host(target), Credential: cookie.Value, Expires: claims.ExpiresAt.Time, ReturnPath: returnPath}
	encoded, err := json.Marshal(s)
	if err != nil {
		http.Error(w, "Tool launch unavailable", http.StatusServiceUnavailable)
		return
	}
	sealed, err := g.cipher.Encrypt(string(encoded))
	if err != nil || len(sealed) > 3800 {
		http.Error(w, "Tool launch unavailable", http.StatusServiceUnavailable)
		return
	}
	grant := randomToken()
	if grant == "" || g.grants.Put(r.Context(), grantKey(grant), sealed, time.Minute) != nil {
		http.Error(w, "Tool launch unavailable", http.StatusServiceUnavailable)
		return
	}
	nonce := randomToken()
	w.Header().Set("Cache-Control", "no-store")
	// Cross-origin form navigations need a non-opaque Origin for redeem.
	// no-referrer makes browsers send Origin: null; disclose only the origin.
	w.Header().Set("Referrer-Policy", "strict-origin")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Origin-Agent-Cluster", "?1")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'self'; form-action https://"+s.Host+"; script-src 'nonce-"+nonce+"'")
	if r.Method != http.MethodHead {
		_ = launchPage.Execute(w, struct{ Action, Grant, Nonce string }{"https://" + s.Host + redeemPath, grant, nonce})
	}
}

func safeLaunchSource(r *http.Request, main string) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != main {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		ref, err := url.Parse(r.Referer())
		return err == nil && ref.Scheme+"://"+ref.Host == main
	default:
		return false
	}
}

func safeReturnPath(path string) bool {
	u, err := url.Parse(path)
	return err == nil && u.Host == "" && u.Scheme == "" && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && !strings.ContainsAny(path, "\\\r\n")
}

func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func grantKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (g *Gateway) redeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Origin") != g.main.String() {
		http.Error(w, "Invalid launch origin", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil {
		http.Error(w, "Invalid launch", 400)
		return
	}
	grant := r.PostForm.Get("grant")
	if len(grant) != 43 {
		http.Error(w, "Invalid launch", http.StatusUnauthorized)
		return
	}
	sealed, found, err := g.grants.Take(r.Context(), grantKey(grant))
	if err != nil || !found {
		http.Error(w, "Launch expired or already used; reopen the tool from Astronomer", http.StatusUnauthorized)
		return
	}
	s, err := g.decode(r.Context(), sealed, r.Host)
	if err != nil || !safeReturnPath(s.ReturnPath) {
		http.Error(w, "Invalid launch", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: sealed, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: s.Expires})
	http.Redirect(w, r, s.ReturnPath, http.StatusSeeOther)
}
