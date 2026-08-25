package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// loggingOutputCapabilities is returned with every logging output. Callers can
// render only supported actions instead of probing endpoints and discovering
// limitations through 501 responses.
type loggingOutputCapabilities struct {
	Ship                bool   `json:"ship"`
	Test                bool   `json:"test"`
	Query               bool   `json:"query"`
	Tail                bool   `json:"tail"`
	Aggregate           bool   `json:"aggregate"`
	LinkOut             bool   `json:"link_out"`
	LinkOutURL          string `json:"link_out_url,omitempty"`
	RetentionVisibility bool   `json:"retention_visibility"`
	QueryMode           string `json:"query_mode"`
}

func loggingCapabilitiesFor(output sqlc.LoggingOutput) loggingOutputCapabilities {
	typeName := strings.ToLower(strings.TrimSpace(output.OutputType))
	capabilities := loggingOutputCapabilities{Ship: true, Test: true, QueryMode: "shipping_only"}
	switch typeName {
	case "loki":
		capabilities.Query = true
		capabilities.Tail = true
		capabilities.Aggregate = true
		capabilities.LinkOut = true
		capabilities.QueryMode = "native"
		capabilities.RetentionVisibility = output.IsSystem
	case "elasticsearch", "opensearch":
		capabilities.Query = true
		capabilities.Aggregate = true
		capabilities.LinkOut = true
		capabilities.QueryMode = "native"
	case "splunk":
		// HEC-only configurations cannot query. An explicit management_url
		// plus query credentials upgrades the output to native query mode.
		cfg := decodeConfiguration(output.Configuration)
		capabilities.Query = configString(cfg, "management_url", "") != "" &&
			(configString(cfg, "query_token", "") != "" || configString(cfg, "username", "") != "")
		capabilities.Aggregate = capabilities.Query
		capabilities.LinkOut = true
		if capabilities.Query {
			capabilities.QueryMode = "native"
		} else {
			capabilities.QueryMode = "link_out"
		}
	case "datadog", "cloudwatch":
		capabilities.LinkOutURL = loggingLinkOutURL(output)
		capabilities.LinkOut = capabilities.LinkOutURL != ""
		if capabilities.LinkOut {
			capabilities.QueryMode = "link_out"
		}
	case "s3", "syslog":
		// Deliberately shipping-only: neither destination is a bounded
		// interactive query service.
	}
	return capabilities
}

var datadogConsoleHosts = map[string]string{
	"datadoghq.com":     "app.datadoghq.com",
	"us3.datadoghq.com": "us3.datadoghq.com",
	"us5.datadoghq.com": "us5.datadoghq.com",
	"datadoghq.eu":      "app.datadoghq.eu",
	"ddog-gov.com":      "app.ddog-gov.com",
	"us2.ddog-gov.com":  "us2.ddog-gov.com",
	"ap1.datadoghq.com": "ap1.datadoghq.com",
	"ap2.datadoghq.com": "ap2.datadoghq.com",
	"uk1.datadoghq.com": "uk1.datadoghq.com",
	"app.datadoghq.com": "app.datadoghq.com",
	"app.datadoghq.eu":  "app.datadoghq.eu",
	"app.ddog-gov.com":  "app.ddog-gov.com",
}

