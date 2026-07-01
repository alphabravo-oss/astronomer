// Control-plane (etcd) DR snapshot sweep worker (migration 125).
//
// Periodic, leader-gated task that fires SCHEDULED control-plane snapshots
// for eligible clusters. It complements the handler's on-demand
// TriggerSnapshot: the handler covers "operator clicks snapshot now", this
// covers "keep a rolling set of DR snapshots without anyone clicking".
//
// Gating (all must hold or the sweep no-ops for that cluster):
//   - feature.control_plane_snapshots is true  (whole feature opt-in).
//   - the cluster's distribution is self-managed (k3s / RKE2 / kubeadm) —
//     managed control planes (EKS/GKE/AKS) have no reachable etcd.
//   - the cluster is DUE: no successful/in-flight snapshot within the
//     interval window.
//
// The sweep creates the control_plane_snapshots row itself, then applies
// the snapshot Job through a callback the handler wires at startup
// (SetControlPlaneSnapshotApplier) — the worker package can't import the
// handler package, so the Job manifest lives in exactly one place
// (handler.ApplySnapshotJob) and is reached here indirectly, mirroring the
// SetSnapshotOutcomeRecorder pattern in cluster_snapshot_poll.go.
//
// Until ConfigureControlPlaneSnapshotSweep + SetControlPlaneSnapshotApplier
// fire the task is a no-op; the scheduler entry still ticks harmlessly.

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ControlPlaneSnapshotSweepType is the periodic task identifier.
const ControlPlaneSnapshotSweepType = "control_plane_snapshot:sweep"

// controlPlaneSnapshotFeatureKey gates the entire feature. Default false
// (feature absent == off), matching the platform_settings.go default the
// operator registers.
const controlPlaneSnapshotFeatureKey = "feature.control_plane_snapshots"

// Schedule knobs. Kept deliberately simple (constants, not per-cluster
// config): snapshot at most once per interval, retain the newest N
// terminal rows per cluster.
const (
	controlPlaneSnapshotInterval  = 24 * time.Hour
	controlPlaneSnapshotRetention = 7
	controlPlaneSnapshotPageSize  = 200
)

// ControlPlaneSnapshotSweepQuerier is the narrow DB surface the sweep
// needs. Satisfied by *sqlc.Queries in production.
type ControlPlaneSnapshotSweepQuerier interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetLatestControlPlaneSnapshotByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ControlPlaneSnapshot, error)
	CreateControlPlaneSnapshot(ctx context.Context, arg sqlc.CreateControlPlaneSnapshotParams) (sqlc.ControlPlaneSnapshot, error)
	MarkControlPlaneSnapshotStatus(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotStatusParams) error
	MarkControlPlaneSnapshotSucceeded(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotSucceededParams) error
	MarkControlPlaneSnapshotFailed(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotFailedParams) error
	ListRunningControlPlaneSnapshots(ctx context.Context, arg sqlc.ListRunningControlPlaneSnapshotsParams) ([]sqlc.ControlPlaneSnapshot, error)
	PruneControlPlaneSnapshots(ctx context.Context, arg sqlc.PruneControlPlaneSnapshotsParams) error
}

// ControlPlaneSnapshotApplier renders + applies the snapshot Job for an
// already-created row. Supplied by the handler package (its
// ApplySnapshotJob method) via SetControlPlaneSnapshotApplier.
type ControlPlaneSnapshotApplier func(ctx context.Context, clusterID, snapshotID, name, family, location string) error

// ControlPlaneSnapshotStatusReader reads a snapshot Job's terminal phase back
// through the tunnel so the sweep can move a row off "running". Supplied by
// the handler (its ReadSnapshotJobStatus method) via
// SetControlPlaneSnapshotStatusReader. phase is one of "succeeded", "failed",
// "running", or "gone" (Job TTL-expired before we polled).
type ControlPlaneSnapshotStatusReader func(ctx context.Context, clusterID, snapshotID string) (phase, detail string, err error)

// ControlPlaneSnapshotSweepDeps is set once at startup.
type ControlPlaneSnapshotSweepDeps struct {
	Queries ControlPlaneSnapshotSweepQuerier
	Log     *slog.Logger
}

var (
	controlPlaneSnapshotDeps         ControlPlaneSnapshotSweepDeps
	controlPlaneSnapshotApplier      ControlPlaneSnapshotApplier
	controlPlaneSnapshotStatusReader ControlPlaneSnapshotStatusReader
)

// ConfigureControlPlaneSnapshotSweep wires the sweep's DB deps. Last
// writer wins; production calls it once.
func ConfigureControlPlaneSnapshotSweep(deps ControlPlaneSnapshotSweepDeps) {
	controlPlaneSnapshotDeps = deps
}

