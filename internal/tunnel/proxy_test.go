package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestHandleK8sProxy_NoAgentConnected(t *testing.T) {
	k8sProxyErrorsTotal.Reset()
	hub := NewHub(slog.Default())
	proxy := NewProxyHandler(hub, slog.Default())

	r := chi.NewRouter()
	r.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/k8s/api/v1/pods", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Cluster agent not connected") {
		t.Fatalf("expected 'Cluster agent not connected' in body, got: %s", body)
	}
	if got := tunnelMetricValue(t, k8sProxyErrorsTotal.WithLabelValues(observability.MetricValues("normal", "agent_unavailable")...)); got != 1 {
		t.Fatalf("agent_unavailable errors = %v, want 1", got)
	}
}

func TestBuildK8sRequestPayload(t *testing.T) {
	bodyContent := `{"apiVersion":"v1","kind":"Pod"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/cluster-1/k8s/api/v1/namespaces/default/pods?pretty=true", strings.NewReader(bodyContent))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Impersonate-User", "system:admin")
	req.Header.Set("Impersonate-Group", "system:masters")
	req.Header.Set("Impersonate-Extra-Scopes", "danger")

	payload, err := buildK8sRequestPayload(req)
	if err != nil {
		t.Fatalf("buildK8sRequestPayload: %v", err)
	}

	if payload.Method != http.MethodPost {
		t.Fatalf("expected POST, got %s", payload.Method)
	}

	if !strings.HasPrefix(payload.Path, "/api/v1/namespaces/default/pods") {
		t.Fatalf("expected path starting with /api/v1/namespaces/default/pods, got %s", payload.Path)
	}

	if !strings.Contains(payload.Path, "pretty=true") {
		t.Fatalf("expected query string in path, got %s", payload.Path)
	}

	if payload.Headers["Content-Type"] != "application/json" {
		t.Fatalf("expected Content-Type header, got %v", payload.Headers)
	}
	for _, header := range []string{"Authorization", "Impersonate-User", "Impersonate-Group", "Impersonate-Extra-Scopes"} {
		if _, ok := payload.Headers[header]; ok {
			t.Fatalf("expected %s to be stripped, headers=%v", header, payload.Headers)
		}
	}

	// Verify body is base64-encoded.
	decoded, err := base64.StdEncoding.DecodeString(payload.Body)
	if err != nil {
		t.Fatalf("body is not valid base64: %v", err)
	}
	if string(decoded) != bodyContent {
		t.Fatalf("expected body %q, got %q", bodyContent, string(decoded))
	}
}

func TestBuildK8sRequestPayload_BodyTooLarge(t *testing.T) {
	// A body just over the cap is rejected (guards the OOM vector); a body at
	// the cap still succeeds.
	oversize := strings.Repeat("a", k8sProxyMaxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c1/k8s/api/v1/pods", strings.NewReader(oversize))
	if _, err := buildK8sRequestPayload(req); !errors.Is(err, errRequestBodyTooLarge) {
		t.Fatalf("oversize body: want errRequestBodyTooLarge, got %v", err)
	}

	atCap := strings.Repeat("a", k8sProxyMaxBodyBytes)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c1/k8s/api/v1/pods", strings.NewReader(atCap))
	if _, err := buildK8sRequestPayload(req); err != nil {
		t.Fatalf("body at cap should succeed, got %v", err)
	}
}

func TestBuildK8sRequestPayload_NoBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/k8s/api/v1/pods", nil)

	payload, err := buildK8sRequestPayload(req)
	if err != nil {
		t.Fatalf("buildK8sRequestPayload: %v", err)
	}

	if payload.Method != http.MethodGet {
		t.Fatalf("expected GET, got %s", payload.Method)
	}

	if payload.Body != "" {
		t.Fatalf("expected empty body, got %q", payload.Body)
	}
}

func TestExtractK8sPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/api/v1/clusters/c1/k8s/api/v1/pods", "/api/v1/pods"},
		{"/api/v1/clusters/c1/k8s/", "/"},
		{"/api/v1/clusters/c1/k8s/api/v1/namespaces/default/pods", "/api/v1/namespaces/default/pods"},
		{"/no-k8s-here", "/"},
	}

	for _, tt := range tests {
		got := extractK8sPath(tt.input)
		if got != tt.expected {
			t.Errorf("extractK8sPath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestHandleK8sProxy_Timeout(t *testing.T) {
	hub := NewHub(slog.Default())

	// Register a fake agent directly in the hub.
	agent := &AgentConnection{
		ClusterID: "cluster-timeout",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}

	hub.agents.Set("cluster-timeout", agent)

	proxy := NewProxyHandler(hub, slog.Default())

	r := chi.NewRouter()
	r.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	// Use a very short timeout context to trigger timeout quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-timeout/k8s/api/v1/pods", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Should timeout since no agent is reading/responding.
	// The request context cancels before the 30s proxy timeout.
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 (timeout), got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleK8sProxy_SuccessfulResponse(t *testing.T) {
	hub := NewHub(slog.Default())

	// Register a fake agent.
	agent := &AgentConnection{
		ClusterID: "cluster-ok",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}

	hub.agents.Set("cluster-ok", agent)

	proxy := NewProxyHandler(hub, slog.Default())

	r := chi.NewRouter()
	r.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	// Start a goroutine to simulate the agent responding.
	go func() {
		// Wait for the message on the agent's send channel.
		msg := <-agent.sendCh

		// Build a response and send it back via the stream.
		respBody := base64.StdEncoding.EncodeToString([]byte(`{"items":[]}`))
		respPayload, _ := json.Marshal(protocol.K8sResponsePayload{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       respBody,
		})

		stream, ok := agent.Streams.GetStream(msg.StreamID)
		if !ok {
			return
		}
		stream.DataCh <- respPayload
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-ok/k8s/api/v1/pods", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	if body := w.Body.String(); body != `{"items":[]}` {
		t.Fatalf("expected body {\"items\":[]}, got %q", body)
	}
}

func TestHandleK8sProxy_InvalidAgentResponseRecordsMetric(t *testing.T) {
	k8sProxyErrorsTotal.Reset()
	hub := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: "cluster-invalid",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set("cluster-invalid", agent)

	proxy := NewProxyHandler(hub, slog.Default())
	r := chi.NewRouter()
	r.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	go func() {
		msg := <-agent.sendCh
		stream, ok := agent.Streams.GetStream(msg.StreamID)
		if ok {
			stream.DataCh <- []byte("not-json")
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-invalid/k8s/api/v1/pods", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
	if got := tunnelMetricValue(t, k8sProxyErrorsTotal.WithLabelValues(observability.MetricValues("normal", "invalid_response")...)); got != 1 {
		t.Fatalf("invalid_response errors = %v, want 1", got)
	}
}

func TestHandleK8sProxy_ForwardsNamedK8sOperations(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		contentType string
		wantType    protocol.MessageType
		wantPath    string
		wantBody    string
		wantWatch   string
	}{
		{
			name:     "GET pods",
			method:   http.MethodGet,
			target:   "/api/v1/clusters/%s/k8s/api/v1/pods",
			wantType: protocol.MsgK8sRequest,
			wantPath: "/api/v1/pods",
		},
		{
			name:        "PATCH deployment",
			method:      http.MethodPatch,
			target:      "/api/v1/clusters/%s/k8s/apis/apps/v1/namespaces/default/deployments/web",
			body:        `{"spec":{"replicas":3}}`,
			contentType: "application/merge-patch+json",
			wantType:    protocol.MsgK8sRequest,
			wantPath:    "/apis/apps/v1/namespaces/default/deployments/web",
			wantBody:    `{"spec":{"replicas":3}}`,
		},
		{
			name:     "DELETE pod",
			method:   http.MethodDelete,
			target:   "/api/v1/clusters/%s/k8s/api/v1/namespaces/default/pods/web-0",
			wantType: protocol.MsgK8sRequest,
			wantPath: "/api/v1/namespaces/default/pods/web-0",
		},
		{
			name:      "WATCH pods",
			method:    http.MethodGet,
			target:    "/api/v1/clusters/%s/k8s/api/v1/pods?watch=true&resourceVersion=42",
			wantType:  protocol.MsgK8sStreamRequest,
			wantPath:  "/api/v1/pods?watch=true&resourceVersion=42",
			wantWatch: `"type":"ADDED"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusterID := "cluster-" + strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(tt.name))
			hub := NewHub(slog.Default())
			agent := &AgentConnection{
				ClusterID: clusterID,
				Streams:   NewStreamManager(256),
				sendCh:    make(chan *protocol.Message, sendChannelSize),
				cancel:    func() {},
			}
			hub.agents.Set(clusterID, agent)

			proxy := NewProxyHandler(hub, slog.Default())
			router := chi.NewRouter()
			router.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

			observed := make(chan protocol.K8sRequestPayload, 1)
			errCh := make(chan error, 1)
			go func() {
				msg := <-agent.sendCh
				if msg.Type != tt.wantType {
					errCh <- fmt.Errorf("message type = %s, want %s", msg.Type, tt.wantType)
					return
				}
				var payload protocol.K8sRequestPayload
				if err := json.Unmarshal(msg.Payload, &payload); err != nil {
					errCh <- fmt.Errorf("unmarshal request payload: %w", err)
					return
				}
				observed <- payload

				stream, ok := agent.Streams.GetStream(msg.StreamID)
				if !ok {
					errCh <- fmt.Errorf("stream %q not found", msg.StreamID)
					return
				}
				if tt.wantType == protocol.MsgK8sStreamRequest {
					header, _ := json.Marshal(protocol.K8sStreamFrame{
						Kind:       protocol.K8sStreamFrameHeader,
						StatusCode: http.StatusOK,
						Headers:    map[string]string{"Content-Type": "application/json"},
					})
					data, _ := json.Marshal(protocol.K8sStreamFrame{
						Kind: protocol.K8sStreamFrameData,
						Body: base64.StdEncoding.EncodeToString([]byte(`{"type":"ADDED","object":{"kind":"Pod","metadata":{"name":"web-0"}}}` + "\n")),
					})
					end, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})
					stream.DataCh <- header
					stream.DataCh <- data
					stream.DataCh <- end
					errCh <- nil
					return
				}

				resp, _ := json.Marshal(protocol.K8sResponsePayload{
					StatusCode: http.StatusOK,
					Headers:    map[string]string{"Content-Type": "application/json"},
					Body:       base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
				})
				stream.DataCh <- resp
				errCh <- nil
			}()

			req := httptest.NewRequest(tt.method, fmt.Sprintf(tt.target, clusterID), strings.NewReader(tt.body))
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if err := <-errCh; err != nil {
				t.Fatal(err)
			}
			got := <-observed
			if got.Method != tt.method {
				t.Fatalf("method = %s, want %s", got.Method, tt.method)
			}
			if got.Path != tt.wantPath {
				t.Fatalf("path = %q, want %q", got.Path, tt.wantPath)
			}
			if tt.contentType != "" && got.Headers["Content-Type"] != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got.Headers["Content-Type"], tt.contentType)
			}
			if tt.wantBody == "" {
				if got.Body != "" {
					t.Fatalf("body = %q, want empty", got.Body)
				}
			} else {
				decoded, err := base64.StdEncoding.DecodeString(got.Body)
				if err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if string(decoded) != tt.wantBody {
					t.Fatalf("body = %q, want %q", string(decoded), tt.wantBody)
				}
			}
			if tt.wantWatch != "" && !strings.Contains(rec.Body.String(), tt.wantWatch) {
				t.Fatalf("watch response missing %s: %s", tt.wantWatch, rec.Body.String())
			}
		})
	}
}

