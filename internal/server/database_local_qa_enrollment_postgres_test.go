//go:build unix && integration

package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This private fixture browser is ROOT-owned admin authority, not a product
// registry policy or artifact-selected grant. One literal HTTPS endpoint and
// one explicit address are captured; no redirects, DNS, proxy or insecure TLS.
func localQAEnrollCanonicalAgent(t *testing.T, ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, input localQARuntimeInput) uuid.UUID {
	t.Helper()
	endpoint, err := url.Parse(cfg.ServerURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.ForceQuery {
		t.Fatal("owned externally reachable private TLS server URL required")
	}
	pinned, err := netip.ParseAddrPort(input.ServerPinnedAddress)
	if err != nil || !pinned.Addr().IsValid() || pinned.Addr().IsUnspecified() || pinned.Addr().IsMulticast() {
		t.Fatal("owned TLS endpoint literal pinned address invalid")
	}
	port := 443
	if endpoint.Port() != "" {
		port, err = strconv.Atoi(endpoint.Port())
		if err != nil || port < 1024 || port > 65535 {
			t.Fatal("owned TLS endpoint port invalid")
		}
	}
	if int(pinned.Port()) != port {
		t.Fatal("owned TLS pin and endpoint port differ")
	}
	ca := localQAReadPrivateInput(t, input.ServerCAFile, 256<<10)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		clear(ca)
		t.Fatal("owned TLS server roots invalid")
	}
	defer clear(ca)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, ServerName: endpoint.Hostname()}, ResponseHeaderTimeout: 15 * time.Second, TLSHandshakeTimeout: 15 * time.Second, DisableCompression: true}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != net.JoinHostPort(endpoint.Hostname(), strconv.Itoa(port)) {
			return nil, os.ErrPermission
		}
		return dialer.DialContext(ctx, "tcp", pinned.String())
	}
	defer transport.CloseIdleConnections()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("owned canonical browser cookie capture unavailable")
	}
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var users, clusters int64
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.users),(SELECT count(*) FROM public.clusters)`).Scan(&users, &clusters); err != nil || users != 0 || clusters != 0 {
		t.Fatal("local QA fresh canonical enrollment requires empty owned identity estate")
	}
	if cfg.BootstrapAdminPassword == "" || cfg.BootstrapAdminEmail == "" || cfg.BootstrapAdminForcePasswordChange {
		t.Fatal("explicit owned typed bootstrap admin required without forced password challenge")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := sqlc.New(pool)
	if auth.EnsureBootstrapAdmin(ctx, q, auth.BootstrapAdminConfig{Password: cfg.BootstrapAdminPassword, Username: cfg.BootstrapAdminUsername, Email: cfg.BootstrapAdminEmail, ForcePasswordChange: false}, logger) != nil || auth.EnsurePlatformConfig(ctx, q, cfg.ServerURL, "", logger) != nil {
		t.Fatal("same production bootstrap admin/platform hooks failed")
	}
	request := func(method, path string, body any, expected int) []byte {
		t.Helper()
		var encoded []byte
		var e error
		if body != nil {
			encoded, e = json.Marshal(body)
			if e != nil {
				t.Fatal("local QA canonical browser request encoding failed")
			}
			defer clear(encoded)
		}
		bounded, stop := context.WithTimeout(ctx, 20*time.Second)
		defer stop()
		req, e := http.NewRequestWithContext(bounded, method, endpoint.String()+path, bytes.NewReader(encoded))
		if e != nil {
			t.Fatal("local QA canonical browser request invalid")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", endpoint.String())
		if method != http.MethodGet {
			for _, cookie := range jar.Cookies(endpoint) {
				if cookie.Name == auth.CSRFCookieName {
					req.Header.Set("X-CSRF-Token", cookie.Value)
				}
			}
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal("local QA actual private TLS API request failed")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(data) > 2<<20 || response.StatusCode != expected {
			clear(data)
			t.Fatal("local QA canonical API response/status invalid")
		}
		return data
	}
	// ROOT establishes the TLS proxy before this parent. A bounded readiness GET
	// never sends login credentials before the actual listener is serving.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	ready := false
	for !ready {
		bounded, stop := context.WithTimeout(ctx, 2*time.Second)
		req, e := http.NewRequestWithContext(bounded, http.MethodGet, endpoint.String()+"/api/v1/settings/branding/", nil)
		if e != nil {
			stop()
			t.Fatal("owned private TLS readiness URL invalid")
		}
		response, e := client.Do(req)
		if e == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			closeErr := response.Body.Close()
			ready = readErr == nil && closeErr == nil && response.StatusCode == http.StatusOK
		}
		stop()
		if !ready {
			select {
			case <-ctx.Done():
				t.Fatal("actual private TLS NewApp endpoint did not become ready")
			case <-ticker.C:
			}
		}
	}
	clear(request(http.MethodPost, "/api/v1/auth/login/", handler.LoginRequest{Email: cfg.BootstrapAdminEmail, Password: cfg.BootstrapAdminPassword}, http.StatusOK))
	session, csrf := false, false
	for _, cookie := range jar.Cookies(endpoint) {
		session = session || cookie.Name == auth.SessionCookieName
		csrf = csrf || cookie.Name == auth.CSRFCookieName
	}
	if !session || !csrf {
		t.Fatal("actual canonical browser login did not issue session and CSRF cookies")
	}
	clear(request(http.MethodPut, "/api/v1/admin/settings/registration.ca_bundle/", map[string]any{"value": string(ca)}, http.StatusOK))
	clear(request(http.MethodPut, "/api/v1/admin/settings/registration.tls_mode/", map[string]any{"value": "private_ca"}, http.StatusOK))
	if input.ClusterName == "" || len(input.ClusterName) > 63 {
		t.Fatal("owned canonical enrollment cluster name invalid")
	}
	annotations, e := json.Marshal(map[string]string{"astronomer.io/image-scanning": "enabled", agenttemplate.PrivilegeProfileAnnotation: agenttemplate.PrivilegeProfileAdmin})
	if e != nil {
		t.Fatal("canonical enrollment annotations invalid")
	}
	created := request(http.MethodPost, "/api/v1/clusters/", handler.CreateClusterRequest{Name: input.ClusterName, DisplayName: "LOCAL QA CERTIFICATE VERIFICATION", Annotations: annotations}, http.StatusCreated)
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(created, &response) != nil {
		clear(created)
		t.Fatal("actual canonical created cluster response invalid")
	}
	clear(created)
	cluster, e := uuid.Parse(response.Data.ID)
	if e != nil || cluster.Version() != 4 || cluster.Variant() != uuid.RFC4122 {
		t.Fatal("actual canonical enrollment returned invalid cluster ID")
	}
	clear(request(http.MethodPut, "/api/v1/clusters/"+cluster.String()+"/registration/options/", map[string]bool{"install_baseline": true}, http.StatusOK))
	manifest := request(http.MethodGet, "/api/v1/clusters/"+cluster.String()+"/manifest/", nil, http.StatusOK)
	defer clear(manifest)
	if len(manifest) == 0 || len(manifest) > 1<<20 || !bytes.Contains(manifest, []byte(cfg.ServerURL)) || !bytes.Contains(manifest, []byte(cluster.String())) || !bytes.Contains(manifest, []byte("ASTRONOMER_CA_CHECKSUM")) || !bytes.Contains(manifest, []byte(base64.StdEncoding.EncodeToString(ca))) || !bytes.Contains(manifest, []byte(cfg.AgentImageRepository)) {
		t.Fatal("canonical real agent installer lacks current private TLS/cluster inputs")
	}
	localQAWriteEnrollmentFile(t, input.InstallerManifest, manifest)
	receipt, e := json.Marshal(struct {
		Scope        string    `json:"scope"`
		SourceCommit string    `json:"source_commit"`
		ClusterID    uuid.UUID `json:"cluster_id"`
		ServerURL    string    `json:"server_url"`
		Installer    string    `json:"installer_manifest"`
	}{localQARuntimeLabel, input.SourceCommit, cluster, cfg.ServerURL, input.InstallerManifest})
	if e != nil {
		t.Fatal("actual enrollment DATA encoding failed")
	}
	localQAWriteEnrollmentFile(t, input.EnrollmentReceipt, receipt)
	return cluster
}

// ROOT waits for the complete enrollment receipt, applies ONLY the returned
// canonical manifest to its owned cluster, then atomically installs the actual
// Pod/runtime identity input. This is coordination DATA, never a scan witness.
func localQAAwaitAgentInput(t *testing.T, ctx context.Context) {
	t.Helper()
	path := os.Getenv("W04_LOCAL_QA_KUBERNETES_INPUT")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		t.Fatal("owned actual agent input path invalid")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := os.Lstat(path)
		if err == nil {
			bytes := localQAReadPrivateInput(t, path, 64<<10)
			clear(bytes)
			return
		}
		if !os.IsNotExist(err) {
			t.Fatal("owned agent input observation failed")
		}
		select {
		case <-ctx.Done():
			t.Fatal("ROOT actual enrolled agent identity input missing before deadline")
		case <-ticker.C:
		}
	}
}

func localQAWriteEnrollmentFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(contents) == 0 || len(contents) > 1<<20 {
		t.Fatal("owned canonical enrollment output invalid")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		t.Fatal("owned enrollment output parent invalid")
	}
	owner, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		t.Fatal("owned enrollment output parent owner differs")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".local-qa-enrollment-")
	if err != nil {
		t.Fatal("owned enrollment temporary output unavailable")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	initial, statErr := file.Stat()
	written, writeErr := file.Write(contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	if statErr != nil || writeErr != nil || written != len(contents) || syncErr != nil || closeErr != nil {
		t.Fatal("canonical enrollment private output incomplete")
	}
	current, err := os.Lstat(temporary)
	actualParent, parentErr := os.Lstat(filepath.Dir(path))
	if err != nil || parentErr != nil || !os.SameFile(initial, current) || !os.SameFile(parent, actualParent) || !current.Mode().IsRegular() || current.Mode().Perm() != 0600 || current.Size() != int64(len(contents)) {
		t.Fatal("owned enrollment output identity changed")
	}
	if err = os.Link(temporary, path); err != nil {
		t.Fatal("owned complete enrollment output must be absent")
	}
	if err = os.Remove(temporary); err != nil {
		t.Fatal("owned enrollment temporary link retained")
	}
	final, err := os.Lstat(path)
	if err != nil || !os.SameFile(initial, final) {
		t.Fatal("owned complete enrollment output changed")
	}
	stat, ok := final.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		t.Fatal("owned complete enrollment output owner/link differs")
	}
}