var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z]+-\d+$`)
var loggingLinkTagPattern = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)

// loggingLinkOutURL builds a browser destination from a small allowlist of
// official console hosts. It deliberately ignores credentials and arbitrary
// configuration URLs: a logging output must never turn its API response into
// an open redirect or leak a token through a query string.
func loggingLinkOutURL(output sqlc.LoggingOutput) string {
	cfg := decodeConfiguration(output.Configuration)
	switch strings.ToLower(strings.TrimSpace(output.OutputType)) {
	case "datadog":
		site := strings.ToLower(strings.TrimSpace(configString(cfg, "site", "datadoghq.com")))
		host, ok := datadogConsoleHosts[site]
		if !ok {
			return ""
		}
		terms := make([]string, 0, 3)
		if output.ClusterID.Valid {
			terms = append(terms, "cluster:"+uuid.UUID(output.ClusterID.Bytes).String())
		}
		for _, key := range []string{"service", "source"} {
			if value := strings.TrimSpace(configString(cfg, key, "")); loggingLinkTagPattern.MatchString(value) {
				terms = append(terms, key+":"+value)
			}
		}
		u := url.URL{Scheme: "https", Host: host, Path: "/logs"}
		if len(terms) > 0 {
			u.RawQuery = url.Values{"query": {strings.Join(terms, " ")}}.Encode()
		}
		return u.String()
	case "cloudwatch":
		region := strings.ToLower(strings.TrimSpace(configString(cfg, "region", "")))
		if !awsRegionPattern.MatchString(region) {
			return ""
		}
		partition := strings.ToLower(strings.TrimSpace(configString(cfg, "partition", "aws")))
		host := ""
		switch partition {
		case "aws":
			host = region + ".console.aws.amazon.com"
		case "aws-us-gov":
			if !strings.HasPrefix(region, "us-gov-") {
				return ""
			}
			host = "console.amazonaws-us-gov.com"
		case "aws-cn":
			if !strings.HasPrefix(region, "cn-") {
				return ""
			}
			host = "console.amazonaws.cn"
		default:
			return ""
		}
		u := url.URL{
			Scheme:   "https",
			Host:     host,
			Path:     "/cloudwatch/home",
			RawQuery: url.Values{"region": {region}}.Encode(),
			Fragment: "logsV2:logs-insights",
		}
		return u.String()
	default:
		return ""
	}
}

func (h *LoggingHandler) queryLoggingOutput(ctx context.Context, output sqlc.LoggingOutput, req loggingQueryRequest) (map[string]any, error) {
	clusterID := ""
	if output.ClusterID.Valid {
		clusterID = uuid.UUID(output.ClusterID.Bytes).String()
	}
	if clusterID == "" {
		return nil, fmt.Errorf("queryable logging output has no owning cluster")
	}
	switch strings.ToLower(strings.TrimSpace(output.OutputType)) {
	case "loki":
		query, err := scopeLogQL(req.Query, clusterID, req.Namespaces)
		if err != nil {
			return nil, err
		}
		req.Query = query
		if output.IsSystem {
			return h.querySystemLoki(ctx, clusterID, req)
		}
		return queryLokiOutput(ctx, output.Configuration, req)
	case "elasticsearch", "opensearch":
		return queryElasticsearchOutput(ctx, output.Configuration, clusterID, req)
	case "splunk":
		return querySplunkOutput(ctx, output.Configuration, clusterID, req)
	default:
		return nil, fmt.Errorf("logging output type %q does not support native queries", output.OutputType)
	}
}

var logQLSelectorPattern = regexp.MustCompile(`\{([^{}]*)\}`)
var logQLClusterMatcherPattern = regexp.MustCompile(`(?:^|,)\s*cluster\s*(=|!=|=~|!~)\s*"([^"]*)"`)

