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
// The task is optional when the deployment feature is disabled. When enabled,
// production composition validation requires the querier, applier, and status
// reader before the tunnel queue starts.

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ControlPlane snapshot task identifiers. The apply task is produced through
// PostgreSQL's task outbox by the API; the sweep remains the periodic status
// reconciler and scheduled-capture recovery path.
const (
	ControlPlaneSnapshotSweepType = "control_plane_snapshot:sweep"
	ControlPlaneSnapshotApplyType = "control_plane_snapshot:apply"
)

type ControlPlaneSnapshotApplyPayload struct {
	SnapshotID string `json:"snapshot_id"`
}

func NewControlPlaneSnapshotApplyTask(snapshotID uuid.UUID) (*asynq.Task, error) {
	if snapshotID == uuid.Nil {
		return nil, errors.New("control-plane snapshot apply requires snapshot_id")
	}
	body, err := json.Marshal(ControlPlaneSnapshotApplyPayload{SnapshotID: snapshotID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal control-plane snapshot apply: %w", err)
	}
	return asynq.NewTask(ControlPlaneSnapshotApplyType, body, asynq.MaxRetry(5)), nil
}

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
	// controlPlaneSnapshotReconcilePageSize bounds the single page of
	// in-flight rows the reconcile walks per tick. It deliberately does NOT
	// offset-paginate (the reconcile mutates the "running" set it lists), so
	// this cap must comfortably exceed the realistic number of concurrently
	// in-flight snapshots; the remainder is handled on the next 1m tick.
	controlPlaneSnapshotReconcilePageSize = 500
	controlPlaneSnapshotPendingPageSize   = 200
)

// ControlPlaneSnapshotSweepQuerier is the narrow DB surface the sweep
// needs. Satisfied by *sqlc.Queries in production.
type ControlPlaneSnapshotSweepQuerier interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetControlPlaneSnapshotByID(ctx context.Context, id uuid.UUID) (sqlc.ControlPlaneSnapshot, error)
	GetLatestControlPlaneSnapshotByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ControlPlaneSnapshot, error)
	CreateControlPlaneSnapshot(ctx context.Context, arg sqlc.CreateControlPlaneSnapshotParams) (sqlc.ControlPlaneSnapshot, error)
	MarkControlPlaneSnapshotStatus(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotStatusParams) error
	MarkControlPlaneSnapshotSucceeded(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotSucceededParams) error
	MarkControlPlaneSnapshotFailed(ctx context.Context, arg sqlc.MarkControlPlaneSnapshotFailedParams) error
	ListRunningControlPlaneSnapshots(ctx context.Context, arg sqlc.ListRunningControlPlaneSnapshotsParams) ([]sqlc.ControlPlaneSnapshot, error)
	ListPendingControlPlaneSnapshots(ctx context.Context, limit int32) ([]sqlc.ControlPlaneSnapshot, error)
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

// NewControlPlaneSnapshotSweepTask returns the periodic task body.
func NewControlPlaneSnapshotSweepTask() *asynq.Task {
	return asynq.NewTask(ControlPlaneSnapshotSweepType, nil, asynq.MaxRetry(2))
}

// HandleControlPlaneSnapshotApply reconciles one committed on-demand intent.
// The Job name derives from the immutable snapshot ID and the applier treats a
// Kubernetes AlreadyExists response as success, so replay after an uncertain
// worker outcome cannot create a duplicate capture.
func (runtime ControlPlaneSnapshotRuntime) HandleControlPlaneSnapshotApply(ctx context.Context, task *asynq.Task) error {
	runtime = runtime.normalized()
	if runtime.Deps.Queries == nil || runtime.Applier == nil {
		return fmt.Errorf("control-plane snapshot apply runtime is not configured")
	}
	if task == nil {
		return asynq.SkipRetry
	}
	var payload ControlPlaneSnapshotApplyPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: decode control-plane snapshot apply: %v", asynq.SkipRetry, err)
	}
	snapshotID, err := uuid.Parse(payload.SnapshotID)
	if err != nil {
		return fmt.Errorf("%w: invalid control-plane snapshot ID", asynq.SkipRetry)
	}
	row, err := runtime.Deps.Queries.GetControlPlaneSnapshotByID(ctx, snapshotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load control-plane snapshot intent: %w", err)
	}
	if row.Status == "running" || row.Status == "succeeded" {
		return nil
	}
	cluster, err := runtime.Deps.Queries.GetClusterByID(ctx, row.ClusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load control-plane snapshot cluster: %w", err)
	}
	family, ok := controlPlaneSnapshotSweepDistro(cluster.Distribution)
	if !ok {
		markErr := runtime.Deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
			ID: row.ID, Error: "cluster distribution no longer supports control-plane snapshots",
		})
		return errors.Join(fmt.Errorf("%w: unsupported control-plane distribution", asynq.SkipRetry), markErr)
	}
	if err := runtime.Applier(ctx, row.ClusterID.String(), row.ID.String(), row.Name, family, row.Location); err != nil {
		markErr := runtime.Deps.Queries.MarkControlPlaneSnapshotStatus(ctx, sqlc.MarkControlPlaneSnapshotStatusParams{
			ID: row.ID, Status: "pending", Error: "snapshot Job submission failed; durable operation will retry",
		})
		return errors.Join(fmt.Errorf("apply control-plane snapshot Job: %w", err), markErr)
	}
	if err := runtime.Deps.Queries.MarkControlPlaneSnapshotStatus(ctx, sqlc.MarkControlPlaneSnapshotStatusParams{
		ID: row.ID, Status: "running", Error: "",
	}); err != nil {
		return fmt.Errorf("mark control-plane snapshot running: %w", err)
	}
	return nil
}

