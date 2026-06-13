package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	// k8sProxyTimeout is the maximum time to wait for a K8s response from the agent.
	k8sProxyTimeout = 30 * time.Second

	// k8sStreamHeaderTimeout caps how long we wait for the agent's first frame
	// on a streaming request. Once the header arrives we stop applying any
	// timeout and let the client/agent decide when to close.
	k8sStreamHeaderTimeout = 30 * time.Second
)

// ProxyHandler forwards K8s API requests through the tunnel to agents.
type ProxyHandler struct {
	hub *Hub
	log *slog.Logger
}

// NewProxyHandler creates a new ProxyHandler.
func NewProxyHandler(hub *Hub, log *slog.Logger) *ProxyHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ProxyHandler{hub: hub, log: log}
}

// HandleK8sProxy handles all requests to /api/v1/clusters/{cluster_id}/k8s/*.
//
// For ordinary HTTP requests it wraps the request as a K8S_REQUEST and waits
// for a single K8S_RESPONSE. For Watch-shaped requests (?watch=true,
// Accept: stream=watch, /watch/ path segment) it sends K8S_STREAM_REQUEST
// and forwards each K8S_STREAM_FRAME chunk to the client via http.Flusher.
// Watch responses have no server-side timeout once the first frame arrives —
// they last until either the client disconnects or the agent closes the
// upstream stream.
func (p *ProxyHandler) HandleK8sProxy(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if clusterID == "" {
		http.Error(w, `{"error":"cluster_id is required"}`, http.StatusBadRequest)
		return
	}

	// Get agent connection from hub.
	agent := p.hub.GetAgent(clusterID)
	if agent == nil {
		// Cross-pod fallback: look up the sibling replica that holds
		// this cluster's WS and reverse-proxy the request there. Without
		// this the in-memory Hub's single-pod-per-cluster invariant
		// means we 503 every request that nginx pinned to the wrong
		// upstream pod (which, with nginx upstream keep-alive, is
		// effectively all requests).
		if p.forwardToOwnerPod(w, r, clusterID) {
			return
		}
		http.Error(w, `{"error":"Cluster agent not connected"}`, http.StatusServiceUnavailable)
		return
	}

	// Create a stream for this request.
	streamID := uuid.New().String()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		p.log.Error("failed to create stream",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		http.Error(w, `{"error":"failed to create stream"}`, http.StatusInternalServerError)
		return
	}
	defer agent.Streams.CloseStream(streamID)

	// Build K8sRequestPayload from the HTTP request.
	payload, err := buildK8sRequestPayload(r)
	if err != nil {
		p.log.Error("failed to build request payload",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		http.Error(w, `{"error":"failed to build request payload"}`, http.StatusInternalServerError)
		return
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"failed to marshal payload"}`, http.StatusInternalServerError)
		return
	}

	streaming := isWatchRequest(r)
	msgType := protocol.MsgK8sRequest
	if streaming {
		msgType = protocol.MsgK8sStreamRequest
	}

	msg := &protocol.Message{
		Type:      msgType,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}

	if err := p.hub.SendToAgent(clusterID, msg); err != nil {
		p.log.Error("failed to send to agent",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		http.Error(w, `{"error":"failed to send request to agent"}`, http.StatusServiceUnavailable)
		return
	}

	if streaming {
		p.consumeStreamingResponse(w, r, stream, clusterID)
		return
	}

	// Wait for K8S_RESPONSE on the stream with timeout.
	ctx, cancel := context.WithTimeout(r.Context(), k8sProxyTimeout)
	defer cancel()

	select {
	case data := <-stream.DataCh:
		var resp protocol.K8sResponsePayload
		if err := json.Unmarshal(data, &resp); err != nil {
			p.log.Error("failed to unmarshal K8s response",
				slog.String("cluster_id", clusterID),
				slog.String("error", err.Error()),
			)
			http.Error(w, `{"error":"invalid response from agent"}`, http.StatusBadGateway)
			return
		}
		writeK8sResponse(w, &resp)

	case <-stream.DoneCh:
		http.Error(w, `{"error":"stream closed unexpectedly"}`, http.StatusBadGateway)

	case <-ctx.Done():
		http.Error(w, `{"error":"request timed out"}`, http.StatusGatewayTimeout)
	}
}

// isWatchRequest reports whether r is a Kubernetes Watch request that needs
// streaming proxy semantics. Matches what kubectl/client-go and ArgoCD's
// live-state controller emit.
func isWatchRequest(r *http.Request) bool {
	q := r.URL.Query()
	if v := q.Get("watch"); v == "true" || v == "1" {
		return true
	}
	if accept := r.Header.Get("Accept"); strings.Contains(accept, "stream=watch") {
		return true
	}
	if strings.Contains(r.URL.Path, "/watch/") {
		return true
	}
	return false
}

// consumeStreamingResponse drains K8sStreamFrame frames from the agent and
// flushes each chunk to the HTTP client. It returns once an end frame is
// received, the stream closes, or the client disconnects.
//
// The first frame must be a header. After that, headers cannot be amended
// (HTTP semantics); any further header frames are ignored.
func (p *ProxyHandler) consumeStreamingResponse(w http.ResponseWriter, r *http.Request, stream *Stream, clusterID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported by ResponseWriter"}`, http.StatusInternalServerError)
		return
	}

	// Wait for the first frame (must be header) with a bounded timeout.
	headerCtx, headerCancel := context.WithTimeout(r.Context(), k8sStreamHeaderTimeout)
	defer headerCancel()

	var firstFrame protocol.K8sStreamFrame
	select {
	case data := <-stream.DataCh:
		if err := json.Unmarshal(data, &firstFrame); err != nil {
			p.log.Error("invalid k8s stream header",
				slog.String("cluster_id", clusterID),
				slog.String("error", err.Error()),
			)
			http.Error(w, `{"error":"invalid stream header from agent"}`, http.StatusBadGateway)
			return
		}
	case <-stream.DoneCh:
		http.Error(w, `{"error":"stream closed before header"}`, http.StatusBadGateway)
		return
	case <-headerCtx.Done():
		if r.Context().Err() != nil {
			return // client disconnected
		}
		http.Error(w, `{"error":"timeout waiting for stream header"}`, http.StatusGatewayTimeout)
		return
	}

	if firstFrame.Kind != protocol.K8sStreamFrameHeader {
		// Agent terminated before sending a header (e.g. immediate error).
		if firstFrame.Kind == protocol.K8sStreamFrameEnd && firstFrame.Error != "" {
			http.Error(w, `{"error":`+strconv.Quote(firstFrame.Error)+`}`, http.StatusBadGateway)
			return
		}
		http.Error(w, `{"error":"first frame was not a header"}`, http.StatusBadGateway)
		return
	}

	// Apply headers + status. Skip hop-by-hop and content-length: chunked.
	for k, v := range firstFrame.Headers {
		if isHopByHopHeader(k) || strings.EqualFold(k, "Content-Length") {
			continue
		}
		w.Header().Set(k, v)
	}
	statusCode := firstFrame.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	flusher.Flush()

	// Pump frames until end or disconnect. No timeout — this is a long-poll.
	for {
		select {
		case data := <-stream.DataCh:
			var frame protocol.K8sStreamFrame
			if err := json.Unmarshal(data, &frame); err != nil {
				p.log.Warn("invalid k8s stream frame, terminating stream",
					slog.String("cluster_id", clusterID),
					slog.String("error", err.Error()),
				)
				return
			}
			switch frame.Kind {
			case protocol.K8sStreamFrameData:
				if frame.Body == "" {
					continue
				}
				bodyBytes, err := base64.StdEncoding.DecodeString(frame.Body)
				if err != nil {
					bodyBytes = []byte(frame.Body)
				}
				if _, werr := w.Write(bodyBytes); werr != nil {
					return
				}
				flusher.Flush()
			case protocol.K8sStreamFrameEnd:
				return
			case protocol.K8sStreamFrameHeader:
				// Already sent — ignore late header frames.
				continue
			default:
				p.log.Warn("unknown k8s stream frame kind",
					slog.String("cluster_id", clusterID),
					slog.String("kind", string(frame.Kind)),
				)
			}
		case <-stream.DoneCh:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// isClientOnlyHeader reports whether a request header from the dashboard /
// browser should be stripped before forwarding the request to the kubernetes
// API. Authorization is the most important one — see buildK8sRequestPayload.
func isClientOnlyHeader(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "authorization", "cookie", "host":
		return true
	}
	if strings.HasPrefix(lower, "x-forwarded-") {
		return true
	}
	if strings.HasPrefix(lower, "impersonate-") {
		return true
	}
	return false
}

// isHopByHopHeader reports whether name is an HTTP/1.1 hop-by-hop header
// that should not be forwarded. See RFC 7230 §6.1.
func isHopByHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailers", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

// buildK8sRequestPayload constructs a K8sRequestPayload from an HTTP request.
func buildK8sRequestPayload(r *http.Request) (*protocol.K8sRequestPayload, error) {
	// Extract the K8s path: everything after /k8s/ in the URL.
	path := extractK8sPath(r.URL.Path)

	// Include query string if present.
	if r.URL.RawQuery != "" {
		path = path + "?" + r.URL.RawQuery
	}

	// Forward only headers that make sense at the kubernetes API. Drop:
	//   - Authorization: this is the caller's Astronomer JWT, not a k8s
	//     bearer; client-go's transport refuses to overwrite it, so the
	//     agent's SA token is bypassed and k8s returns 401.
	//   - Cookie / Host / X-Forwarded-* : browser/proxy headers that are
	//     either wrong (Host) or noise at the upstream.
	//   - Impersonate-* : user-controlled Kubernetes impersonation headers.
	//     If we add impersonation later, the server must derive those values
	//     from Astronomer RBAC, not trust inbound client headers.
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		if isClientOnlyHeader(key) {
			continue
		}
		headers[key] = values[0]
	}

	// Read and base64-encode the body.
	var body string
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("reading request body: %w", err)
		}
		if len(bodyBytes) > 0 {
			body = base64.StdEncoding.EncodeToString(bodyBytes)
		}
	}

	return &protocol.K8sRequestPayload{
		Method:  r.Method,
		Path:    path,
		Headers: headers,
		Body:    body,
	}, nil
}

