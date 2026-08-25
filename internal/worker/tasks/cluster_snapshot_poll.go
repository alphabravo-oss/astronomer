// Per-cluster Velero snapshot lifecycle workers (migration 052).
//
// Four asynq task types live in this file because they share the same
// Querier interface + Velero-driver adapter:
//
//   cluster_snapshot:poll               every 30s
//     Walk cluster_snapshots / cluster_restores rows in a non-terminal
//     phase; GET the corresponding Velero CR; mirror status fields.
//
//   cluster_snapshot:dispatch_scheduled every 1m
//     Walk enabled cluster_snapshot_schedules rows; for each whose cron
//     expression has elapsed since last_run_at, enqueue a fresh
//     cluster_snapshots row (and POST the Velero Backup CRD).
//
//   cluster_snapshot:cleanup_expired    every 1d
//     Drop cluster_snapshots rows whose expires_at < now() AND phase is
//     terminal. Velero handles the object-store cleanup via its own TTL;
//     this task is purely DB hygiene.
//
//   cluster_snapshot:apply_operation    on demand
//     Reconciles one committed snapshot create/delete or restore intent.
//     The API writes this task through the PostgreSQL task outbox in the
//     same transaction as desired state and mandatory audit.
//
// All three tasks coordinate through one immutable ClusterSnapshotRuntime.
// Missing dependencies fail worker construction before queue consumption.

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron/v3"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Task type constants. Re-exported in internal/worker/worker.go for the
// mux wiring (`TypeClusterSnapshotPoll`, …).
const (
	ClusterSnapshotPollType              = "cluster_snapshot:poll"
	ClusterSnapshotDispatchScheduledType = "cluster_snapshot:dispatch_scheduled"
	ClusterSnapshotCleanupExpiredType    = "cluster_snapshot:cleanup_expired"
	ClusterSnapshotApplyOperationType    = "cluster_snapshot:apply_operation"
)

type ClusterSnapshotOperation string

const (
	ClusterSnapshotOperationCreate  ClusterSnapshotOperation = "create_snapshot"
	ClusterSnapshotOperationDelete  ClusterSnapshotOperation = "delete_snapshot"
	ClusterSnapshotOperationRestore ClusterSnapshotOperation = "create_restore"
)

// ClusterSnapshotOperationPayload contains only durable identifiers and the
// non-secret external reference needed after a local delete. Snapshot and
// restore specs remain in PostgreSQL and are loaded by the worker.
type ClusterSnapshotOperationPayload struct {
	Operation       ClusterSnapshotOperation `json:"operation"`
	SnapshotID      string                   `json:"snapshot_id,omitempty"`
	RestoreID       string                   `json:"restore_id,omitempty"`
	ClusterID       string                   `json:"cluster_id,omitempty"`
	VeleroName      string                   `json:"velero_name,omitempty"`
	VeleroNamespace string                   `json:"velero_namespace,omitempty"`
}

func NewClusterSnapshotOperationTask(payload ClusterSnapshotOperationPayload) (*asynq.Task, error) {
	if err := payload.validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal cluster snapshot operation: %w", err)
	}
	return asynq.NewTask(ClusterSnapshotApplyOperationType, body, asynq.MaxRetry(5)), nil
}

func (payload ClusterSnapshotOperationPayload) validate() error {
	switch payload.Operation {
	case ClusterSnapshotOperationCreate:
		if _, err := uuid.Parse(payload.SnapshotID); err != nil {
			return fmt.Errorf("create snapshot operation requires snapshot_id: %w", err)
		}
	case ClusterSnapshotOperationRestore:
		if _, err := uuid.Parse(payload.RestoreID); err != nil {
			return fmt.Errorf("restore operation requires restore_id: %w", err)
		}
	case ClusterSnapshotOperationDelete:
		if _, err := uuid.Parse(payload.SnapshotID); err != nil {
			return fmt.Errorf("delete snapshot operation requires snapshot_id: %w", err)
		}
		if _, err := uuid.Parse(payload.ClusterID); err != nil {
			return fmt.Errorf("delete snapshot operation requires cluster_id: %w", err)
		}
		if strings.TrimSpace(payload.VeleroName) == "" || strings.TrimSpace(payload.VeleroNamespace) == "" {
			return errors.New("delete snapshot operation requires Velero name and namespace")
		}
	default:
		return fmt.Errorf("unsupported cluster snapshot operation %q", payload.Operation)
	}
	return nil
}

