// Per-cluster Velero snapshot lifecycle workers (migration 052).
//
// Three asynq task types live in this file because they share the same
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
// All three tasks coordinate through a single ClusterSnapshotDeps struct
// set once at startup via ConfigureClusterSnapshotTasks. Until that
// fires the handlers are no-ops — the periodic schedule entries still
// fire on the cron tick, they just return nil immediately. This matches
// the pattern used by ConfigureWebhook / ConfigureClusterRegistryApply.

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
)

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
	// PostBackup creates a Velero Backup CR (used by the scheduled
	// dispatcher when firing a cron-driven snapshot).
	PostBackup(ctx context.Context, clusterID string, body map[string]any) error
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

// ClusterSnapshotDeps is set once at server startup. All three workers
// in this file consult it; until it's set, the handlers no-op.
type ClusterSnapshotDeps struct {
	Queries ClusterSnapshotPollQuerier
	Driver  VeleroSnapshotDriver
	Log     *slog.Logger
}

var (
	clusterSnapshotDepsMu sync.RWMutex
	clusterSnapshotDeps   ClusterSnapshotDeps
)

// ConfigureClusterSnapshotTasks wires the three workers. Safe to call
// multiple times — last writer wins (production wires it exactly once;
// tests may swap fakes between cases).
func ConfigureClusterSnapshotTasks(deps ClusterSnapshotDeps) {
	clusterSnapshotDepsMu.Lock()
	defer clusterSnapshotDepsMu.Unlock()
	clusterSnapshotDeps = deps
}

