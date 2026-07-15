package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// TEST-06: EventStreamHandler.Stream with no JWT wired accepts (dev mode)
// and writes SSE headers + connected comment.
func TestEventStream_UnauthedWhenNoJWT(t *testing.T) {
	bus := events.NewBus()
	h := NewEventStreamHandler(bus)
	// Cancel client quickly so Stream exits.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream/", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.Stream(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type=%q", ct)
	}
	if !strings.Contains(rec.Body.String(), "connected") {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

// TEST-06: when JWT is wired without credentials, stream rejects.
func TestEventStream_RequiresAuthWhenJWTWired(t *testing.T) {
	bus := events.NewBus()
	h := NewEventStreamHandler(bus)
	jwt := auth.NewJWTManager("test-secret-key-for-stream", 15)
	h.SetAuth(jwt, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream/", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestEventStream_NilBusUnavailable(t *testing.T) {
	h := &EventStreamHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream/", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

// runStream starts h.Stream in a goroutine against a context-bound request
// and returns the recorder plus a wait func that blocks until the stream
// exits (via the ctx deadline).
func runStream(t *testing.T, h *EventStreamHandler, timeout time.Duration, decorate func(*http.Request)) (*httptest.ResponseRecorder, func()) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream/", nil)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req = req.WithContext(ctx)
	if decorate != nil {
		decorate(req)
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.Stream(rec, req)
		close(done)
	}()
	return rec, func() {
		defer cancel()
		select {
		case <-done:
		case <-time.After(timeout + 2*time.Second):
			t.Fatal("stream did not exit")
		}
	}
}

// P4.1: bus events are written as default-message frames — id + data only,
// no `event:` line (the JSON envelope carries `type`).
func TestEventStream_DefaultMessageFraming(t *testing.T) {
	bus := events.NewBus()
	h := NewEventStreamHandler(bus)
	rec, wait := runStream(t, h, 300*time.Millisecond, nil)
	time.Sleep(50 * time.Millisecond) // let Stream subscribe
	bus.Publish(events.TypeClusterConnected, map[string]any{"cluster_id": uuid.New().String()})
	wait()

	body := rec.Body.String()
	if !strings.Contains(body, "id: ") {
		t.Fatalf("missing id line: %q", body)
	}
	if !strings.Contains(body, `"type":"cluster.connected"`) {
		t.Fatalf("event envelope not delivered: %q", body)
	}
	if strings.Contains(body, "\nevent: ") || strings.HasPrefix(body, "event: ") {
		t.Fatalf("named-event framing must be gone: %q", body)
	}
}

// P4.1: the keepalive is a real sys.ping data frame, not an SSE comment.
func TestEventStream_HeartbeatPingFrame(t *testing.T) {
	bus := events.NewBus()
	h := NewEventStreamHandler(bus)
	h.keepaliveInterval = 20 * time.Millisecond
	rec, wait := runStream(t, h, 150*time.Millisecond, nil)
	wait()

	body := rec.Body.String()
	if !strings.Contains(body, `data: {"type":"sys.ping","time":`) {
		t.Fatalf("missing sys.ping data frame: %q", body)
	}
	if strings.Contains(body, ": ping") {
		t.Fatalf("comment keepalive must be gone: %q", body)
	}
	if strings.Contains(body, "\nevent: ") {
		t.Fatalf("ping must be a default-message frame: %q", body)
	}
}

type stubStreamRBACQuerier struct {
	bindings []rbac.RoleBinding
}

func (s stubStreamRBACQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return s.bindings, nil
}

// P4.1: restricted users keep receiving sys.* frames (no cluster_id — exempt
// from the SEC-R07 drop) while unscoped domain events are still dropped.
func TestEventStream_RestrictedUserGetsPingNotUnscopedEvents(t *testing.T) {
	bus := events.NewBus()
	h := NewEventStreamHandler(bus)
	jwtMgr := auth.NewJWTManager("test-secret-key-for-stream", 15)
	h.SetAuth(jwtMgr, nil)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	allowedCluster := uuid.New()
	h.SetAuthorization(rbac.NewEngine(), stubStreamRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: allowedCluster.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}},
	}}})
	h.keepaliveInterval = 20 * time.Millisecond

	rec, wait := runStream(t, h, 300*time.Millisecond, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	})
	time.Sleep(50 * time.Millisecond) // let Stream subscribe
	// Unscoped domain event: no cluster_id → dropped for restricted users.
	bus.Publish(events.TypeClusterConnected, map[string]any{"foo": "bar"})
	// sys.* bus event: unscoped by design but exempt from the drop.
	bus.Publish(events.Type("sys.ping"), map[string]any{})
	// Scoped event for the allowed cluster still flows.
	bus.Publish(events.TypeClusterMetrics, map[string]any{"cluster_id": allowedCluster.String()})
	wait()

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"sys.ping"`) {
		t.Fatalf("restricted user must receive sys.ping: %q", body)
	}
	if strings.Contains(body, "cluster.connected") {
		t.Fatalf("unscoped domain event must be dropped for restricted user: %q", body)
	}
	if !strings.Contains(body, `"type":"cluster.metrics"`) {
		t.Fatalf("scoped event for allowed cluster must be delivered: %q", body)
	}
}
