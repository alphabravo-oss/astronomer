package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
)

// TapQuerier is the database surface the bus tap needs. *sqlc.Queries
// satisfies it directly.
type TapQuerier interface {
	ListEnabledWebhookSubscriptions(ctx context.Context) ([]sqlc.WebhookSubscription, error)
	InsertWebhookDelivery(ctx context.Context, arg sqlc.InsertWebhookDeliveryParams) (sqlc.WebhookDelivery, error)
}

// SubscriptionCacheTTL is how long the tap caches the enabled-subscriptions
// list before refetching. Short enough that an operator's "enable + emit
// an event" interaction feels live; long enough that a busy stack
// (thousands of events per minute) doesn't hammer the SELECT.
const SubscriptionCacheTTL = 15 * time.Second

// Tap subscribes to an *events.Bus and inserts webhook_deliveries rows
// for every event that matches at least one enabled subscription's
// filter list. The Tap is a singleton per process — Start launches a
// background goroutine that consumes the bus channel until ctx cancels.
type Tap struct {
	q   TapQuerier
	bus *events.Bus
	log *slog.Logger

	mu       sync.Mutex
	cache    []sqlc.WebhookSubscription
	cachedAt time.Time
	cacheTTL time.Duration
}

// NewTap wires the dependencies. q is the *sqlc.Queries handle the
// rest of the platform uses; log is optional (default: slog.Default).
func NewTap(q TapQuerier, bus *events.Bus, log *slog.Logger) *Tap {
	if log == nil {
		log = slog.Default()
	}
	return &Tap{
		q:        q,
		bus:      bus,
		log:      log,
		cacheTTL: SubscriptionCacheTTL,
	}
}

// SetCacheTTL is the test seam; production keeps the default.
func (t *Tap) SetCacheTTL(d time.Duration) {
	if d > 0 {
		t.cacheTTL = d
	}
}

// Start launches the bus-consumer goroutine. The goroutine runs until
// ctx cancels — the server lifecycle ctx is the canonical caller.
func (t *Tap) Start(ctx context.Context) {
	if t == nil || t.q == nil || t.bus == nil {
		return
	}
	ch := t.bus.Subscribe(ctx)
	go t.run(ctx, ch)
}

// HandleEvent is the per-event hot path. Public so tests can drive the
// Tap without spinning up a real Bus.
func (t *Tap) HandleEvent(ctx context.Context, ev events.Event) {
	// CORR-R02: only the publishing pod enqueues webhook deliveries.
	if ev.Remote {
		return
	}
	subs, err := t.subscriptions(ctx)
	if err != nil {
		t.log.WarnContext(ctx, "webhook tap: list subscriptions failed",
			"event", ev.Type, "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	eventName := string(ev.Type)
	now := time.Now().UTC()
	// SEC-R09: redact secret-shaped keys before persisting the delivery
	// payload so webhook_deliveries never stores live credentials.
	detail := redactEventDetail(ev.Data)
	payload, err := json.Marshal(eventEnvelope{
		EventName: eventName,
		EventID:   fmt.Sprintf("%d", ev.ID),
		Timestamp: ev.Time,
		Detail:    events.RawJSON(detail),
	})
	if err != nil {
		t.log.WarnContext(ctx, "webhook tap: marshal event failed",
			"event", eventName, "error", err)
		return
	}
	for _, sub := range subs {
		filters, ok := events.DecodeFilterGlobs(sub.EventFilters)
		if !ok {
			t.log.WarnContext(ctx, "webhook tap: malformed event_filters; skipping",
				"subscription_id", sub.ID.String(), "name", sub.Name)
			continue
		}
		if !MatchFilters(eventName, filters) {
			continue
		}
		if _, err := t.q.InsertWebhookDelivery(ctx, sqlc.InsertWebhookDeliveryParams{
			SubscriptionID: sub.ID,
			EventName:      eventName,
			EventID:        fmt.Sprintf("%d", ev.ID),
			Payload:        payload,
			PayloadSize:    int32(len(payload)),
			Status:         "queued",
			NextAttemptAt:  pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			t.log.WarnContext(ctx, "webhook tap: insert delivery failed",
				"subscription_id", sub.ID.String(), "event", eventName, "error", err)
		}
	}
}

func (t *Tap) run(ctx context.Context, ch <-chan events.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			t.HandleEvent(ctx, ev)
		}
	}
}

// subscriptions returns the cached enabled-subscriptions list, refetching
// when older than cacheTTL. The cache is per-Tap so test isolation is
// implicit.
func (t *Tap) subscriptions(ctx context.Context) ([]sqlc.WebhookSubscription, error) {
	t.mu.Lock()
	if t.cache != nil && time.Since(t.cachedAt) < t.cacheTTL {
		out := t.cache
		t.mu.Unlock()
		return out, nil
	}
	t.mu.Unlock()

	fresh, err := t.q.ListEnabledWebhookSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.cache = fresh
	t.cachedAt = time.Now()
	t.mu.Unlock()
	return fresh, nil
}

// Invalidate drops the cached enabled-subscriptions list. The webhook
// handler calls this on every CREATE/UPDATE/DELETE so a config change
// is reflected on the very next event.
func (t *Tap) Invalidate() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.cache = nil
	t.cachedAt = time.Time{}
	t.mu.Unlock()
}

// eventEnvelope is the JSON shape the tap persists onto webhook_deliveries.payload.
// The dispatcher rehydrates this and hands it to the sender, which
// either ships it raw or runs it through the operator's template.
type eventEnvelope struct {
	EventName string          `json:"event_name"`
	EventID   string          `json:"event_id"`
	Timestamp time.Time       `json:"timestamp"`
	Detail    json.RawMessage `json:"detail,omitempty"`
}

// redactEventDetail strips credential-shaped keys from an event payload
// before it is written to webhook_deliveries (SEC-R09). Prefer audit
// sanitize for map-shaped audit details; fall back to redaction.Payload
// for other JSON-like shapes.
func redactEventDetail(data any) any {
	if data == nil {
		return nil
	}
	switch typed := data.(type) {
	case map[string]any:
		return audit.SanitizeDetail(typed)
	default:
		return redaction.Payload(data)
	}
}
