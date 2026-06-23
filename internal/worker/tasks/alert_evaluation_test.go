package tasks

import (
	"context"
	"encoding/json"
	"testing"

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
