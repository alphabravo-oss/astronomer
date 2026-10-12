package handler

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type MetricSnapshotBegin func(context.Context, pgx.TxOptions) (pgx.Tx, error)

func init() {
	observability.RegisterMetricGenerationRetirement(observability.MetricClusterGroup, func(instance, id string) {
		clusterGroupClusters.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": instance, "group_slug": id})
	})
}

// The historical label key group_slug remains, but its value is the immutable
// verified group ID. Display-name/slug changes no longer create new children.
// This metric migration affects dashboards, never group visibility or counts.
func RefreshClusterGroupMetricSnapshot(ctx context.Context, begin MetricSnapshotBegin) bool {
	if ctx == nil || begin == nil || ctx.Err() != nil {
		return false
	}
	started := time.Now().UTC()
	lookup := observability.BeginMetricGenerationLookup(observability.MetricClusterGroup)
	rows, err := collectClusterGroupMetricValues(ctx, begin)
	if err != nil || ctx.Err() != nil {
		return false
	}
	identities := make([]observability.MetricGenerationRow, 0, len(rows))
	counts := make(map[uuid.UUID]int64, len(rows))
	for _, row := range rows {
		identities = append(identities, observability.MetricGenerationRow{ID: row.ID, Generation: row.Generation})
		counts[row.ID] = row.ClusterCount
	}
	published := lookup.PublishValuesAndComplete(identities, started, func(instance, id string) {
		value := counts[uuid.MustParse(id)] // Registry accepts only actual UUID rows.
		clusterGroupClusters.WithLabelValues(instance, id).Set(float64(value))
	}, func() { clusterGroupsTotal.Set(float64(len(rows))) })
	return published
}

func collectClusterGroupMetricValues(ctx context.Context, begin MetricSnapshotBegin) ([]sqlc.ListClusterGroupMetricValuesPageRow, error) {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := begin(bounded, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(bounded), 2*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	queries := sqlc.New(tx)
	const pageSize int32 = 500
	var complete []sqlc.ListClusterGroupMetricValuesPageRow
	for offset := int32(0); offset <= observability.MetricOwnerMax; offset += pageSize {
		if err := bounded.Err(); err != nil {
			return nil, err
		}
		rows, err := queries.ListClusterGroupMetricValuesPage(bounded, sqlc.ListClusterGroupMetricValuesPageParams{QueryLimit: pageSize, QueryOffset: offset})
		if err != nil {
			return nil, err
		}
		if err := bounded.Err(); err != nil {
			return nil, err
		}
		if len(rows) > int(pageSize) || len(complete)+len(rows) > observability.MetricOwnerMax {
			return nil, errors.New("group metrics owner budget exceeded")
		}
		complete = append(complete, rows...)
		if len(rows) < int(pageSize) {
			break
		}
	}
	if err := tx.Commit(bounded); err != nil {
		return nil, err
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	return complete, nil
}
