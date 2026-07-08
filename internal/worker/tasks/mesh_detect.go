// Package tasks — migration 071 service-mesh detection worker.
//
// Task type "mesh:detect" runs every 5 minutes plus on-demand from the
// /api/v1/clusters/{id}/service-mesh/detect/ handler. For each healthy
// cluster it:
//
//   1. Calls internal/mesh.Detect with the tunnel-backed K8sRequester.
//   2. UPSERTs the result row into cluster_service_mesh.
//   3. Emits an audit row (cluster.service_mesh.detected, plus a
//      cluster.service_mesh.changed sibling when the detected mesh
//      flipped vs the prior row).
//   4. Updates the astronomer_cluster_mesh{cluster,mesh} gauge.
//
// The task is read-side: it never mutates the cluster. The 5-minute
// cadence is deliberately loose — meshes don't move fast and the only
// downstream consumer is the UI tile.

package tasks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/mesh"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// MeshDetectType is the asynq task type identifier.
const MeshDetectType = "mesh:detect"

// NewMeshDetectTask builds the asynq envelope. Payload is empty —
// every fire walks the full healthy-cluster set. A future per-cluster
// variant would put cluster_id in the payload.
func NewMeshDetectTask() (*asynq.Task, error) {
	return asynq.NewTask(MeshDetectType, nil), nil
}

// MeshDetectQuerier is the DB surface the task uses. The handler-
// driven on-demand path uses the same surface so a single set of
// fakes covers both call sites.
type MeshDetectQuerier interface {
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetClusterServiceMesh(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterServiceMesh, error)
	UpsertClusterServiceMesh(ctx context.Context, arg sqlc.UpsertClusterServiceMeshParams) (sqlc.ClusterServiceMesh, error)
}

// MeshDetectDeps is the wiring stored at startup. Tests overwrite via
// ConfigureMeshDetect / ResetMeshDetect.
type MeshDetectDeps struct {
	Queries   MeshDetectQuerier
	Requester K8sRequester
	// MirroredQuerier is the optional sprint-069 CRD mirror surface.
	// nil falls back to direct tunnel probes inside the detector.
	MirroredQuerier mesh.Querier
}

var meshDetectDeps MeshDetectDeps

// ConfigureMeshDetect stores runtime deps; called from server startup
// once the DB + tunnel hub are wired.
func ConfigureMeshDetect(deps MeshDetectDeps) {
	meshDetectDeps = deps
}

// ResetMeshDetect clears the deps. Test-only.
func ResetMeshDetect() {
	meshDetectDeps = MeshDetectDeps{}
}

// astronomerClusterMesh is the per-(cluster,mesh) gauge. We emit one
// series for the detected mesh = 1 and zero out the prior series when
// the mesh flips. A dashboard can graph sum by (mesh) to compute the
// fleet's mesh distribution.
var astronomerClusterMesh = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "astronomer",
		Name:      "cluster_mesh",
		Help:      "1 if the cluster has the given service mesh detected; 0 otherwise.",
	},
	observability.MetricLabels("cluster", "mesh"),
)

// meshDetectAttempts counts each detection by outcome. outcome is
// "success", "failure", or "skipped" (cluster not connected).
var meshDetectAttempts = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "astronomer",
		Name:      "mesh_detect_attempts_total",
		Help:      "Service-mesh detection attempts grouped by outcome.",
	},
	observability.MetricLabels("outcome"),
)

func init() {
	prometheus.MustRegister(astronomerClusterMesh)
	prometheus.MustRegister(meshDetectAttempts)
}

// HandleMeshDetect is the asynq mux handler. It runs under the
// leader-elected periodic wrapper so only one replica drives the
// 5m fleet sweep.
func HandleMeshDetect(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, MeshDetectType, func() error {
		// F6: bound the whole sweep so an interval overrun can't run for many
		// minutes while the scheduler keeps enqueuing on top of it. Shorter than
		// the 5m cadence so a stuck tail is abandoned before the next tick.
		ctx, cancel := context.WithTimeout(ctx, meshDetectSweepDeadline)
		defer cancel()

		if meshDetectDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "mesh detect runtime not configured, skipping")
			return nil
		}
		if meshDetectDeps.Requester == nil {
			runtimeLogger().InfoContext(ctx, "mesh detect requester not configured, skipping")
			return nil
		}
		clusters, err := meshDetectDeps.Queries.ListClusters(ctx, sqlc.ListClustersParams{
			Limit:  1000,
			Offset: 0,
		})
		if err != nil {
			return fmt.Errorf("list clusters: %w", err)
		}
		// Select the CONNECTED clusters. The gate historically compared against
		// "healthy" — a status the fleet never carries (health_check.go /
		// metrics/publisher.go only ever write "active" / "disconnected", and
		// the schema defaults to "pending"), so this sweep skipped EVERY cluster
		// and was inert. "active" is the real connected status.
		active := clusters[:0:0]
		for _, c := range clusters {
			if c.Status != "active" {
				meshDetectAttempts.WithLabelValues(observability.MetricValues("skipped")...).Inc()
				continue
			}
			active = append(active, c)
		}
		// F6: fan out the per-cluster tunnel detection with bounded concurrency
		// and a per-cluster timeout so a slow/disconnected agent is
		// skipped-with-log instead of stalling the whole 5m tick.
		fanOutClusters(ctx, active, meshDetectPerClusterTimeout, func(ctx context.Context, c sqlc.Cluster) {
			if err := DetectAndUpsert(ctx, c.ID); err != nil {
				runtimeLogger().WarnContext(ctx, "mesh detect failed",
					"cluster_id", c.ID.String(),
					"error", err)
			}
		})
		return nil
	})
}

