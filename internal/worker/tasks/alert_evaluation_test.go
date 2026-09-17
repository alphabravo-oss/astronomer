package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// alertDispatchQuerier embeds RuntimeQuerier so only the one method the
// dispatch path touches needs an implementation; any other call would
// nil-deref, flagging an unexpected path.
type alertDispatchQuerier struct {
	RuntimeQuerier
	channels  []sqlc.NotificationChannel
	outbox    []sqlc.UpsertTaskOutboxParams
	events    []sqlc.AlertEvent
	outboxErr error
}

func (q *alertDispatchQuerier) ListChannelsForAlertRule(_ context.Context, _ uuid.UUID) ([]sqlc.NotificationChannel, error) {
	return q.channels, nil
}

func (q *alertDispatchQuerier) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if q.outboxErr != nil {
		return sqlc.TaskOutbox{}, q.outboxErr
	}
	q.outbox = append(q.outbox, arg)
	return sqlc.TaskOutbox{ID: uuid.New()}, nil
}

func (q *alertDispatchQuerier) CreateAlertEvent(_ context.Context, arg sqlc.CreateAlertEventParams) (sqlc.AlertEvent, error) {
	event := sqlc.AlertEvent{
		ID: uuid.New(), RuleID: arg.RuleID, ClusterID: arg.ClusterID, Status: arg.Status,
		Message: arg.Message, Details: arg.Details, FiredAt: time.Now().UTC(),
	}
	q.events = append(q.events, event)
	return event, nil
}

func (q *alertDispatchQuerier) UpdateAlertEventStatus(context.Context, sqlc.UpdateAlertEventStatusParams) error {
	return nil
}

// A global rule (empty rule.ClusterID) firing on a specific cluster must
// report that firing cluster in the notification, not the empty rule one.
func TestDispatchAlertNotifications_GlobalRuleReportsFiringCluster(t *testing.T) {
	firingCluster := uuid.New()
	q := &alertDispatchQuerier{
		channels: []sqlc.NotificationChannel{
			{ID: uuid.New(), ChannelType: "webhook", Enabled: true, Configuration: []byte(`{"url":"https://alerts.example.test/hook"}`)},
		},
	}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q})

	rule := sqlc.AlertRule{ID: uuid.New(), Name: "global-rule"} // ClusterID zero/invalid => global
	event := sqlc.AlertEvent{
		ID:        uuid.New(),
		RuleID:    rule.ID,
		ClusterID: pgtype.UUID{Bytes: firingCluster, Valid: true},
	}

	if err := dispatchAlertNotifications(ctx, q, rule, event, "subject", "body", false); err != nil {
		t.Fatal(err)
	}

	if len(q.outbox) != 1 {
		t.Fatalf("expected 1 durable notification, got %d", len(q.outbox))
	}
	var p NotificationSendPayload
	if err := json.Unmarshal(q.outbox[0].Payload, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.ClusterID != firingCluster.String() {
		t.Fatalf("notification reported cluster %q, want firing cluster %q", p.ClusterID, firingCluster.String())
	}
	if p.ChannelID != q.channels[0].ID.String() || p.EventID != event.ID.String() || p.DeliveryID == "" {
		t.Fatalf("notification destination identity missing: %+v", p)
	}
}

func TestAlertEventAndNotificationIntentRollbackTogether(t *testing.T) {
	q := &alertDispatchQuerier{
		channels: []sqlc.NotificationChannel{{
			ID: uuid.New(), ChannelType: "webhook", Enabled: true,
			Configuration: []byte(`{"url":"https://alerts.example.test/hook"}`),
		}},
		outboxErr: errors.New("task-outbox unavailable"),
	}
	runTx := func(ctx context.Context, fn func(AlertNotificationMutationTx) error) error {
		events := append([]sqlc.AlertEvent(nil), q.events...)
		outbox := append([]sqlc.UpsertTaskOutboxParams(nil), q.outbox...)
		if err := fn(q); err != nil {
			q.events, q.outbox = events, outbox
			return err
		}
		return nil
	}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q, AlertNotificationRunTx: runTx})
	err := processRuleEvaluation(ctx, sqlc.AlertRule{ID: uuid.New(), Name: "database unavailable", Severity: "critical"}, ruleClusterEval{
		triggered: true, message: "database unavailable", details: []byte(`{}`),
	}, nil, nil, nil, nil)
	if err == nil || len(q.events) != 0 || len(q.outbox) != 0 {
		t.Fatalf("event/outbox did not roll back together: err=%v events=%d outbox=%d", err, len(q.events), len(q.outbox))
	}
}

