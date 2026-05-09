package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// saTokenPath is the standard projected service-account token mount.
const saTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// K8sProxy forwards Kubernetes API requests from the tunnel to the local cluster.
//
// SA token rotation: Kubernetes projected service-account tokens rotate while
// the pod is running. We DO NOT cache the token at startup. The bearer token
// is re-read from saTokenPath on each call (cached for tokenCacheTTL to avoid
// disk thrash). The transport is built with a custom RoundTripper that
// stamps the freshest token onto every request.
type K8sProxy struct {
	client     *kubernetes.Clientset
	restConfig *rest.Config
	httpClient *http.Client
	log        *slog.Logger

	tokenSrc *bearerTokenSource
}

// bearerTokenSource provides a refreshing bearer token for in-cluster requests.
// The token is re-read from disk at most once per tokenCacheTTL.
type bearerTokenSource struct {
	path     string
	fallback string
	mu       sync.RWMutex
	cached   string
	expires  time.Time
}

const tokenCacheTTL = 30 * time.Second

func newBearerTokenSource(path, fallback string) *bearerTokenSource {
	return &bearerTokenSource{path: path, fallback: fallback}
}

// Token returns the freshest bearer token, re-reading from disk if the cache
// has expired. Returns the rest.Config fallback if the file is unreadable.
func (s *bearerTokenSource) Token() string {
	s.mu.RLock()
	if time.Now().Before(s.expires) && s.cached != "" {
		t := s.cached
		s.mu.RUnlock()
		return t
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check after acquiring write lock.
	if time.Now().Before(s.expires) && s.cached != "" {
		return s.cached
	}
	if data, err := os.ReadFile(s.path); err == nil {
		s.cached = strings.TrimSpace(string(data))
		s.expires = time.Now().Add(tokenCacheTTL)
		return s.cached
	}
	// File unavailable — fall back to the in-memory token from rest.Config.
	return s.fallback
}

// bearerTokenRoundTripper stamps a fresh bearer token on each request.
type bearerTokenRoundTripper struct {
	src  *bearerTokenSource
	base http.RoundTripper
}

func (rt *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if tok := rt.src.Token(); tok != "" {
		// Clone to avoid mutating the caller's request.
		clone := req.Clone(req.Context())
		clone.Header.Set("Authorization", "Bearer "+tok)
		return rt.base.RoundTrip(clone)
	}
	return rt.base.RoundTrip(req)
}

// NewK8sProxy creates a K8sProxy using the in-cluster configuration.
//
// We rely on client-go's built-in projected-token rotation: rest.InClusterConfig
// sets BearerTokenFile, and rest.TransportFor wraps with a refreshing
// transport that re-reads the file on each request. The clientset and the
// raw httpClient share the same rest.Config so they both get the rotation.
func NewK8sProxy(log *slog.Logger) (*K8sProxy, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("get in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}

	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	// Keep the custom token source around for tests that exercise it; not
	// installed in the request path because client-go already rotates.
	tokenSrc := newBearerTokenSource(saTokenPath, cfg.BearerToken)

	return &K8sProxy{
		client:     clientset,
		restConfig: cfg,
		httpClient: &http.Client{Transport: transport},
		log:        log,
		tokenSrc:   tokenSrc,
	}, nil
}

// Client returns the Kubernetes clientset. Used by handlers that share the
// connection (exec/logs/rbac/health).
func (p *K8sProxy) Client() *kubernetes.Clientset {
	return p.client
}

// RESTConfig returns the underlying rest.Config (without static bearer token).
func (p *K8sProxy) RESTConfig() *rest.Config {
	return p.restConfig
}

// NewK8sProxyWithConfig creates a K8sProxy with an explicit rest.Config.
// Useful for testing or running outside the cluster.
func NewK8sProxyWithConfig(cfg *rest.Config, log *slog.Logger) (*K8sProxy, error) {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}

	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	return &K8sProxy{
		client:     clientset,
		restConfig: cfg,
		httpClient: &http.Client{Transport: transport},
		log:        log,
	}, nil
}

// k8sStreamChunkSize is the buffer size used to forward a streaming response
// body chunk-by-chunk over the tunnel. Sized to match the chunk granularity
// k8s typically uses for watch events without dwarfing the WS frame budget.
const k8sStreamChunkSize = 16 * 1024

// HandleStreamRequest processes a K8S_STREAM_REQUEST and emits one or more
// K8S_STREAM_FRAME messages back over the tunnel. Used for Watch and other
// long-lived k8s responses that the single-shot HandleRequest path can't
// service (it would otherwise block on io.ReadAll until the server-side 30s
// timeout fires).
//
// Frame lifecycle: exactly one header, zero or more data, exactly one end.
// On any error mid-stream we still emit an end frame so the server can clean
// up the consumer goroutine instead of waiting for stream close.
func (p *K8sProxy) HandleStreamRequest(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error {
	var req protocol.K8sRequestPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return p.sendStreamEnd(sendFn, msg.StreamID, fmt.Errorf("unmarshal: %w", err))
	}

	p.log.Info("proxying k8s stream", "method", req.Method, "path", req.Path)

	targetURL := p.restConfig.Host + req.Path

	var bodyReader io.Reader
	if req.Body != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return p.sendStreamEnd(sendFn, msg.StreamID, fmt.Errorf("decode body: %w", err))
		}
		bodyReader = bytes.NewReader(decoded)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bodyReader)
	if err != nil {
		return p.sendStreamEnd(sendFn, msg.StreamID, fmt.Errorf("new request: %w", err))
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return p.sendStreamEnd(sendFn, msg.StreamID, fmt.Errorf("execute: %w", err))
	}
	defer resp.Body.Close()

	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	if err := p.sendStreamFrame(sendFn, msg.StreamID, protocol.K8sStreamFrame{
		Kind:       protocol.K8sStreamFrameHeader,
		StatusCode: resp.StatusCode,
		Headers:    headers,
	}); err != nil {
		return err
	}

	buf := make([]byte, k8sStreamChunkSize)
	for {
		if ctx.Err() != nil {
			return p.sendStreamEnd(sendFn, msg.StreamID, ctx.Err())
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err := p.sendStreamFrame(sendFn, msg.StreamID, protocol.K8sStreamFrame{
				Kind: protocol.K8sStreamFrameData,
				Body: base64.StdEncoding.EncodeToString(buf[:n]),
			}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return p.sendStreamEnd(sendFn, msg.StreamID, nil)
		}
		if readErr != nil {
			return p.sendStreamEnd(sendFn, msg.StreamID, readErr)
		}
	}
}

