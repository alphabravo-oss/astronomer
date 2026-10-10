package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type metricsSnapshotTx struct {
	pgx.Tx
	queries     int
	queryError  error
	commitError error
	committed   bool
	rolledBack  bool
	rowsError   error
}

func (tx *metricsSnapshotTx) Query(ctx context.Context, _ string, args ...any) (pgx.Rows, error) {
	tx.queries++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args[0] != int32((tx.queries-1)*500) || args[1] != int32(500) {
		return nil, errors.New("unexpected snapshot pagination bounds")
	}
	if tx.queries == 2 && tx.queryError != nil {
		return nil, tx.queryError
	}
	size := 500
	if tx.queries == 2 {
		size = 1
	}
	return &metricsSnapshotRows{remaining: size, err: tx.rowsError}, nil
}

func (tx *metricsSnapshotTx) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx.commitError != nil {
		return tx.commitError
	}
	tx.committed = true
	return nil
}

func (tx *metricsSnapshotTx) Rollback(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	tx.rolledBack = true
	return nil
}

type metricsSnapshotRows struct {
	pgx.Rows
	remaining int
	err       error
}

func (rows *metricsSnapshotRows) Next() bool { rows.remaining--; return rows.remaining >= 0 }
func (rows *metricsSnapshotRows) Close()     {}
func (rows *metricsSnapshotRows) Err() error { return rows.err }
func (rows *metricsSnapshotRows) Scan(values ...any) error {
	*values[0].(*uuid.UUID) = uuid.New()
	*values[1].(*string) = "synthetic-snapshot-cluster"
	*values[2].(*string) = "disconnected"
	*values[3].(*time.Time) = time.Now()
	return nil
}

func TestConnectionMetricsSnapshotTransactionBoundaries(t *testing.T) {
	failure := errors.New("synthetic transaction failure")
	for _, stage := range []string{"success", "begin", "second-page", "iterator", "commit", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx := &metricsSnapshotTx{}
			switch stage {
			case "second-page":
				tx.queryError = failure
			case "iterator":
				tx.rowsError = failure
			case "commit":
				tx.commitError = failure
			}
			rows, err := collectConnectionMetricsSnapshot(ctx, func(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
				if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly {
					t.Fatal("metrics transaction permits writes or inconsistent OFFSET observations")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					t.Fatal("metrics transaction has no bounded collection deadline")
				}
				if stage == "begin" {
					return nil, failure
				}
				if stage == "cancel" {
					cancel()
				}
				return tx, nil
			})
			if stage == "success" {
				if err != nil || len(rows) != 501 || !tx.committed {
					t.Fatalf("complete consistent snapshot not returned: rows=%d committed=%t error=%v", len(rows), tx.committed, err)
				}
			} else if err == nil || rows != nil || tx.committed {
				t.Fatal("failed transaction exposed a partial observation or committed")
			}
			if stage != "begin" && !tx.rolledBack {
				t.Fatal("transaction cleanup did not run with a live bounded context")
			}
		})
	}
}