// snapshotPollBatchSize caps how many in-flight rows the poller looks
// at per tick. Velero typically has at most a handful of in-flight
// backups per cluster; this is a defense-in-depth bound, not a
// throughput knob.
const snapshotPollBatchSize = 200

// expiredCleanupBatchSize caps the daily DB-cleanup sweep.
const expiredCleanupBatchSize = 500

// ClusterSnapshotQuerier is the narrow DB surface the three workers
// need. Same name as the handler's querier but slightly different
// shape — the handler doesn't poll status, the worker doesn't audit.
type ClusterSnapshotPollQuerier interface {
	// Snapshots
	ListPendingClusterSnapshots(ctx context.Context, lim int32) ([]sqlc.ClusterSnapshot, error)
	MarkSnapshotPhase(ctx context.Context, arg sqlc.MarkSnapshotPhaseParams) error
	ListExpiredTerminalSnapshots(ctx context.Context, lim int32) ([]sqlc.ClusterSnapshot, error)
	DeleteClusterSnapshot(ctx context.Context, id uuid.UUID) error

	// Restores
	ListPendingClusterRestores(ctx context.Context, lim int32) ([]sqlc.ClusterRestore, error)
	MarkRestorePhase(ctx context.Context, arg sqlc.MarkRestorePhaseParams) error
	GetClusterSnapshotByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterSnapshot, error)
	GetClusterRestoreByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterRestore, error)

	// Schedules
	ListEnabledSnapshotSchedules(ctx context.Context) ([]sqlc.ClusterSnapshotSchedule, error)
	MarkSnapshotScheduleRan(ctx context.Context, arg sqlc.MarkSnapshotScheduleRanParams) error
	CreateClusterSnapshot(ctx context.Context, arg sqlc.CreateClusterSnapshotParams) (sqlc.ClusterSnapshot, error)
}

// VeleroSnapshotDriver is the narrow tunnel surface the poller / scheduler
// need to drive Velero CRDs. The handler package supplies an adapter
// implementation; the worker doesn't reach into the handler package
// directly so we keep the dep graph one-directional (handler → tasks
// for enqueue, tasks → handler for nothing).
type VeleroSnapshotDriver interface {
	// GetBackup fetches a Velero Backup CR. Returns os.ErrNotExist-style
	// ErrVeleroCRDMissing when the CR has been deleted out from under us.
	GetBackup(ctx context.Context, clusterID, namespace, name string) (VeleroBackupStatusSnapshot, error)
	// GetRestore fetches a Velero Restore CR.
	GetRestore(ctx context.Context, clusterID, namespace, name string) (VeleroRestoreStatusSnapshot, error)
	CreateSnapshot(ctx context.Context, snapshot sqlc.ClusterSnapshot) error
	CreateRestore(ctx context.Context, restore sqlc.ClusterRestore, snapshot sqlc.ClusterSnapshot) error
	DeleteSnapshot(ctx context.Context, clusterID, namespace, backupName, operationID string) error
}

// VeleroBackupStatusSnapshot is the worker-side view of Velero's
// BackupStatus. The handler renders this from its parsed unstructured
// CR. We intentionally use a value type (no map[string]any leaking
// out of the driver) so test fakes don't have to know Velero's full
// CR shape.
type VeleroBackupStatusSnapshot struct {
	Phase           string
	StartTime       time.Time
	CompletionTime  time.Time
	Warnings        int
	Errors          int
	NotFound        bool   // set to true when the driver returned ErrVeleroCRDMissing
	ValidationError string // first validation error, when present
}

