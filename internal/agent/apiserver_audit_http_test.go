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
	s := newHTTPAuditSender(srv.Client(), wsURL, clusterID, token)

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

	s := newHTTPAuditSender(srv.Client(), srv.URL, "cid", "tok")
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

	s := newHTTPAuditSender(srv.Client(), srv.URL, "cid", "tok")
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

	// Default (empty / "tunnel") and unknown values both pick the tunnel sender,
	// so audit works out-of-the-box with no extra credential.
	for _, d := range []string{"", "tunnel", "bogus"} {
		if _, ok := SelectAuditSender(cfg(d), &captureSender{}, "astro_agent_ingest_x", nil).(tunnelAuditSender); !ok {
			t.Errorf("delivery %q: expected tunnelAuditSender", d)
		}
	}

	// http with a delivered ingest token -> HTTP sender (PATH A).
	if _, ok := SelectAuditSender(cfg("http"), &captureSender{}, "astro_agent_ingest_x", nil).(httpAuditSender); !ok {
		t.Error("delivery http with token: expected httpAuditSender")
	}
	// http without a token can't authenticate, so it falls back to the tunnel.
	if _, ok := SelectAuditSender(cfg("http"), &captureSender{}, "", nil).(tunnelAuditSender); !ok {
		t.Error("delivery http without token: expected tunnel fallback")
	}

	// stub -> the no-op logging sender.
	if _, ok := SelectAuditSender(cfg("stub"), &captureSender{}, "astro_agent_ingest_x", nil).(stubAuditSender); !ok {
		t.Error("delivery stub: expected stubAuditSender")
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
