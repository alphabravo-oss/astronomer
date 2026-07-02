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
	"github.com/alphabravocompany/astronomer-go/pkg/proxyhdr"
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
	mode := k8sProxyMode(r)
	if clusterID == "" {
		recordK8sProxyError(mode, "missing_cluster_id")
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
		recordK8sProxyError(mode, "agent_unavailable")
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
		recordK8sProxyError(mode, "stream_create_failed")
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
		recordK8sProxyError(mode, "payload_build_failed")
		http.Error(w, `{"error":"failed to build request payload"}`, http.StatusInternalServerError)
		return
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		recordK8sProxyError(mode, "payload_marshal_failed")
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
		recordK8sProxyError(mode, "send_failed")
		http.Error(w, `{"error":"failed to send request to agent"}`, http.StatusServiceUnavailable)
		return
	}

	if streaming {
		p.consumeStreamingResponse(w, r, stream, clusterID)
		return
	}

	// Wait for the K8S_RESPONSE on the stream with timeout. The agent sends
	// small bodies as a single K8sResponsePayload but CHUNKS large bodies
	// (>K8sChunkSizeBytes) into header+data…+end stream frames even for unary
	// requests — so reassemble both shapes. Reading only the first frame here
	// (the old behaviour) returned status+headers with a zero-length body for
	// every chunked response, e.g. a 398 KiB secrets list -> HTTP 200 + empty,
	// which broke ArgoCD's cluster-cache discovery and any large user list.
	ctx, cancel := context.WithTimeout(r.Context(), k8sProxyTimeout)
	defer cancel()

	resp, err := reassembleK8sResponse(ctx, stream.DataCh, stream.DoneCh)
	if err != nil {
		if ctx.Err() != nil {
			recordK8sProxyError(mode, "timeout")
			http.Error(w, `{"error":"request timed out"}`, http.StatusGatewayTimeout)
			return
		}
		p.log.Error("failed to assemble K8s response",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		recordK8sProxyError(mode, "invalid_response")
		http.Error(w, `{"error":"invalid response from agent"}`, http.StatusBadGateway)
		return
	}

	// Namespace-scoped RBAC: when the authz middleware admitted a cluster-wide
	// LIST for a namespace-restricted user, it stashed the allow-set in the
	// context. Filter the buffered list body down to those namespaces before it
	// reaches the client. A 2xx body that cannot be filtered safely fails closed
	// with a 403; non-2xx apiserver errors pass through unchanged.
	if allowed, ok := namespaceFilterFromContext(r.Context()); ok {
		if err := applyNamespaceFilter(resp, allowed); err != nil {
			recordK8sProxyError(mode, "namespace_filter_forbidden")
			http.Error(w, `{"error":"You do not have permission to perform this action"}`, http.StatusForbidden)
			return
		}
	}

	writeK8sResponse(w, resp)
}

// isWatchRequest reports whether r is a Kubernetes Watch request that needs
// streaming proxy semantics. Matches what kubectl/client-go and ArgoCD's
// live-state controller emit.
func isWatchRequest(r *http.Request) bool {
	q := r.URL.Query()
	// Match the apiserver's own ?watch parsing (strconv.ParseBool), which
	// accepts TRUE/True/t/T/1 etc. — NOT just "true"/"1". A stricter check here
	// would misclassify ?watch=TRUE as a unary request while the apiserver
	// streams a watch (a scoped-RBAC bypass + a hang on the unary path).
	if v := q.Get("watch"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil && b {
			return true
		}
	}
	if accept := r.Header.Get("Accept"); strings.Contains(accept, "stream=watch") {
		return true
	}
	if strings.Contains(r.URL.Path, "/watch/") {
		return true
	}
	return false
}

func k8sProxyMode(r *http.Request) string {
	if isWatchRequest(r) {
		return "watch"
	}
	return "normal"
}

