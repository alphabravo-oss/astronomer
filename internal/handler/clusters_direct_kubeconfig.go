package handler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

const (
	directReaderNamespace      = "astronomer-system"
	directReaderServiceAccount = "astronomer-direct-reader"
	directCredentialTTL        = 15 * time.Minute
	directProbeTimeout         = 4 * time.Second
)

var directTokenRequestPath = "/api/v1/namespaces/" + directReaderNamespace +
	"/serviceaccounts/" + directReaderServiceAccount + "/token"

type directTokenRequest struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Spec       directTokenRequestSpec `json:"spec"`
}

type directTokenRequestSpec struct {
	ExpirationSeconds int64 `json:"expirationSeconds"`
}

type directTokenResponse struct {
	Status struct {
		Token               string    `json:"token"`
		ExpirationTimestamp time.Time `json:"expirationTimestamp"`
	} `json:"status"`
}

// validateDirectAccessConfig validates only durable, non-secret connection
// coordinates. Network reachability and certificate identity are rechecked at
// issuance time so stale DNS/firewall state cannot produce a misleading file.
func validateDirectAccessConfig(rawURL, caPEM string) error {
	rawURL = strings.TrimSpace(rawURL)
	caPEM = strings.TrimSpace(caPEM)
	if rawURL == "" {
		if caPEM != "" {
			return errors.New("ca_certificate requires api_server_url")
		}
		return nil
	}
	if len(rawURL) > 512 {
		return errors.New("api_server_url must be at most 512 bytes")
	}
	if len(caPEM) > 256*1024 {
		return errors.New("ca_certificate must be at most 256 KiB")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("api_server_url must be an absolute HTTPS Kubernetes API origin without credentials, path, query, or fragment")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || host == "kubernetes" || host == "kubernetes.default" || strings.HasSuffix(host, ".svc") || strings.HasSuffix(host, ".cluster.local") {
		return errors.New("api_server_url must be reachable outside the member cluster; in-cluster service names are not supported")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if port != "443" && port != "6443" {
		return errors.New("api_server_url port must be 443 or 6443")
	}
	if ip := net.ParseIP(host); ip != nil && unsafeDirectProbeIP(ip) {
		return errors.New("api_server_url cannot use a loopback, link-local, multicast, or unspecified address")
	}
	if caPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return errors.New("ca_certificate must contain at least one valid PEM certificate")
		}
	}
	return nil
}

func unsafeDirectProbeIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

// probeDirectEndpoint performs an unauthenticated TLS request. No newly minted
// or existing credential is ever sent during reachability validation.
func probeDirectEndpoint(ctx context.Context, rawURL, caPEM string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	var roots *x509.CertPool
	if strings.TrimSpace(caPEM) != "" {
		// A configured CA is an identity pin, not an optional extra root. Mixing
		// it with the OS pool would let a publicly trusted certificate succeed
		// even when the operator's member-cluster CA does not match.
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(caPEM)) {
			return errors.New("configured CA certificate is invalid")
		}
	} else {
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
	}
	dialer := &net.Dialer{Timeout: directProbeTimeout}
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: u.Hostname(),
		},
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			ips, lookupErr := net.DefaultResolver.LookupIP(dialCtx, "ip", host)
			if lookupErr != nil || len(ips) == 0 {
				return nil, fmt.Errorf("resolve endpoint: %w", lookupErr)
			}
			for _, ip := range ips {
				if unsafeDirectProbeIP(ip) {
					return nil, errors.New("endpoint resolved to a prohibited address")
				}
			}
			return dialer.DialContext(dialCtx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   directProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Kubernetes API endpoint redirects are not allowed")
		},
	}
	defer transport.CloseIdleConnections()
	probeURL := strings.TrimRight(u.String(), "/") + "/version"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("TLS reachability check failed: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("Kubernetes API endpoint returned %s", resp.Status)
	}
	return nil
}

