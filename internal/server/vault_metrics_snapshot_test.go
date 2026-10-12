package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type vaultMetricsSnapshotTx struct {
	metricsSnapshotTx
	commitCancel context.CancelFunc
}

func (tx *vaultMetricsSnapshotTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	if !strings.Contains(query, "AS auth_configured") {
		return nil, errors.New("not using credential-free Vault list projection")
	}
	rows, err := tx.metricsSnapshotTx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &vaultMetricsSnapshotRows{Rows: rows}, nil
}

func (tx *vaultMetricsSnapshotTx) Commit(ctx context.Context) error {
	if tx.commitCancel != nil {
		tx.committed = true
		tx.commitCancel()
		return nil
	}
	return tx.metricsSnapshotTx.Commit(ctx)
}

type vaultMetricsSnapshotRows struct{ pgx.Rows }

func (rows *vaultMetricsSnapshotRows) Scan(values ...any) error {
	*values[0].(*uuid.UUID) = uuid.New()
	*values[1].(*string) = "owned-vault"
	return nil
}

func TestVaultMetricsSnapshotTransactionBoundaries(t *testing.T) {
	failure := errors.New("synthetic Vault metrics failure")
	for _, stage := range []string{"success", "begin", "second-page", "iterator", "commit", "cancel", "cancel-in-query", "cancel-during-commit", "oversized", "exhausted", "exact-ceiling"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx := &vaultMetricsSnapshotTx{}
			switch stage {
			case "second-page":
				tx.queryError = failure
			case "iterator":
				tx.rowsError = failure
			case "commit":
				tx.commitError = failure
			case "cancel-in-query":
				tx.cancel = cancel
			case "cancel-during-commit":
				tx.commitCancel = cancel
			case "oversized":
				tx.oversized = true
			case "exhausted":
				tx.rowCount = observability.MetricOwnerMax + 1
			case "exact-ceiling":
				tx.rowCount = observability.MetricOwnerMax
			}
			rows, err := collectVaultMetricsSnapshot(ctx, func(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
				if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly {
					t.Fatal("Vault snapshot can write or observe shifted OFFSET pages")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					t.Fatal("unbounded Vault snapshot")
				}
				if stage == "begin" {
					return nil, failure
				}
				if stage == "cancel" {
					cancel()
				}
				return tx, nil
			})
			if stage == "success" || stage == "exact-ceiling" {
				want := 501
				if stage == "exact-ceiling" {
					want = observability.MetricOwnerMax
				}
				if err != nil || len(rows) != want || !tx.committed {
					t.Fatalf("complete Vault snapshot unavailable: rows%d commit%t err%v", len(rows), tx.committed, err)
				}
			} else if err == nil || rows != nil || (tx.committed && stage != "cancel-during-commit") {
				t.Fatal("failed Vault snapshot exposed partial rows")
			}
			if stage != "begin" && !tx.rolledBack {
				t.Fatal("bounded independent rollback did not run")
			}
			if tx.queries > 21 {
				t.Fatal("unbounded Vault snapshot queries")
			}
		})
	}
}
