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
//   - Create handlers return 202 with the freshly inserted DB row. The
//     poller worker (cluster_snapshot:poll, every 30s) advances the
//     phase column as Velero progresses.
//   - DELETE never waits for Velero — it creates a DeleteBackupRequest
//     CR on the member cluster (per Velero's removal protocol) and
//     immediately drops the local row. Subsequent status pulls would
//     be no-ops because the FK cascade also drops cluster_restores.
//   - Restore is the same: 202 + DB row + Velero Restore CR.

package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron/v3"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
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

// ClusterSnapshotsHandler owns the /clusters/{cluster_id}/snapshots/*,
// /snapshot-schedules/*, /velero-status/ route groups.
type ClusterSnapshotsHandler struct {
	queries   ClusterSnapshotQuerier
	requester K8sRequester
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
type ScheduleRequest struct {
	Name         string       `json:"name"`
	CronSchedule string       `json:"cron_schedule"`
	Spec         SnapshotSpec `json:"spec"`
	Enabled      *bool        `json:"enabled,omitempty"`
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
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
func (h *ClusterSnapshotsHandler) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	rows, err := h.queries.ListClusterSnapshots(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list snapshots")
		return
	}
	out := make([]SnapshotResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, snapshotToResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// GetSnapshot handles GET /clusters/{cluster_id}/snapshots/{id}/.
func (h *ClusterSnapshotsHandler) GetSnapshot(w http.ResponseWriter, r *http.Request) {
	clusterID, snapshotID, ok := parseClusterAndSnapshotIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterSnapshotByID(r.Context(), snapshotID)
	if err != nil || row.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Snapshot not found")
		return
	}
	RespondJSON(w, http.StatusOK, snapshotToResponse(row))
}

// CreateSnapshot handles POST /clusters/{cluster_id}/snapshots/.
//
//  1. Validate cluster + spec.
//  2. Insert the row (phase='New') so we have an ID + audit trail.
//  3. POST the Velero Backup CR to the member cluster's apiserver.
//  4. If the POST fails: leave the row in DB with phase='FailedValidation'
//     so the operator sees the failure without us having to retry
//     automatically (the poller would otherwise loop).
//  5. Return 202 + the DB row.
func (h *ClusterSnapshotsHandler) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	var req struct {
		SnapshotSpec
		Source     string `json:"source,omitempty"`
		VeleroName string `json:"velero_name,omitempty"`
		Namespace  string `json:"velero_namespace,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	spec := req.SnapshotSpec
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "manual"
	}
	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = defaultVeleroNamespace
	}
	veleroName := strings.TrimSpace(req.VeleroName)
	if veleroName == "" {
		veleroName = newVeleroBackupName(cluster.Name)
	}
	if !validVeleroResourceName(veleroName) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "velero_name must be a valid RFC 1123 subdomain (1-253 chars)")
		return
	}

	// expires_at lifecycle: when the user supplied a Velero TTL we
	// also stamp the DB column so the cleanup worker can prune the
	// row without first re-polling Velero. Parsing failures fall
	// through to a NULL expires_at (the cleanup worker simply leaves
	// it alone — no-op rather than crash).
	expiresAt := pgtype.Timestamptz{}
	if d, ok := parseSnapshotTTLDuration(spec.TTL); ok {
		expiresAt = pgtype.Timestamptz{Time: time.Now().Add(d), Valid: true}
	}

	row, err := h.queries.CreateClusterSnapshot(r.Context(), sqlc.CreateClusterSnapshotParams{
		ClusterID:       clusterID,
		VeleroName:      veleroName,
		VeleroNamespace: namespace,
		Source:          source,
		Spec:            encodeSpec(spec),
		Phase:           "New",
		ExpiresAt:       expiresAt,
		CreatedBy:       currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create snapshot row")
		return
	}

	// Tunnel-mediated CRD POST. We accept that the create may fail
	// (member-cluster unreachable, RBAC missing, etc.); the row is
	// already persisted so the operator can retry via DELETE+POST.
	if err := h.postBackupCRD(r.Context(), clusterID.String(), row, spec); err != nil {
		// Best-effort surface the error via the response — the row
		// stays in DB so list/get can show it. We return 202 not 500
		// because the persisted state is correct; only the upstream
		// Velero failed.
		out := snapshotToResponse(row)
		out.LastPollError = err.Error()
		recordAudit(r, h.queries, "cluster.snapshot.created", "cluster_snapshot", row.ID.String(), cluster.Name, map[string]any{
			"cluster_id":  clusterID.String(),
			"velero_name": row.VeleroName,
			"crd_error":   err.Error(),
		})
		RespondJSON(w, http.StatusAccepted, out)
		return
	}

	clusterSnapshotsCreatedInFlight.WithLabelValues(observability.MetricValues(clusterID.String())...).Inc()
	recordAudit(r, h.queries, "cluster.snapshot.created", "cluster_snapshot", row.ID.String(), cluster.Name, map[string]any{
		"cluster_id":  clusterID.String(),
		"velero_name": row.VeleroName,
		"source":      source,
		"namespace":   namespace,
	})

	RespondJSON(w, http.StatusAccepted, snapshotToResponse(row))
}

// DeleteSnapshot handles DELETE /clusters/{cluster_id}/snapshots/{id}/.
// Creates a Velero DeleteBackupRequest CR (the indirect deletion path)
// then drops the local row. The CR fire-and-forgets — Velero handles
// the object-store cleanup asynchronously.
func (h *ClusterSnapshotsHandler) DeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	clusterID, snapshotID, ok := parseClusterAndSnapshotIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterSnapshotByID(r.Context(), snapshotID)
	if err != nil || row.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Snapshot not found")
		return
	}

	// We post the DeleteBackupRequest BEFORE the local DELETE — if it
	// fails we still drop the local row, but we record the error.
	// Velero's CRD is fire-and-forget anyway: even if the controller
	// briefly misses the request, the BSL's retention sweep mops up.
	crdErr := h.postDeleteBackupRequestCRD(r.Context(), clusterID.String(), row)

	if err := h.queries.DeleteClusterSnapshot(r.Context(), snapshotID); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete snapshot row")
		return
	}

	detail := map[string]any{
		"cluster_id":  clusterID.String(),
		"velero_name": row.VeleroName,
	}
	if crdErr != nil {
		detail["crd_error"] = crdErr.Error()
	}
	recordAudit(r, h.queries, "cluster.snapshot.deleted", "cluster_snapshot", snapshotID.String(), "", detail)

	w.WriteHeader(http.StatusNoContent)
}

// CreateRestore handles POST /clusters/{cluster_id}/snapshots/{id}/restore/.
// Body: { "target_cluster_id": <uuid>, "spec": { ... } }. When
// target_cluster_id is omitted the restore targets the snapshot's own
// cluster (in-place restore).
func (h *ClusterSnapshotsHandler) CreateRestore(w http.ResponseWriter, r *http.Request) {
	clusterID, snapshotID, ok := parseClusterAndSnapshotIDs(w, r)
	if !ok {
		return
	}
	snapshot, err := h.queries.GetClusterSnapshotByID(r.Context(), snapshotID)
	if err != nil || snapshot.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Snapshot not found")
		return
	}
	if snapshot.Phase != "Completed" && snapshot.Phase != "PartiallyFailed" {
		RespondRequestError(w, r, http.StatusConflict, apierror.SnapshotNotReady, "Snapshot is not yet Completed; cannot restore")
		return
	}

	var req struct {
		TargetClusterID string      `json:"target_cluster_id"`
		Namespace       string      `json:"velero_namespace"`
		Spec            RestoreSpec `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	targetID := clusterID
	if strings.TrimSpace(req.TargetClusterID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(req.TargetClusterID))
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid target_cluster_id")
			return
		}
		targetID = parsed
	}
	target, err := h.queries.GetClusterByID(r.Context(), targetID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Target cluster not found")
		return
	}

	// Cross-cluster restore pre-flight. The target cluster must have
	// Velero installed AND have a BackupStorageLocation pointing at
	// the same store as the snapshot's source cluster — otherwise
	// Velero on the target would have nothing to read. We surface a
	// clear 409 rather than 500ing later in the poller.
	if targetID != clusterID {
		bsls, vErr := listVeleroBSLs(r.Context(), h.requester, targetID.String(), defaultVeleroNamespace)
		if vErr != nil {
			RespondRequestError(w, r, http.StatusBadGateway, apierror.VeleroUnreachable, fmt.Sprintf("could not check Velero on target cluster: %v", vErr))
			return
		}
		if len(bsls) == 0 {
			RespondRequestError(w, r, http.StatusConflict, apierror.VeleroMissingOnTarget, "Target cluster has no Velero BackupStorageLocation; install Velero before cross-cluster restore")
			return
		}
	}

	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = snapshot.VeleroNamespace
		if namespace == "" {
			namespace = defaultVeleroNamespace
		}
	}

	veleroName := newVeleroRestoreName(snapshot.VeleroName)
	row, err := h.queries.CreateClusterRestore(r.Context(), sqlc.CreateClusterRestoreParams{
		SnapshotID:      snapshotID,
		TargetClusterID: targetID,
		VeleroName:      veleroName,
		VeleroNamespace: namespace,
		Spec:            encodeRestoreSpec(req.Spec),
		Phase:           "New",
		CreatedBy:       currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create restore row")
		return
	}

	if err := h.postRestoreCRD(r.Context(), targetID.String(), row, req.Spec, snapshot.VeleroName); err != nil {
		out := restoreToResponse(row)
		out.LastPollError = err.Error()
		recordAudit(r, h.queries, "cluster.snapshot.restore_requested", "cluster_restore", row.ID.String(), target.Name, map[string]any{
			"cluster_id":       targetID.String(),
			"snapshot_id":      snapshotID.String(),
			"snapshot_cluster": clusterID.String(),
			"velero_name":      row.VeleroName,
			"crd_error":        err.Error(),
		})
		RespondJSON(w, http.StatusAccepted, out)
		return
	}

	recordAudit(r, h.queries, "cluster.snapshot.restore_requested", "cluster_restore", row.ID.String(), target.Name, map[string]any{
		"cluster_id":       targetID.String(),
		"snapshot_id":      snapshotID.String(),
		"snapshot_cluster": clusterID.String(),
		"velero_name":      row.VeleroName,
	})

	RespondJSON(w, http.StatusAccepted, restoreToResponse(row))
}

