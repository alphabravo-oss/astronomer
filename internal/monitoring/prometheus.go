package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

type BackendConfig struct {
	QueryURL string
	TenantID string
	AuthType string
	// AuthConfig is the stored auth_config JSONB column: the non-secret
	// projection for a sealed row, or the complete document for a row that
	// predates migration 146.
	AuthConfig json.RawMessage
	// AuthConfigSealed is the auth_config_* sealed column family. When
	// non-zero it is authoritative and Decryptor must be able to open it.
	//
	// Both halves live on this struct — rather than the caller resolving and
	// passing one document — because this is the ONLY live reader of the
	// monitoring credential and there are five construction sites across two
	// processes. A site that forgot to resolve would authenticate with the
	// stripped projection and get an upstream 401 that reads like a wrong
	// password; a site that cannot forget to pass a column it must copy off
	// the same row cannot make that mistake.
	AuthConfigSealed crypto.Sealed
	// Decryptor opens AuthConfigSealed. Nil-safe: a nil decryptor reads
	// unsealed rows fine and declines to authenticate sealed ones.
	Decryptor Decryptor
	// Logger records a decrypt failure. Optional; slog.Default() otherwise.
	Logger             *slog.Logger
	DefaultStepSeconds int32
	TimeoutSeconds     int32
}

type TimeSeriesPoint struct {
	Timestamp string
	Value     float64
}

type Client struct {
	baseURL    string
	tenantID   string
	authType   string
	authConfig map[string]any
	httpClient *http.Client
}

func NewClient(cfg BackendConfig) (*Client, error) {
	baseURL := strings.TrimRight(cfg.QueryURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("query url is required")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// Migration 146: the credential lives in the sealed value whenever
	// auth_config_key_id is non-empty.
	//
	// A decrypt failure yields NO credential and a logged error — never the
	// ciphertext, and never a hard failure of the client. Returning an error
	// here would take every dashboard, alert evaluation and reconcile tick
	// down over a key-management problem; sending the ciphertext as a password
	// (the shape decryptGitAuth in gitops_sync.go has) would report a key
	// problem as an upstream authentication failure. The backend answers 401,
	// the reconciler records the backend as degraded, and the log line says
	// what actually happened.
	resolved, resolveErr := ResolveAuthConfig(cfg.AuthConfigSealed, cfg.AuthConfig, cfg.Decryptor)
	if resolveErr != nil {
		logger := cfg.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("monitoring backend request sent unauthenticated: credential could not be decrypted",
			"query_url", baseURL, "error", resolveErr)
		resolved = nil
	}
	authCfg := map[string]any{}
	if len(resolved) > 0 {
		if err := json.Unmarshal(resolved, &authCfg); err != nil {
			return nil, fmt.Errorf("decode auth config: %w", err)
		}
	}
	// SEC-R04: dial-guarded client. Prometheus backends are typically
	// in-cluster (private), so AllowPrivate is on — loopback, link-local,
	// and cloud metadata (169.254.169.254) remain blocked.
	return &Client{
		baseURL:    baseURL,
		tenantID:   cfg.TenantID,
		authType:   cfg.AuthType,
		authConfig: authCfg,
		httpClient: httpclient.SafeClientAllowPrivate(timeout),
	}, nil
}

func (c *Client) QueryScalar(ctx context.Context, promQL string) (float64, error) {
	data, err := c.doQuery(ctx, "/api/v1/query", url.Values{"query": []string{promQL}})
	if err != nil {
		return 0, err
	}
	return scalarFromResult(data)
}

func (c *Client) QueryRange(ctx context.Context, promQL string, start, end time.Time, step time.Duration) ([]TimeSeriesPoint, error) {
	if step <= 0 {
		step = time.Minute
	}
	values := url.Values{
		"query": []string{promQL},
		"start": []string{strconv.FormatInt(start.Unix(), 10)},
		"end":   []string{strconv.FormatInt(end.Unix(), 10)},
		"step":  []string{strconv.Itoa(int(step.Seconds()))},
	}
	data, err := c.doQuery(ctx, "/api/v1/query_range", values)
	if err != nil {
		return nil, err
	}
	return matrixFromResult(data)
}

func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/-/healthy", nil)
	if err != nil {
		return err
	}
	if c.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.tenantID)
	}
	switch strings.ToLower(c.authType) {
	case "bearer":
		if token, ok := c.authConfig["token"].(string); ok && token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "basic":
		username, _ := c.authConfig["username"].(string)
		password, _ := c.authConfig["password"].(string)
		req.SetBasicAuth(username, password)
	}
	resp, err := c.httpClient.Do(req)
	if err == nil {
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode < http.StatusBadRequest {
			return nil
		}
		if resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("prometheus backend health returned HTTP status %d", resp.StatusCode)
		}
	}
	_, err = c.QueryScalar(ctx, "vector(1)")
	return err
}

func (c *Client) doQuery(ctx context.Context, path string, values url.Values) (promResponseData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?"+values.Encode(), nil)
	if err != nil {
		return promResponseData{}, err
	}
	req.Header.Set("Accept", "application/json")
	if c.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.tenantID)
	}
	switch strings.ToLower(c.authType) {
	case "bearer":
		if token, ok := c.authConfig["token"].(string); ok && token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "basic":
		username, _ := c.authConfig["username"].(string)
		password, _ := c.authConfig["password"].(string)
		req.SetBasicAuth(username, password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return promResponseData{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return promResponseData{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return promResponseData{}, fmt.Errorf("prometheus backend returned HTTP status %d", resp.StatusCode)
	}
	var payload promResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return promResponseData{}, err
	}
	if payload.Status != "success" {
		return promResponseData{}, fmt.Errorf("prometheus query failed (upstream error body omitted)")
	}
	return payload.Data, nil
}

type promResponse struct {
	Status string           `json:"status"`
	Data   promResponseData `json:"data"`
	Error  string           `json:"error"`
}

type promResponseData struct {
	ResultType string            `json:"resultType"`
	Result     []json.RawMessage `json:"result"`
}

func scalarFromResult(data promResponseData) (float64, error) {
	if len(data.Result) == 0 {
		return 0, nil
	}
	var vector struct {
		Value []any `json:"value"`
	}
	if err := json.Unmarshal(data.Result[0], &vector); err != nil {
		return 0, err
	}
	if len(vector.Value) < 2 {
		return 0, nil
	}
	return parsePromNumber(vector.Value[1])
}

func matrixFromResult(data promResponseData) ([]TimeSeriesPoint, error) {
	if len(data.Result) == 0 {
		return []TimeSeriesPoint{}, nil
	}
	var matrix struct {
		Values [][]any `json:"values"`
	}
	if err := json.Unmarshal(data.Result[0], &matrix); err != nil {
		return nil, err
	}
	points := make([]TimeSeriesPoint, 0, len(matrix.Values))
	for _, item := range matrix.Values {
		if len(item) < 2 {
			continue
		}
		ts, err := parsePromNumber(item[0])
		if err != nil {
			return nil, err
		}
		value, err := parsePromNumber(item[1])
		if err != nil {
			return nil, err
		}
		points = append(points, TimeSeriesPoint{
			Timestamp: time.Unix(int64(ts), 0).UTC().Format(time.RFC3339),
			Value:     value,
		})
	}
	return points, nil
}

func parsePromNumber(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case string:
		return strconv.ParseFloat(v, 64)
	case json.Number:
		return v.Float64()
	default:
		return 0, fmt.Errorf("unexpected prometheus number type %T", raw)
	}
}
