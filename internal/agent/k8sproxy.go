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
	"github.com/alphabravocompany/astronomer-go/pkg/proxyhdr"
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

	// streams tracks the per-StreamID cancel func for in-flight
	// HandleStreamRequest watches so a MsgK8sStreamStop from the server can
	// cancel the kube-apiserver watch + pump goroutine. Mirrors LogHandler.
	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc
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
		streams:    make(map[string]context.CancelFunc),
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
		streams:    make(map[string]context.CancelFunc),
	}, nil
}

// k8sStreamChunkSize is the buffer size used to forward a streaming response
// body chunk-by-chunk over the tunnel. Sized to match the chunk granularity
// k8s typically uses for watch events without dwarfing the WS frame budget.
const k8sStreamChunkSize = 16 * 1024

// maxK8sResponseBodyBytes caps how large a single proxied unary k8s response
// body the agent will buffer in memory (via HandleRequest/HandleRequestStreaming).
// Matches the server-side reassembly cap (handler.maxAssembledResponseBytes,
// 64 MiB) so nothing that previously succeeded through chunking is newly
// rejected; bodies past it fail closed with a 413 Status object. Watches use
// the true-streaming HandleStreamRequest path and are not bounded here. Kept a
// var (not const) so it is configurable and overridable in tests.
var maxK8sResponseBodyBytes int64 = 64 * 1024 * 1024

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

	// Per-stream cancellation: when the server's consumer stops draining (client
	// disconnect, end frame, or stream close) it sends MsgK8sStreamStop, which
	// HandleStreamStop uses to cancel this context. That aborts the in-flight
	// kube-apiserver watch (httpReq is built with streamCtx) so resp.Body.Read
	// unblocks and this goroutine drains instead of streaming forever into a
	// discarded stream.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.registerStream(msg.StreamID, cancel)
	defer p.unregisterStream(msg.StreamID)
	ctx = streamCtx

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
		if !proxyhdr.ShouldForwardRequestHeader(k) {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return p.sendStreamEnd(sendFn, msg.StreamID, fmt.Errorf("execute: %w", err))
	}
	defer func() {
		_ = resp.Body.Close()
	}()

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

// registerStream records the cancel func for an in-flight stream so a later
// MsgK8sStreamStop can terminate it.
func (p *K8sProxy) registerStream(streamID string, cancel context.CancelFunc) {
	p.streamsMu.Lock()
	if p.streams == nil {
		p.streams = make(map[string]context.CancelFunc)
	}
	p.streams[streamID] = cancel
	p.streamsMu.Unlock()
}

// unregisterStream drops the cancel func for a completed stream.
func (p *K8sProxy) unregisterStream(streamID string) {
	p.streamsMu.Lock()
	delete(p.streams, streamID)
	p.streamsMu.Unlock()
}

// HandleStreamStop cancels an in-flight k8s stream (Watch) whose consumer has
// gone away. Cancelling the per-stream context aborts the kube-apiserver watch
// and lets HandleStreamRequest's read loop drain and emit its end frame.
// Unknown stream IDs are a no-op (the stream may have already ended), matching
// LogHandler.HandleLogStop.
func (p *K8sProxy) HandleStreamStop(msg *protocol.Message) error {
	p.streamsMu.Lock()
	cancel, ok := p.streams[msg.StreamID]
	p.streamsMu.Unlock()
	if !ok {
		p.log.Debug("k8s stream stop for unknown stream", "stream_id", msg.StreamID)
		return nil
	}
	cancel()
	return nil
}

func (p *K8sProxy) sendStreamEnd(sendFn func(*protocol.Message) error, streamID string, cause error) error {
	frame := protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd}
	if cause != nil {
		frame.Error = cause.Error()
	}
	return p.sendStreamFrame(sendFn, streamID, frame)
}