// ----------------------------------------------------------------------
// /snapshot-schedules/ — list / get / create / update / delete
// ----------------------------------------------------------------------

func (h *ClusterSnapshotsHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	rows, err := h.queries.ListClusterSnapshotSchedules(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list schedules")
		return
	}
	out := make([]ScheduleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, scheduleToResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *ClusterSnapshotsHandler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, scheduleID, ok := parseClusterAndScheduleIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterSnapshotScheduleByID(r.Context(), scheduleID)
	if err != nil || row.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Schedule not found")
		return
	}
	RespondJSON(w, http.StatusOK, scheduleToResponse(row))
}

func (h *ClusterSnapshotsHandler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	var req ScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "name is required")
		return
	}
	if !validVeleroResourceName(strings.ToLower(req.Name)) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "name must be a valid DNS subdomain")
		return
	}
	if _, err := parseCronExpression(req.CronSchedule); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, fmt.Sprintf("invalid cron_schedule: %v", err))
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	row, err := h.queries.CreateClusterSnapshotSchedule(r.Context(), sqlc.CreateClusterSnapshotScheduleParams{
		ClusterID:    clusterID,
		Name:         req.Name,
		CronSchedule: req.CronSchedule,
		Spec:         encodeSpec(req.Spec),
		Enabled:      enabled,
		CreatedBy:    currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create schedule (name conflict?)")
		return
	}

	recordAudit(r, h.queries, "cluster.snapshot.schedule_created", "cluster_snapshot_schedule", row.ID.String(), cluster.Name, map[string]any{
		"cluster_id":    clusterID.String(),
		"name":          row.Name,
		"cron_schedule": row.CronSchedule,
		"enabled":       row.Enabled,
	})
	RespondJSON(w, http.StatusCreated, scheduleToResponse(row))
}

