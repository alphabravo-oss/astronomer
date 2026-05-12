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
//     parented to each enabled group. The slug is used (not the id) so
//     dashboard panels stay readable; the cardinality is bounded by the
//     number of operator-defined groups (typical fleet: < 100).
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
func RefreshClusterGroupMetrics(ctx context.Context, q ClusterGroupMetricsQuerier) {
	if q == nil {
		return
	}
	groups, err := q.ListClusterGroups(ctx)
	if err != nil {
		return
	}
	clusterGroupsTotal.Set(float64(len(groups)))
	clusterGroupClusters.Reset()
	for _, g := range groups {
		n, err := q.CountClustersInGroup(ctx, g.ID)
		if err != nil {
			continue
		}
		clusterGroupClusters.WithLabelValues(g.Slug).Set(float64(n))
	}
}
