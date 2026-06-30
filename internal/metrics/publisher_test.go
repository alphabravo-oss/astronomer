package metrics

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestStaleThresholdMatchesWorkerNoFlap is the M3 guard: the publisher's
// staleness threshold MUST equal the worker health-check window (2m), and a
// cluster stale for a time in the old flap band (60s–120s) must NOT be flipped
// to disconnected — both writers agree, so no active<->disconnected flap.
func TestStaleThresholdMatchesWorkerNoFlap(t *testing.T) {
	const workerHealthCheckWindow = 2 * time.Minute
	if staleHeartbeatThreshold != workerHealthCheckWindow {
		t.Fatalf("publisher staleHeartbeatThreshold = %s, must match the worker health-check window %s (M3: different thresholds flap clusters.status)", staleHeartbeatThreshold, workerHealthCheckWindow)
	}

	stale := func(age time.Duration) sqlc.Cluster {
		return sqlc.Cluster{
			ID:            uuid.New(),
			Status:        "active",
			LastHeartbeat: pgtype.Timestamptz{Time: time.Now().Add(-age), Valid: true},
		}
	}
	// Old flap band: 90s stale. Under the old 60s publisher threshold this
	// flipped to disconnected while the 2m worker kept it active. Now both
	// agree it is still active.
	if next := decideStatus(stale(90*time.Second), staleHeartbeatThreshold); next != "" {
		t.Fatalf("90s-stale active cluster should stay active (no change), got transition to %q", next)
	}
	// Past the window it does flip.
	if next := decideStatus(stale(3*time.Minute), staleHeartbeatThreshold); next != "disconnected" {
		t.Fatalf("3m-stale active cluster should flip to disconnected, got %q", next)
	}
}