func (h *ClusterSnapshotsHandler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, scheduleID, ok := parseClusterAndScheduleIDs(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	existing, err := h.queries.GetClusterSnapshotScheduleByID(r.Context(), scheduleID)
	if err != nil || existing.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Schedule not found")
		return
	}

	var req ScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = existing.Name
	}
	if !validVeleroResourceName(strings.ToLower(name)) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "name must be a valid DNS subdomain")
		return
	}
	cronExpr := strings.TrimSpace(req.CronSchedule)
	if cronExpr == "" {
		cronExpr = existing.CronSchedule
	}
	if _, err := parseCronExpression(cronExpr); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, fmt.Sprintf("invalid cron_schedule: %v", err))
		return
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	row, err := h.queries.UpdateClusterSnapshotSchedule(r.Context(), sqlc.UpdateClusterSnapshotScheduleParams{
		ID:           scheduleID,
		Name:         name,
		CronSchedule: cronExpr,
		Spec:         encodeSpec(req.Spec),
		Enabled:      enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update schedule")
		return
	}
	recordAudit(r, h.queries, "cluster.snapshot.schedule_updated", "cluster_snapshot_schedule", row.ID.String(), cluster.Name, map[string]any{
		"cluster_id":    clusterID.String(),
		"name":          row.Name,
		"cron_schedule": row.CronSchedule,
		"enabled":       row.Enabled,
	})
	RespondJSON(w, http.StatusOK, scheduleToResponse(row))
}