// scopeLogQL binds every stream selector to the output's owning cluster and
// optional namespaces. Tenant selection at the Loki proxy is the primary
// isolation boundary; the label rewrite is defense in depth and keeps saved
// queries portable across system and BYO Loki outputs.
func scopeLogQL(raw, clusterID string, namespaces []string) (string, error) {
	query := strings.TrimSpace(raw)
	if query == "" {
		query = `{job=~".+"}`
	}
	if !logQLSelectorPattern.MatchString(query) {
		return "", fmt.Errorf("LogQL query must contain at least one stream selector")
	}
	if len(namespaces) > 20 {
		return "", fmt.Errorf("at most 20 namespaces may be queried at once")
	}
	nsMatchers := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" {
			continue
		}
		if !isSafeK8sName(namespace) {
			return "", fmt.Errorf("invalid namespace %q", namespace)
		}
		nsMatchers = append(nsMatchers, regexp.QuoteMeta(namespace))
	}
	var rewriteErr error
	query = logQLSelectorPattern.ReplaceAllStringFunc(query, func(selector string) string {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(selector, "{"), "}"))
		if match := logQLClusterMatcherPattern.FindStringSubmatch(inner); len(match) == 3 {
			if match[1] != "=" || match[2] != clusterID {
				rewriteErr = fmt.Errorf("cluster matcher must equal the authorized cluster")
				return selector
			}
		} else {
			if inner != "" {
				inner += ","
			}
			inner += `cluster="` + clusterID + `"`
		}
		if len(nsMatchers) > 0 && !regexp.MustCompile(`(?:^|,)\s*namespace\s*`).MatchString(inner) {
			inner += `,namespace=~"` + strings.Join(nsMatchers, "|") + `"`
		}
		return "{" + inner + "}"
	})
	if rewriteErr != nil {
		return "", rewriteErr
	}
	return query, nil
}

func normalizedLokiWindow(req loggingQueryRequest) (start, end time.Time, limit int, direction string) {
	limit = req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	end = time.Now().UTC()
	start = end.Add(-time.Hour)
	if parsed, ok := parseLokiTime(req.End); ok {
		end = parsed
	}
	if parsed, ok := parseLokiTime(req.Start); ok {
		start = parsed
	}
	if !start.Before(end) {
		start = end.Add(-time.Hour)
	}
	direction = strings.ToLower(strings.TrimSpace(req.Direction))
	if direction != "forward" {
		direction = "backward"
	}
	return
}

func lokiQueryValues(req loggingQueryRequest) (url.Values, time.Time, time.Time, int, string) {
	start, end, limit, direction := normalizedLokiWindow(req)
	values := url.Values{}
	values.Set("query", req.Query)
	values.Set("limit", strconv.Itoa(limit))
	values.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	values.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	values.Set("direction", direction)
	return values, start, end, limit, direction
}

func (h *LoggingHandler) querySystemLoki(ctx context.Context, clusterID string, req loggingQueryRequest) (map[string]any, error) {
	if h.requester == nil || h.lokiAttach == nil {
		return nil, fmt.Errorf("Astronomer Loki query proxy is not configured")
	}
	state := h.lokiAttach.LokiAttachState(ctx)
	if !strings.EqualFold(state.Status, "healthy") || state.ManagementClusterID == "" {
		return nil, fmt.Errorf("Astronomer Loki is not ready for queries")
	}
	namespace := defaultString(state.Namespace, "monitoring")
	release := defaultString(state.ReleaseName, sharedLokiDefaultRelease)
	if !isSafeK8sName(namespace) || !isSafeK8sName(release) || !isSafeK8sName(state.ManagementClusterID) {
		// ManagementClusterID is a UUID, which satisfies the same restricted
		// character set. Reject malformed persisted metadata fail-closed.
		return nil, fmt.Errorf("Astronomer Loki query target metadata is invalid")
	}
	values, start, end, limit, _ := lokiQueryValues(req)
	service := release + "-gateway"
	path := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:80/proxy/loki/api/v1/query_range?%s",
		namespace, service, values.Encode())
	headers := requestHeaders("")
	headers["X-Scope-OrgID"] = clusterID
	resp, err := h.requester.Do(ctx, state.ManagementClusterID, http.MethodGet, path, nil, headers)
	if err != nil {
		return nil, fmt.Errorf("query Astronomer Loki: %w", err)
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, fmt.Errorf("query Astronomer Loki: %w", err)
	}
	body, err := decodeResponseBody(resp)
	if err != nil {
		return nil, fmt.Errorf("decode Astronomer Loki response: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode Astronomer Loki JSON: %w", err)
	}
	return map[string]any{
		"backend": "astronomer_loki", "query": req.Query, "limit": limit,
		"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339), "data": decoded,
	}, nil
}

