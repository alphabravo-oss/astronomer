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
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	proxyTimeout   = 60 * time.Second
	proxyBodyLimit = 10 << 20 // 10 MiB
)

// Config is resolved by the executable bootstrap. The proxy must not require
// Redis or ASTRONOMER_SECRET_KEY.
type Config struct {
	ListenAddr    string
	Upstream      *url.URL
	AstronomerURL string
	PublicPath    string
	HMACKey       []byte
	Redeem        func(ticket string) (redeemResult, error)
	Now           func() time.Time
}

type redeemResult struct {
	Email      string   `json:"email"`
	Role       string   `json:"role"`
	TTL        int      `json:"ttl"`
	Explore    bool     `json:"explore"`
	Admin      bool     `json:"admin"`
	ClusterIDs []string `json:"clusterIds,omitempty"`
}

func ParseConfig(listen, upstreamRaw, astronomerURL, publicPath, key string) (Config, error) {
	upstreamRaw = strings.TrimSpace(upstreamRaw)
	astro := strings.TrimRight(strings.TrimSpace(astronomerURL), "/")
	publicPath = strings.TrimSpace(publicPath)
	key = strings.TrimSpace(key)
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = ":8080"
	}
	if publicPath == "" {
		publicPath = "/api/v1/observability/grafana/"
	}
	if !strings.HasPrefix(publicPath, "/") || strings.ContainsAny(publicPath, "?#\r\n") {
		return Config{}, fmt.Errorf("GRAFANA_PUBLIC_PATH must be an absolute URL path")
	}
	publicPath = "/" + strings.Trim(publicPath, "/") + "/"
	if upstreamRaw == "" || astro == "" || key == "" {
		return Config{}, fmt.Errorf("GRAFANA_UPSTREAM, ASTRONOMER_URL, and GRAFANA_PROXY_KEY are required")
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
		PublicPath:    publicPath,
		HMACKey:       []byte(key),
	}, nil
}

func Run(cfg Config) error {
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
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del(protocol.GrafanaProxyTicketHeader)
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
	auth, signed, ttl, err := p.authFromRequest(r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	if signed != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     grafanaAuthCookie,
			Value:    signed,
			Path:     p.cfg.PublicPath,
			HttpOnly: true,
			Secure:   strings.HasPrefix(strings.ToLower(p.cfg.AstronomerURL), "https://"),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(ttl / time.Second),
		})
	}
	prepared, status, err := PrepareRequest(r, Identity{
		Email: auth.Email, Role: auth.Role, Explore: auth.Explore,
		Admin: auth.Admin, ClusterIDs: auth.ClusterIDs,
	})
	if status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	r = prepared
	r = r.WithContext(context.WithValue(r.Context(), grafanaAuthContextKey{}, auth))
	p.reverse.ServeHTTP(w, r)
}

func (p *proxy) authFromRequest(r *http.Request) (grafanaAuth, string, time.Duration, error) {
	c, err := r.Cookie(grafanaAuthCookie)
	if err == nil && c.Value != "" {
		if auth, verifyErr := verifyGrafanaAuth(p.cfg.HMACKey, c.Value); verifyErr == nil {
			return auth, "", 0, nil
		}
	}
	ticket := strings.TrimSpace(r.Header.Get(protocol.GrafanaProxyTicketHeader))
	if ticket == "" {
		return grafanaAuth{}, "", 0, errGrafanaAuthInvalid
	}
	result, err := p.cfg.Redeem(ticket)
	if err != nil {
		return grafanaAuth{}, "", 0, errGrafanaAuthInvalid
	}
	ttl := time.Duration(result.TTL) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	exp := p.cfg.Now().Add(ttl)
	signed, err := signGrafanaAuth(p.cfg.HMACKey, grafanaAuth{
		Email:      result.Email,
		Role:       result.Role,
		Explore:    result.Explore || result.Role == "Editor" || result.Role == "Admin",
		Admin:      result.Admin || result.Role == "Admin",
		ClusterIDs: result.ClusterIDs,
		Exp:        exp.Unix(),
	})
	if err != nil {
		return grafanaAuth{}, "", 0, errGrafanaAuthInvalid
	}
	auth, err := verifyGrafanaAuth(p.cfg.HMACKey, signed)
	return auth, signed, ttl, err
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
	defer func() { _ = resp.Body.Close() }()
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

// Identity is the authorization context Astronomer derived for one Grafana
// request. ClusterIDs is empty only for a globally authorized user.
type Identity struct {
	Email      string
	Role       string
	Explore    bool
	Admin      bool
	ClusterIDs []string
}

// PrepareRequest applies the same path authorization and tenant-query rewrite
// used by the in-cluster proxy. The management API uses this before forwarding
// a same-origin request, so an alternate transport cannot bypass the proxy's
// cluster scoping rules.
func PrepareRequest(r *http.Request, identity Identity) (*http.Request, int, error) {
	auth := grafanaAuth{
		Email: identity.Email, Role: identity.Role, Explore: identity.Explore,
		Admin: identity.Admin, ClusterIDs: identity.ClusterIDs,
	}
	if status := authorizePath(r, auth); status != 0 {
		return nil, status, nil
	}
	if len(auth.ClusterIDs) == 0 {
		return r, 0, nil
	}
	rewritten, err := rewriteTenantQuery(r, auth.ClusterIDs)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	return rewritten, 0, nil
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
