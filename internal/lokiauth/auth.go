// Package lokiauth is the ingest/query front for hosted Loki.
//
// Push: Ingress → this process → Loki gateway ClusterIP. Bearer SHA-256 is
// looked up in the reconciled hash Secret (no plaintext). Query: Grafana
// datasource URL points here; X-Grafana-User is mapped to an allow-list and
// X-Scope-OrgID is selected hop-by-hop. No Postgres, Redis, or
// ASTRONOMER_SECRET_KEY.
package lokiauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	proxyTimeout   = 60 * time.Second
	proxyBodyLimit = 8 << 20 // 8 MiB, SimpleScalable ingest cap
	reloadEvery    = 30 * time.Second
	scopeHeader    = "X-Scope-OrgID"
	grafanaUserHdr = "X-Grafana-User"
	hashesFileKey  = "hashes"
	aclFileKey     = "acl"
)

// Config is env-driven. No Redis, Postgres, or ASTRONOMER_SECRET_KEY.
type Config struct {
	ListenAddr string
	Upstream   *url.URL
	HashesPath string
	ACLPath    string
	Now        func() time.Time
	Log        *slog.Logger
	// Optional in-memory stores for tests. When set, file paths are ignored.
	Hashes func() map[string]string
	ACL    func() QueryACL
}

type QueryACL struct {
	Admins []string            `json:"admins"`
	Users  map[string][]string `json:"users"`
}

func ConfigFromEnv() (Config, error) {
	listen := strings.TrimSpace(os.Getenv("LISTEN_ADDR"))
	if listen == "" {
		listen = ":8080"
	}
	upstreamRaw := strings.TrimSpace(os.Getenv("LOKI_UPSTREAM"))
	if upstreamRaw == "" {
		return Config{}, fmt.Errorf("LOKI_UPSTREAM is required")
	}
	upstream, err := url.Parse(upstreamRaw)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return Config{}, fmt.Errorf("LOKI_UPSTREAM is not a valid URL")
	}
	hashes := strings.TrimSpace(os.Getenv("HASHES_PATH"))
	if hashes == "" {
		hashes = "/var/run/loki-auth/hashes/" + hashesFileKey
	}
	acl := strings.TrimSpace(os.Getenv("ACL_PATH"))
	if acl == "" {
		acl = "/var/run/loki-auth/acl/" + aclFileKey
	}
	return Config{
		ListenAddr: listen,
		Upstream:   upstream,
		HashesPath: hashes,
		ACLPath:    acl,
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

// HashBearer is the SHA-256 hex digest projected into the hash Secret.
func HashBearer(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type handler struct {
	cfg     Config
	reverse *httputil.ReverseProxy
	mu      sync.RWMutex
	hashes  map[string]string // cluster_id → hash
	acl     QueryACL
	log     *slog.Logger
}

func New(cfg Config) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	h := &handler{
		cfg:    cfg,
		hashes: map[string]string{},
		acl:    QueryACL{Users: map[string][]string{}},
		log:    log,
	}
	h.reload()
	if cfg.Hashes == nil && cfg.ACL == nil {
		go h.reloadLoop()
	}
	h.reverse = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.Upstream)
			pr.Out.Host = cfg.Upstream.Host
			stripHopByHop(pr.Out.Header)
			pr.Out.Header.Del("Authorization")
			org, _ := pr.In.Context().Value(boundOrgContextKey{}).(string)
			if org != "" {
				pr.Out.Header.Set(scopeHeader, org)
			}
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: proxyTimeout,
		},
	}
	return h
}

type boundOrgContextKey struct{}

func (h *handler) reloadLoop() {
	ticker := time.NewTicker(reloadEvery)
	defer ticker.Stop()
	for range ticker.C {
		h.reload()
	}
}

func (h *handler) reload() {
	hashes := h.cfg.Hashes
	if hashes == nil {
		hashes = func() map[string]string { return loadHashFile(h.cfg.HashesPath) }
	}
	acl := h.cfg.ACL
	if acl == nil {
		acl = func() QueryACL { return loadACLFile(h.cfg.ACLPath) }
	}
	h.mu.Lock()
	h.hashes = hashes()
	h.acl = normalizeACL(acl())
	h.mu.Unlock()
}

func normalizeACL(in QueryACL) QueryACL {
	out := QueryACL{Users: map[string][]string{}}
	for _, admin := range in.Admins {
		email := strings.ToLower(strings.TrimSpace(admin))
		if email != "" {
			out.Admins = append(out.Admins, email)
		}
	}
	for user, clusters := range in.Users {
		email := strings.ToLower(strings.TrimSpace(user))
		if email == "" {
			continue
		}
		out.Users[email] = append([]string(nil), clusters...)
	}
	return out
}

