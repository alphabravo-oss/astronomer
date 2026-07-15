package webhook

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// fakeTapQuerier records every InsertWebhookDelivery call so the test
// can assert on the (subscription, event_name) pairs produced.
type fakeTapQuerier struct {
	mu       sync.Mutex
	subs     []sqlc.WebhookSubscription
	inserted []sqlc.InsertWebhookDeliveryParams
}

func (f *fakeTapQuerier) ListEnabledWebhookSubscriptions(_ context.Context) ([]sqlc.WebhookSubscription, error) {
	return f.subs, nil
}

func (f *fakeTapQuerier) InsertWebhookDelivery(_ context.Context, arg sqlc.InsertWebhookDeliveryParams) (sqlc.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserted = append(f.inserted, arg)
	return sqlc.WebhookDelivery{ID: uuid.New()}, nil
}

func newTapSubscription(name string, filters []string) sqlc.WebhookSubscription {
	raw, _ := json.Marshal(filters)
	return sqlc.WebhookSubscription{
		ID:             uuid.New(),
		Name:           name,
		Url:            "http://example.invalid/" + name,
		EventFilters:   raw,
		ExtraHeaders:   json.RawMessage(`{}`),
		Enabled:        true,
		MaxRetries:     5,
		TimeoutSeconds: 10,
	}
}

func TestEventBusTap_QueuesMatchingDeliveries(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{
			newTapSubscription("slack-audit", []string{"audit.*"}),
			newTapSubscription("pager-cluster", []string{"cluster.*"}),
		},
	}
	tap := NewTap(q, nil, nil)
	tap.SetCacheTTL(time.Millisecond) // bypass cache during the test
	ev := events.Event{
		ID:   1,
		Type: "audit.user.login",
		Time: time.Now().UTC(),
		Data: map[string]any{"user": "alice"},
	}
	tap.HandleEvent(context.Background(), ev)

	// Only the audit-* subscription should have a row.
	if got := len(q.inserted); got != 1 {
		t.Fatalf("expected 1 inserted delivery, got %d", got)
	}
	got := q.inserted[0]
	if got.EventName != "audit.user.login" {
		t.Errorf("event_name = %q, want audit.user.login", got.EventName)
	}
	if !got.NextAttemptAt.Valid {
		t.Errorf("next_attempt_at must be set on enqueue (dispatcher picks up on next tick)")
	}
	if got.Status != "queued" {
		t.Errorf("status = %q, want queued", got.Status)
	}
	// The payload should contain the JSON-encoded envelope.
	if got.PayloadSize == 0 {
		t.Errorf("payload_size = 0, expected non-zero")
	}
}

func TestEventBusTap_IgnoresNonMatching(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{
			newTapSubscription("only-auth", []string{"auth.login_failed"}),
		},
	}
	tap := NewTap(q, nil, nil)
	tap.SetCacheTTL(time.Millisecond)
	tap.HandleEvent(context.Background(), events.Event{
		ID:   1,
		Type: "cluster.connected",
		Time: time.Now().UTC(),
	})
	if got := len(q.inserted); got != 0 {
		t.Fatalf("expected 0 inserted deliveries (no matching filter), got %d", got)
	}
}

func TestEventBusTap_SkipsRemoteDuplicate(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{newTapSubscription("cluster-events", []string{"cluster.*"})},
	}
	tap := NewTap(q, nil, nil)
	tap.SetCacheTTL(time.Hour)
	event := events.Event{ID: 42, Type: events.TypeClusterConnected, Time: time.Now().UTC()}
	tap.HandleEvent(context.Background(), event)
	event.Remote = true
	tap.HandleEvent(context.Background(), event)
	if got := len(q.inserted); got != 1 {
		t.Fatalf("webhook inserts = %d, want one origin delivery and no remote duplicate", got)
	}
}

func TestEventBusTap_MultipleSubscriptionsMatch_OneEvent(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{
			newTapSubscription("a", []string{"audit.*"}),
			newTapSubscription("b", []string{"*"}),
			newTapSubscription("c", []string{"audit.user.*"}),
		},
	}
	tap := NewTap(q, nil, nil)
	tap.SetCacheTTL(time.Millisecond)
	tap.HandleEvent(context.Background(), events.Event{
		ID: 1, Type: "audit.user.login", Time: time.Now().UTC(),
	})
	if got, want := len(q.inserted), 3; got != want {
		t.Errorf("expected %d inserts (one per matching subscription), got %d", want, got)
	}
}

