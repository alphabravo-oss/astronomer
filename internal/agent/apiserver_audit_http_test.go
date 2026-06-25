package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestHTTPAuditSenderPostsBatchWithBearer(t *testing.T) {
	clusterID := "11111111-2222-3333-4444-555555555555"
	const token = "astro_agent_ingest_secret"

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotType   string
		gotBody   protocol.ApiserverAuditPayload
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	// srv.URL is http://...; feed the ws:// equivalent so we also exercise the
	// scheme rewrite.
	wsURL := "ws://" + srv.Listener.Addr().String()
	s := newHTTPAuditSender(srv.Client(), wsURL, clusterID, func() string { return token }, nil, nil)

	events := []json.RawMessage{
		json.RawMessage(`{"auditID":"a1","verb":"get"}`),
		json.RawMessage(`{"auditID":"a2","verb":"list"}`),
	}
	if err := s.Send(context.Background(), events); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: got %s, want POST", gotMethod)
	}
	if want := "/api/v1/clusters/" + clusterID + "/apiserver-audit/"; gotPath != want {
		t.Errorf("path: got %s, want %s", gotPath, want)
	}
	if want := "Bearer " + token; gotAuth != want {
		t.Errorf("auth header: got %q, want %q", gotAuth, want)
	}
	if gotType != "application/json" {
		t.Errorf("content-type: got %q, want application/json", gotType)
	}
	if len(gotBody.Events) != len(events) {
		t.Fatalf("events: got %d, want %d", len(gotBody.Events), len(events))
	}
	for i := range events {
		if string(gotBody.Events[i]) != string(events[i]) {
			t.Errorf("event %d: got %s, want %s", i, gotBody.Events[i], events[i])
		}
	}
}

func TestHTTPAuditSenderNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	s := newHTTPAuditSender(srv.Client(), srv.URL, "cid", func() string { return "tok" }, nil, nil)
	err := s.Send(context.Background(), []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)})
	if err == nil {
		t.Fatal("expected error on non-2xx so the tailer holds its checkpoint")
	}
}

func TestHTTPAuditSenderEmptyBatchNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	s := newHTTPAuditSender(srv.Client(), srv.URL, "cid", func() string { return "tok" }, nil, nil)
	if err := s.Send(context.Background(), nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if called {
		t.Fatal("empty batch must not issue an HTTP request")
	}
}

func TestSelectAuditSenderByDelivery(t *testing.T) {
	cfg := func(delivery string) *AgentConfig {
		return &AgentConfig{ServerURL: "ws://host:8000", ClusterID: "cid", AuditDelivery: delivery}
	}

	tok := func() string { return "astro_agent_ingest_x" }
	empty := func() string { return "" }

	// Default (empty / "tunnel") and unknown values both pick the tunnel sender,
	// so audit works out-of-the-box with no extra credential.
	for _, d := range []string{"", "tunnel", "bogus"} {
		if _, ok := SelectAuditSender(cfg(d), &captureSender{}, tok, nil).(tunnelAuditSender); !ok {
			t.Errorf("delivery %q: expected tunnelAuditSender", d)
		}
	}

	// http -> the HTTP sender (PATH A). It reads the token lazily on each Send,
	// so selection no longer depends on whether a token is present yet; the
	// tunnel fallback is carried INSIDE the sender for the not-yet-delivered case.
	if _, ok := SelectAuditSender(cfg("http"), &captureSender{}, tok, nil).(*httpAuditSender); !ok {
		t.Error("delivery http: expected *httpAuditSender")
	}
	if _, ok := SelectAuditSender(cfg("http"), &captureSender{}, empty, nil).(*httpAuditSender); !ok {
		t.Error("delivery http (no token yet): expected *httpAuditSender with tunnel fallback inside")
	}

	// stub -> the no-op logging sender.
	if _, ok := SelectAuditSender(cfg("stub"), &captureSender{}, tok, nil).(stubAuditSender); !ok {
		t.Error("delivery stub: expected stubAuditSender")
	}
}

// TestHTTPAuditSenderLazyTokenFallback: with no token yet (getter returns ""),
// the http sender must NOT issue an HTTP request and must instead deliver the
// batch over the tunnel fallback. Once the token arrives (the CONNECT_ACK case),
// the next batch POSTs with the bearer token.
func TestHTTPAuditSenderLazyTokenFallback(t *testing.T) {
	var httpCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpCalls++
		if got := r.Header.Get("Authorization"); got != "Bearer late-token" {
			t.Errorf("auth header: got %q, want Bearer late-token", got)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	token := "" // empty until the "ACK" arrives below
	fallback := &captureSender{}
	s := newHTTPAuditSender(srv.Client(), "ws://"+srv.Listener.Addr().String(), "cid",
		func() string { return token }, newTunnelAuditSender(fallback), nil)

	events := []json.RawMessage{json.RawMessage(`{"auditID":"a1"}`)}

	// Phase 1: no token yet -> tunnel fallback, no HTTP request.
	if err := s.Send(context.Background(), events); err != nil {
		t.Fatalf("Send (pre-token): %v", err)
	}
	if httpCalls != 0 {
		t.Errorf("expected 0 HTTP calls before token arrives, got %d", httpCalls)
	}
	if len(fallback.payloads) != 1 {
		t.Fatalf("expected 1 tunnel-fallback batch, got %d", len(fallback.payloads))
	}

	// Phase 2: token delivered (CONNECT_ACK) -> HTTP POST with bearer.
	token = "late-token"
	if err := s.Send(context.Background(), events); err != nil {
		t.Fatalf("Send (post-token): %v", err)
	}
	if httpCalls != 1 {
		t.Errorf("expected 1 HTTP call after token arrives, got %d", httpCalls)
	}
	if len(fallback.payloads) != 1 {
		t.Errorf("tunnel fallback must not receive the post-token batch, got %d", len(fallback.payloads))
	}
}

func TestHTTPBaseFromWS(t *testing.T) {
	cases := map[string]string{
		"ws://host:8000":   "http://host:8000",
		"wss://host:8000/": "https://host:8000",
		"https://host":     "https://host",
		"http://host/":     "http://host",
	}
	for in, want := range cases {
		if got := httpBaseFromWS(in); got != want {
			t.Errorf("httpBaseFromWS(%q) = %q, want %q", in, got, want)
		}
	}
}