// DetectAndUpsert runs one detection cycle for a single cluster and
// upserts the result. Exported so the handler's on-demand path can
// call it directly without going through the asynq queue (the
// operator-clicked "Re-detect" button wants synchronous feedback;
// the periodic sweep doesn't).
//
// Audit emission lives in this function so both call sites get the
// same row shape.
func DetectAndUpsert(ctx context.Context, clusterID uuid.UUID) error {
	if meshDetectDeps.Queries == nil || meshDetectDeps.Requester == nil {
		return fmt.Errorf("mesh detect not configured")
	}
	// Read the prior row so we can detect a mesh flip and emit the
	// "changed" audit/metric sibling. pgx.ErrNoRows is normal on the
	// first detection for a cluster.
	prior, priorErr := meshDetectDeps.Queries.GetClusterServiceMesh(ctx, clusterID)
	priorMesh := ""
	if priorErr == nil {
		priorMesh = prior.DetectedMesh
	}
	det, err := mesh.Detect(ctx, meshDetectDeps.MirroredQuerier, meshDetectDeps.Requester, clusterID)
	if err != nil {
		meshDetectAttempts.WithLabelValues(observability.MetricValues("failure")...).Inc()
		// Even on detector error we want to record last_error so the
		// UI can surface the reason — flow into the upsert with the
		// unknown mesh + the error string.
		det.Mesh = mesh.MeshUnknown
		det.Errors = append(det.Errors, err.Error())
	}
	row, err := meshDetectDeps.Queries.UpsertClusterServiceMesh(ctx, sqlc.UpsertClusterServiceMeshParams{
		ClusterID:               clusterID,
		DetectedMesh:            det.Mesh,
		DetectedVersion:         det.Version,
		ControlPlaneNamespace:   det.ControlPlaneNamespace,
		GatewayCount:            int32(det.GatewayCount),
		VirtualServiceCount:     int32(det.VirtualServiceCount),
		DestinationRuleCount:    int32(det.DestinationRuleCount),
		PeerAuthenticationCount: int32(det.PeerAuthCount),
		ServiceProfileCount:     int32(det.ServiceProfileCount),
		ServerAuthCount:         int32(det.ServerAuthCount),
		MtlsCoveragePct:         int32(det.MTLSCoveragePct),
		LastError:               joinErrors(det.Errors),
	})
	if err != nil {
		meshDetectAttempts.WithLabelValues(observability.MetricValues("failure")...).Inc()
		return fmt.Errorf("upsert mesh: %w", err)
	}
	meshDetectAttempts.WithLabelValues(observability.MetricValues("success")...).Inc()
	// Zero out the prior mesh's gauge (so a flip cleanly shows up)
	// and set the new one to 1. When prior == current this is two
	// writes of the same value — harmless and cheap.
	if priorMesh != "" && priorMesh != row.DetectedMesh {
		astronomerClusterMesh.WithLabelValues(observability.MetricValues(clusterID.String(), priorMesh)...).Set(0)
	}
	astronomerClusterMesh.WithLabelValues(observability.MetricValues(clusterID.String(), row.DetectedMesh)...).Set(1)
	return nil
}

// joinErrors flattens the detector's per-probe error list into a
// single string for the last_error column. We cap the length at 1 KiB
// to keep the DB row narrow — the operator-facing UI only shows the
// first error anyway.
func joinErrors(errs []string) string {
	if len(errs) == 0 {
		return ""
	}
	out := strings.Join(errs, "; ")
	if len(out) > 1024 {
		out = out[:1024]
	}
	return out
}

// meshDetectInterval is the periodic cadence registered with the
// asynq scheduler. Exported so the scheduler-wiring file can read
// the same constant.
const meshDetectInterval = 5 * time.Minute

var _ = meshDetectInterval // reserved for the scheduler wiring

// meshDetectSweepDeadline caps the whole fan-out; meshDetectPerClusterTimeout
// caps a single cluster's tunnel detection. The aggregate deadline is well
// under the 5m cadence so a stuck sweep is abandoned before the next tick, and
// the per-cluster timeout keeps one slow agent from consuming the budget.
const (
	meshDetectSweepDeadline     = 4 * time.Minute
	meshDetectPerClusterTimeout = 20 * time.Second
)

// ensureClusterRow guarantees a placeholder row exists for the
// cluster before the first detection runs. The current upsert path
// already covers this, so the function is a no-op; it's reserved as
// an attachment point for the cluster_register handler if a future
// sprint wants the "Service mesh: pending detection" badge to appear
// the instant a cluster lands.
func ensureClusterRow(_ context.Context, _ uuid.UUID) error { //nolint:unused
	return nil
}
