package charlie

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type recordingAgentStatusWriter struct {
	params sqlc.UpdateCharlieAgentStatusParams
}

func (w *recordingAgentStatusWriter) UpdateCharlieAgentStatus(_ context.Context, params sqlc.UpdateCharlieAgentStatusParams) (sqlc.CharlieConnection, error) {
	w.params = params
	return sqlc.CharlieConnection{
		ID: params.ID, Active: true, LeaderInstanceID: params.LeaderInstanceID,
		FencingEpoch: params.FencingEpoch, HealthState: params.HealthState,
	}, nil
}

func TestSyncAgentStatusPersistsLiveLeaderAndFencingEpoch(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	writer := &recordingAgentStatusWriter{}
	connection, err := syncAgentStatus(t.Context(), writer, sqlc.CharlieConnection{ID: id, Active: true}, AdminBridgeStatus{
		CentralHealth: "healthy", LeaderInstanceID: "instance-current", Epoch: 11,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if connection.FencingEpoch != 11 || writer.params.ID != id || writer.params.LeaderInstanceID != "instance-current" ||
		writer.params.HealthState != "ready" || !writer.params.LastConnectedAt.Valid || !writer.params.LastConnectedAt.Time.Equal(now) {
		t.Fatalf("live product-agent leadership was not persisted exactly: %#v", writer.params)
	}
}

func TestSyncAgentStatusNeverOverwritesFenceWithInvalidLeadership(t *testing.T) {
	writer := &recordingAgentStatusWriter{}
	for name, status := range map[string]AdminBridgeStatus{
		"missing leader": {CentralHealth: "healthy", Epoch: 12},
		"missing epoch":  {CentralHealth: "healthy", LeaderInstanceID: "instance-current"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := syncAgentStatus(t.Context(), writer, sqlc.CharlieConnection{ID: uuid.New(), Active: true}, status, time.Now()); err == nil {
				t.Fatal("invalid leadership was persisted")
			}
		})
	}
}