func TestWriteK8sResponse(t *testing.T) {
	resp := &protocol.K8sResponsePayload{
		StatusCode: 404,
		Headers:    map[string]string{"X-Custom": "test"},
		Body:       base64.StdEncoding.EncodeToString([]byte("not found")),
	}

	w := httptest.NewRecorder()
	writeK8sResponse(w, resp)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if w.Header().Get("X-Custom") != "test" {
		t.Fatalf("expected X-Custom header")
	}
	if w.Body.String() != "not found" {
		t.Fatalf("expected 'not found', got %q", w.Body.String())
	}
}

func TestWriteK8sResponseSanitizesUnsafeHeaders(t *testing.T) {
	resp := &protocol.K8sResponsePayload{
		StatusCode: http.StatusOK,
		Headers: map[string]string{
			"Audit-Id":            "audit-123",
			"Cache-Control":       "no-cache",
			"Content-Type":        "application/json",
			"Authorization":       "Bearer leaked",
			"Clear-Site-Data":     `"cookies"`,
			"Connection":          "upgrade",
			"Content-Length":      "999",
			"Cookie":              "session=leaked",
			"Keep-Alive":          "timeout=5",
			"Proxy-Authenticate":  "Basic",
			"Proxy-Authorization": "Basic leaked",
			"Set-Cookie":          "k8s_session=leaked; Path=/",
			"Set-Cookie2":         "legacy=leaked",
			"TE":                  "trailers",
			"Trailer":             "Expires",
			"Trailers":            "Expires",
			"Transfer-Encoding":   "chunked",
			"Upgrade":             "websocket",
			"WWW-Authenticate":    "Bearer",
		},
		Body: base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
	}

	w := httptest.NewRecorder()
	writeK8sResponse(w, resp)

	for _, header := range []string{"Audit-Id", "Cache-Control", "Content-Type"} {
		if w.Header().Get(header) == "" {
			t.Fatalf("expected safe header %s to be preserved", header)
		}
	}
	for _, header := range []string{
		"Authorization",
		"Clear-Site-Data",
		"Connection",
		"Content-Length",
		"Cookie",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Set-Cookie",
		"Set-Cookie2",
		"TE",
		"Trailer",
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
		"WWW-Authenticate",
	} {
		if got := w.Header().Get(header); got != "" {
			t.Fatalf("expected unsafe header %s to be stripped, got %q", header, got)
		}
	}
}

func TestIsWatchRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{
			name: "non-watch GET",
			req:  httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/k8s/api/v1/pods", nil),
			want: false,
		},
		{
			name: "watch=true query",
			req:  httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/k8s/api/v1/pods?watch=true", nil),
			want: true,
		},
		{
			name: "watch=1 query",
			req:  httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/k8s/api/v1/pods?watch=1", nil),
			want: true,
		},
		{
			name: "Accept stream=watch",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/k8s/api/v1/pods", nil)
				r.Header.Set("Accept", "application/json;stream=watch")
				return r
			}(),
			want: true,
		},
		{
			name: "/watch/ path segment",
			req:  httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/k8s/api/v1/watch/pods", nil),
			want: true,
		},
		{
			name: "watch=false explicitly",
			req:  httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/k8s/api/v1/pods?watch=false", nil),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWatchRequest(tt.req); got != tt.want {
				t.Errorf("isWatchRequest = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleK8sProxy_StreamingWatch(t *testing.T) {
	hub := NewHub(slog.Default())

	agent := &AgentConnection{
		ClusterID: "cluster-watch",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set("cluster-watch", agent)

	proxy := NewProxyHandler(hub, slog.Default())
	router := chi.NewRouter()
	router.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	go func() {
		msg := <-agent.sendCh
		if msg.Type != protocol.MsgK8sStreamRequest {
			t.Errorf("expected K8S_STREAM_REQUEST, got %s", msg.Type)
			return
		}
		stream, ok := agent.Streams.GetStream(msg.StreamID)
		if !ok {
			return
		}
		// Header
		hdr, _ := json.Marshal(protocol.K8sStreamFrame{
			Kind:       protocol.K8sStreamFrameHeader,
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})
		stream.DataCh <- hdr
		// Two data chunks
		for _, chunk := range []string{`{"type":"ADDED","object":{"kind":"Pod","metadata":{"name":"a"}}}` + "\n", `{"type":"MODIFIED","object":{"kind":"Pod","metadata":{"name":"a"}}}` + "\n"} {
			d, _ := json.Marshal(protocol.K8sStreamFrame{
				Kind: protocol.K8sStreamFrameData,
				Body: base64.StdEncoding.EncodeToString([]byte(chunk)),
			})
			stream.DataCh <- d
		}
		// End
		end, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})
		stream.DataCh <- end
	}()

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/cluster-watch/k8s/api/v1/pods?watch=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"name":"a"`) {
		t.Fatalf("expected watch events in body, got: %s", body)
	}
	if !strings.Contains(body, `"ADDED"`) || !strings.Contains(body, `"MODIFIED"`) {
		t.Fatalf("expected both ADDED and MODIFIED events, got: %s", body)
	}
}

func TestHandleK8sProxyStreamingWatchSanitizesResponseHeaders(t *testing.T) {
	hub := NewHub(slog.Default())

	agent := &AgentConnection{
		ClusterID: "cluster-watch-headers",
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set("cluster-watch-headers", agent)

	proxy := NewProxyHandler(hub, slog.Default())
	router := chi.NewRouter()
	router.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	go func() {
		msg := <-agent.sendCh
		stream, ok := agent.Streams.GetStream(msg.StreamID)
		if !ok {
			return
		}
		hdr, _ := json.Marshal(protocol.K8sStreamFrame{
			Kind:       protocol.K8sStreamFrameHeader,
			StatusCode: http.StatusOK,
			Headers: map[string]string{
				"Audit-Id":          "audit-stream",
				"Content-Type":      "application/json",
				"Set-Cookie":        "k8s_session=leaked; Path=/",
				"Transfer-Encoding": "chunked",
				"WWW-Authenticate":  "Bearer",
			},
		})
		data, _ := json.Marshal(protocol.K8sStreamFrame{
			Kind: protocol.K8sStreamFrameData,
			Body: base64.StdEncoding.EncodeToString([]byte(`{"type":"ADDED"}` + "\n")),
		})
		end, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})
		stream.DataCh <- hdr
		stream.DataCh <- data
		stream.DataCh <- end
	}()

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/cluster-watch-headers/k8s/api/v1/pods?watch=true", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Audit-Id"); got != "audit-stream" {
		t.Fatalf("expected Audit-Id to be preserved, got %q", got)
	}
	for _, header := range []string{"Set-Cookie", "Transfer-Encoding", "WWW-Authenticate"} {
		if got := w.Header().Get(header); got != "" {
			t.Fatalf("expected unsafe streaming header %s to be stripped, got %q", header, got)
		}
	}
}

func TestForwardToOwnerPodSanitizesResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Fatalf("expected sibling auth header to be preserved")
		}
		w.Header().Set("Audit-Id", "audit-forward")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "k8s_session=leaked; Path=/")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"forwarded":true}`))
	}))
	defer upstream.Close()

	oldClient := proxyHTTPClient
	proxyHTTPClient = upstream.Client()
	t.Cleanup(func() {
		proxyHTTPClient = oldClient
	})

	clusterID := "cluster-forward"
	hub := NewHub(slog.Default())
	hub.SetLocator(NewFakeLocatorForTest("self:8000", map[string]string{
		clusterID: strings.TrimPrefix(upstream.URL, "http://"),
	}))
	proxy := NewProxyHandler(hub, slog.Default())

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+clusterID+"/k8s/api/v1/pods", nil)
	req.Header.Set("Authorization", "Bearer caller")
	w := httptest.NewRecorder()

	if !proxy.forwardToOwnerPod(w, req, clusterID) {
		t.Fatal("expected request to be forwarded")
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Audit-Id"); got != "audit-forward" {
		t.Fatalf("expected Audit-Id to be preserved, got %q", got)
	}
	for _, header := range []string{"Set-Cookie", "Transfer-Encoding", "WWW-Authenticate"} {
		if got := w.Header().Get(header); got != "" {
			t.Fatalf("expected unsafe forwarded header %s to be stripped, got %q", header, got)
		}
	}
}

func TestWriteK8sResponse_ZeroStatusCode(t *testing.T) {
	resp := &protocol.K8sResponsePayload{
		StatusCode: 0,
	}

	w := httptest.NewRecorder()
	writeK8sResponse(w, resp)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for zero status code, got %d", w.Code)
	}
}

func tunnelMetricValue(t *testing.T, collector interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := collector.Write(m); err != nil {
		t.Fatalf("collector.Write(): %v", err)
	}
	switch {
	case m.Counter != nil:
		return m.Counter.GetValue()
	case m.Gauge != nil:
		return m.Gauge.GetValue()
	default:
		t.Fatal("unsupported metric type")
		return 0
	}
}