// SetControlPlaneSnapshotApplier wires the handler-side Job applier. nil
// leaves the sweep unable to apply Jobs (it will mark scheduled rows
// failed with a clear message rather than crash).
func SetControlPlaneSnapshotApplier(fn ControlPlaneSnapshotApplier) {
	controlPlaneSnapshotApplier = fn
}

// SetControlPlaneSnapshotStatusReader wires the handler-side Job poller. nil
// leaves running rows un-reconciled (they simply stay "running") rather than
// crashing the sweep.
func SetControlPlaneSnapshotStatusReader(fn ControlPlaneSnapshotStatusReader) {
	controlPlaneSnapshotStatusReader = fn
}

// NewControlPlaneSnapshotSweepTask returns the periodic task body.
func NewControlPlaneSnapshotSweepTask() *asynq.Task {
	return asynq.NewTask(ControlPlaneSnapshotSweepType, nil, asynq.MaxRetry(2))
}

// HandleControlPlaneSnapshotSweep is the asynq handler. Leader-gated like
// every other periodic reconciler.
func HandleControlPlaneSnapshotSweep(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ControlPlaneSnapshotSweepType, func() error {
		deps := controlPlaneSnapshotDeps
		if deps.Queries == nil {
			// Not wired (control_plane_snapshots_enabled=false, startup
			// race, or tests) — the whole feature is off.
			return nil
		}
		// Reconcile in-flight rows to a terminal state whenever the feature
		// is wired. This covers BOTH on-demand snapshots (handler
		// TriggerSnapshot) and scheduled ones, so a "running" row always
		// eventually resolves.
		reconcileControlPlaneSnapshots(ctx, deps)
		// Auto-scheduling is a further opt-in on top: only create rolling
		// snapshots when the operator flips feature.control_plane_snapshots.
		if controlPlaneSnapshotFeatureEnabled(ctx, deps.Queries) {
			sweepControlPlaneSnapshots(ctx, deps)
		}
		return nil
	})
}

// reconcileControlPlaneSnapshots polls each in-flight snapshot Job and moves
// its row to succeeded/failed. Best-effort: a transport error just leaves the
// row for the next tick; a nil reader (poller not wired) is a no-op.
func reconcileControlPlaneSnapshots(ctx context.Context, deps ControlPlaneSnapshotSweepDeps) {
	reader := controlPlaneSnapshotStatusReader
	if reader == nil {
		return
	}
	log := controlPlaneSnapshotLogger(deps)

	var offset int32
	for {
		rows, err := deps.Queries.ListRunningControlPlaneSnapshots(ctx, sqlc.ListRunningControlPlaneSnapshotsParams{
			Limit:  controlPlaneSnapshotPageSize,
			Offset: offset,
		})
		if err != nil {
			log.Warn("control-plane snapshot reconcile: list running", "error", err.Error())
			return
		}
		if len(rows) == 0 {
			return
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			phase, detail, err := reader(ctx, row.ClusterID.String(), row.ID.String())
			if err != nil {
				// Agent unreachable / transient — retry next tick.
				continue
			}
			switch phase {
			case "succeeded":
				// ponytail: size unknown (no log scrape) → NULL; UI renders "—".
				_ = deps.Queries.MarkControlPlaneSnapshotSucceeded(ctx, sqlc.MarkControlPlaneSnapshotSucceededParams{
					ID:        row.ID,
					SizeBytes: pgtype.Int8{},
				})
			case "failed":
				_ = deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
					ID:    row.ID,
					Error: detail,
				})
			case "gone":
				// Job object expired (ttlSecondsAfterFinished) before we
				// observed a terminal condition. We can't prove the outcome,
				// so fail closed with a clear, non-alarming message.
				_ = deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
					ID:    row.ID,
					Error: "snapshot Job completed and was garbage-collected before its result could be read; check the cluster's on-disk snapshots",
				})
			default:
				// "running" — leave it.
			}
		}
		if len(rows) < controlPlaneSnapshotPageSize {
			return
		}
		offset += controlPlaneSnapshotPageSize
	}
}

// controlPlaneSnapshotFeatureEnabled reads the feature flag. Absent /
// unparseable / errored == false (opt-in default).
func controlPlaneSnapshotFeatureEnabled(ctx context.Context, q ControlPlaneSnapshotSweepQuerier) bool {
	row, err := q.GetPlatformSetting(ctx, controlPlaneSnapshotFeatureKey)
	if err != nil {
		return false
	}
	var v bool
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return false
	}
	return v
}

