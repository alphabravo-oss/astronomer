// Per-cluster Velero snapshot + restore self-service handler (migration 052).
//
// Routes owned by this handler (all under /api/v1):
//
//   GET    /clusters/{cluster_id}/snapshots/                 — list
//   POST   /clusters/{cluster_id}/snapshots/                 — create ad-hoc
//   GET    /clusters/{cluster_id}/snapshots/{id}/            — get
//   DELETE /clusters/{cluster_id}/snapshots/{id}/            — delete (creates DeleteBackupRequest)
//   POST   /clusters/{cluster_id}/snapshots/{id}/restore/    — create Restore
//   GET    /clusters/{cluster_id}/snapshot-schedules/        — list schedules
//   POST   /clusters/{cluster_id}/snapshot-schedules/        — create schedule
//   GET    /clusters/{cluster_id}/snapshot-schedules/{id}/   — get
//   PUT    /clusters/{cluster_id}/snapshot-schedules/{id}/   — update
//   DELETE /clusters/{cluster_id}/snapshot-schedules/{id}/   — delete
//   GET    /clusters/{cluster_id}/velero-status/             — pre-flight
//
// RBAC: list/get gated on clusters:read; mutating endpoints on
// clusters:update. We deliberately don't introduce a separate
// "snapshots" RBAC resource — operators who can update a cluster can
// also snapshot it (and a separate role didn't earn its weight when
// the parent gate is already cluster-scoped).
//
// Async model:
//   - Production create/restore/delete handlers commit desired state,
//     a tunnel-owned task-outbox intent, and mandatory audit together.
//     They never mutate the member cluster before the transaction commits.
//   - The operation worker creates the immutable Velero CR and tolerates
//     AlreadyExists, so replay after an uncertain result is safe. The poller
//     (cluster_snapshot:poll, every 30s) mirrors terminal status afterward.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// ClusterSnapshotQuerier is the narrow DB surface ClusterSnapshotsHandler
// uses. Defined locally so unit tests can stand up a fake without
// pulling in the full *sqlc.Queries.
type ClusterSnapshotQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)

	ListClusterSnapshots(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterSnapshot, error)
	GetClusterSnapshotByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterSnapshot, error)
	CreateClusterSnapshot(ctx context.Context, arg sqlc.CreateClusterSnapshotParams) (sqlc.ClusterSnapshot, error)
	DeleteClusterSnapshot(ctx context.Context, id uuid.UUID) error

	ListClusterRestores(ctx context.Context, targetClusterID uuid.UUID) ([]sqlc.ClusterRestore, error)
	GetClusterRestoreByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterRestore, error)
	CreateClusterRestore(ctx context.Context, arg sqlc.CreateClusterRestoreParams) (sqlc.ClusterRestore, error)

	ListClusterSnapshotSchedules(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterSnapshotSchedule, error)
	GetClusterSnapshotScheduleByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterSnapshotSchedule, error)
	CreateClusterSnapshotSchedule(ctx context.Context, arg sqlc.CreateClusterSnapshotScheduleParams) (sqlc.ClusterSnapshotSchedule, error)
	UpdateClusterSnapshotSchedule(ctx context.Context, arg sqlc.UpdateClusterSnapshotScheduleParams) (sqlc.ClusterSnapshotSchedule, error)
	DeleteClusterSnapshotSchedule(ctx context.Context, id uuid.UUID) error
}