// HandleControlPlaneSnapshotSweep is the asynq handler. Leader-gated like
// every other periodic reconciler.
func (runtime ControlPlaneSnapshotRuntime) HandleControlPlaneSnapshotSweep(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ControlPlaneSnapshotSweepType, func() error {
		deps := runtime.normalized().Deps
		if deps.Queries == nil {
			// This task is explicitly optional in the task registry.
			return ErrPeriodicTaskSkipped
		}
		// Repair pending rows first. This closes the crash window for both
		// task-outbox delivery and worker-scheduled rows created immediately
		// before process loss.
		if err := runtime.reconcilePendingControlPlaneSnapshots(ctx, deps); err != nil {
			return err
		}
		// Reconcile in-flight rows to a terminal state whenever the feature
		// is wired. This covers BOTH on-demand snapshots (handler
		// TriggerSnapshot) and scheduled ones, so a "running" row always
		// eventually resolves.
		if err := runtime.reconcileControlPlaneSnapshots(ctx, deps); err != nil {
			return err
		}
		// Auto-scheduling is a further opt-in on top: only create rolling
		// snapshots when the operator flips feature.control_plane_snapshots.
		enabled, err := controlPlaneSnapshotFeatureEnabled(ctx, deps.Queries)
		if err != nil {
			return err
		}
		if enabled {
			return runtime.sweepControlPlaneSnapshots(ctx, deps)
		}
		return nil
	})
}

func (runtime ControlPlaneSnapshotRuntime) reconcilePendingControlPlaneSnapshots(ctx context.Context, deps ControlPlaneSnapshotSweepDeps) error {
	rows, err := deps.Queries.ListPendingControlPlaneSnapshots(ctx, controlPlaneSnapshotPendingPageSize)
	if err != nil {
		return fmt.Errorf("list pending control-plane snapshots: %w", err)
	}
	var repairErrors []error
	for _, row := range rows {
		task, taskErr := NewControlPlaneSnapshotApplyTask(row.ID)
		if taskErr != nil {
			repairErrors = append(repairErrors, taskErr)
			continue
		}
		if applyErr := runtime.HandleControlPlaneSnapshotApply(ctx, task); applyErr != nil {
			repairErrors = append(repairErrors, fmt.Errorf("repair pending snapshot %s: %w", row.ID, applyErr))
		}
	}
	return errors.Join(repairErrors...)
}

// reconcileControlPlaneSnapshots polls each in-flight snapshot Job and moves
// its row to succeeded/failed. Transient per-row failures leave the row for the
// next tick and are returned so the reconciliation is observable.
func (runtime ControlPlaneSnapshotRuntime) reconcileControlPlaneSnapshots(ctx context.Context, deps ControlPlaneSnapshotSweepDeps) error {
	reader := runtime.StatusReader
	if reader == nil {
		return fmt.Errorf("control-plane snapshot status reader is not configured")
	}

	// Fetch a SINGLE bounded page (OFFSET 0) per tick and process it, then
	// return. We must NOT offset-paginate here: reconcile mutates the very
	// set it lists (terminal transitions drop rows out of the "running"
	// filter), so a growing OFFSET would skip the still-running rows that
	// shifted into the slots we already walked past. The task fires every
	// minute, so any rows beyond this page (rows that stayed "running", plus
	// freshly-shrunk-in rows) are picked up on the next tick — the reconcile
	// is idempotent, so re-listing an already-terminal row is harmless.
	rows, err := deps.Queries.ListRunningControlPlaneSnapshots(ctx, sqlc.ListRunningControlPlaneSnapshotsParams{
		Limit:  controlPlaneSnapshotReconcilePageSize,
		Offset: 0,
	})
	if err != nil {
		return fmt.Errorf("list running control-plane snapshots: %w", err)
	}
	var reconcileErrors []error
	for _, row := range rows {
		if ctx.Err() != nil {
			return errors.Join(append(reconcileErrors, ctx.Err())...)
		}
		phase, detail, err := reader(ctx, row.ClusterID.String(), row.ID.String())
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("read snapshot %s status: %w", row.ID, err))
			continue
		}
		var persistErr error
		switch phase {
		case "succeeded":
			// ponytail: size unknown (no log scrape) → NULL; UI renders "—".
			persistErr = deps.Queries.MarkControlPlaneSnapshotSucceeded(ctx, sqlc.MarkControlPlaneSnapshotSucceededParams{
				ID:        row.ID,
				SizeBytes: pgtype.Int8{},
			})
		case "failed":
			persistErr = deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
				ID:    row.ID,
				Error: detail,
			})
		case "gone":
			// Job object expired (ttlSecondsAfterFinished) before we
			// observed a terminal condition. We can't prove the outcome,
			// so fail closed with a clear, non-alarming message.
			persistErr = deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
				ID:    row.ID,
				Error: "snapshot Job completed and was garbage-collected before its result could be read; check the cluster's on-disk snapshots",
			})
		default:
			// "running" — leave it.
		}
		if persistErr != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("persist snapshot %s phase %s: %w", row.ID, phase, persistErr))
		}
	}
	return errors.Join(reconcileErrors...)
}

