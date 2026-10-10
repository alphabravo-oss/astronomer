// Prometheus metrics for cluster groups (migration 066).
//
// Two signals surface at the management plane:
//
//   - astronomer_cluster_groups_total — gauge of enabled groups. Updated
//     by the periodic refresh helper so the value reflects ground truth
//     even if a create / delete bypassed the handler (chart-side admin
//     scripts, manual DB fixups).
//
//   - astronomer_cluster_group_clusters{group_slug} — gauge of clusters
//     parented to each enabled group. The historical group_slug label key
//     now carries verified UUIDs, preserving one owner across rename. Complete
//     observations and admission enforce a10k family budget.
//
// Both gauges are refreshed by RefreshClusterGroupMetrics — called from
// a small periodic task every ~5m (membership doesn't churn hot).
// Metric registration is sync.Once-guarded so test harnesses that import
// the handler package alongside cmd/server don't trip
// duplicate-registration.

package handler

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	clusterGroupsRegisterOnce sync.Once

	clusterGroupsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "cluster_groups_total",
			Help:      "Count of enabled cluster groups (operator-defined folder hierarchy).",
		},
	)

	clusterGroupClusters = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "cluster_group_clusters",
			Help:      "Clusters parented to each enabled group (direct children — the subtree rollup is the sum across descendants).",
		},
		observability.MetricLabels("group_slug"),
	)
)

// RegisterClusterGroupMetrics wires the cluster group gauges into the
// default Prometheus registry. Safe to call multiple times.
func RegisterClusterGroupMetrics() {
	clusterGroupsRegisterOnce.Do(func() {
		prometheus.MustRegister(clusterGroupsTotal, clusterGroupClusters)
	})
}

// ClusterGroupMetricsQuerier is the database surface RefreshClusterGroupMetrics
// uses. *sqlc.Queries satisfies it.
type ClusterGroupMetricsQuerier interface {
	ListClusterGroups(ctx context.Context) ([]sqlc.ClusterGroup, error)
	CountClustersInGroup(ctx context.Context, groupID uuid.UUID) (int64, error)
}

// RefreshClusterGroupMetrics polls the database and refreshes the
// gauges. Callers should invoke this every ~5 minutes — group membership
// doesn't churn fast enough to need tighter cadence and the recursive
// CTE walks the whole tree each time. Errors are non-fatal (the
// previous gauge values stay until the next successful refresh).
// This legacy helper buffers every count before publishing. Production uses
// the bounded read-only RR helper; callers lacking durable ownership reads
// cannot allocate name-based gauges. No error resets another active instance.
func RefreshClusterGroupMetrics(ctx context.Context, q ClusterGroupMetricsQuerier) {
	if ctx == nil || ctx.Err() != nil || q == nil {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started := time.Now().UTC()
	lookup := observability.BeginMetricGenerationLookup(observability.MetricClusterGroup)
	groups, err := q.ListClusterGroups(bounded)
	if err != nil || bounded.Err() != nil || len(groups) > observability.MetricOwnerMax {
		return
	}
	counts := make(map[uuid.UUID]int64, len(groups))
	for _, group := range groups {
		if bounded.Err() != nil {
			return
		}
		count, err := q.CountClustersInGroup(bounded, group.ID)
		if err != nil || count < 0 || bounded.Err() != nil {
			return
		}
		counts[group.ID] = count
	}
	reader, ok := q.(observability.MetricGenerationReader)
	if !ok {
		return
	}
	rows := make([]observability.MetricGenerationRow, 0, len(groups))
	for _, group := range groups {
		token, err := reader.GetMetricOwnerGeneration(bounded, sqlc.GetMetricOwnerGenerationParams{Family: string(observability.MetricClusterGroup), OwnerID: group.ID})
		if err != nil || bounded.Err() != nil {
			return
		}
		rows = append(rows, observability.MetricGenerationRow{ID: group.ID, Generation: token})
	}
	lookup.PublishValuesAndComplete(rows, started, func(instance, id string) {
		clusterGroupClusters.WithLabelValues(instance, id).Set(float64(counts[uuid.MustParse(id)]))
	}, func() { clusterGroupsTotal.Set(float64(len(rows))) })
}
