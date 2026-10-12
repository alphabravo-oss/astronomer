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
	"github.com/prometheus/client_golang/prometheus"
)

type registrationMetricsSnapshotTx struct {
	metricsSnapshotTx
	commitCancel context.CancelFunc
}

func (tx *registrationMetricsSnapshotTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	if !strings.Contains(query, "registration_phase") || !strings.Contains(query, "WHERE decommissioned_at IS NULL") {
		return nil, errors.New("not reading canonical active committed phases")
	}
	// Canonical ListClusters takes LIMIT then OFFSET, unlike the connection and
	// Vault list query. The shared fake still validates every exact page bound.
	rows, err := tx.metricsSnapshotTx.Query(ctx, query, args[1], args[0])
	if err != nil {
		return nil, err
	}
	return &registrationMetricsSnapshotRows{Rows: rows}, nil
}
func (tx *registrationMetricsSnapshotTx) Commit(ctx context.Context) error {
	if tx.commitCancel != nil {
		tx.committed = true
		tx.commitCancel()
		return nil
	}
	return tx.metricsSnapshotTx.Commit(ctx)
}

type registrationMetricsSnapshotRows struct{ pgx.Rows }

func (rows *registrationMetricsSnapshotRows) Scan(values ...any) error {
	*values[0].(*uuid.UUID) = uuid.New()
	*values[23].(*string) = "ready"
	return nil
}

func TestRegistrationMetricsSnapshotTransactionBoundaries(t *testing.T) {
	failure := errors.New("synthetic registration snapshot failure")
	for _, stage := range []string{"success", "begin", "second-page", "iterator", "commit", "cancel", "cancel-in-query", "cancel-during-commit", "oversized", "exhausted", "exact-ceiling"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tx := &registrationMetricsSnapshotTx{}
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
			rows, err := collectRegistrationMetricsSnapshot(ctx, func(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
				if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly {
					t.Fatal("phase snapshot can write or observe shifted OFFSET pages")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					t.Fatal("phase snapshot has no bounded deadline")
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
					t.Fatalf("complete phase snapshot unavailable: rows%d commit%t err%v", len(rows), tx.committed, err)
				}
				for _, row := range rows {
					if row.Phase != "ready" {
						t.Fatal("snapshot lost the authoritative phase")
					}
				}
			} else if err == nil || rows != nil || (tx.committed && stage != "cancel-during-commit") {
				t.Fatal("failed or canceled phase snapshot exposed partial rows")
			}
			if stage != "begin" && !tx.rolledBack {
				t.Fatal("cleanup did not use bounded independent context")
			}
			if tx.queries > 21 {
				t.Fatal("phase observation exceeded finite query budget")
			}
		})
	}
}

func TestOwnerMetricsCanceledCommitPreservesPublishedPhase(t *testing.T) {
	previous := observability.InstanceID()
	observability.SetInstanceID(t.Name())
	t.Cleanup(func() {
		observability.RetireRegistrationMetrics(nil, observability.RegistrationMetricsWatermark())
		observability.SetInstanceID(previous)
	})
	row := observability.RegistrationMetricSnapshotRow{ClusterID: uuid.New().String(), Phase: "connected"}
	observability.PublishRegistrationMetricSnapshot([]observability.RegistrationMetricSnapshotRow{row}, observability.RegistrationMetricsWatermark(), time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tx := &registrationMetricsSnapshotTx{commitCancel: cancel}
	runRegistrationMetricsCollector(ctx, func(context.Context, pgx.TxOptions) (pgx.Tx, error) { return tx, nil }, nil)
	// A fresh complete read proving the row remains current can still update it;
	// the canceled collector returned without publishing its fabricated ready rows.
	watermark := observability.RegistrationMetricsWatermark()
	if !observability.RetireRegistrationMetrics([]string{row.ClusterID}, watermark) {
		t.Fatal("current owner retirement observation rejected")
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, family := range families {
		if family.GetName() != "astronomer_cluster_registration_phase" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["astronomer_instance_id"] == t.Name() {
				matched++
				if labels["cluster"] != row.ClusterID || labels["phase"] != "connected" {
					t.Fatalf("canceled commit changed prior publication: %v", labels)
				}
			}
		}
	}
	if matched != 1 {
		t.Fatalf("prior phase samples=%d want1", matched)
	}
}
