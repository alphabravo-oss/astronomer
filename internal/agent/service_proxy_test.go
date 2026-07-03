package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestServiceProxyStripsClientAuthHeaders(t *testing.T) {
	var seen http.Header
	proxy := NewServiceProxy(slog.New(slog.NewTextHandler(io.Discard, nil)))
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    req,
		}, nil
	})}

	payload, err := json.Marshal(protocol.ServiceProxyRequestPayload{
		ServiceName: "grafana",
		Namespace:   "observability",
		Port:        3000,
		Method:      http.MethodGet,
		Path:        "/api/health",
		Headers: map[string]string{
			"Accept":                    "application/json",
			"Authorization":             "Bearer browser-jwt",
			"Connection":                "upgrade",
			"Content-Type":              "application/json",
			"Cookie":                    "astronomer_session=abc",
			"Host":                      "astronomer.example.com",
			"Impersonate-User":          "system:admin",
			"Proxy-Authorization":       "Basic abc",
			"X-Forwarded-Authorization": "Bearer forwarded",
			"X-Forwarded-For":           "203.0.113.10",
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	resp, err := proxy.HandleRequest(context.Background(), &protocol.Message{Payload: payload})
	if err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	var decoded protocol.ServiceProxyResponsePayload
	if err := json.Unmarshal(resp.Payload, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d; error=%s", decoded.StatusCode, http.StatusOK, decoded.Error)
	}

	for _, name := range []string{
		"Authorization",
		"Connection",
		"Cookie",
		"Host",
		"Impersonate-User",
		"Proxy-Authorization",
		"X-Forwarded-Authorization",
		"X-Forwarded-For",
	} {
		if got := seen.Get(name); got != "" {
			t.Fatalf("%s forwarded to service as %q", name, got)
		}
	}
	if got := seen.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want application/json", got)
	}
	if got := seen.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

// TestServiceProxyRejectsSSRFHostSmuggling proves a crafted ServiceName or
// Namespace can't redirect the in-cluster call to an arbitrary host, and that a
// dialed request always targets the *.svc.cluster.local authority.
func TestServiceProxyRejectsSSRFHostSmuggling(t *testing.T) {
	var dialedHost string
	proxy := NewServiceProxy(slog.New(slog.NewTextHandler(io.Discard, nil)))
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		dialedHost = req.URL.Host
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})}

	for _, tc := range []struct{ name, svc, ns string }{
		{"userinfo in service", "svc@169.254.169.254", "default"},
		{"slash in service", "svc/evil", "default"},
		{"colon port in namespace", "svc", "ns:6443"},
		{"uppercase (not a label)", "Svc", "default"},
	} {
		payload, _ := json.Marshal(protocol.ServiceProxyRequestPayload{
			ServiceName: tc.svc, Namespace: tc.ns, Port: 80, Method: http.MethodGet, Path: "/",
		})
		resp, err := proxy.HandleRequest(context.Background(), &protocol.Message{Payload: payload})
		if err != nil {
			t.Fatalf("%s: HandleRequest: %v", tc.name, err)
		}
		var decoded protocol.ServiceProxyResponsePayload
		_ = json.Unmarshal(resp.Payload, &decoded)
		if decoded.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (must reject)", tc.name, decoded.StatusCode)
		}
	}

	// A legitimate request still dials the cluster-local authority.
	payload, _ := json.Marshal(protocol.ServiceProxyRequestPayload{
		ServiceName: "grafana", Namespace: "observability", Port: 3000, Method: http.MethodGet, Path: "/api/health",
	})
	if _, err := proxy.HandleRequest(context.Background(), &protocol.Message{Payload: payload}); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if dialedHost != "grafana.observability.svc.cluster.local:3000" {
		t.Fatalf("dialed host = %q, want grafana.observability.svc.cluster.local:3000", dialedHost)
	}
}