func TestEventBusTap_CacheRefreshOnInvalidate(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{newTapSubscription("a", []string{"audit.*"})},
	}
	tap := NewTap(q, nil, nil)
	// Long TTL so the second event would normally use the cache.
	tap.SetCacheTTL(time.Hour)

	tap.HandleEvent(context.Background(), events.Event{ID: 1, Type: "audit.user.login", Time: time.Now()})
	if got := len(q.inserted); got != 1 {
		t.Fatalf("first event: inserts = %d, want 1", got)
	}

	// Operator adds a new subscription. The handler is supposed to
	// Invalidate() to make this visible on the next event.
	q.subs = append(q.subs, newTapSubscription("b", []string{"audit.*"}))
	tap.Invalidate()

	tap.HandleEvent(context.Background(), events.Event{ID: 2, Type: "audit.user.login", Time: time.Now()})
	// Both subscriptions match now → cumulative inserts = 1 (first event) + 2 (second event) = 3.
	if got := len(q.inserted); got != 3 {
		t.Errorf("after invalidate: cumulative inserts = %d, want 3", got)
	}
}

// SEC-R09: secret-shaped keys in event detail must be redacted before insert.
func TestEventBusTap_RedactsSecretShapedPayload(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{
			newTapSubscription("audit-all", []string{"audit.*"}),
		},
	}
	tap := NewTap(q, nil, nil)
	tap.SetCacheTTL(time.Millisecond)
	tap.HandleEvent(context.Background(), events.Event{
		ID:   1,
		Type: "audit.user.login",
		Time: time.Now().UTC(),
		Data: map[string]any{
			"user":     "alice",
			"password": "super-secret-value",
			"token":    "tok-abc",
			"nested": map[string]any{
				"client_secret": "still-secret",
			},
		},
	})
	if got := len(q.inserted); got != 1 {
		t.Fatalf("expected 1 insert, got %d", got)
	}
	var env struct {
		Detail map[string]any `json:"detail"`
	}
	if err := json.Unmarshal(q.inserted[0].Payload, &env); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if env.Detail["password"] != "[redacted]" {
		t.Errorf("password = %v, want [redacted]", env.Detail["password"])
	}
	if env.Detail["token"] != "[redacted]" {
		t.Errorf("token = %v, want [redacted]", env.Detail["token"])
	}
	if env.Detail["user"] != "alice" {
		t.Errorf("non-secret user field must pass through, got %v", env.Detail["user"])
	}
	nested, _ := env.Detail["nested"].(map[string]any)
	if nested == nil || nested["client_secret"] != "[redacted]" {
		t.Errorf("nested client_secret = %v, want [redacted]", nested)
	}
	// Ensure the raw secret never appears anywhere in the stored JSON.
	raw := string(q.inserted[0].Payload)
	if strings.Contains(raw, "super-secret-value") || strings.Contains(raw, "still-secret") {
		t.Errorf("payload leaked secret material: %s", raw)
	}
}

func TestEventBusTap_MalformedFiltersSkippedNotPanic(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{
			{
				ID:           uuid.New(),
				Name:         "broken",
				Url:          "http://x.invalid",
				EventFilters: json.RawMessage(`not-json`),
				ExtraHeaders: json.RawMessage(`{}`),
				Enabled:      true,
			},
			newTapSubscription("good", []string{"audit.*"}),
		},
	}
	tap := NewTap(q, nil, nil)
	tap.SetCacheTTL(time.Millisecond)
	tap.HandleEvent(context.Background(), events.Event{ID: 1, Type: "audit.user.login", Time: time.Now()})
	if got := len(q.inserted); got != 1 {
		t.Errorf("expected 1 insert (broken subscription skipped, good one inserted), got %d", got)
	}
}

// TestEventBusTap_ExcludesSysEvents locks the R9 (P4.6) decision: sys.*
// stream-plumbing events (sys.ping heartbeats) never create delivery rows,
// even for a `*` filter — otherwise a broad subscription would enqueue one
// row per heartbeat tick.
func TestEventBusTap_ExcludesSysEvents(t *testing.T) {
	q := &fakeTapQuerier{
		subs: []sqlc.WebhookSubscription{
			newTapSubscription("catch-all", []string{"*"}),
		},
	}
	tap := NewTap(q, nil, nil)
	tap.SetCacheTTL(time.Millisecond)
	tap.HandleEvent(context.Background(), events.Event{ID: 1, Type: "sys.ping", Time: time.Now()})
	if got := len(q.inserted); got != 0 {
		t.Fatalf("expected 0 inserts for sys.ping (sys.* excluded from tap matching), got %d", got)
	}
	// A non-sys event on the same subscription still queues — the exclusion
	// is prefix-scoped, not a general filter change.
	tap.HandleEvent(context.Background(), events.Event{ID: 2, Type: "cluster.k8s_changed", Time: time.Now()})
	if got := len(q.inserted); got != 1 {
		t.Fatalf("expected 1 insert for cluster.k8s_changed (tap semantics unchanged), got %d", got)
	}
}