// extractK8sPath extracts the Kubernetes API path from the full URL path.
// Given /api/v1/clusters/{cluster_id}/k8s/api/v1/pods it returns /api/v1/pods.
func extractK8sPath(fullPath string) string {
	const marker = "/k8s/"
	idx := strings.Index(fullPath, marker)
	if idx == -1 {
		return "/"
	}
	path := fullPath[idx+len(marker)-1:] // keep the leading /
	if path == "" {
		return "/"
	}
	return path
}

// writeK8sResponse writes the K8sResponsePayload back to the HTTP response.
func writeK8sResponse(w http.ResponseWriter, resp *protocol.K8sResponsePayload) {
	// Set response headers.
	for key, value := range resp.Headers {
		w.Header().Set(key, value)
	}

	// Write status code.
	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	// Decode base64 body.
	var bodyBytes []byte
	if resp.Body != "" {
		var err error
		bodyBytes, err = base64.StdEncoding.DecodeString(resp.Body)
		if err != nil {
			// If not valid base64, use raw string.
			bodyBytes = []byte(resp.Body)
		}
	}

	w.WriteHeader(statusCode)
	if len(bodyBytes) > 0 {
		w.Write(bodyBytes)
	}
}

// forwardToOwnerPod reverse-proxies r to whichever sibling pod owns the
// cluster's WebSocket, per the redis-backed locator. Returns true when
// the request was forwarded (the response was already written),
// false when there was no locator, no entry, or the locator says we
// are the owner (a stale entry from a prior life of this pod). The
// caller then falls back to the original 503 path.
//
// We don't recurse: the forwarded request goes to the sibling's normal
// /api/v1/clusters/{id}/k8s/* endpoint, which the sibling handles with
// its local Hub.GetAgent — which (by construction of the locator)
// succeeds. If the sibling's WS dropped between the lookup and the
// forward, the sibling will return its own 503; we surface that to the
// client without retrying so we don't bounce indefinitely.
func (p *ProxyHandler) forwardToOwnerPod(w http.ResponseWriter, r *http.Request, clusterID string) bool {
	loc := p.hub.Locator()
	if loc == nil {
		return false
	}
	addr, err := loc.Lookup(r.Context(), clusterID)
	if err != nil {
		p.log.Warn("tunnel proxy: locator lookup failed",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		return false
	}
	if addr == "" {
		return false
	}
	// Don't loop back to ourselves: an entry pointing at our own
	// address means our Hub had the WS recently and lost it. Telling
	// the caller "not forwarded" lets it return 503, which surfaces the
	// real "agent disconnected" state instead of a request-forward
	// flap.
	if addr == loc.Address() {
		return false
	}

	target := fmt.Sprintf("http://%s%s", addr, r.URL.RequestURI())
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		p.log.Warn("tunnel proxy: build upstream request",
			slog.String("cluster_id", clusterID),
			slog.String("target", target),
			slog.String("error", err.Error()),
		)
		return false
	}
	// Copy headers verbatim — the sibling handler does its own auth /
	// RBAC check based on Authorization, so we mustn't strip it.
	for k, v := range r.Header {
		upstreamReq.Header[k] = v
	}
	// Hop-by-hop headers stripped per RFC 7230; keep this minimal.
	upstreamReq.Header.Del("Connection")
	upstreamReq.Header.Set("X-Astronomer-Forwarded-By", loc.Address())

	resp, err := proxyHTTPClient.Do(upstreamReq)
	if err != nil {
		p.log.Warn("tunnel proxy: forward to owner pod failed",
			slog.String("cluster_id", clusterID),
			slog.String("target", target),
			slog.String("error", err.Error()),
		)
		return false
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

// proxyHTTPClient is the client used for cross-pod request forwarding.
// No global timeout because some forwarded requests (e.g. watches) are
// long-lived; per-request context cancellation governs lifetime.
var proxyHTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}
