package server

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/jackc/pgx/v5"
)

type complianceSnapshotTestTx struct {
	pgx.Tx
	commits, rollbacks int
	cleanupActive      bool
	commitError        error
}

func (tx *complianceSnapshotTestTx) Commit(context.Context) error {
	tx.commits++
	return tx.commitError
}
func (tx *complianceSnapshotTestTx) Rollback(ctx context.Context) error {
	tx.rollbacks++
	tx.cleanupActive = ctx.Err() == nil
	_, ok := ctx.Deadline()
	tx.cleanupActive = tx.cleanupActive && ok
	return nil
}

func TestComplianceReadSnapshotTransactionOwnership(t *testing.T) {
	for _, name := range []string{"success", "callback-failure", "canceled-callback", "commit-failure"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx := &complianceSnapshotTestTx{}
			failure := errors.New("owned read failure")
			if name == "commit-failure" {
				tx.commitError = failure
			}
			run := complianceReadSnapshotRunner(func(got context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
				if got != ctx || opts.IsoLevel != pgx.RepeatableRead || opts.AccessMode != pgx.ReadOnly {
					t.Fatal("snapshot transaction options or ownership drift")
				}
				return tx, nil
			})
			err := run(ctx, func(q handler.ComplianceQuerier) error {
				if q == nil {
					t.Fatal("missing transaction-bound query set")
				}
				if name == "callback-failure" {
					return failure
				}
				if name == "canceled-callback" {
					cancel()
				}
				return nil
			})
			if name == "success" {
				if err != nil || tx.commits != 1 {
					t.Fatalf("snapshot success: %v commits=%d", err, tx.commits)
				}
			} else if err == nil {
				t.Fatal("failed snapshot was accepted")
			}
			if (name == "callback-failure" || name == "canceled-callback") && tx.commits != 0 {
				t.Fatal("failed callback committed")
			}
			if tx.rollbacks != 1 || !tx.cleanupActive {
				t.Fatal("snapshot cleanup did not receive its own bounded active context")
			}
		})
	}
}
