package provider

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Run is composed only into the existing leader's joined loop group. It does
// not detach work or return while a tunnel/transaction child is running. User
// requests and unrelated machine contexts cannot be promoted to this origin.
func (owner *DatabaseScanOwner) Run(ctx context.Context, logger *slog.Logger) {
	if ctx == nil || owner == nil || owner.pool == nil || callerid.HasAuthenticatedUserContext(ctx) {
		return
	}
	if _, marked := callerid.MachineSourceFromContext(ctx); marked {
		return
	}
	ctx = callerid.WithMachine(ctx, callerid.SourceDatabaseScan)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		err := owner.sweep(ctx)
		if ctx.Err() != nil {
			return
		}
		if logger != nil && err != nil {
			if errors.Is(err, errDatabaseScanResourceCapacity) {
				logger.Error("owned database scan resource capacity exhausted", "posture", "unknown", "reason", "resource-capacity-exhausted", "allocation_limit", databaseScanMaximumUnremovedAllocations, "operator_action", "inspect unresolved terminal resource UID ownership; no automatic release")
			} else {
				logger.Warn("owned database scan sweep failed", "posture", "unknown")
			}
		}
		// Pending Kubernetes phases retain their current durable lease and are
		// revisited without blocking a fleet loop on a 50-minute Job deadline.
		timer.Reset(time.Minute)
	}
}
func (owner *DatabaseScanOwner) sweep(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cleanup := owner.cleanupTerminalScanClaims(bounded)
	// Reduce-only retained cleanup remains available to an already-owned loop
	// even if current mounted trust was removed; it cannot accept or scan.
	if err := owner.revalidateScanMaterial(bounded); err != nil {
		return errors.Join(cleanup, err)
	}
	if err := owner.projection.revalidateOwnedSnapshot(bounded); err != nil {
		return errors.Join(cleanup, err)
	}
	queries := sqlc.New(owner.pool)
	// Current projections are only retrieval DATA. Every actual reservation
	// locks and validates current base/source/spec/target/receipt independently.
	return errors.Join(cleanup, progressOwnedDatabaseScans(bounded, databaseScanSQLPages{queries: queries}, owner, &owner.scanCursors))
}