// VeleroRestoreStatusSnapshot is the worker-side view of Velero's RestoreStatus.
type VeleroRestoreStatusSnapshot struct {
	Phase           string
	StartTime       time.Time
	CompletionTime  time.Time
	Warnings        int
	Errors          int
	NotFound        bool
	ValidationError string
}

// ClusterSnapshotDeps wires all three workers in this file.
type ClusterSnapshotDeps struct {
	Queries ClusterSnapshotPollQuerier
	Driver  VeleroSnapshotDriver
	Log     *slog.Logger
}

// terminalSnapshotPhases is the set of Velero BackupStatus.Phase values
// we treat as "done, don't poll again". Maps 1:1 onto Velero's own
// enum — we deliberately store the exact upstream string so a future
// Velero release adding a new terminal phase doesn't require a code
// change, just a constant update.
var terminalSnapshotPhases = map[string]struct{}{
	"Completed":        {},
	"Failed":           {},
	"FailedValidation": {},
	"PartiallyFailed":  {},
	"Deleted":          {},
}

// outcomeForPhase maps a terminal Velero phase into one of the three
// outcome labels the astronomer_cluster_snapshots_total counter uses.
// Non-terminal phases yield "" — the caller skips the counter inc.
func outcomeForPhase(phase string) string {
	switch phase {
	case "Completed":
		return "completed"
	case "PartiallyFailed":
		return "partial"
	case "Failed", "FailedValidation", "Deleted":
		return "failed"
	default:
		return ""
	}
}

// ----------------------------------------------------------------------
// cluster_snapshot:apply_operation
// ----------------------------------------------------------------------

// HandleClusterSnapshotApplyOperation executes one durable external-effect
// intent. Kubernetes create conflicts are normalized by the driver, making a
// replay after an API/worker crash safe. Missing desired-state rows are stale
// tasks and therefore successful no-ops.
func (runtime ClusterSnapshotRuntime) HandleClusterSnapshotApplyOperation(ctx context.Context, task *asynq.Task) error {
	deps := runtime.normalized().Deps
	if deps.Queries == nil || deps.Driver == nil {
		return fmt.Errorf("cluster snapshot operation runtime is not configured")
	}
	if task == nil {
		return asynq.SkipRetry
	}
	var payload ClusterSnapshotOperationPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode cluster snapshot operation: %w: %w", err, asynq.SkipRetry)
	}
	if err := payload.validate(); err != nil {
		return fmt.Errorf("validate cluster snapshot operation: %w: %w", err, asynq.SkipRetry)
	}

	switch payload.Operation {
	case ClusterSnapshotOperationCreate:
		snapshotID, _ := uuid.Parse(payload.SnapshotID)
		row, err := deps.Queries.GetClusterSnapshotByID(ctx, snapshotID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load snapshot intent: %w", err)
		}
		if err := deps.Driver.CreateSnapshot(ctx, row); err != nil {
			return fmt.Errorf("create Velero snapshot: %w", err)
		}
		return nil

	case ClusterSnapshotOperationRestore:
		restoreID, _ := uuid.Parse(payload.RestoreID)
		restore, err := deps.Queries.GetClusterRestoreByID(ctx, restoreID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load restore intent: %w", err)
		}
		snapshot, err := deps.Queries.GetClusterSnapshotByID(ctx, restore.SnapshotID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load restore source snapshot: %w", err)
		}
		if err := deps.Driver.CreateRestore(ctx, restore, snapshot); err != nil {
			return fmt.Errorf("create Velero restore: %w", err)
		}
		return nil

	case ClusterSnapshotOperationDelete:
		if err := deps.Driver.DeleteSnapshot(ctx, payload.ClusterID, payload.VeleroNamespace, payload.VeleroName, payload.SnapshotID); err != nil {
			return fmt.Errorf("delete Velero snapshot: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported cluster snapshot operation %q: %w", payload.Operation, asynq.SkipRetry)
	}
}

// ----------------------------------------------------------------------
// cluster_snapshot:poll
// ----------------------------------------------------------------------

