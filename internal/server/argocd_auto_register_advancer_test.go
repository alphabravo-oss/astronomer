package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type recordingRegistrationAdvancer struct {
	called       bool
	clusterID    uuid.UUID
	agentVersion string
}

func (r *recordingRegistrationAdvancer) OnAgentConnected(_ context.Context, clusterID uuid.UUID, agentVersion string) error {
	r.called = true
	r.clusterID = clusterID
	r.agentVersion = agentVersion
	return nil
}

type recordingTaskOutboxWriter struct {
	args []sqlc.UpsertTaskOutboxParams
}

func (r *recordingTaskOutboxWriter) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	r.args = append(r.args, arg)
	return sqlc.TaskOutbox{}, nil
}

func TestArgoCDAutoRegisterAdvancerWritesTaskOutbox(t *testing.T) {
	clusterID := uuid.New()
	base := &recordingRegistrationAdvancer{}
	outbox := &recordingTaskOutboxWriter{}
	advancer := &argoCDAutoRegisterAdvancer{
		base:       base,
		taskOutbox: outbox,
	}

	if err := advancer.OnAgentConnected(context.Background(), clusterID, "v1.2.3"); err != nil {
		t.Fatalf("OnAgentConnected: %v", err)
	}

	if !base.called {
		t.Fatal("expected base registration advancer to be called")
	}
	if base.clusterID != clusterID || base.agentVersion != "v1.2.3" {
		t.Fatalf("base call = %s/%s, want %s/v1.2.3", base.clusterID, base.agentVersion, clusterID)
	}
	if len(outbox.args) != 1 {
		t.Fatalf("outbox writes = %d, want 1", len(outbox.args))
	}
	arg := outbox.args[0]
	if arg.TaskType != tasks.ArgoCDAutoRegisterClusterType {
		t.Fatalf("task type = %q", arg.TaskType)
	}
	if arg.QueueName != "default" || arg.MaxRetry != 5 || arg.UniqueSeconds != int32((10*time.Minute)/time.Second) {
		t.Fatalf("queue/retry/unique = %s/%d/%d", arg.QueueName, arg.MaxRetry, arg.UniqueSeconds)
	}
	var payload tasks.ArgoCDAutoRegisterClusterPayload
	if err := json.Unmarshal(arg.Payload, &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.ClusterID != clusterID.String() {
		t.Fatalf("payload cluster_id = %q, want %q", payload.ClusterID, clusterID.String())
	}
}