func queryElasticsearchOutput(ctx context.Context, configuration json.RawMessage, clusterID string, req loggingQueryRequest) (map[string]any, error) {
	cfg := decodeConfiguration(configuration)
	base, err := loggingHTTPBase(cfg, "url", "host", "9200")
	if err != nil {
		return nil, err
	}
	index := strings.Trim(configString(cfg, "index", "kubernetes-logs-*"), "/")
	if index == "" || strings.Contains(index, "..") {
		return nil, fmt.Errorf("invalid Elasticsearch index")
	}
	start, end, limit, _ := normalizedLokiWindow(req)
	filters := []any{
		map[string]any{"term": map[string]any{"cluster.keyword": clusterID}},
		map[string]any{"range": map[string]any{"@timestamp": map[string]any{"gte": start.Format(time.RFC3339Nano), "lte": end.Format(time.RFC3339Nano)}}},
	}
	if len(req.Namespaces) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{"namespace.keyword": safeNamespaces(req.Namespaces)}})
	}
	must := []any{}
	if query := strings.TrimSpace(req.Query); query != "" {
		must = append(must, map[string]any{"query_string": map[string]any{"query": query, "analyze_wildcard": true}})
	}
	body, _ := json.Marshal(map[string]any{
		"size":  limit,
		"sort":  []any{map[string]any{"@timestamp": map[string]any{"order": "desc"}}},
		"query": map[string]any{"bool": map[string]any{"filter": filters, "must": must}},
	})
	u := strings.TrimRight(base, "/") + "/" + url.PathEscape(index) + "/_search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	setLoggingHTTPAuth(httpReq, cfg, "")
	decoded, err := executeLoggingHTTP(httpReq, "Elasticsearch")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"backend": strings.ToLower(configString(cfg, "distribution", "elasticsearch")),
		"query":   req.Query, "limit": limit, "start": start.Format(time.RFC3339),
		"end": end.Format(time.RFC3339), "data": decoded,
	}, nil
}

func querySplunkOutput(ctx context.Context, configuration json.RawMessage, clusterID string, req loggingQueryRequest) (map[string]any, error) {
	cfg := decodeConfiguration(configuration)
	base := strings.TrimRight(configString(cfg, "management_url", ""), "/")
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("Splunk query requires a valid management_url")
	}
	start, end, limit, _ := normalizedLokiWindow(req)
	search := strings.TrimSpace(req.Query)
	if search == "" {
		search = "*"
	}
	search = strings.ReplaceAll(search, `"`, `\"`)
	search = `search (` + search + `) cluster="` + clusterID + `"`
	if namespaces := safeNamespaces(req.Namespaces); len(namespaces) > 0 {
		quoted := make([]string, 0, len(namespaces))
		for _, namespace := range namespaces {
			quoted = append(quoted, `namespace="`+namespace+`"`)
		}
		search += " (" + strings.Join(quoted, " OR ") + ")"
	}
	form := url.Values{
		"search":        []string{search},
		"output_mode":   []string{"json"},
		"earliest_time": []string{start.Format(time.RFC3339)},
		"latest_time":   []string{end.Format(time.RFC3339)},
		"count":         []string{strconv.Itoa(limit)},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/services/search/v2/jobs/export", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setLoggingHTTPAuth(httpReq, cfg, "query_token")
	decoded, err := executeLoggingHTTP(httpReq, "Splunk")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"backend": "splunk", "query": req.Query, "limit": limit,
		"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339), "data": decoded,
	}, nil
}

func loggingHTTPBase(cfg map[string]any, urlKey, hostKey, defaultPort string) (string, error) {
	if raw := strings.TrimSpace(configString(cfg, urlKey, "")); raw != "" {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return "", fmt.Errorf("invalid logging backend URL")
		}
		return strings.TrimRight(raw, "/"), nil
	}
	host := strings.TrimSpace(configString(cfg, hostKey, ""))
	if host == "" {
		return "", fmt.Errorf("logging backend has no host configured")
	}
	port := configString(cfg, "port", defaultPort)
	scheme := strings.ToLower(configString(cfg, "scheme", "https"))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("invalid logging backend URL scheme")
	}
	return scheme + "://" + host + ":" + port, nil
}