// HandleClusterSnapshotPoll is the asynq handler for the periodic poll
// task. Walks every cluster_snapshots and cluster_restores row in a
// non-terminal phase, fetches the corresponding Velero CR, and mirrors
// the status fields. Errors against an individual row are recorded as
// last_poll_error on the row — they never fail the whole task (asynq
// would otherwise retry the whole batch).
func (runtime ClusterSnapshotRuntime) HandleClusterSnapshotPoll(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterSnapshotPollType, func() error {
		deps := runtime.normalized().Deps
		if deps.Queries == nil || deps.Driver == nil {
			return fmt.Errorf("cluster snapshot poll runtime is not configured")
		}
		return errors.Join(pollSnapshots(ctx, deps), pollRestores(ctx, deps))
	})
}

func pollSnapshots(ctx context.Context, deps ClusterSnapshotDeps) error {
	rows, err := deps.Queries.ListPendingClusterSnapshots(ctx, snapshotPollBatchSize)
	if err != nil {
		logSnapshotErr(deps.Log, "list pending snapshots", err)
		return fmt.Errorf("list pending snapshots: %w", err)
	}
	// Refresh the per-cluster in-flight gauge from the pending set observed
	// at fetch time (before any of these rows is mirrored to a terminal
	// phase below). ListPendingClusterSnapshots only returns non-terminal
	// rows, so a straight per-cluster count is the in-flight depth. Clusters
	// that fell to zero since the last tick are reset explicitly.
	inFlight := map[string]int{}
	for _, row := range rows {
		inFlight[row.ClusterID.String()]++
	}
	updateInFlightSnapshotGauges(inFlight)
	var batchErr error
	for _, row := range rows {
		if err := pollOneSnapshot(ctx, deps, row); err != nil {
			logSnapshotErr(deps.Log, "poll snapshot "+row.ID.String(), err)
			batchErr = errors.Join(batchErr, fmt.Errorf("poll snapshot %s: %w", row.ID, err))
		}
	}
	return batchErr
}

// inFlightGaugeState remembers which cluster_ids carried a non-zero
// in-flight gauge on the previous tick so a cluster that drains to zero can
// be reset to 0 (otherwise its gauge would be pinned at its last non-zero
// value forever, since a zero-count cluster no longer appears in the
// pending list).
var (
	inFlightGaugeMu   sync.Mutex
	inFlightGaugeSeen = map[string]struct{}{}
)

// updateInFlightSnapshotGauges sets the gauge for every cluster with pending
// snapshots this tick and zeroes any cluster that had a non-zero gauge last
// tick but no longer appears.
func updateInFlightSnapshotGauges(counts map[string]int) {
	inFlightGaugeMu.Lock()
	defer inFlightGaugeMu.Unlock()
	for cid, n := range counts {
		setInFlightSnapshotGauge(cid, float64(n))
	}
	for cid := range inFlightGaugeSeen {
		if _, ok := counts[cid]; !ok {
			setInFlightSnapshotGauge(cid, 0)
		}
	}
	next := make(map[string]struct{}, len(counts))
	for cid := range counts {
		next[cid] = struct{}{}
	}
	inFlightGaugeSeen = next
}