func sweepControlPlaneSnapshots(ctx context.Context, deps ControlPlaneSnapshotSweepDeps) {
	log := controlPlaneSnapshotLogger(deps)
	now := time.Now().UTC()

	var offset int32
	for {
		clusters, err := deps.Queries.ListClusters(ctx, sqlc.ListClustersParams{
			Limit:  controlPlaneSnapshotPageSize,
			Offset: offset,
		})
		if err != nil {
			log.Warn("control-plane snapshot sweep: list clusters", "error", err.Error())
			return
		}
		if len(clusters) == 0 {
			return
		}
		for _, cluster := range clusters {
			if err := sweepOneCluster(ctx, deps, cluster, now); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				log.Warn("control-plane snapshot sweep", "cluster_id", cluster.ID.String(), "error", err.Error())
			}
		}
		if len(clusters) < controlPlaneSnapshotPageSize {
			return
		}
		offset += controlPlaneSnapshotPageSize
	}
}

func sweepOneCluster(ctx context.Context, deps ControlPlaneSnapshotSweepDeps, cluster sqlc.Cluster, now time.Time) error {
	family, ok := controlPlaneSnapshotSweepDistro(cluster.Distribution)
	if !ok {
		return nil // managed / unsupported — never snapshot.
	}
	if !controlPlaneSnapshotDue(ctx, deps.Queries, cluster.ID, now) {
		return nil
	}

	name := controlPlaneSnapshotSweepName(cluster.Name, now)
	row, err := deps.Queries.CreateControlPlaneSnapshot(ctx, sqlc.CreateControlPlaneSnapshotParams{
		ClusterID:     cluster.ID,
		Name:          name,
		Status:        "pending",
		Location:      "local",
		RequestedByID: emptyUUID(), // worker-scheduled: no human actor.
	})
	if err != nil {
		return err
	}

	if controlPlaneSnapshotApplier == nil {
		_ = deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
			ID:    row.ID,
			Error: "control-plane snapshot applier not configured",
		})
		return nil
	}
	if err := controlPlaneSnapshotApplier(ctx, cluster.ID.String(), row.ID.String(), name, family, "local"); err != nil {
		_ = deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
			ID:    row.ID,
			Error: err.Error(),
		})
		return nil
	}
	_ = deps.Queries.MarkControlPlaneSnapshotStatus(ctx, sqlc.MarkControlPlaneSnapshotStatusParams{
		ID:     row.ID,
		Status: "running",
		Error:  "",
	})

	// Best-effort retention prune. Never fails the sweep.
	_ = deps.Queries.PruneControlPlaneSnapshots(ctx, sqlc.PruneControlPlaneSnapshotsParams{
		ClusterID: cluster.ID,
		Limit:     controlPlaneSnapshotRetention,
	})
	return nil
}

// controlPlaneSnapshotDue reports whether the cluster needs a fresh
// scheduled snapshot: true when there is no prior snapshot, or the most
// recent one is older than the interval. A pending/running row inside the
// window suppresses a duplicate; a failed row does NOT (we retry next
// tick once the interval passes).
func controlPlaneSnapshotDue(ctx context.Context, q ControlPlaneSnapshotSweepQuerier, clusterID uuid.UUID, now time.Time) bool {
	latest, err := q.GetLatestControlPlaneSnapshotByCluster(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true // never snapshotted.
		}
		return false // DB error — skip this tick rather than spam.
	}
	return now.Sub(latest.CreatedAt) >= controlPlaneSnapshotInterval
}

// controlPlaneSnapshotSweepDistro mirrors the handler's eligibility check
// (duplicated to keep the worker → handler import edge one-directional).
func controlPlaneSnapshotSweepDistro(distribution string) (family string, ok bool) {
	d := strings.ToLower(strings.TrimSpace(distribution))
	switch {
	case strings.Contains(d, "k3s"), strings.Contains(d, "k3d"):
		return "k3s", true
	case strings.Contains(d, "rke2"):
		return "rke2", true
	case strings.Contains(d, "kubeadm"):
		return "kubeadm", true
	default:
		return "", false
	}
}

// controlPlaneSnapshotSweepName builds the scheduled snapshot's name:
// "<cluster>-cpsched-<timestamp>". Lowercased + sanitized so it passes
// the RFC-1123 check both here and on the k8s side.
func controlPlaneSnapshotSweepName(cluster string, now time.Time) string {
	stamp := now.Format("20060102t150405")
	base := strings.ToLower(strings.TrimSpace(cluster))
	out := make([]byte, 0, len(base))
	for i := 0; i < len(base); i++ {
		c := base[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	name := strings.Trim(string(out), "-")
	if name == "" {
		name = "cluster"
	}
	name = name + "-cpsched-" + stamp
	if len(name) > 253 {
		name = strings.TrimRight(name[:253], "-")
	}
	return name
}

func controlPlaneSnapshotLogger(deps ControlPlaneSnapshotSweepDeps) *slog.Logger {
	if deps.Log != nil {
		return deps.Log
	}
	return runtimeLogger()
}
