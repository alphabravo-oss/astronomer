// Package argocd contains a small typed client for the upstream ArgoCD HTTP
// API. It is intentionally narrow: just enough surface area for the reconciler
// in internal/handler/argocd.go to drive a real sync, observe its progress,
// and surface useful response fields back into our argocd_operations table.
//
// The client is constructed per-request from a stored argocd_instances row;
// it is not a singleton. All methods are context-aware, return typed errors
// on non-2xx responses, and never panic.
package argocd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/alphabravocompany/astronomer-go/internal/argosecurity"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Default timeout for all client requests. Kept short because the reconciler
// loop holds its own mutex while these calls are in flight.
const DefaultTimeout = 10 * time.Second

const maxResponseDrainBytes = 64 << 10

const responseBodyLimitMessage = "Argo CD response exceeds the 16 MiB limit"

var (
	clientTracer = otel.Tracer("astronomer/argocd-client")

	clientMetricsOnce = sync.Once{}

	clientRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Subsystem: "argocd_client",
			Name:      "requests_total",
			Help:      "Total upstream Argo CD API requests by method, path family, and status.",
		},
		observability.MetricLabels("method", "path_family", "status"),
	)
	clientRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "astronomer",
			Subsystem: "argocd_client",
			Name:      "request_duration_seconds",
			Help:      "Upstream Argo CD API request latency by method, path family, and status.",
			Buckets:   prometheus.DefBuckets,
		},
		observability.MetricLabels("method", "path_family", "status"),
	)
)

func registerClientMetrics() {
	clientMetricsOnce.Do(func() {
		prometheus.MustRegister(clientRequestsTotal, clientRequestDurationSeconds)
	})
}

// ErrorKind classifies upstream ArgoCD errors so callers can react.
type ErrorKind int

const (
	// ErrUnknown is the default when no specific classification applies.
	ErrUnknown ErrorKind = iota
	// ErrUnauthorized indicates the auth token was rejected (401/403).
	ErrUnauthorized
	// ErrNotFound indicates the requested resource does not exist (404).
	ErrNotFound
	// ErrConflict indicates a sync conflict / operation in progress (409).
	ErrConflict
	// ErrUnreachable indicates the network call could not complete.
	ErrUnreachable
	// ErrServer indicates a 5xx response.
	ErrServer
)

// APIError is returned for non-2xx responses. The Body field carries the raw
// response body for debugging; Message is the upstream-supplied human string
// when ArgoCD returns one.
type APIError struct {
	Kind    ErrorKind
	Status  int
	Message string
	Body    string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return fmt.Sprintf("argocd api: status=%d %s", e.Status, e.Message)
	}
	return fmt.Sprintf("argocd api: status=%d", e.Status)
}

// IsKind reports whether err is an *APIError of the given kind.
func IsKind(err error, kind ErrorKind) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Kind == kind
	}
	return false
}

// PublicErrorMessage returns a stable, non-echoing diagnostic for API
// responses, operation rows, events and audits. APIError.Message/Body are
// untrusted upstream documents and must never cross those boundaries.
func PublicErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Kind {
		case ErrUnauthorized:
			return "Argo CD rejected the configured credentials"
		case ErrNotFound:
			return "Argo CD resource was not found"
		case ErrConflict:
			return "Argo CD rejected the operation because of a conflict"
		case ErrServer:
			return "Argo CD upstream service failed"
		default:
			return "Argo CD upstream request failed"
		}
	}
	if IsKind(err, ErrUnreachable) {
		return "Argo CD upstream service is unreachable"
	}
	return "Argo CD upstream request failed"
}

// Client speaks the ArgoCD HTTP API for a single instance.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// Options control client construction.
type Options struct {
	// VerifySSL toggles TLS verification. False matches argocd-cli's
	// --insecure flag for self-signed clusters.
	VerifySSL bool
	// Timeout overrides DefaultTimeout when non-zero.
	Timeout time.Duration
	// HTTPClient lets tests inject an httptest.Server-backed client.
	// When set, VerifySSL and Timeout are ignored.
	HTTPClient *http.Client
}