// GenerateDirectKubeconfig handles
// POST /api/v1/clusters/{id}/generate-direct-kubeconfig/.
//
// It mints a non-renewable, 15-minute TokenRequest for the dedicated
// read-only ServiceAccount. Registration, agent identity and administrator
// tokens are never read or reused by this path.
func (h *ClusterHandler) GenerateDirectKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if cluster.IsLocal {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Direct kubeconfig is available only for adopted clusters")
		return
	}
	if err := validateDirectAccessConfig(cluster.ApiServerUrl, cluster.CaCertificate); err != nil || strings.TrimSpace(cluster.ApiServerUrl) == "" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A valid external Kubernetes API endpoint must be configured before direct access can be issued")
		return
	}
	if h.directRequester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "Direct credential issuer is unavailable")
		return
	}
	intentDetail := map[string]any{
		"mode": "direct", "access": "read_only", "service_account": directReaderServiceAccount,
		"ttl_seconds": int64(directCredentialTTL / time.Second), "endpoint_host": endpointHostPort(cluster.ApiServerUrl),
	}
	// Persist a durable intent before either the network probe or the remote
	// TokenRequest. Audit failure therefore guarantees zero remote calls.
	if err := h.recordDirectKubeconfigAudit(r, cluster, "cluster.direct_kubeconfig.issue_requested", http.StatusAccepted, intentDetail); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable; no direct credential request was sent")
		return
	}
	remoteRequested := false
	recordOutcome := func(outcome string, status int, extra map[string]any) bool {
		detail := map[string]any{
			"mode": "direct", "access": "read_only", "service_account": directReaderServiceAccount,
			"outcome": outcome, "endpoint_host": endpointHostPort(cluster.ApiServerUrl),
		}
		for key, value := range extra {
			detail[key] = value
		}
		if err := h.recordDirectKubeconfigAudit(r, cluster, "cluster.direct_kubeconfig.issue_completed", status, detail); err != nil {
			message := "Mandatory audit storage became unavailable; no credential was returned"
			if remoteRequested {
				message = "Mandatory audit storage became unavailable; a credential may have been minted but was not returned. Wait for the 15-minute credential window to expire before retrying"
			}
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, message)
			return false
		}
		return true
	}
	if err := probeDirectEndpoint(r.Context(), cluster.ApiServerUrl, cluster.CaCertificate); err != nil {
		if !recordOutcome("preflight_failed", http.StatusServiceUnavailable, nil) {
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "The configured Kubernetes API endpoint is not reachable with its configured TLS identity")
		return
	}

	requestBody, _ := json.Marshal(directTokenRequest{
		APIVersion: "authentication.k8s.io/v1",
		Kind:       "TokenRequest",
		Spec:       directTokenRequestSpec{ExpirationSeconds: int64(directCredentialTTL / time.Second)},
	})
	remoteRequested = true
	response, err := h.directRequester.Do(r.Context(), cluster.ID.String(), http.MethodPost, directTokenRequestPath, requestBody, map[string]string{"Content-Type": "application/json"})
	if err != nil || response == nil {
		if !recordOutcome("request_failed", http.StatusServiceUnavailable, nil) {
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "The connected cluster agent cannot issue a scoped direct credential")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if !recordOutcome("denied", http.StatusServiceUnavailable, map[string]any{"member_status": response.StatusCode}) {
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "The member cluster denied the scoped direct credential request; update its agent manifest")
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Body)
	if err != nil {
		if !recordOutcome("invalid_response", http.StatusServiceUnavailable, nil) {
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "The member cluster returned an invalid credential response")
		return
	}
	var tokenResponse directTokenResponse
	if err := json.Unmarshal(decoded, &tokenResponse); err != nil || strings.TrimSpace(tokenResponse.Status.Token) == "" {
		if !recordOutcome("invalid_response", http.StatusServiceUnavailable, nil) {
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "The member cluster returned an invalid credential response")
		return
	}
	now := time.Now().UTC()
	expiresAt := tokenResponse.Status.ExpirationTimestamp.UTC()
	if expiresAt.Before(now.Add(time.Minute)) || expiresAt.After(now.Add(directCredentialTTL+time.Minute)) {
		if !recordOutcome("invalid_expiry", http.StatusServiceUnavailable, nil) {
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "The member cluster returned a credential outside the allowed lifetime")
		return
	}

	kubeconfig := buildDirectKubeconfig(cluster, tokenResponse.Status.Token)
	yamlBytes, err := yaml.Marshal(kubeconfig)
	if err != nil {
		if !recordOutcome("render_failed", http.StatusInternalServerError, nil) {
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RenderError, "Failed to render kubeconfig")
		return
	}
	if !recordOutcome("issued", http.StatusOK, map[string]any{"expires_at": expiresAt.Format(time.RFC3339)}) {
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-direct-kubeconfig.yaml"`, cluster.Name))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Astronomer-Kubeconfig-Mode", "direct")
	w.Header().Set("X-Astronomer-Credential-Expires-At", expiresAt.Format(time.RFC3339))
	_, _ = w.Write(yamlBytes)
}

func (h *ClusterHandler) recordDirectKubeconfigAudit(r *http.Request, cluster sqlc.Cluster, action string, status int, detail map[string]any) error {
	if h == nil || h.runTx == nil {
		return audit.ErrOutboxUnavailable
	}
	return h.runTx(r.Context(), func(q ClusterMutationTx) error {
		return recordAuditOutbox(r, q, action, "cluster", cluster.ID.String(), cluster.Name, status, detail)
	})
}

func buildDirectKubeconfig(cluster sqlc.Cluster, token string) map[string]any {
	clusterConfig := map[string]any{"server": strings.TrimRight(cluster.ApiServerUrl, "/")}
	if ca := strings.TrimSpace(cluster.CaCertificate); ca != "" {
		clusterConfig["certificate-authority-data"] = base64.StdEncoding.EncodeToString([]byte(ca))
	}
	contextName := cluster.Name + "-direct"
	return map[string]any{
		"apiVersion": "v1", "kind": "Config",
		"clusters":        []map[string]any{{"name": cluster.Name, "cluster": clusterConfig}},
		"contexts":        []map[string]any{{"name": contextName, "context": map[string]any{"cluster": cluster.Name, "user": directReaderServiceAccount}}},
		"current-context": contextName,
		"users":           []map[string]any{{"name": directReaderServiceAccount, "user": map[string]any{"token": token}}},
	}
}

func endpointHostPort(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "invalid"
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(mustPort(port)))
}

func mustPort(raw string) int {
	port, _ := strconv.Atoi(raw)
	return port
}