func pollOneSnapshot(ctx context.Context, deps ClusterSnapshotDeps, row sqlc.ClusterSnapshot) error {
	status, err := deps.Driver.GetBackup(ctx, row.ClusterID.String(), row.VeleroNamespace, row.VeleroName)
	if err != nil {
		// Record the error on the row so the operator can see it via
		// list/get. Never short-circuit — the next tick retries.
		return deps.Queries.MarkSnapshotPhase(ctx, sqlc.MarkSnapshotPhaseParams{
			ID:             row.ID,
			Phase:          row.Phase,
			StartTime:      row.StartTime,
			CompletionTime: row.CompletionTime,
			WarningsCount:  row.WarningsCount,
			ErrorsCount:    row.ErrorsCount,
			LastPollError:  err.Error(),
		})
	}
	if status.NotFound {
		if row.Phase == "New" {
			if applyErr := deps.Driver.CreateSnapshot(ctx, row); applyErr != nil {
				persistErr := deps.Queries.MarkSnapshotPhase(ctx, sqlc.MarkSnapshotPhaseParams{
					ID: row.ID, Phase: row.Phase, StartTime: row.StartTime,
					CompletionTime: row.CompletionTime, WarningsCount: row.WarningsCount,
					ErrorsCount: row.ErrorsCount, LastPollError: "snapshot submission is pending retry",
				})
				return errors.Join(fmt.Errorf("repair missing Velero snapshot: %w", applyErr), persistErr)
			}
			return nil
		}
		// Velero removed the CR (TTL sweep, operator kubectl delete).
		// Move the row to the terminal "Deleted" phase so the cleanup
		// worker can drop it. Stop polling.
		return deps.Queries.MarkSnapshotPhase(ctx, sqlc.MarkSnapshotPhaseParams{
			ID:             row.ID,
			Phase:          "Deleted",
			StartTime:      row.StartTime,
			CompletionTime: row.CompletionTime,
			WarningsCount:  row.WarningsCount,
			ErrorsCount:    row.ErrorsCount,
			LastPollError:  "",
		})
	}

	nextPhase := status.Phase
	if nextPhase == "" {
		nextPhase = "New"
	}
	var st pgtype.Timestamptz
	if !status.StartTime.IsZero() {
		st = pgtype.Timestamptz{Time: status.StartTime, Valid: true}
	} else {
		st = row.StartTime
	}
	var ct pgtype.Timestamptz
	if !status.CompletionTime.IsZero() {
		ct = pgtype.Timestamptz{Time: status.CompletionTime, Valid: true}
	} else {
		ct = row.CompletionTime
	}

	if err := deps.Queries.MarkSnapshotPhase(ctx, sqlc.MarkSnapshotPhaseParams{
		ID:             row.ID,
		Phase:          nextPhase,
		StartTime:      st,
		CompletionTime: ct,
		WarningsCount:  int32(status.Warnings),
		ErrorsCount:    int32(status.Errors),
		LastPollError:  status.ValidationError,
	}); err != nil {
		return fmt.Errorf("mark snapshot phase: %w", err)
	}

	if _, terminal := terminalSnapshotPhases[nextPhase]; terminal && row.Phase != nextPhase {
		// Terminal-state transition. Surface the outcome to metrics.
		// Tests + the handler package own the metric registration —
		// the worker calls into a thin shim so we don't pull in the
		// prometheus client direct here.
		recordSnapshotOutcome(row.ClusterID.String(), outcomeForPhase(nextPhase))
	}
	return nil
}

func pollRestores(ctx context.Context, deps ClusterSnapshotDeps) error {
	rows, err := deps.Queries.ListPendingClusterRestores(ctx, snapshotPollBatchSize)
	if err != nil {
		logSnapshotErr(deps.Log, "list pending restores", err)
		return fmt.Errorf("list pending restores: %w", err)
	}
	var batchErr error
	for _, row := range rows {
		if err := pollOneRestore(ctx, deps, row); err != nil {
			logSnapshotErr(deps.Log, "poll restore "+row.ID.String(), err)
			batchErr = errors.Join(batchErr, fmt.Errorf("poll restore %s: %w", row.ID, err))
		}
	}
	return batchErr
}