// NewClient constructs a typed client. baseURL is the ArgoCD api_url
// (e.g. https://argocd.example.com). token is the decrypted bearer token.
func NewClient(baseURL, token string, opts Options) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
	}
	if opts.HTTPClient != nil {
		c.httpClient = opts.HTTPClient
		return c
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	// SEC-R05: dial-guarded client. Argo CD is commonly in-cluster, so
	// AllowPrivate is enabled — loopback, link-local, and cloud metadata
	// remain blocked. VerifySSL=false keeps the existing insecure path
	// for self-signed management clusters.
	var tlsCfg *tls.Config
	if !opts.VerifySSL {
		tlsCfg = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — operator-opt-in
	}
	c.httpClient = httpclient.SafeClientAllowPrivateWithTLS(timeout, tlsCfg)
	return c
}

// SyncOptions carries the body of POST /api/v1/applications/{name}/sync.
// Field names match the upstream ArgoCD API JSON shape (camelCase).
type SyncOptions struct {
	// Revision overrides the application's targetRevision for this sync.
	Revision string `json:"revision,omitempty"`
	// Prune deletes resources that exist in the cluster but not in Git.
	Prune bool `json:"prune,omitempty"`
	// DryRun runs the sync without applying changes.
	DryRun bool `json:"dryRun,omitempty"`
}

// OperationState mirrors the subset of ArgoCD's operationState we care about.
// Reference: https://argo-cd.readthedocs.io/en/stable/operator-manual/server-commands/argocd-server/
type OperationState struct {
	Phase      string    `json:"phase"`
	Message    string    `json:"message,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	SyncResult *struct {
		Revision string `json:"revision,omitempty"`
	} `json:"syncResult,omitempty"`
	Operation *struct {
		Sync *struct {
			Revision string `json:"revision,omitempty"`
		} `json:"sync,omitempty"`
	} `json:"operation,omitempty"`
}

// Application is the trimmed projection of /api/v1/applications/{name}.
type Application struct {
	Metadata struct {
		Name            string            `json:"name"`
		UID             string            `json:"uid,omitempty"`
		Namespace       string            `json:"namespace,omitempty"`
		Labels          map[string]string `json:"labels,omitempty"`
		OwnerReferences []OwnerReference  `json:"ownerReferences,omitempty"`
	} `json:"metadata"`
	Spec   ApplicationSpec `json:"spec,omitempty"`
	Status struct {
		Sync struct {
			Status   string `json:"status,omitempty"`
			Revision string `json:"revision,omitempty"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status,omitempty"`
		} `json:"health"`
		OperationState *OperationState  `json:"operationState,omitempty"`
		Resources      []ResourceStatus `json:"resources,omitempty"`
	} `json:"status"`
}

// OwnerReference is the subset of Kubernetes ownerReferences needed to detect
// ApplicationSet-generated Applications with stale ownership metadata.
type OwnerReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Name       string `json:"name,omitempty"`
	UID        string `json:"uid,omitempty"`
	Controller *bool  `json:"controller,omitempty"`
}

// ResourceStatus mirrors the subset of ArgoCD's per-resource status used for
// cluster-level drift summaries.
type ResourceStatus struct {
	Group           string `json:"group,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name,omitempty"`
	Status          string `json:"status,omitempty"`
	RequiresPruning bool   `json:"requiresPruning,omitempty"`
}

// ServerStatus mirrors /api/version.
type ServerStatus struct {
	Version string `json:"Version"`
}

