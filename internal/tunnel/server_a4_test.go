package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// auditValidatorStub satisfies AgentTokenValidator (via the embedded nil
// interface — none of those methods are called by the audit helpers) AND
// audit.Querier (the explicit CreateAuditLogV1) so a Hub built with it routes
// audit.Record through the synchronous fallback into recorded.
type auditValidatorStub struct {
	AgentTokenValidator
	recorded []sqlc.CreateAuditLogV1Params
	err      error
}

func (s *auditValidatorStub) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	s.recorded = append(s.recorded, arg)
	return s.err
}

func TestHandleWebSocketRejects429WhenBlocked(t *testing.T) {
	hub := NewHub(nil)
	lim := NewConnectFailureLimiter(3, time.Minute, nil)
	hub.SetConnectLimiter(lim, 0)

	// httptest default RemoteAddr is 192.0.2.1:1234 -> key 192.0.2.1.
	const ip = "192.0.2.1"
	for i := 0; i < 3; i++ {
		lim.Fail(ip)
	}

	req := httptest.NewRequest("GET", "/api/v1/ws/agent/tunnel/c1/", nil)
	rec := httptest.NewRecorder()
	hub.HandleWebSocket(rec, req)

	if rec.Code != 429 {
		t.Fatalf("expected 429 before upgrade, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on the 429")
	}
	if got := rec.Body.String(); got != `{"code":"rate_limited"}` {
		t.Fatalf("unexpected 429 body %q", got)
	}
}

func TestHandleWebSocketBlockedIsPerIP(t *testing.T) {
	hub := NewHub(nil)
	lim := NewConnectFailureLimiter(3, time.Minute, nil)
	hub.SetConnectLimiter(lim, 0)
	for i := 0; i < 3; i++ {
		lim.Fail("192.0.2.1")
	}

	// A different source IP must NOT be throttled — it sails past the gate and
	// fails later (here: on the read of the never-sent CONNECT frame), i.e. it
	// is not a 429.
	req := httptest.NewRequest("GET", "/api/v1/ws/agent/tunnel/c1/", nil)
	req.RemoteAddr = "198.51.100.50:9000"
	rec := httptest.NewRecorder()
	hub.HandleWebSocket(rec, req)

	if rec.Code == 429 {
		t.Fatal("an unrelated IP must not receive a 429")
	}
}

func TestConnectTimestampOutsideSkew(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	skew := 5 * time.Minute

	cases := []struct {
		name string
		ts   time.Time
		skew time.Duration
		want bool
	}{
		{"within window", now.Add(-2 * time.Minute), skew, false},
		{"stale replay", now.Add(-10 * time.Minute), skew, true},
		{"far future", now.Add(10 * time.Minute), skew, true},
		{"exact edge accepted", now.Add(-5 * time.Minute), skew, false},
		{"zero timestamp back-compat", time.Time{}, skew, false},
		{"disabled knob", now.Add(-10 * time.Minute), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectTimestampOutsideSkew(now, tc.ts, tc.skew); got != tc.want {
				t.Fatalf("connectTimestampOutsideSkew = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRecordAgentConnectedAudit(t *testing.T) {
	stub := &auditValidatorStub{}
	hub := NewHubWithValidator(nil, stub)
	addr := netip.MustParseAddr("203.0.113.5")
	req := httptest.NewRequest("GET", "/x", nil)
	payload := protocol.ConnectPayload{ClusterID: "cluster-1", AgentID: "agent-1", AgentVersion: "1.2.3"}

	hub.recordAgentConnected(context.Background(), payload, &addr, req, "agent", "sess-9", true)

	if len(stub.recorded) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(stub.recorded))
	}
	row := stub.recorded[0]
	if row.Action != actionAgentConnected {
		t.Fatalf("action = %q, want %q", row.Action, actionAgentConnected)
	}
	if row.ResourceID != "cluster-1" {
		t.Fatalf("resource_id = %q, want cluster-1", row.ResourceID)
	}
	detail := decodeDetail(t, row.Detail)
	if detail["token_kind"] != "durable" {
		t.Fatalf("token_kind = %v, want durable", detail["token_kind"])
	}
	if detail["source_ip"] != "203.0.113.5" {
		t.Fatalf("source_ip = %v, want 203.0.113.5", detail["source_ip"])
	}
	if detail["agent_version"] != "1.2.3" {
		t.Fatalf("agent_version = %v, want 1.2.3", detail["agent_version"])
	}
	if detail["session_id"] != "sess-9" {
		t.Fatalf("session_id = %v, want sess-9", detail["session_id"])
	}
}

func TestRecordAgentConnectedRegistrationKind(t *testing.T) {
	stub := &auditValidatorStub{}
	hub := NewHubWithValidator(nil, stub)
	addr := netip.MustParseAddr("203.0.113.6")
	req := httptest.NewRequest("GET", "/x", nil)

	hub.recordAgentConnected(context.Background(), protocol.ConnectPayload{ClusterID: "c2"}, &addr, req, "registration", "s", false)
	detail := decodeDetail(t, stub.recorded[0].Detail)
	if detail["token_kind"] != "registration" {
		t.Fatalf("token_kind = %v, want registration", detail["token_kind"])
	}
}

func TestRecordAgentAuthFailedAudit(t *testing.T) {
	stub := &auditValidatorStub{}
	hub := NewHubWithValidator(nil, stub)
	addr := netip.MustParseAddr("203.0.113.9")
	req := httptest.NewRequest("GET", "/x", nil)
	payload := protocol.ConnectPayload{ClusterID: "cluster-bad", AgentVersion: "9.9.9"}

	hub.recordAgentAuthFailed(context.Background(), payload, &addr, req, "invalid", "timestamp_skew")

	if len(stub.recorded) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(stub.recorded))
	}
	row := stub.recorded[0]
	if row.Action != actionAgentAuthFailed {
		t.Fatalf("action = %q, want %q", row.Action, actionAgentAuthFailed)
	}
	detail := decodeDetail(t, row.Detail)
	if detail["token_kind"] != "invalid" {
		t.Fatalf("token_kind = %v, want invalid", detail["token_kind"])
	}
	if detail["reason"] != "timestamp_skew" {
		t.Fatalf("reason = %v, want timestamp_skew", detail["reason"])
	}
	if detail["source_ip"] != "203.0.113.9" {
		t.Fatalf("source_ip = %v, want 203.0.113.9", detail["source_ip"])
	}
}

func TestRecordAgentConnectedFailOpen(t *testing.T) {
	stub := &auditValidatorStub{err: errors.New("db down")}
	hub := NewHubWithValidator(nil, stub)
	addr := netip.MustParseAddr("203.0.113.5")
	req := httptest.NewRequest("GET", "/x", nil)

	// A write error must not propagate or panic — the helper returns void and
	// the connection would proceed.
	hub.recordAgentConnected(context.Background(), protocol.ConnectPayload{ClusterID: "c"}, &addr, req, "agent", "s", false)
}

func decodeDetail(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("empty detail JSON")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	return m
}
