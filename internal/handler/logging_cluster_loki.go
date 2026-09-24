package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Only an explicit Kubernetes service FQDN is routed over the owning cluster's
// tunnel. External URLs retain the dial-guarded HTTP client and its SSRF policy.
func (h *LoggingHandler) queryClusterLoki(ctx context.Context, clusterID string, configuration json.RawMessage, req loggingQueryRequest) (map[string]any, error) {
	cfg := decodeConfiguration(configuration)
	base, err := loggingHTTPBase(cfg, "url", "host", "3100")
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if !strings.HasSuffix(host, ".svc.cluster.local") && !strings.HasSuffix(host, ".svc") {
		return queryLokiOutput(ctx, configuration, req)
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(host, ".cluster.local"), ".svc"), ".")
	if len(parts) != 2 || len(validation.IsDNS1035Label(parts[0])) != 0 || len(validation.IsDNS1123Label(parts[1])) != 0 || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid cluster Loki service destination")
	}
	if h.requester == nil {
		return nil, fmt.Errorf("cluster Loki requester is not configured")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if !isValidServiceProxyPort(port) || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid cluster Loki service port or scheme")
	}
	values, start, end, limit, _ := lokiQueryValues(req)
	path := fmt.Sprintf("/api/v1/namespaces/%s/services/%s:%s:%s/proxy/loki/api/v1/query_range?%s", parts[1], u.Scheme, parts[0], port, values.Encode())
	// Scope and query authorization are enforced before choosing this transport.
	headers := requestHeaders("")
	headers["X-Scope-OrgID"] = configString(cfg, "tenant_id", "")
	authReq, _ := http.NewRequest(http.MethodGet, "http://cluster-loki", nil)
	setLoggingHTTPAuth(authReq, cfg, "query_token")
	if auth := authReq.Header.Get("Authorization"); auth != "" {
		headers["Authorization"] = auth
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, headers)
	if err != nil {
		return nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	body, err := decodeResponseBody(resp)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return map[string]any{"backend": "loki", "query": req.Query, "limit": limit, "start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339), "data": decoded}, nil
}