// Sync triggers a sync on the named application. ArgoCD returns the updated
// application object whose status.operationState carries the initial phase.
func (c *Client) Sync(ctx context.Context, name string, opts SyncOptions) (*Application, error) {
	body := map[string]any{
		"name":  name,
		"prune": opts.Prune,
	}
	if opts.DryRun {
		body["dryRun"] = true
	}
	if opts.Revision != "" {
		body["revision"] = opts.Revision
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var app Application
	if err := c.do(ctx, http.MethodPost, "/api/v1/applications/"+url.PathEscape(name)+"/sync", raw, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// GetApp fetches an application's current state from ArgoCD.
func (c *Client) GetApp(ctx context.Context, name string) (*Application, error) {
	var app Application
	if err := c.do(ctx, http.MethodGet, "/api/v1/applications/"+url.PathEscape(name), nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// Refresh asks ArgoCD to recompute the application state from the source
// repo. Hard refresh re-reads the helm/kustomize templates from disk.
func (c *Client) Refresh(ctx context.Context, name string, hard bool) (*Application, error) {
	mode := "normal"
	if hard {
		mode = "hard"
	}
	var app Application
	if err := c.do(ctx, http.MethodGet, "/api/v1/applications/"+url.PathEscape(name)+"?refresh="+mode, nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// Health probes /api/version. A 2xx response is sufficient evidence the
// instance is reachable and the token is valid for read access.
func (c *Client) Health(ctx context.Context) (*ServerStatus, error) {
	var status ServerStatus
	if err := c.do(ctx, http.MethodGet, "/api/version", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// do is the single HTTP funnel; it owns auth, error classification,
// tracing, and JSON decoding.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) (retErr error) {
	registerClientMetrics()
	start := time.Now()
	pathFamily := argoCDPathFamily(path)
	statusCode := 0
	ctx, span := clientTracer.Start(ctx, "argocd "+method)
	span.SetAttributes(
		attribute.String("http.request.method", method),
		attribute.String("argocd.path_family", pathFamily),
	)
	defer func() {
		statusLabel := argocdMetricStatus(statusCode, retErr)
		labels := observability.MetricValues(method, pathFamily, statusLabel)
		clientRequestsTotal.WithLabelValues(labels...).Inc()
		clientRequestDurationSeconds.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, retErr.Error())
			return
		}
		span.SetStatus(codes.Ok, "")
	}()
	if c == nil || c.baseURL == "" {
		return &APIError{Kind: ErrUnreachable, Message: "argocd client not configured"}
	}
	url := c.baseURL + path
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	// Always set Content-Type, even for bodyless DELETEs. ArgoCD's gRPC-
	// gateway frontend rejects bodyless requests that lack Content-Type
	// with 415 "Invalid content type", so we send a default for every
	// method.
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &APIError{Kind: ErrUnreachable, Message: err.Error()}
	}
	defer func() {
		// A bounded drain permits connection reuse for ordinary short bodies
		// without letting an oversized upstream response consume unbounded
		// bandwidth after the allocation limit has fired.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseDrainBytes))
		_ = resp.Body.Close()
	}()
	statusCode = resp.StatusCode
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, argosecurity.MaxArgoResponseBodyBytes+1))
	if readErr != nil {
		return &APIError{Kind: classifyErrorKind(resp.StatusCode), Status: resp.StatusCode, Message: "Argo CD response could not be read"}
	}
	if len(raw) > argosecurity.MaxArgoResponseBodyBytes {
		return &APIError{Kind: classifyErrorKind(resp.StatusCode), Status: resp.StatusCode, Message: responseBodyLimitMessage}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return classifyError(resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{Kind: ErrUnknown, Status: resp.StatusCode, Message: "decode error: " + err.Error(), Body: string(raw)}
	}
	return nil
}

func argocdMetricStatus(statusCode int, err error) string {
	if statusCode > 0 {
		return strconv.Itoa(statusCode)
	}
	if err != nil {
		return "error"
	}
	return "unknown"
}

func argoCDPathFamily(path string) string {
	clean := strings.Split(path, "?")[0]
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
		if clean == "" {
			return "/"
		}
		return clean
	}
	switch parts[2] {
	case "applications":
		if len(parts) >= 5 && parts[4] == "sync" {
			return "/api/v1/applications/*/sync"
		}
		if len(parts) >= 4 {
			return "/api/v1/applications/*"
		}
	case "applicationsets":
		if len(parts) >= 4 {
			return "/api/v1/applicationsets/*"
		}
	case "projects":
		if len(parts) >= 4 {
			return "/api/v1/projects/*"
		}
	case "clusters":
		if len(parts) >= 4 {
			return "/api/v1/clusters/*"
		}
	case "repositories":
		if len(parts) >= 4 {
			return "/api/v1/repositories/*"
		}
	}
	return "/" + strings.Join(parts, "/")
}

// classifyError maps an HTTP status + body into an *APIError of the right
// kind. ArgoCD's error body is `{"error": "...", "message": "..."}`.
func classifyError(status int, body []byte) error {
	var parsed struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)
	msg := parsed.Message
	if msg == "" {
		msg = parsed.Error
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	kind := classifyErrorKind(status)
	return &APIError{Kind: kind, Status: status, Message: msg, Body: string(body)}
}

func classifyErrorKind(status int) ErrorKind {
	kind := ErrUnknown
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = ErrUnauthorized
	case status == http.StatusNotFound:
		kind = ErrNotFound
	case status == http.StatusConflict:
		kind = ErrConflict
	case status >= http.StatusInternalServerError:
		kind = ErrServer
	}
	return kind
}