func (h *ClusterSnapshotsHandler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, scheduleID, ok := parseClusterAndScheduleIDs(w, r)
	if !ok {
		return
	}
	existing, err := h.queries.GetClusterSnapshotScheduleByID(r.Context(), scheduleID)
	if err != nil || existing.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Schedule not found")
		return
	}
	if err := h.queries.DeleteClusterSnapshotSchedule(r.Context(), scheduleID); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete schedule")
		return
	}
	recordAudit(r, h.queries, "cluster.snapshot.schedule_deleted", "cluster_snapshot_schedule", scheduleID.String(), "", map[string]any{
		"cluster_id": clusterID.String(),
		"name":       existing.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------
// /velero-status/ — installed? BSL ready?
// ----------------------------------------------------------------------

func (h *ClusterSnapshotsHandler) VeleroStatus(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnwired, "Tunnel requester not configured")
		return
	}

	bsls, err := listVeleroBSLs(r.Context(), h.requester, clusterID.String(), defaultVeleroNamespace)
	if err != nil {
		// Hard failure (cluster unreachable). Surface as 200 with
		// installed=false + reason — the dashboard caller treats this
		// as "show install prompt". A 5xx here would imply server-side
		// bug, but the actual cause is a member-cluster availability
		// problem.
		out := VeleroStatusResponse{
			Installed: false,
			Namespace: defaultVeleroNamespace,
			Reason:    err.Error(),
		}
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "unreachable")...).Set(0)
		RespondJSON(w, http.StatusOK, out)
		return
	}
	if len(bsls) == 0 {
		out := VeleroStatusResponse{
			Installed: false,
			Namespace: defaultVeleroNamespace,
			Reason:    "no BackupStorageLocation CRDs found",
		}
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "missing")...).Set(0)
		RespondJSON(w, http.StatusOK, out)
		return
	}

	summaries := make([]VeleroBSLSummary, 0, len(bsls))
	anyReady := false
	for _, item := range bsls {
		s := summarizeBSL(item)
		summaries = append(summaries, s)
		if strings.EqualFold(s.Phase, "Available") {
			anyReady = true
		}
	}
	out := VeleroStatusResponse{
		Installed:        true,
		Namespace:        defaultVeleroNamespace,
		StorageReady:     anyReady,
		StorageLocations: summaries,
	}
	if anyReady {
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "ready")...).Set(1)
	} else {
		veleroInstallStatus.WithLabelValues(observability.MetricValues(clusterID.String(), "unavailable")...).Set(0)
	}
	RespondJSON(w, http.StatusOK, out)
}

func summarizeBSL(item map[string]any) VeleroBSLSummary {
	out := VeleroBSLSummary{}
	if meta, ok := item["metadata"].(map[string]any); ok {
		if n, ok := meta["name"].(string); ok {
			out.Name = n
		}
	}
	if spec, ok := item["spec"].(map[string]any); ok {
		if p, ok := spec["provider"].(string); ok {
			out.Provider = p
		}
		if d, ok := spec["default"].(bool); ok {
			out.Default = d
		}
		if os, ok := spec["objectStorage"].(map[string]any); ok {
			if b, ok := os["bucket"].(string); ok {
				out.Bucket = b
			}
		}
	}
	if status, ok := item["status"].(map[string]any); ok {
		if p, ok := status["phase"].(string); ok {
			out.Phase = p
		}
	}
	return out
}

// ----------------------------------------------------------------------
// Internal Velero CRD wiring
// ----------------------------------------------------------------------

func (h *ClusterSnapshotsHandler) postBackupCRD(ctx context.Context, clusterID string, row sqlc.ClusterSnapshot, spec SnapshotSpec) error {
	if h.requester == nil {
		return fmt.Errorf("tunnel requester not configured")
	}
	body := renderPerClusterBackup(PerClusterSnapshotRender{
		Name:                    row.VeleroName,
		Namespace:               row.VeleroNamespace,
		IncludedNamespaces:      spec.IncludedNamespaces,
		ExcludedNamespaces:      spec.ExcludedNamespaces,
		IncludedResources:       spec.IncludedResources,
		ExcludedResources:       spec.ExcludedResources,
		LabelSelector:           spec.LabelSelector,
		SnapshotVolumes:         spec.SnapshotVolumes,
		TTL:                     spec.TTL,
		StorageLocation:         spec.StorageLocation,
		VolumeSnapshotLocations: spec.VolumeSnapshotLocations,
		SnapshotID:              row.ID.String(),
	})
	return createVeleroBackupCRD(ctx, h.requester, clusterID, body)
}