func (p *K8sProxy) sendStreamFrame(sendFn func(*protocol.Message) error, streamID string, frame protocol.K8sStreamFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal stream frame: %w", err)
	}
	return sendFn(&protocol.Message{
		Type:      protocol.MsgK8sStreamFrame,
		StreamID:  streamID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	})
}

func (p *K8sProxy) sendStreamEnd(sendFn func(*protocol.Message) error, streamID string, cause error) error {
	frame := protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd}
	if cause != nil {
		frame.Error = cause.Error()
	}
	return p.sendStreamFrame(sendFn, streamID, frame)
}

// HandleRequest processes a K8S_REQUEST message and returns a K8S_RESPONSE.
func (p *K8sProxy) HandleRequest(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	var req protocol.K8sRequestPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, fmt.Errorf("unmarshal k8s request: %w", err)
	}

	p.log.Info("proxying k8s request", "method", req.Method, "path", req.Path)

	// Build the target URL using the API server host from the rest config.
	targetURL := p.restConfig.Host + req.Path

	// Decode body if present.
	var bodyReader io.Reader
	if req.Body != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return nil, fmt.Errorf("decode request body: %w", err)
		}
		bodyReader = bytes.NewReader(decoded)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute k8s request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read k8s response body: %w", err)
	}

	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	responsePayload := protocol.K8sResponsePayload{
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       base64.StdEncoding.EncodeToString(respBody),
	}

	payloadBytes, err := json.Marshal(responsePayload)
	if err != nil {
		return nil, fmt.Errorf("marshal k8s response: %w", err)
	}

	return &protocol.Message{
		Type:      protocol.MsgK8sResponse,
		StreamID:  msg.StreamID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}, nil
}