func loadHashFile(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func loadACLFile(path string) QueryACL {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return QueryACL{Users: map[string][]string{}}
	}
	var acl QueryACL
	if err := json.Unmarshal(raw, &acl); err != nil {
		return QueryACL{Users: map[string][]string{}}
	}
	return normalizeACL(acl)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, proxyBodyLimit)
	}
	path := r.URL.Path
	if path == "/ready" || path == "/ready/" || path == "/healthz" || path == "/healthz/" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if isPushPath(path) {
		h.handlePush(w, r)
		return
	}
	h.handleQuery(w, r)
}

func isPushPath(path string) bool {
	p := strings.ToLower(path)
	return p == "/loki/api/v1/push" || strings.HasPrefix(p, "/loki/api/v1/push/") ||
		p == "/otlp/v1/logs" || strings.HasPrefix(p, "/otlp/v1/logs")
}

func (h *handler) handlePush(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		h.deny(w, r, "bad_token", http.StatusUnauthorized, "unauth")
		return
	}
	org, ok := h.lookupHash(HashBearer(token))
	if !ok {
		h.deny(w, r, "bad_token", http.StatusUnauthorized, "unauth")
		return
	}
	clientOrg := strings.TrimSpace(r.Header.Get(scopeHeader))
	if clientOrg != "" && !strings.EqualFold(clientOrg, org) {
		h.deny(w, r, "org_mismatch", http.StatusUnauthorized, "unauth")
		return
	}
	ingestRequests.WithLabelValues("ok").Inc()
	if r.ContentLength > 0 {
		ingestBytes.WithLabelValues("ok").Add(float64(r.ContentLength))
	}
	h.proxy(w, r, org)
}

func (h *handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	user := strings.TrimSpace(r.Header.Get(grafanaUserHdr))
	allow := h.allowList(user)
	clientOrg := strings.TrimSpace(r.Header.Get(scopeHeader))
	org, status := SelectOrg(clientOrg, allow)
	if status != 0 {
		reason := "org_mismatch"
		if status == http.StatusBadRequest {
			reason = "org_missing"
		}
		h.deny(w, r, reason, status, "unauth")
		return
	}
	ingestRequests.WithLabelValues("ok").Inc()
	h.proxy(w, r, org)
}

func (h *handler) proxy(w http.ResponseWriter, r *http.Request, org string) {
	r = r.WithContext(context.WithValue(r.Context(), boundOrgContextKey{}, org))
	h.reverse.ServeHTTP(w, r)
}

func (h *handler) lookupHash(sum string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for clusterID, hash := range h.hashes {
		if hash == sum && clusterID != "" {
			return clusterID, true
		}
	}
	return "", false
}

func (h *handler) allowList(user string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if user == "" {
		return nil
	}
	email := strings.ToLower(user)
	for _, admin := range h.acl.Admins {
		if strings.ToLower(strings.TrimSpace(admin)) == email {
			out := make([]string, 0, len(h.hashes))
			for clusterID := range h.hashes {
				out = append(out, clusterID)
			}
			return out
		}
	}
	return append([]string(nil), h.acl.Users[email]...)
}

func (h *handler) deny(w http.ResponseWriter, r *http.Request, reason string, status int, result string) {
	ingestRequests.WithLabelValues(result).Inc()
	if h.log != nil {
		h.log.Info("loki ingest denied",
			"event", "loki_ingest_denied",
			"reason", reason,
			"status", status,
			"path", r.URL.Path,
		)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"status":"error","error":"` + reason + `"}`))
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	const prefix = "bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}

// SelectOrg implements the allow-list org-selection rule (every caller):
//  1. client X-Scope-OrgID ∈ allow-list → use it
//  2. header missing and len(allow)==1 → use that
//  3. header missing and len≠1 → 400
//  4. client org outside the list → 401
//
// An empty allow-list is deny (401).
func SelectOrg(clientOrg string, allow []string) (org string, status int) {
	clientOrg = strings.TrimSpace(clientOrg)
	allow = uniqueFold(allow)
	if len(allow) == 0 {
		return "", http.StatusUnauthorized
	}
	inList := func(id string) bool {
		for _, a := range allow {
			if strings.EqualFold(strings.TrimSpace(a), id) {
				return true
			}
		}
		return false
	}
	if clientOrg == "" {
		if len(allow) == 1 {
			return allow[0], 0
		}
		return "", http.StatusBadRequest
	}
	if inList(clientOrg) {
		return clientOrg, 0
	}
	return "", http.StatusUnauthorized
}

func uniqueFold(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

func stripHopByHop(hdr http.Header) {
	for _, name := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade",
	} {
		hdr.Del(name)
	}
}