// consumeStreamingResponse drains K8sStreamFrame frames from the agent and
// flushes each chunk to the HTTP client. It returns once an end frame is
// received, the stream closes, or the client disconnects.
//
// The first frame must be a header. After that, headers cannot be amended
// (HTTP semantics); any further header frames are ignored.
func (p *ProxyHandler) consumeStreamingResponse(w http.ResponseWriter, r *http.Request, stream *Stream, clusterID string) {
	// When we stop draining this watch (client disconnect, end frame, or stream
	// close) tell the agent to cancel its upstream kube-apiserver watch. Without
	// this the agent's HandleStreamRequest goroutine + apiserver watch leak, and
	// its continued Send() calls for every watch event can fill the 256-slot
	// sendCh and force a full-tunnel reset that kills every other stream/op for
	// the cluster. Mirrors logs_consumer.go emitting MsgLogStop.
	defer func() {
		_ = p.hub.SendToAgent(clusterID, &protocol.Message{
			Type:      protocol.MsgK8sStreamStop,
			StreamID:  stream.ID,
			ClusterID: clusterID,
			Timestamp: time.Now().UTC(),
		})
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		recordK8sProxyError("watch", "streaming_not_supported")
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
			recordK8sProxyError("watch", "invalid_stream_header")
			http.Error(w, `{"error":"invalid stream header from agent"}`, http.StatusBadGateway)
			return
		}
	case <-stream.DoneCh:
		recordK8sProxyError("watch", "stream_closed_before_header")
		http.Error(w, `{"error":"stream closed before header"}`, http.StatusBadGateway)
		return
	case <-headerCtx.Done():
		if r.Context().Err() != nil {
			return // client disconnected
		}
		recordK8sProxyError("watch", "stream_header_timeout")
		http.Error(w, `{"error":"timeout waiting for stream header"}`, http.StatusGatewayTimeout)
		return
	}

	if firstFrame.Kind != protocol.K8sStreamFrameHeader {
		// Agent terminated before sending a header (e.g. immediate error).
		if firstFrame.Kind == protocol.K8sStreamFrameEnd && firstFrame.Error != "" {
			recordK8sProxyError("watch", "agent_stream_error")
			http.Error(w, `{"error":`+strconv.Quote(firstFrame.Error)+`}`, http.StatusBadGateway)
			return
		}
		recordK8sProxyError("watch", "first_frame_not_header")
		http.Error(w, `{"error":"first frame was not a header"}`, http.StatusBadGateway)
		return
	}

	// Apply headers + status. Only forward response headers that are safe at
	// the browser-facing proxy boundary.
	for k, v := range firstFrame.Headers {
		if !k8sProxyResponseHeaderAllowed(k) {
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
				recordK8sProxyError("watch", "invalid_stream_frame")
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
					recordK8sProxyError("watch", "client_write_failed")
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

// isHopByHopHeader reports whether name is an HTTP/1.1 hop-by-hop header
// that should not be forwarded. See RFC 7230 §6.1.
func isHopByHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "trailers", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

func k8sProxyResponseHeaderAllowed(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "":
		return false
	case "authorization", "clear-site-data", "content-length", "cookie", "proxy-authenticate", "proxy-authorization", "set-cookie", "set-cookie2", "www-authenticate":
		return false
	default:
		return !isHopByHopHeader(lower)
	}
}

// buildK8sRequestPayload constructs a K8sRequestPayload from an HTTP request.
func buildK8sRequestPayload(r *http.Request) (*protocol.K8sRequestPayload, error) {
	// Extract the K8s path: everything after /k8s/ in the URL.
	path := extractK8sPath(r.URL.Path)

	// Include query string if present.
	if r.URL.RawQuery != "" {
		path = path + "?" + r.URL.RawQuery
	}

	// Forward only the small allowlist of headers that the kubernetes API
	// actually needs (see proxyhdr.ShouldForwardRequestHeader). Everything
	// else is dropped — the allowlist fails closed against header-spoofing,
	// including Authorization (caller's Astronomer JWT, not a k8s bearer),
	// Cookie/Host/X-Forwarded-*, user-controlled Impersonate-* headers, and
	// the front-proxy identity headers X-Remote-User/X-Remote-Group/
	// X-Remote-Extra-* honored by clusters using --requestheader auth.
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		if !proxyhdr.ShouldForwardRequestHeader(key) {
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
		if !k8sProxyResponseHeaderAllowed(key) {
			continue
		}
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
		_, _ = w.Write(bodyBytes)
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
		recordK8sProxyError(k8sProxyMode(r), "owner_lookup_failed")
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
		recordK8sProxyError(k8sProxyMode(r), "owner_request_build_failed")
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
		recordK8sProxyError(k8sProxyMode(r), "owner_forward_failed")
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Namespace-scoped RBAC fail-closed on the cross-pod path. When this
	// (front) pod's authz middleware admitted a cluster-wide LIST for a
	// namespace-restricted user it stashed the allow-set in r's context. The
	// owner pod re-runs the same middleware and filters its buffered response,
	// but we must NEVER stream an owner response through unfiltered when a
	// filter was requested. A filtered request is always a unary LIST (watches
	// are 403'd at the middleware), so buffer the owner's body and re-apply the
	// filter here — idempotent when the owner already filtered, and fail-closed
	// (403) if the body is unfilterable.
	if allowed, ok := namespaceFilterFromContext(r.Context()); ok {
		if p.forwardFilteredOwnerResponse(w, resp, allowed) {
			return true
		}
		recordK8sProxyError(k8sProxyMode(r), "namespace_filter_forbidden")
		http.Error(w, `{"error":"You do not have permission to perform this action"}`, http.StatusForbidden)
		return true
	}

	for k, v := range resp.Header {
		if !k8sProxyResponseHeaderAllowed(k) {
			continue
		}
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	if isWatchRequest(r) && flusher != nil {
		// Streaming (watch/SSE) response: flush every chunk to the client
		// immediately. A plain io.Copy with a single trailing Flush buffers
		// each event in bufio until ~2KB accumulate, so small ADDED/MODIFIED/
		// bookmark events on a quiet resource are held for minutes and the
		// cross-pod watch appears frozen even though data is flowing. Copy-
		// and-flush per read mirrors consumeStreamingResponse's local path.
		buf := make([]byte, 32*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					break
				}
				flusher.Flush()
			}
			if rerr != nil {
				break
			}
		}
		return true
	}
	_, _ = io.Copy(w, resp.Body)
	if flusher != nil {
		flusher.Flush()
	}
	return true
}

// forwardFilteredOwnerResponse buffers the owner pod's response, filters the
// body to the namespace allow-set, and writes it to w. It returns false (having
// written nothing) when the body cannot be filtered safely, so the caller fails
// closed with a 403. Non-2xx owner responses pass through unchanged.
func (p *ProxyHandler) forwardFilteredOwnerResponse(w http.ResponseWriter, resp *http.Response, allowed map[string]struct{}) bool {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	payload := &protocol.K8sResponsePayload{
		StatusCode: resp.StatusCode,
		Headers:    map[string]string{},
		Body:       base64.StdEncoding.EncodeToString(bodyBytes),
	}
	for k, v := range resp.Header {
		if len(v) == 0 || !k8sProxyResponseHeaderAllowed(k) {
			continue
		}
		payload.Headers[k] = v[0]
	}

	if err := applyNamespaceFilter(payload, allowed); err != nil {
		return false
	}

	writeK8sResponse(w, payload)
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
