package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// alertDispatchQuerier embeds RuntimeQuerier so only the one method the
// dispatch path touches needs an implementation; any other call would
// nil-deref, flagging an unexpected path.
type alertDispatchQuerier struct {
	RuntimeQuerier
	channels []sqlc.NotificationChannel
}

func (q *alertDispatchQuerier) ListChannelsForAlertRule(_ context.Context, _ uuid.UUID) ([]sqlc.NotificationChannel, error) {
	return q.channels, nil
}

// recordingEnqueuer captures the tasks dispatchAlertNotifications enqueues
// so the test can decode the payload.
type recordingEnqueuer struct {
	tasks []*asynq.Task
}

func (e *recordingEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	return &asynq.TaskInfo{}, nil
}

// A global rule (empty rule.ClusterID) firing on a specific cluster must
// report that firing cluster in the notification, not the empty rule one.
func TestDispatchAlertNotifications_GlobalRuleReportsFiringCluster(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	firingCluster := uuid.New()
	q := &alertDispatchQuerier{
		channels: []sqlc.NotificationChannel{
			{ID: uuid.New(), ChannelType: "webhook", Enabled: true},
		},
	}
	enq := &recordingEnqueuer{}
	runtimeDeps = RuntimeDependencies{Queries: q, Enqueuer: enq}

	rule := sqlc.AlertRule{ID: uuid.New(), Name: "global-rule"} // ClusterID zero/invalid => global
	event := sqlc.AlertEvent{
		ID:        uuid.New(),
		RuleID:    rule.ID,
		ClusterID: pgtype.UUID{Bytes: firingCluster, Valid: true},
	}

	dispatchAlertNotifications(context.Background(), rule, event, "subject", "body", false)

	if len(enq.tasks) != 1 {
		t.Fatalf("expected 1 enqueued notification, got %d", len(enq.tasks))
	}
	var p NotificationSendPayload
	if err := json.Unmarshal(enq.tasks[0].Payload(), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.ClusterID != firingCluster.String() {
		t.Fatalf("notification reported cluster %q, want firing cluster %q", p.ClusterID, firingCluster.String())
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
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

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
	runtimeDeps = RuntimeDependencies{Queries: q}

	got, err := listAllAlertEventsByRule(context.Background(), ruleID)
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