type ClusterSnapshotMutationTx interface {
	ClusterSnapshotQuerier
	resourceOperationIdempotencyQuerier
	GetClusterSnapshotForUpdate(context.Context, uuid.UUID) (sqlc.ClusterSnapshot, error)
	GetClusterSnapshotScheduleForUpdate(context.Context, uuid.UUID) (sqlc.ClusterSnapshotSchedule, error)
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type clusterSnapshotRunTxFunc func(context.Context, func(ClusterSnapshotMutationTx) error) error

// ClusterSnapshotsHandler owns the /clusters/{cluster_id}/snapshots/*,
// /snapshot-schedules/*, /velero-status/ route groups.
type ClusterSnapshotsHandler struct {
	queries   ClusterSnapshotQuerier
	requester K8sRequester
	bus       *events.Bus
	runTx     clusterSnapshotRunTxFunc
}

func (h *ClusterSnapshotsHandler) SetRunTx(runTx clusterSnapshotRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ClusterSnapshotsHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetEventBus wires the SSE bus for snapshot.changed liveness events (P4.5).
// Optional: fire-and-forget and nil-safe.
func (h *ClusterSnapshotsHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishSnapshotChanged emits the metadata-only snapshot.changed event
// after a successful DB write. kind discriminates snapshot|restore|schedule.
func (h *ClusterSnapshotsHandler) publishSnapshotChanged(clusterID, id uuid.UUID, kind string) {
	if h == nil {
		return
	}
	events.PublishChanged(h.bus, "snapshot", clusterID.String(), id.String(), map[string]any{"kind": kind})
}

// NewClusterSnapshotsHandler wires the handler against the provided
// queries surface. The K8s requester is attached via SetRequester so
// the test wiring can stay minimal — handlers degrade to a 503
// (tunnel_unwired) when the requester is nil.
func NewClusterSnapshotsHandler(queries ClusterSnapshotQuerier) *ClusterSnapshotsHandler {
	return &ClusterSnapshotsHandler{queries: queries}
}

// SetRequester wires the tunnel-backed K8sRequester used to drive the
// Velero CRDs on the target member cluster.
func (h *ClusterSnapshotsHandler) SetRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

func enqueueClusterSnapshotOperation(ctx context.Context, q tasks.TaskOutboxWriter, payload tasks.ClusterSnapshotOperationPayload) error {
	task, err := tasks.NewClusterSnapshotOperationTask(payload)
	if err != nil {
		return err
	}
	enriched := observability.EnrichTaskPayload(ctx, task.Payload(), reqctx.CorrelationID(ctx))
	task = asynq.NewTask(task.Type(), enriched, asynq.MaxRetry(5))
	operationID := payload.SnapshotID
	if payload.Operation == tasks.ClusterSnapshotOperationRestore {
		operationID = payload.RestoreID
	}
	_, err = tasks.EnqueueTaskOutbox(ctx, q, task, tasks.TaskOutboxOptions{
		DedupeKey:           fmt.Sprintf("cluster_snapshot:%s:%s", payload.Operation, operationID),
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            5,
		Timeout:             2 * time.Minute,
		MaxDeliveryAttempts: 20,
	})
	return err
}

// ----------------------------------------------------------------------
// DTOs
// ----------------------------------------------------------------------

// SnapshotSpec is the JSON body the operator supplies on Create. It
// mirrors the subset of Velero's BackupSpec our UI surfaces; the
// renderer projects this into the unstructured CR body.
type SnapshotSpec struct {
	IncludedNamespaces      []string `json:"includedNamespaces,omitempty"`
	ExcludedNamespaces      []string `json:"excludedNamespaces,omitempty"`
	IncludedResources       []string `json:"includedResources,omitempty"`
	ExcludedResources       []string `json:"excludedResources,omitempty"`
	LabelSelector           string   `json:"labelSelector,omitempty"`
	SnapshotVolumes         *bool    `json:"snapshotVolumes,omitempty"`
	TTL                     string   `json:"ttl,omitempty"`
	StorageLocation         string   `json:"storageLocation,omitempty"`
	VolumeSnapshotLocations []string `json:"volumeSnapshotLocations,omitempty"`
}

// RestoreSpec captures the operator's restore request body. Restore
// is a subset of Backup spec plus the namespace remap and the
// target_cluster_id (which lives at the top of the body, not inside
// SnapshotSpec, because cross-cluster restore is a meaningful concept).
type RestoreSpec struct {
	IncludedNamespaces []string          `json:"includedNamespaces,omitempty"`
	ExcludedNamespaces []string          `json:"excludedNamespaces,omitempty"`
	NamespaceMapping   map[string]string `json:"namespaceMapping,omitempty"`
	LabelSelector      string            `json:"labelSelector,omitempty"`
	RestorePVs         *bool             `json:"restorePVs,omitempty"`
}

// SnapshotResponse is the wire-format DTO returned by every snapshot
// endpoint.
type SnapshotResponse struct {
	ID              uuid.UUID    `json:"id"`
	ClusterID       uuid.UUID    `json:"cluster_id"`
	VeleroName      string       `json:"velero_name"`
	VeleroNamespace string       `json:"velero_namespace"`
	Source          string       `json:"source"`
	Spec            SnapshotSpec `json:"spec"`
	Phase           string       `json:"phase"`
	StartTime       *time.Time   `json:"start_time,omitempty"`
	CompletionTime  *time.Time   `json:"completion_time,omitempty"`
	ExpiresAt       *time.Time   `json:"expires_at,omitempty"`
	WarningsCount   int32        `json:"warnings_count"`
	ErrorsCount     int32        `json:"errors_count"`
	LastPollAt      *time.Time   `json:"last_poll_at,omitempty"`
	LastPollError   string       `json:"last_poll_error,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// RestoreResponse is the wire-format DTO for restore operations.
type RestoreResponse struct {
	ID              uuid.UUID   `json:"id"`
	SnapshotID      uuid.UUID   `json:"snapshot_id"`
	TargetClusterID uuid.UUID   `json:"target_cluster_id"`
	VeleroName      string      `json:"velero_name"`
	VeleroNamespace string      `json:"velero_namespace"`
	Spec            RestoreSpec `json:"spec"`
	Phase           string      `json:"phase"`
	StartTime       *time.Time  `json:"start_time,omitempty"`
	CompletionTime  *time.Time  `json:"completion_time,omitempty"`
	WarningsCount   int32       `json:"warnings_count"`
	ErrorsCount     int32       `json:"errors_count"`
	LastPollAt      *time.Time  `json:"last_poll_at,omitempty"`
	LastPollError   string      `json:"last_poll_error,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

// ScheduleRequest is the create/update body for cron-driven snapshots.
// openapi:request SnapshotScheduleRequest
// openapi:request SnapshotScheduleUpdateRequest
type ScheduleRequest struct {
	Name         string       `json:"name"`
	CronSchedule string       `json:"cron_schedule"`
	Spec         SnapshotSpec `json:"spec"`
	Enabled      *bool        `json:"enabled,omitempty"`
}

func snapshotScheduleUpdateParams(scheduleID uuid.UUID, req ScheduleRequest, existing sqlc.ClusterSnapshotSchedule) (sqlc.UpdateClusterSnapshotScheduleParams, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = existing.Name
	}
	if !validVeleroResourceName(strings.ToLower(name)) {
		return sqlc.UpdateClusterSnapshotScheduleParams{}, errors.New("name must be a valid DNS subdomain")
	}
	cronExpr := strings.TrimSpace(req.CronSchedule)
	if cronExpr == "" {
		cronExpr = existing.CronSchedule
	}
	if _, err := parseCronExpression(cronExpr); err != nil {
		return sqlc.UpdateClusterSnapshotScheduleParams{}, fmt.Errorf("invalid cron_schedule: %w", err)
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return sqlc.UpdateClusterSnapshotScheduleParams{
		ID: scheduleID, Name: name, CronSchedule: cronExpr,
		Spec: encodeSpec(req.Spec), Enabled: enabled,
	}, nil
}

// ScheduleResponse is the wire DTO for a snapshot schedule.
type ScheduleResponse struct {
	ID            uuid.UUID    `json:"id"`
	ClusterID     uuid.UUID    `json:"cluster_id"`
	Name          string       `json:"name"`
	CronSchedule  string       `json:"cron_schedule"`
	Spec          SnapshotSpec `json:"spec"`
	Enabled       bool         `json:"enabled"`
	LastRunAt     *time.Time   `json:"last_run_at,omitempty"`
	LastRunStatus string       `json:"last_run_status,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// VeleroStatusResponse is the wire DTO for the /velero-status/ pre-flight.
type VeleroStatusResponse struct {
	Installed        bool               `json:"installed"`
	Namespace        string             `json:"namespace"`
	StorageReady     bool               `json:"storage_ready"`
	StorageLocations []VeleroBSLSummary `json:"storage_locations"`
	Reason           string             `json:"reason,omitempty"`
}

// VeleroBSLSummary is the per-BackupStorageLocation row in the
// velero-status response.
type VeleroBSLSummary struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Default  bool   `json:"default"`
	Phase    string `json:"phase"`
	Bucket   string `json:"bucket"`
}

// ----------------------------------------------------------------------
// Spec encoding helpers
// ----------------------------------------------------------------------

// encodeSpec marshals a SnapshotSpec back to JSONB for the DB column.
// Always returns a non-null body — the spec column defaults to '{}'
// and we want stored rows to stay that shape even when every field is
// omitted.
func encodeSpec(spec SnapshotSpec) json.RawMessage {
	raw, err := json.Marshal(spec)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func decodeSpec(raw json.RawMessage) SnapshotSpec {
	var out SnapshotSpec
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func encodeRestoreSpec(spec RestoreSpec) json.RawMessage {
	raw, err := json.Marshal(spec)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func decodeRestoreSpec(raw json.RawMessage) RestoreSpec {
	var out RestoreSpec
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func snapshotToResponse(row sqlc.ClusterSnapshot) SnapshotResponse {
	out := SnapshotResponse{
		ID:              row.ID,
		ClusterID:       row.ClusterID,
		VeleroName:      row.VeleroName,
		VeleroNamespace: row.VeleroNamespace,
		Source:          row.Source,
		Spec:            decodeSpec(row.Spec),
		Phase:           row.Phase,
		WarningsCount:   row.WarningsCount,
		ErrorsCount:     row.ErrorsCount,
		LastPollError:   row.LastPollError,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
	if row.StartTime.Valid {
		t := row.StartTime.Time
		out.StartTime = &t
	}
	if row.CompletionTime.Valid {
		t := row.CompletionTime.Time
		out.CompletionTime = &t
	}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		out.ExpiresAt = &t
	}
	if row.LastPollAt.Valid {
		t := row.LastPollAt.Time
		out.LastPollAt = &t
	}
	return out
}

func restoreToResponse(row sqlc.ClusterRestore) RestoreResponse {
	out := RestoreResponse{
		ID:              row.ID,
		SnapshotID:      row.SnapshotID,
		TargetClusterID: row.TargetClusterID,
		VeleroName:      row.VeleroName,
		VeleroNamespace: row.VeleroNamespace,
		Spec:            decodeRestoreSpec(row.Spec),
		Phase:           row.Phase,
		WarningsCount:   row.WarningsCount,
		ErrorsCount:     row.ErrorsCount,
		LastPollError:   row.LastPollError,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
	if row.StartTime.Valid {
		t := row.StartTime.Time
		out.StartTime = &t
	}
	if row.CompletionTime.Valid {
		t := row.CompletionTime.Time
		out.CompletionTime = &t
	}
	if row.LastPollAt.Valid {
		t := row.LastPollAt.Time
		out.LastPollAt = &t
	}
	return out
}

func scheduleToResponse(row sqlc.ClusterSnapshotSchedule) ScheduleResponse {
	out := ScheduleResponse{
		ID:            row.ID,
		ClusterID:     row.ClusterID,
		Name:          row.Name,
		CronSchedule:  row.CronSchedule,
		Spec:          decodeSpec(row.Spec),
		Enabled:       row.Enabled,
		LastRunStatus: row.LastRunStatus,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if row.LastRunAt.Valid {
		t := row.LastRunAt.Time
		out.LastRunAt = &t
	}
	return out
}

// ----------------------------------------------------------------------
// URL param helpers
// ----------------------------------------------------------------------

func parseClusterAndSnapshotIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	snapshotID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid snapshot ID")
		return uuid.Nil, uuid.Nil, false
	}
	return clusterID, snapshotID, true
}

func parseClusterAndScheduleIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	scheduleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid schedule ID")
		return uuid.Nil, uuid.Nil, false
	}
	return clusterID, scheduleID, true
}

// ----------------------------------------------------------------------
// /snapshots/ — list / get / create / delete / restore
// ----------------------------------------------------------------------

// ListSnapshots handles GET /clusters/{cluster_id}/snapshots/.