func pollOneRestore(ctx context.Context, deps ClusterSnapshotDeps, row sqlc.ClusterRestore) error {
	status, err := deps.Driver.GetRestore(ctx, row.TargetClusterID.String(), row.VeleroNamespace, row.VeleroName)
	if err != nil {
		return deps.Queries.MarkRestorePhase(ctx, sqlc.MarkRestorePhaseParams{
			ID:             row.ID,
			Phase:          row.Phase,
			StartTime:      row.StartTime,
			CompletionTime: row.CompletionTime,
			WarningsCount:  row.WarningsCount,
			ErrorsCount:    row.ErrorsCount,
			LastPollError:  err.Error(),
		})
	}
	if status.NotFound {
		if row.Phase == "New" {
			snapshot, loadErr := deps.Queries.GetClusterSnapshotByID(ctx, row.SnapshotID)
			if loadErr != nil {
				return fmt.Errorf("load restore source for repair: %w", loadErr)
			}
			if applyErr := deps.Driver.CreateRestore(ctx, row, snapshot); applyErr != nil {
				persistErr := deps.Queries.MarkRestorePhase(ctx, sqlc.MarkRestorePhaseParams{
					ID: row.ID, Phase: row.Phase, StartTime: row.StartTime,
					CompletionTime: row.CompletionTime, WarningsCount: row.WarningsCount,
					ErrorsCount: row.ErrorsCount, LastPollError: "restore submission is pending retry",
				})
				return errors.Join(fmt.Errorf("repair missing Velero restore: %w", applyErr), persistErr)
			}
			return nil
		}
		return deps.Queries.MarkRestorePhase(ctx, sqlc.MarkRestorePhaseParams{
			ID:             row.ID,
			Phase:          "Deleted",
			StartTime:      row.StartTime,
			CompletionTime: row.CompletionTime,
			WarningsCount:  row.WarningsCount,
			ErrorsCount:    row.ErrorsCount,
			LastPollError:  "",
		})
	}
	nextPhase := status.Phase
	if nextPhase == "" {
		nextPhase = "New"
	}
	st := row.StartTime
	if !status.StartTime.IsZero() {
		st = pgtype.Timestamptz{Time: status.StartTime, Valid: true}
	}
	ct := row.CompletionTime
	if !status.CompletionTime.IsZero() {
		ct = pgtype.Timestamptz{Time: status.CompletionTime, Valid: true}
	}
	return deps.Queries.MarkRestorePhase(ctx, sqlc.MarkRestorePhaseParams{
		ID:             row.ID,
		Phase:          nextPhase,
		StartTime:      st,
		CompletionTime: ct,
		WarningsCount:  int32(status.Warnings),
		ErrorsCount:    int32(status.Errors),
		LastPollError:  status.ValidationError,
	})
}

// ----------------------------------------------------------------------
// cluster_snapshot:dispatch_scheduled
// ----------------------------------------------------------------------

// HandleClusterSnapshotDispatchScheduled fires scheduled snapshots whose
// next-run-at (derived from the cron expression + last_run_at) has
// elapsed. We compute next-run-at in-process via robfig/cron rather
// than registering one asynq.Scheduler entry per row — that would
// require restarting the scheduler on every PUT and doesn't compose
// across replicas.
func (runtime ClusterSnapshotRuntime) HandleClusterSnapshotDispatchScheduled(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterSnapshotDispatchScheduledType, func() error {
		deps := runtime.normalized().Deps
		if deps.Queries == nil || deps.Driver == nil {
			return fmt.Errorf("scheduled cluster snapshot runtime is not configured")
		}
		schedules, err := deps.Queries.ListEnabledSnapshotSchedules(ctx)
		if err != nil {
			logSnapshotErr(deps.Log, "list enabled schedules", err)
			return fmt.Errorf("list enabled snapshot schedules: %w", err)
		}
		now := time.Now().UTC()
		var batchErr error
		for _, sched := range schedules {
			due, err := scheduleIsDue(sched, now)
			if err != nil {
				logSnapshotErr(deps.Log, "evaluate cron "+sched.ID.String(), err)
				batchErr = errors.Join(batchErr, fmt.Errorf("evaluate snapshot schedule %s: %w", sched.ID, err))
				continue
			}
			if !due {
				continue
			}
			if err := fireScheduledSnapshot(ctx, deps, sched); err != nil {
				logSnapshotErr(deps.Log, "fire scheduled "+sched.ID.String(), err)
				markErr := deps.Queries.MarkSnapshotScheduleRan(ctx, sqlc.MarkSnapshotScheduleRanParams{
					ID:            sched.ID,
					LastRunStatus: "error: " + err.Error(),
				})
				batchErr = errors.Join(batchErr, fmt.Errorf("fire snapshot schedule %s: %w", sched.ID, err), markErr)
				continue
			}
			if err := deps.Queries.MarkSnapshotScheduleRan(ctx, sqlc.MarkSnapshotScheduleRanParams{
				ID:            sched.ID,
				LastRunStatus: "fired",
			}); err != nil {
				batchErr = errors.Join(batchErr, fmt.Errorf("mark snapshot schedule %s fired: %w", sched.ID, err))
			}
		}
		return batchErr
	})
}