// controlPlaneSnapshotFeatureEnabled reads the feature flag. Absent /
// unparseable / errored == false (opt-in default).
func controlPlaneSnapshotFeatureEnabled(ctx context.Context, q ControlPlaneSnapshotSweepQuerier) (bool, error) {
	row, err := q.GetPlatformSetting(ctx, controlPlaneSnapshotFeatureKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read control-plane snapshot feature setting: %w", err)
	}
	if len(row.Value) == 0 {
		return false, nil
	}
	var v bool
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return false, fmt.Errorf("decode control-plane snapshot feature setting: %w", err)
	}
	return v, nil
}

func (runtime ControlPlaneSnapshotRuntime) sweepControlPlaneSnapshots(ctx context.Context, deps ControlPlaneSnapshotSweepDeps) error {
	now := time.Now().UTC()
	var sweepErrors []error

	var offset int32
	for {
		clusters, err := deps.Queries.ListClusters(ctx, sqlc.ListClustersParams{
			Limit:  controlPlaneSnapshotPageSize,
			Offset: offset,
		})
		if err != nil {
			return errors.Join(append(sweepErrors, fmt.Errorf("list clusters for control-plane snapshots: %w", err))...)
		}
		if len(clusters) == 0 {
			return errors.Join(sweepErrors...)
		}
		for _, cluster := range clusters {
			if err := runtime.sweepOneCluster(ctx, deps, cluster, now); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return errors.Join(append(sweepErrors, err)...)
				}
				sweepErrors = append(sweepErrors, fmt.Errorf("snapshot cluster %s: %w", cluster.ID, err))
			}
		}
		if len(clusters) < controlPlaneSnapshotPageSize {
			return errors.Join(sweepErrors...)
		}
		offset += controlPlaneSnapshotPageSize
	}
}

func (runtime ControlPlaneSnapshotRuntime) sweepOneCluster(ctx context.Context, deps ControlPlaneSnapshotSweepDeps, cluster sqlc.Cluster, now time.Time) error {
	family, ok := controlPlaneSnapshotSweepDistro(cluster.Distribution)
	if !ok {
		return nil // managed / unsupported — never snapshot.
	}
	due, err := controlPlaneSnapshotDue(ctx, deps.Queries, cluster.ID, now)
	if err != nil {
		return err
	}
	if !due {
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

	if runtime.Applier == nil {
		markErr := deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
			ID:    row.ID,
			Error: "control-plane snapshot applier not configured",
		})
		return errors.Join(fmt.Errorf("control-plane snapshot applier is not configured"), markErr)
	}
	if err := runtime.Applier(ctx, cluster.ID.String(), row.ID.String(), name, family, "local"); err != nil {
		markErr := deps.Queries.MarkControlPlaneSnapshotFailed(ctx, sqlc.MarkControlPlaneSnapshotFailedParams{
			ID:    row.ID,
			Error: err.Error(),
		})
		return errors.Join(fmt.Errorf("apply snapshot job: %w", err), markErr)
	}
	if err := deps.Queries.MarkControlPlaneSnapshotStatus(ctx, sqlc.MarkControlPlaneSnapshotStatusParams{
		ID:     row.ID,
		Status: "running",
		Error:  "",
	}); err != nil {
		return fmt.Errorf("mark snapshot running: %w", err)
	}

	if err := deps.Queries.PruneControlPlaneSnapshots(ctx, sqlc.PruneControlPlaneSnapshotsParams{
		ClusterID: cluster.ID,
		Limit:     controlPlaneSnapshotRetention,
	}); err != nil {
		return fmt.Errorf("prune control-plane snapshot history: %w", err)
	}
	return nil
}

// controlPlaneSnapshotDue reports whether the cluster needs a fresh
// scheduled snapshot: true when there is no prior snapshot, or the most
// recent one is older than the interval. A pending/running row inside the
// window suppresses a duplicate; a failed row does NOT (we retry next
// tick once the interval passes).
func controlPlaneSnapshotDue(ctx context.Context, q ControlPlaneSnapshotSweepQuerier, clusterID uuid.UUID, now time.Time) (bool, error) {
	latest, err := q.GetLatestControlPlaneSnapshotByCluster(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil // never snapshotted.
		}
		return false, fmt.Errorf("read latest control-plane snapshot: %w", err)
	}
	return now.Sub(latest.CreatedAt) >= controlPlaneSnapshotInterval, nil
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