func setLoggingHTTPAuth(req *http.Request, cfg map[string]any, tokenKey string) {
	if tokenKey != "" {
		if token := configString(cfg, tokenKey, ""); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			return
		}
	}
	user := configString(cfg, "username", configString(cfg, "http_user", ""))
	password := configString(cfg, "password", configString(cfg, "http_passwd", ""))
	if user != "" {
		req.SetBasicAuth(user, password)
	}
}

func executeLoggingHTTP(req *http.Request, backend string) (any, error) {
	resp, err := httpclient.SafeClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s query: %w", backend, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", backend, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s returned %d: %s", backend, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		// Splunk export may return newline-delimited JSON. Preserve it as
		// bounded text for the normalization layer instead of dropping data.
		return map[string]any{"raw": string(body)}, nil
	}
	return decoded, nil
}

func safeNamespaces(namespaces []string) []string {
	out := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		namespace = strings.TrimSpace(namespace)
		if namespace != "" && isSafeK8sName(namespace) {
			out = append(out, namespace)
		}
	}
	return out
}

func recordLoggingQueryAudit(r *http.Request, queries any, output sqlc.LoggingOutput, req loggingQueryRequest) {
	digest := sha256.Sum256([]byte(req.Query))
	clusterID := ""
	if output.ClusterID.Valid {
		clusterID = uuid.UUID(output.ClusterID.Bytes).String()
	}
	recordAudit(r, queries, "logging.output.query", "logging_output", output.ID.String(), output.Name, map[string]any{
		"cluster_id": clusterID, "provider": strings.ToLower(output.OutputType),
		"query_sha256": fmt.Sprintf("%x", digest[:]), "limit": req.Limit,
		"namespace_count": len(req.Namespaces),
	})
}

// queryLokiOutput runs a LogQL query against a Loki output configuration
// (DIR-06). Host/port (and optional scheme/tenant_id) come from the
// operator-supplied configuration JSON; the dial goes through SafeClient
// so private/loopback SSRF targets are refused.
func queryLokiOutput(ctx context.Context, configuration json.RawMessage, req loggingQueryRequest) (map[string]any, error) {
	cfg := map[string]any{}
	if len(configuration) > 0 {
		if err := json.Unmarshal(configuration, &cfg); err != nil {
			return nil, fmt.Errorf("decode output configuration: %w", err)
		}
	}
	base, err := loggingHTTPBase(cfg, "url", "host", "3100")
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		// Default to a broad stream selector when labels are configured.
		if labels := configString(cfg, "labels", ""); labels != "" {
			query = "{" + labels + "}"
		} else {
			query = `{job=~".+"}`
		}
	}
	values, start, end, limit, _ := lokiQueryValues(req)
	u, err := url.Parse(strings.TrimRight(base, "/") + "/loki/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("build loki url: %w", err)
	}
	u.RawQuery = values.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if tenant := configString(cfg, "tenant_id", ""); tenant != "" {
		httpReq.Header.Set("X-Scope-OrgID", tenant)
	}
	setLoggingHTTPAuth(httpReq, cfg, "query_token")

	client := httpclient.SafeClient(30 * time.Second)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("loki query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read loki response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("loki returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode loki response: %w", err)
	}
	return map[string]any{
		"backend": "loki",
		"query":   query,
		"limit":   limit,
		"start":   start.Format(time.RFC3339),
		"end":     end.Format(time.RFC3339),
		"data":    decoded,
	}, nil
}

func parseLokiTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		// Loki accepts ns; also accept seconds for operator convenience.
		if n < 1e12 {
			return time.Unix(n, 0).UTC(), true
		}
		return time.Unix(0, n).UTC(), true
	}
	return time.Time{}, false
}