// scheduleIsDue evaluates whether sched's cron expression has elapsed
// since last_run_at (or since creation, for a never-fired schedule).
// Errors propagate so the dispatcher can record them on the row.
func scheduleIsDue(sched sqlc.ClusterSnapshotSchedule, now time.Time) (bool, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	expr, err := parser.Parse(strings.TrimSpace(sched.CronSchedule))
	if err != nil {
		return false, fmt.Errorf("parse cron: %w", err)
	}
	last := sched.CreatedAt
	if sched.LastRunAt.Valid {
		last = sched.LastRunAt.Time
	}
	next := expr.Next(last)
	return !next.IsZero() && !next.After(now), nil
}

func fireScheduledSnapshot(ctx context.Context, deps ClusterSnapshotDeps, sched sqlc.ClusterSnapshotSchedule) error {
	// Decode spec; reject schedules whose spec column has been
	// corrupted to non-JSON. Production code never writes a corrupt
	// spec, but this is the defense-in-depth.
	var spec map[string]any
	if len(sched.Spec) > 0 {
		if err := json.Unmarshal(sched.Spec, &spec); err != nil {
			return fmt.Errorf("decode spec: %w", err)
		}
	}
	if spec == nil {
		spec = map[string]any{}
	}

	// Synthesize a Velero Backup name. Stable prefix per schedule
	// (sched.Name + timestamp) so operators can correlate.
	stamp := time.Now().UTC().Format("20060102t150405")
	veleroName := scheduleSnapshotName(sched.Name, stamp)
	namespace := "velero"

	expiresAt := pgtype.Timestamptz{}
	if ttl, ok := spec["ttl"].(string); ok {
		if d, err := time.ParseDuration(ttl); err == nil && d > 0 {
			expiresAt = pgtype.Timestamptz{Time: time.Now().Add(d), Valid: true}
		}
	}

	row, err := deps.Queries.CreateClusterSnapshot(ctx, sqlc.CreateClusterSnapshotParams{
		ClusterID:       sched.ClusterID,
		VeleroName:      veleroName,
		VeleroNamespace: namespace,
		Source:          "scheduled",
		Spec:            sched.Spec,
		Phase:           "New",
		ExpiresAt:       expiresAt,
		CreatedBy:       sched.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("create snapshot row: %w", err)
	}

	if err := deps.Driver.CreateSnapshot(ctx, row); err != nil {
		// Leave desired state retryable. The poller repairs every New row
		// whose external CR is still missing, covering a tunnel outage or
		// crash between row creation and this call.
		_ = deps.Queries.MarkSnapshotPhase(ctx, sqlc.MarkSnapshotPhaseParams{
			ID:             row.ID,
			Phase:          "New",
			StartTime:      pgtype.Timestamptz{},
			CompletionTime: pgtype.Timestamptz{},
			WarningsCount:  0,
			ErrorsCount:    0,
			LastPollError:  "snapshot submission is pending retry",
		})
		return err
	}
	return nil
}

// scheduleSnapshotName composes a deterministic-ish Velero CR name from
// the schedule's user-facing name + a timestamp. Names are forced
// lowercase + alphanumeric-or-hyphen so they pass the RFC-1123 check
// Velero enforces.
func scheduleSnapshotName(scheduleName, stamp string) string {
	name := strings.ToLower(strings.TrimSpace(scheduleName)) + "-" + stamp
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	// Trim leading / trailing dashes, cap at 253.
	s := strings.Trim(string(out), "-")
	if len(s) > 253 {
		s = strings.TrimRight(s[:253], "-")
	}
	return s
}

// ----------------------------------------------------------------------
// cluster_snapshot:cleanup_expired
// ----------------------------------------------------------------------

// HandleClusterSnapshotCleanupExpired drops cluster_snapshots rows whose
// expires_at < now() AND whose phase is terminal. Velero handles the
// actual object-store cleanup via its own TTL; this task is purely DB
// hygiene so the snapshot list doesn't grow forever.
func (runtime ClusterSnapshotRuntime) HandleClusterSnapshotCleanupExpired(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterSnapshotCleanupExpiredType, func() error {
		deps := runtime.normalized().Deps
		if deps.Queries == nil {
			return fmt.Errorf("cluster snapshot cleanup runtime is not configured")
		}
		rows, err := deps.Queries.ListExpiredTerminalSnapshots(ctx, expiredCleanupBatchSize)
		if err != nil {
			logSnapshotErr(deps.Log, "list expired snapshots", err)
			return fmt.Errorf("list expired snapshots: %w", err)
		}
		var batchErr error
		for _, row := range rows {
			if err := deps.Queries.DeleteClusterSnapshot(ctx, row.ID); err != nil {
				logSnapshotErr(deps.Log, "delete expired snapshot "+row.ID.String(), err)
				batchErr = errors.Join(batchErr, fmt.Errorf("delete expired snapshot %s: %w", row.ID, err))
			}
		}
		return batchErr
	})
}

