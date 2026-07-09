package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

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
	host := configString(cfg, "host", "")
	if host == "" {
		return nil, fmt.Errorf("loki output has no host configured")
	}
	port := configString(cfg, "port", "3100")
	scheme := configString(cfg, "scheme", "http")
	if scheme != "http" && scheme != "https" {
		scheme = "http"
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
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	end := time.Now().UTC()
	start := end.Add(-1 * time.Hour)
	if t, ok := parseLokiTime(req.End); ok {
		end = t
	}
	if t, ok := parseLokiTime(req.Start); ok {
		start = t
	}
	direction := strings.ToLower(strings.TrimSpace(req.Direction))
	if direction != "forward" {
		direction = "backward"
	}

	base := fmt.Sprintf("%s://%s:%s", scheme, host, port)
	u, err := url.Parse(base + "/loki/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("build loki url: %w", err)
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("direction", direction)
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if tenant := configString(cfg, "tenant_id", ""); tenant != "" {
		httpReq.Header.Set("X-Scope-OrgID", tenant)
	}
	if user := configString(cfg, "http_user", ""); user != "" {
		httpReq.SetBasicAuth(user, configString(cfg, "http_passwd", ""))
	}

	client := httpclient.SafeClient(30 * time.Second)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("loki query: %w", err)
	}
	defer resp.Body.Close()
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