// pagingEventQuerier serves alert events from an in-memory slice honoring
// Limit/Offset so listAllAlertEventsByRule's paging can be exercised.
type pagingEventQuerier struct {
	RuntimeQuerier
	events []sqlc.AlertEvent
	calls  int
}

func (q *pagingEventQuerier) ListAlertEventsByRule(_ context.Context, arg sqlc.ListAlertEventsByRuleParams) ([]sqlc.AlertEvent, error) {
	q.calls++
	start := int(arg.Offset)
	if start >= len(q.events) {
		return nil, nil
	}
	end := start + int(arg.Limit)
	if end > len(q.events) {
		end = len(q.events)
	}
	return q.events[start:end], nil
}

// A global rule on a large fleet fires one event per cluster. The read-back
// must return EVERY active event, not just the first page — otherwise a firing
// cluster past the old 200-row cap is invisible (stuck-firing + alert storm).
func TestListAllAlertEventsByRule_PagesBeyondOneBatch(t *testing.T) {
	ruleID := uuid.New()
	total := int(alertEvalSweepPageSize)*2 + 37 // spans three pages
	sentinel := uuid.New()
	events := make([]sqlc.AlertEvent, total)
	for i := range events {
		events[i] = sqlc.AlertEvent{ID: uuid.New(), RuleID: ruleID, Status: "firing"}
	}
	// Put a firing event well past the old 200-row cap; it must still be read.
	events[total-1] = sqlc.AlertEvent{
		ID:        sentinel,
		RuleID:    ruleID,
		Status:    "firing",
		ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
	}
	q := &pagingEventQuerier{events: events}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q})

	got, err := listAllAlertEventsByRule(ctx, ruleID)
	if err != nil {
		t.Fatalf("listAllAlertEventsByRule: %v", err)
	}
	if len(got) != total {
		t.Fatalf("expected all %d events read across pages, got %d", total, len(got))
	}
	found := false
	for _, e := range got {
		if e.ID == sentinel {
			found = true
		}
	}
	if !found {
		t.Fatal("event past the first page (old 200-row cap) was not read back")
	}
	if q.calls < 3 {
		t.Fatalf("expected paging to issue >=3 reads, got %d", q.calls)
	}
}

func TestCooldownElapsed_ResolvedEventWithinWindowBlocks(t *testing.T) {
	rule := sqlc.AlertRule{CooldownMinutes: 10}
	now := time.Now().UTC()
	// A resolved event that fired 2m ago (inside the 10m window) must still
	// block a re-fire — the fire->resolve->fire flap the cooldown exists to damp.
	events := []sqlc.AlertEvent{{Status: "resolved", FiredAt: now.Add(-2 * time.Minute)}}
	if cooldownElapsed(rule, events, pgtype.UUID{}) {
		t.Fatal("resolved event within cooldown window should block re-fire")
	}
	// Older than the window -> allowed.
	old := []sqlc.AlertEvent{{Status: "resolved", FiredAt: now.Add(-20 * time.Minute)}}
	if !cooldownElapsed(rule, old, pgtype.UUID{}) {
		t.Fatal("event older than cooldown window should not block")
	}
	// No prior events -> allowed.
	if !cooldownElapsed(rule, nil, pgtype.UUID{}) {
		t.Fatal("no prior events should not block")
	}
}
