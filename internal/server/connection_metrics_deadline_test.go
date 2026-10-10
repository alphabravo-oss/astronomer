package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The test driver observes expiry of the collector's derived deadline, yet
// reports a successful Commit. The outer caller remains live, so a caller-only
// cancellation check cannot conceal publication of expired observations.
type expiredConnectionMetricsCommitTx struct{ metricsSnapshotTx }

func (tx *expiredConnectionMetricsCommitTx) Commit(ctx context.Context) error {
	<-ctx.Done()
	tx.committed = true
	return nil
}

func TestConnectionMetricsDerivedDeadlineDuringCommitPreservesObservation(t *testing.T) {
	caller, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	tx := &expiredConnectionMetricsCommitTx{}
	rows, err := collectConnectionMetricsSnapshot(caller, func(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) > 10*time.Second {
			t.Fatal("snapshot did not derive its own bounded deadline")
		}
		return tx, nil
	})
	if caller.Err() != nil {
		t.Fatal("caller expired; regression no longer isolates the derived deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) || rows != nil {
		t.Fatalf("expired observation exposed: rows=%d deadlineError=%t", len(rows), errors.Is(err, context.DeadlineExceeded))
	}
	if !tx.committed || !tx.rolledBack {
		t.Fatal("test did not reach commit and independent cleanup")
	}
}