// HandleRequest processes a K8S_REQUEST message and returns a K8S_RESPONSE.
// Kept as a single-shot handler for backward compatibility with the legacy
// MessageHandler shape; the production wiring uses HandleRequestStreaming
// which chunks large bodies. Small responses go
// through here unchanged.
func (p *K8sProxy) HandleRequest(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	respBody, statusCode, respHeaders, err := p.executeUpstream(ctx, msg)
	if err != nil {
		return nil, err
	}
	responsePayload := protocol.K8sResponsePayload{
		StatusCode: statusCode,
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

// HandleRequestStreaming is the same as HandleRequest but emits chunked
// K8sStreamFrame messages when the response body exceeds
// protocol.K8sChunkSizeBytes. The server's k8s_requester auto-detects
// the shape (header frame → data frames → end frame) and reassembles a
// single K8sResponsePayload before returning to its caller — the
// handler-side contract is unchanged.
//
// Why two methods: this one fits the AdaptStreamingHandler shape
// (returns frames via sendFn). The legacy single-shot HandleRequest
// stays for tests + any callers that want one in-memory response.
func (p *K8sProxy) HandleRequestStreaming(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error {
	respBody, statusCode, respHeaders, err := p.executeUpstream(ctx, msg)
	if err != nil {
		return err
	}

	// Small body: single K8sResponse, same wire shape as before. The
	// server's requester handles both shapes; keeping the small path
	// unchanged avoids regressing the common case (single-resource
	// reads).
	if len(respBody) <= protocol.K8sChunkSizeBytes {
		responsePayload := protocol.K8sResponsePayload{
			StatusCode: statusCode,
			Headers:    respHeaders,
			Body:       base64.StdEncoding.EncodeToString(respBody),
		}
		payloadBytes, merr := json.Marshal(responsePayload)
		if merr != nil {
			return fmt.Errorf("marshal k8s response: %w", merr)
		}
		return sendFn(&protocol.Message{
			Type:      protocol.MsgK8sResponse,
			StreamID:  msg.StreamID,
			Timestamp: time.Now().UTC(),
			Payload:   payloadBytes,
		})
	}

	// Large body: chunked stream. Header frame carries StatusCode +
	// Headers; data frames carry K8sChunkSizeBytes of body per frame
	// (final chunk may be smaller); end frame closes the stream.
	if err := p.sendStreamFrame(sendFn, msg.StreamID, protocol.K8sStreamFrame{
		Kind:       protocol.K8sStreamFrameHeader,
		StatusCode: statusCode,
		Headers:    respHeaders,
	}); err != nil {
		return err
	}
	for offset := 0; offset < len(respBody); offset += protocol.K8sChunkSizeBytes {
		end := offset + protocol.K8sChunkSizeBytes
		if end > len(respBody) {
			end = len(respBody)
		}
		if err := p.sendStreamFrame(sendFn, msg.StreamID, protocol.K8sStreamFrame{
			Kind: protocol.K8sStreamFrameData,
			Body: base64.StdEncoding.EncodeToString(respBody[offset:end]),
		}); err != nil {
			return err
		}
	}
	return p.sendStreamEnd(sendFn, msg.StreamID, nil)
}

// executeUpstream performs the actual k8s API call and returns the raw
// body + status + headers. Factored out so HandleRequest and
// HandleRequestStreaming share the I/O path; the only difference is how
// they frame the response on the way back.
func (p *K8sProxy) executeUpstream(ctx context.Context, msg *protocol.Message) ([]byte, int, map[string]string, error) {
	var req protocol.K8sRequestPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, 0, nil, fmt.Errorf("unmarshal k8s request: %w", err)
	}

	p.log.Info("proxying k8s request", "method", req.Method, "path", req.Path)

	targetURL := p.restConfig.Host + req.Path

	var bodyReader io.Reader
	if req.Body != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("decode request body: %w", err)
		}
		bodyReader = bytes.NewReader(decoded)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bodyReader)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("create http request: %w", err)
	}

	for k, v := range req.Headers {
		if !proxyhdr.ShouldForwardRequestHeader(k) {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("execute k8s request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Cap the in-memory read: a single non-paginated LIST on a large cluster
	// (all pods/events/secrets) or a GET of a multi-hundred-MiB object would
	// otherwise allocate the full body plus its base64 copy at once and, with
	// goroutine-per-message dispatch, several concurrent large reads multiply
	// this and OOM the agent pod. service_proxy caps the same way. Read one
	// byte past the limit so we can distinguish "exactly at cap" from "over".
	limited := io.LimitReader(resp.Body, maxK8sResponseBodyBytes+1)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("read k8s response body: %w", err)
	}
	if int64(len(respBody)) > maxK8sResponseBodyBytes {
		// Fail closed with a 413 Status object rather than forwarding a
		// truncated (and therefore corrupt/unparseable) body. The status body
		// mirrors what the apiserver itself returns so clients handle it.
		statusJSON := fmt.Sprintf(
			`{"kind":"Status","apiVersion":"v1","metadata":{},"status":"Failure","message":"response body exceeds agent %d-byte limit","reason":"RequestEntityTooLarge","code":413}`,
			maxK8sResponseBodyBytes,
		)
		return []byte(statusJSON), http.StatusRequestEntityTooLarge,
			map[string]string{"Content-Type": "application/json"}, nil
	}

	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}
	return respBody, resp.StatusCode, respHeaders, nil
}