func getClusterSnapshotDeps() ClusterSnapshotDeps {
	clusterSnapshotDepsMu.RLock()
	defer clusterSnapshotDepsMu.RUnlock()
	return clusterSnapshotDeps
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
// cluster_snapshot:poll
// ----------------------------------------------------------------------

// HandleClusterSnapshotPoll is the asynq handler for the periodic poll
// task. Walks every cluster_snapshots and cluster_restores row in a
// non-terminal phase, fetches the corresponding Velero CR, and mirrors
// the status fields. Errors against an individual row are recorded as
// last_poll_error on the row — they never fail the whole task (asynq
// would otherwise retry the whole batch).
func HandleClusterSnapshotPoll(ctx context.Context, _ *asynq.Task) error {
	deps := getClusterSnapshotDeps()
	if deps.Queries == nil || deps.Driver == nil {
		// Pre-wiring no-op (test fakes, startup race).
		return nil
	}
	pollSnapshots(ctx, deps)
	pollRestores(ctx, deps)
	return nil
}

func pollSnapshots(ctx context.Context, deps ClusterSnapshotDeps) {
	rows, err := deps.Queries.ListPendingClusterSnapshots(ctx, snapshotPollBatchSize)
	if err != nil {
		logSnapshotErr(deps.Log, "list pending snapshots", err)
		return
	}
	for _, row := range rows {
		if err := pollOneSnapshot(ctx, deps, row); err != nil {
			logSnapshotErr(deps.Log, "poll snapshot "+row.ID.String(), err)
		}
	}
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

func pollRestores(ctx context.Context, deps ClusterSnapshotDeps) {
	rows, err := deps.Queries.ListPendingClusterRestores(ctx, snapshotPollBatchSize)
	if err != nil {
		logSnapshotErr(deps.Log, "list pending restores", err)
		return
	}
	for _, row := range rows {
		if err := pollOneRestore(ctx, deps, row); err != nil {
			logSnapshotErr(deps.Log, "poll restore "+row.ID.String(), err)
		}
	}
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
func HandleClusterSnapshotDispatchScheduled(ctx context.Context, _ *asynq.Task) error {
	deps := getClusterSnapshotDeps()
	if deps.Queries == nil || deps.Driver == nil {
		return nil
	}
	schedules, err := deps.Queries.ListEnabledSnapshotSchedules(ctx)
	if err != nil {
		logSnapshotErr(deps.Log, "list enabled schedules", err)
		return nil
	}
	now := time.Now().UTC()
	for _, sched := range schedules {
		due, err := scheduleIsDue(sched, now)
		if err != nil {
			logSnapshotErr(deps.Log, "evaluate cron "+sched.ID.String(), err)
			continue
		}
		if !due {
			continue
		}
		if err := fireScheduledSnapshot(ctx, deps, sched); err != nil {
			logSnapshotErr(deps.Log, "fire scheduled "+sched.ID.String(), err)
			_ = deps.Queries.MarkSnapshotScheduleRan(ctx, sqlc.MarkSnapshotScheduleRanParams{
				ID:            sched.ID,
				LastRunStatus: "error: " + err.Error(),
			})
			continue
		}
		_ = deps.Queries.MarkSnapshotScheduleRan(ctx, sqlc.MarkSnapshotScheduleRanParams{
			ID:            sched.ID,
			LastRunStatus: "fired",
		})
	}
	return nil
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

	// POST the Velero Backup CRD via the driver. The body is built
	// inline rather than reusing the handler's renderer because the
	// worker package can't import the handler package (cycle).
	body := scheduleSnapshotCRBody(row.ID, veleroName, namespace, spec)
	if err := deps.Driver.PostBackup(ctx, sched.ClusterID.String(), body); err != nil {
		// Roll back-ish: mark the row's last_poll_error so the
		// operator sees the failure. Don't delete — leaving the row
		// gives auditable history.
		_ = deps.Queries.MarkSnapshotPhase(ctx, sqlc.MarkSnapshotPhaseParams{
			ID:             row.ID,
			Phase:          "FailedValidation",
			StartTime:      pgtype.Timestamptz{},
			CompletionTime: pgtype.Timestamptz{},
			WarningsCount:  0,
			ErrorsCount:    1,
			LastPollError:  err.Error(),
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

// scheduleSnapshotCRBody constructs an unstructured Velero Backup CRD
// from the schedule's stored spec. Mirrors what the handler's
// renderPerClusterBackup produces but lives in the worker package so
// the dispatcher doesn't take a handler dep.
func scheduleSnapshotCRBody(snapshotID uuid.UUID, name, namespace string, spec map[string]any) map[string]any {
	specOut := map[string]any{}
	// Copy through the supported fields verbatim; Velero validates the
	// rest. We deliberately don't filter — schedules created via the
	// handler are pre-validated, and a schedule with extra fields
	// should still propagate (operators might pre-stage upcoming
	// Velero features).
	for _, k := range []string{
		"includedNamespaces", "excludedNamespaces",
		"includedResources", "excludedResources",
		"snapshotVolumes", "ttl", "storageLocation",
		"volumeSnapshotLocations",
	} {
		if v, ok := spec[k]; ok {
			specOut[k] = v
		}
	}
	if sel, ok := spec["labelSelector"].(string); ok && sel != "" {
		// Reuse handler-side parser when possible. Worker can't import
		// handler, so we inline a minimal version: "k=v,k=v" → matchLabels.
		labels := parseScheduleLabelSelector(sel)
		if len(labels) > 0 {
			specOut["labelSelector"] = map[string]any{"matchLabels": labels}
		}
	}
	return map[string]any{
		"apiVersion": "velero.io/v1",
		"kind":       "Backup",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/snapshot-id":    snapshotID.String(),
				"astronomer.io/source":         "scheduled",
			},
		},
		"spec": specOut,
	}
}

func parseScheduleLabelSelector(s string) map[string]string {
	out := map[string]string{}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		idx := strings.Index(tok, "=")
		if idx <= 0 || idx == len(tok)-1 {
			continue
		}
		k := strings.TrimSpace(tok[:idx])
		v := strings.TrimSpace(tok[idx+1:])
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// ----------------------------------------------------------------------
// cluster_snapshot:cleanup_expired
// ----------------------------------------------------------------------

// HandleClusterSnapshotCleanupExpired drops cluster_snapshots rows whose
// expires_at < now() AND whose phase is terminal. Velero handles the
// actual object-store cleanup via its own TTL; this task is purely DB
// hygiene so the snapshot list doesn't grow forever.
func HandleClusterSnapshotCleanupExpired(ctx context.Context, _ *asynq.Task) error {
	deps := getClusterSnapshotDeps()
	if deps.Queries == nil {
		return nil
	}
	rows, err := deps.Queries.ListExpiredTerminalSnapshots(ctx, expiredCleanupBatchSize)
	if err != nil {
		logSnapshotErr(deps.Log, "list expired snapshots", err)
		return nil
	}
	for _, row := range rows {
		if err := deps.Queries.DeleteClusterSnapshot(ctx, row.ID); err != nil {
			logSnapshotErr(deps.Log, "delete expired snapshot "+row.ID.String(), err)
		}
	}
	return nil
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