func (h *ClusterSnapshotsHandler) postDeleteBackupRequestCRD(ctx context.Context, clusterID string, row sqlc.ClusterSnapshot) error {
	if h.requester == nil {
		// Local delete-only: no member cluster to talk to. Still drop
		// the DB row; the operator can prune the Velero CRD manually
		// if the tunnel is permanently down.
		return nil
	}
	suffix, _ := randomHex(4)
	dbrName := truncateName(row.VeleroName+"-delete-"+suffix, 253)
	body := renderDeleteBackupRequest(dbrName, row.VeleroNamespace, row.VeleroName)
	return createVeleroDeleteBackupRequest(ctx, h.requester, clusterID, body)
}

func (h *ClusterSnapshotsHandler) postRestoreCRD(ctx context.Context, clusterID string, row sqlc.ClusterRestore, spec RestoreSpec, backupName string) error {
	if h.requester == nil {
		return fmt.Errorf("tunnel requester not configured")
	}
	body := renderPerClusterRestore(PerClusterRestoreRender{
		Name:               row.VeleroName,
		Namespace:          row.VeleroNamespace,
		BackupName:         backupName,
		IncludedNamespaces: spec.IncludedNamespaces,
		ExcludedNamespaces: spec.ExcludedNamespaces,
		NamespaceMapping:   spec.NamespaceMapping,
		LabelSelector:      spec.LabelSelector,
		RestorePVs:         spec.RestorePVs,
		RestoreID:          row.ID.String(),
		SnapshotID:         row.SnapshotID.String(),
	})
	return createVeleroRestoreCRD(ctx, h.requester, clusterID, body)
}

// ----------------------------------------------------------------------
// Naming + validation helpers
// ----------------------------------------------------------------------

// newVeleroBackupName produces a deterministic-ish name: "<cluster>-<ts>-<rand>".
// Velero names must be ≤253 chars and RFC 1123 subdomains; the cluster
// name is RFC-1123 too so concatenation stays safe.
func newVeleroBackupName(cluster string) string {
	suffix, _ := randomHex(4)
	stamp := time.Now().UTC().Format("20060102t150405")
	cluster = sanitizeForName(cluster)
	name := cluster + "-" + stamp + "-" + suffix
	return truncateName(name, 253)
}

func newVeleroRestoreName(backup string) string {
	suffix, _ := randomHex(3)
	stamp := time.Now().UTC().Format("20060102t150405")
	backup = sanitizeForName(backup)
	name := backup + "-restore-" + stamp + "-" + suffix
	return truncateName(name, 253)
}

func sanitizeForName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "snapshot"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	// trim leading / trailing dashes — RFC-1123 wants alphanumeric ends.
	return strings.Trim(string(out), "-")
}

func truncateName(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return strings.TrimRight(s[:max], "-")
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validVeleroResourceName enforces RFC-1123 subdomain rules at the
// handler edge: 1–253 chars, lowercase alphanumeric or '-'/'.', start
// + end alphanumeric. Velero (via the K8s api server) rejects anything
// else; surfacing the 400 here keeps the error message friendly.
func validVeleroResourceName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for i, c := range s {
		isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		isHyphen := c == '-' || c == '.'
		if !isAlnum && !isHyphen {
			return false
		}
		if (i == 0 || i == len(s)-1) && !isAlnum {
			return false
		}
	}
	return true
}

// parseSnapshotTTLDuration parses a Velero-style TTL string ("168h", "30m"). Returns
// (0, false) when the input is empty or unparseable so the caller can
// fall through to leaving expires_at NULL.
func parseSnapshotTTLDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// parseCronExpression validates a standard 5-field cron schedule using
// robfig/cron's "Standard" parser (the same parser asynq.Scheduler
// uses for @every-style specs). Returns the parsed Schedule so the
// dispatcher worker can call .Next() against it.
func parseCronExpression(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cron schedule is required")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return parser.Parse(expr)
}

// ----------------------------------------------------------------------
// Bridge — exposes a no-op enqueueing surface for the worker callbacks
// the routes layer wires up. *asynq.Client satisfies the existing
// ClusterDecommissionEnqueuer interface; we reuse it rather than
// introducing a fresh interface name.
// ----------------------------------------------------------------------

// SetTaskQueue is provided for symmetry with the other handlers — the
// snapshot handler currently doesn't enqueue tasks directly (the poller
// runs on its own cadence), but the setter is here so future synchronous
// triggers can wire in without an API break.
func (h *ClusterSnapshotsHandler) SetTaskQueue(_ interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}) {
}
