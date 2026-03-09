package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	// k8sProxyTimeout is the maximum time to wait for a K8s response from the agent.
	k8sProxyTimeout = 30 * time.Second
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

// HandleK8sProxy handles all requests to /api/v1/clusters/{cluster_id}/k8s/*
// It wraps the HTTP request as a K8S_REQUEST message, sends it through the tunnel,
// and waits for a K8S_RESPONSE.
func (p *ProxyHandler) HandleK8sProxy(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if clusterID == "" {
		http.Error(w, `{"error":"cluster_id is required"}`, http.StatusBadRequest)
		return
	}

	// Get agent connection from hub.
	agent := p.hub.GetAgent(clusterID)
	if agent == nil {
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

	msg := &protocol.Message{
		Type:      protocol.MsgK8sRequest,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}

	// Send K8S_REQUEST to agent.
	if err := p.hub.SendToAgent(clusterID, msg); err != nil {
		p.log.Error("failed to send to agent",
			slog.String("cluster_id", clusterID),
			slog.String("error", err.Error()),
		)
		http.Error(w, `{"error":"failed to send request to agent"}`, http.StatusServiceUnavailable)
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

// buildK8sRequestPayload constructs a K8sRequestPayload from an HTTP request.
func buildK8sRequestPayload(r *http.Request) (*protocol.K8sRequestPayload, error) {
	// Extract the K8s path: everything after /k8s/ in the URL.
	path := extractK8sPath(r.URL.Path)

	// Include query string if present.
	if r.URL.RawQuery != "" {
		path = path + "?" + r.URL.RawQuery
	}

	// Copy relevant headers.
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
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
