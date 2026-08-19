package grafanaproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	proxyTimeout   = 60 * time.Second
	proxyBodyLimit = 10 << 20 // 10 MiB
)

// Config is env-driven. The process must not require Redis or ASTRONOMER_SECRET_KEY.
type Config struct {
	ListenAddr    string
	Upstream      *url.URL
	AstronomerURL string
	GrafanaHost   string
	HMACKey       []byte
	Redeem        func(ticket string) (redeemResult, error)
	Now           func() time.Time
}

type redeemResult struct {
	Email   string `json:"email"`
	Role    string `json:"role"`
	TTL     int    `json:"ttl"`
	Explore bool   `json:"explore"`
	Admin   bool   `json:"admin"`
}

func ConfigFromEnv() (Config, error) {
	upstreamRaw := strings.TrimSpace(os.Getenv("GRAFANA_UPSTREAM"))
	astro := strings.TrimRight(strings.TrimSpace(os.Getenv("ASTRONOMER_URL")), "/")
	host := strings.TrimSpace(os.Getenv("GRAFANA_HOST"))
	key := strings.TrimSpace(os.Getenv("GRAFANA_PROXY_KEY"))
	listen := strings.TrimSpace(os.Getenv("LISTEN_ADDR"))
	if listen == "" {
		listen = ":8080"
	}
	if upstreamRaw == "" || astro == "" || host == "" || key == "" {
		return Config{}, fmt.Errorf("GRAFANA_UPSTREAM, ASTRONOMER_URL, GRAFANA_HOST, and GRAFANA_PROXY_KEY are required")
	}
	if strings.Contains(strings.ToUpper(key), "SECRET_KEY") {
		return Config{}, fmt.Errorf("GRAFANA_PROXY_KEY must be the Grafana-family HMAC, not ASTRONOMER_SECRET_KEY")
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return Config{}, fmt.Errorf("GRAFANA_UPSTREAM is not a valid URL")
	}
	return Config{
		ListenAddr:    listen,
		Upstream:      upstream,
		AstronomerURL: astro,
		GrafanaHost:   host,
		HMACKey:       []byte(key),
	}, nil
}

func Run() error {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           New(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

func New(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Redeem == nil {
		cfg.Redeem = cfg.redeemHTTP
	}
	p := &proxy{cfg: cfg}
	p.reverse = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.Upstream)
			pr.Out.Host = cfg.Upstream.Host
			stripHopByHop(pr.Out.Header)
			pr.Out.Header.Del("X-WEBAUTH-USER")
			pr.Out.Header.Del("X-WEBAUTH-ROLE")
			pr.Out.Header.Del("X-Dashboard-Uid")
			if auth, ok := pr.In.Context().Value(grafanaAuthContextKey{}).(grafanaAuth); ok {
				pr.Out.Header.Set("X-WEBAUTH-USER", auth.Email)
				pr.Out.Header.Set("X-WEBAUTH-ROLE", auth.Role)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			return nil
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: proxyTimeout,
		},
	}
	return p
}

type proxy struct {
	cfg     Config
	reverse *httputil.ReverseProxy
}

type grafanaAuthContextKey struct{}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, proxyBodyLimit)
	}
	if r.URL.Path == "/auth/callback" || strings.HasPrefix(r.URL.Path, "/auth/callback/") {
		p.handleCallback(w, r)
		return
	}
	auth, err := p.authFromRequest(r)
	if err != nil {
		p.redirectToMint(w, r)
		return
	}
	if status := authorizePath(r, auth); status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), grafanaAuthContextKey{}, auth))
	p.reverse.ServeHTTP(w, r)
}

func (p *proxy) authFromRequest(r *http.Request) (grafanaAuth, error) {
	c, err := r.Cookie(grafanaAuthCookie)
	if err != nil || c.Value == "" {
		return grafanaAuth{}, errGrafanaAuthInvalid
	}
	return verifyGrafanaAuth(p.cfg.HMACKey, c.Value)
}

func (p *proxy) redirectToMint(w http.ResponseWriter, r *http.Request) {
	scheme := "https"
	if strings.HasPrefix(strings.ToLower(p.cfg.AstronomerURL), "http://") {
		scheme = "http"
	}
	returnURL := scheme + "://" + p.cfg.GrafanaHost + "/auth/callback"
	mint := strings.TrimRight(p.cfg.AstronomerURL, "/") + "/api/v1/observability/grafana-ticket?return=" + url.QueryEscape(returnURL)
	http.Redirect(w, r, mint, http.StatusFound)
}

func (p *proxy) handleCallback(w http.ResponseWriter, r *http.Request) {
	ticket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if ticket == "" {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	result, err := p.cfg.Redeem(ticket)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	ttl := time.Duration(result.TTL) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	exp := p.cfg.Now().Add(ttl)
	signed, err := signGrafanaAuth(p.cfg.HMACKey, grafanaAuth{
		Email:   result.Email,
		Role:    result.Role,
		Explore: result.Explore || result.Role == "Editor" || result.Role == "Admin",
		Admin:   result.Admin || result.Role == "Admin",
		Exp:     exp.Unix(),
	})
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     grafanaAuthCookie,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(strings.ToLower(p.cfg.AstronomerURL), "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl / time.Second),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (c Config) redeemHTTP(ticket string) (redeemResult, error) {
	body, err := json.Marshal(map[string]string{"ticket": ticket})
	if err != nil {
		return redeemResult{}, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.AstronomerURL, "/")+"/api/v1/observability/grafana-ticket/redeem", bytes.NewReader(body))
	if err != nil {
		return redeemResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return redeemResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return redeemResult{}, fmt.Errorf("redeem status %d", resp.StatusCode)
	}
	var wrap struct {
		Data redeemResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return redeemResult{}, err
	}
	if wrap.Data.Email == "" {
		return redeemResult{}, errGrafanaAuthInvalid
	}
	return wrap.Data, nil
}

func authorizePath(r *http.Request, auth grafanaAuth) int {
	path := r.URL.Path
	if r.Method == http.MethodGet && isExplorePath(path) && !auth.Explore {
		return http.StatusForbidden
	}
	if isAdminAPI(path) && !auth.Admin {
		return http.StatusForbidden
	}
	if isMutatingDatasources(r) && !auth.Admin {
		return http.StatusForbidden
	}
	return 0
}

func isExplorePath(path string) bool {
	p := strings.ToLower(path)
	return p == "/explore" || strings.HasPrefix(p, "/explore/")
}

func isAdminAPI(path string) bool {
	p := strings.ToLower(path)
	return p == "/api/admin" || strings.HasPrefix(p, "/api/admin/")
}

func isMutatingDatasources(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	p := strings.ToLower(r.URL.Path)
	p = strings.TrimSuffix(p, "/")
	if p == "/api/datasources" {
		return true
	}
	if !strings.HasPrefix(p, "/api/datasources/") {
		return false
	}
	rest := strings.TrimPrefix(p, "/api/datasources/")
	segs := strings.Split(rest, "/")
	if segs[0] == "proxy" {
		return false
	}
	if segs[0] == "uid" {
		if len(segs) >= 3 {
			switch segs[2] {
			case "proxy", "resources", "health":
				return false
			}
		}
		return true
	}
	if len(segs) >= 2 {
		switch segs[1] {
		case "proxy", "resources", "health":
			return false
		}
	}
	return true
}

func stripHopByHop(h http.Header) {
	for _, name := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade",
	} {
		h.Del(name)
	}
}
