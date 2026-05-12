// Prometheus metrics for the per-cluster snapshot self-service (migration 052).
//
// We surface three signals at the management plane:
//
//   - astronomer_cluster_snapshots_total{outcome,cluster_id} — counter
//     of completed snapshots, partitioned by the terminal Velero phase
//     mapped into one of completed / failed / partial. The poller
//     increments this when it transitions a row into a terminal state.
//
//   - astronomer_cluster_snapshots_in_flight{cluster_id} — gauge of
//     New / InProgress rows per cluster. The poller adjusts this on
//     every status pull so a stuck Velero shows up as a non-zero
//     gauge that never drains.
//
//   - astronomer_velero_install_status{cluster_id,status} — gauge of
//     the velero-status endpoint's outcome per cluster. status =
//     "ready" / "unavailable" / "missing" / "unreachable"; the
//     dashboard's "Velero" badge feeds off this directly.
//
// Metric registration is sync.Once-guarded so the handler package can
// be imported by tests + cmd/server in any order without
// duplicate-registration panics.

package handler

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

var (
	clusterSnapshotsRegisterOnce sync.Once

	clusterSnapshotsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "cluster_snapshots_total",
			Help:      "Per-cluster Velero snapshots that reached a terminal phase.",
		},
		observability.MetricLabels("cluster_id", "outcome"),
	)

	clusterSnapshotsCreatedInFlight = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "cluster_snapshots_created_total",
			Help:      "Per-cluster Velero snapshots successfully created (Backup CRD POSTed).",
		},
		observability.MetricLabels("cluster_id"),
	)

	clusterSnapshotsInFlight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "cluster_snapshots_in_flight",
			Help:      "Per-cluster snapshots currently in a non-terminal phase (New, InProgress).",
		},
		observability.MetricLabels("cluster_id"),
	)

	veleroInstallStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "velero_install_status",
			Help:      "Velero install state per member cluster (1=ready, 0 otherwise).",
		},
		observability.MetricLabels("cluster_id", "status"),
	)
)

// RegisterClusterSnapshotsMetrics wires the snapshot metrics into the
// default Prometheus registry. Safe to call multiple times — the
// sync.Once guards re-registration. The caller (cmd/server) invokes
// this alongside the rest of the per-package MustRegister hooks.
func RegisterClusterSnapshotsMetrics() {
	clusterSnapshotsRegisterOnce.Do(func() {
		prometheus.MustRegister(
			clusterSnapshotsTotal,
			clusterSnapshotsCreatedInFlight,
			clusterSnapshotsInFlight,
			veleroInstallStatus,
		)
	})
}