// ----------------------------------------------------------------------
// Task constructors (used by ad-hoc enqueue paths + the periodic schedule).
// ----------------------------------------------------------------------

// NewClusterSnapshotPollTask returns the poll task body.
func NewClusterSnapshotPollTask() (*asynq.Task, error) {
	return asynq.NewTask(ClusterSnapshotPollType, nil), nil
}

// NewClusterSnapshotDispatchScheduledTask returns the dispatcher task.
func NewClusterSnapshotDispatchScheduledTask() (*asynq.Task, error) {
	return asynq.NewTask(ClusterSnapshotDispatchScheduledType, nil), nil
}

// NewClusterSnapshotCleanupExpiredTask returns the cleanup task.
func NewClusterSnapshotCleanupExpiredTask() (*asynq.Task, error) {
	return asynq.NewTask(ClusterSnapshotCleanupExpiredType, nil), nil
}

// ----------------------------------------------------------------------
// Metric / log shim
// ----------------------------------------------------------------------

// recordSnapshotOutcome is the worker-side hook into the handler's
// cluster_snapshots_total Counter. We resolve it through a function
// variable so the handler package wires it once at startup (avoiding
// a tasks → handler import cycle).
var recordSnapshotOutcome = func(clusterID, outcome string) {
	// no-op until the handler wires SetSnapshotOutcomeRecorder.
}

// SetSnapshotOutcomeRecorder swaps the metric callback. Called once
// from the handler package at startup.
func SetSnapshotOutcomeRecorder(fn func(clusterID, outcome string)) {
	if fn == nil {
		return
	}
	recordSnapshotOutcome = fn
}

// setInFlightSnapshotGauge is the worker-side hook into the handler's
// cluster_snapshots_in_flight GaugeVec. Resolved through a function
// variable (like recordSnapshotOutcome) so the handler package wires it
// once at startup without a tasks → handler import cycle. No-op until wired.
var setInFlightSnapshotGauge = func(clusterID string, count float64) {}

// SetInFlightSnapshotGaugeSetter swaps the in-flight gauge callback.
// Called once from the handler package at startup.
func SetInFlightSnapshotGaugeSetter(fn func(clusterID string, count float64)) {
	if fn == nil {
		return
	}
	setInFlightSnapshotGauge = fn
}

func logSnapshotErr(log *slog.Logger, msg string, err error) {
	if err == nil {
		return
	}
	if log == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Don't spam the log on graceful shutdown.
		return
	}
	log.Warn("cluster snapshot worker", "phase", msg, "error", err.Error())
}
