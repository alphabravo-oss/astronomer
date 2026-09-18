package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *ClusterSnapshotsHandler) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
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
//  2. Atomically insert phase='New', the durable apply task, and audit intent.
//  3. Return 202; a tunnel worker idempotently creates the Velero Backup CR.
func (h *ClusterSnapshotsHandler) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "cluster-snapshot"))
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	// openapi:request SnapshotCreateRequest
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

	params := sqlc.CreateClusterSnapshotParams{
		ClusterID:       clusterID,
		VeleroName:      veleroName,
		VeleroNamespace: namespace,
		Source:          source,
		Spec:            encodeSpec(spec),
		Phase:           "New",
		ExpiresAt:       expiresAt,
		CreatedBy:       currentUserUUID(r),
	}
	type createSnapshotResult struct {
		row       sqlc.ClusterSnapshot
		remoteErr error
		replay    bool
	}
	result, err := executeMutation(r, h.runTx,
		func(q ClusterSnapshotMutationTx) (createSnapshotResult, error) {
			existingID, replay, err := claimResourceOperation(r.Context(), q, "cluster_snapshots")
			if err != nil {
				return createSnapshotResult{}, err
			}
			if replay {
				row, getErr := q.GetClusterSnapshotByID(r.Context(), existingID)
				if getErr != nil || row.ClusterID != clusterID {
					return createSnapshotResult{}, errOperationIdempotencyConflict
				}
				return createSnapshotResult{row: row, replay: true}, nil
			}
			row, err := q.CreateClusterSnapshot(r.Context(), params)
			if err != nil {
				return createSnapshotResult{}, err
			}
			if err := enqueueClusterSnapshotOperation(r.Context(), q, tasks.ClusterSnapshotOperationPayload{
				Operation: tasks.ClusterSnapshotOperationCreate, SnapshotID: row.ID.String(),
			}); err != nil {
				return createSnapshotResult{}, err
			}
			if err := attachResourceOperation(r.Context(), q, "cluster_snapshots", row.ID, snapshotToResponse(row)); err != nil {
				return createSnapshotResult{}, err
			}
			return createSnapshotResult{row: row}, nil
		},
		func(result createSnapshotResult) mutationAuditEvent {
			if result.replay {
				return mutationAuditEvent{}
			}
			detail := map[string]any{
				"cluster_id": clusterID.String(), "velero_name": result.row.VeleroName,
				"source": source, "namespace": namespace,
			}
			if result.remoteErr != nil {
				detail["remote_submission"] = "failed"
			}
			return mutationAuditEvent{
				action: "cluster.snapshot.created", resourceType: "cluster_snapshot",
				resourceID: result.row.ID.String(), resourceName: cluster.Name,
				status: http.StatusAccepted, detail: detail,
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, err.Error())
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create snapshot intent")
		return
	}
	row := result.row
	clusterSnapshotsCreatedInFlight.WithLabelValues(observability.MetricValues(clusterID.String())...).Inc()
	h.publishSnapshotChanged(clusterID, row.ID, "snapshot")
	out := snapshotToResponse(row)
	if result.remoteErr != nil {
		out.LastPollError = result.remoteErr.Error()
	}
	RespondAcceptedOperation(w, fmt.Sprintf("/api/v1/clusters/%s/snapshots/%s", clusterID, row.ID), out)
}

// DeleteSnapshot handles DELETE /clusters/{cluster_id}/snapshots/{id}/.
// Atomically records a durable Velero DeleteBackupRequest intent and audit,
// then drops the local row. The task carries the non-secret external reference
// needed after deletion; Velero handles object-store cleanup asynchronously.
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

	type deleteSnapshotResult struct {
		row       sqlc.ClusterSnapshot
		remoteErr error
	}
	_, err = executeMutation(r, h.runTx,
		func(q ClusterSnapshotMutationTx) (deleteSnapshotResult, error) {
			locked, err := q.GetClusterSnapshotForUpdate(r.Context(), snapshotID)
			if err != nil {
				return deleteSnapshotResult{}, err
			}
			if locked.ClusterID != clusterID {
				return deleteSnapshotResult{}, pgx.ErrNoRows
			}
			veleroNamespace := strings.TrimSpace(locked.VeleroNamespace)
			if veleroNamespace == "" {
				veleroNamespace = defaultVeleroNamespace
			}
			if err := enqueueClusterSnapshotOperation(r.Context(), q, tasks.ClusterSnapshotOperationPayload{
				Operation: tasks.ClusterSnapshotOperationDelete, SnapshotID: locked.ID.String(),
				ClusterID: locked.ClusterID.String(), VeleroName: locked.VeleroName,
				VeleroNamespace: veleroNamespace,
			}); err != nil {
				return deleteSnapshotResult{}, err
			}
			if err := q.DeleteClusterSnapshot(r.Context(), snapshotID); err != nil {
				return deleteSnapshotResult{}, err
			}
			return deleteSnapshotResult{row: locked}, nil
		},
		func(result deleteSnapshotResult) mutationAuditEvent {
			detail := map[string]any{"cluster_id": clusterID.String(), "velero_name": result.row.VeleroName}
			if result.remoteErr != nil {
				detail["remote_submission"] = "failed"
			}
			return mutationAuditEvent{
				action: "cluster.snapshot.deleted", resourceType: "cluster_snapshot",
				resourceID: snapshotID.String(), status: http.StatusNoContent, detail: detail,
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Snapshot not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete snapshot intent")
		return
	}
	h.publishSnapshotChanged(clusterID, snapshotID, "snapshot")

	w.WriteHeader(http.StatusNoContent)
}

// CreateRestore handles POST /clusters/{cluster_id}/snapshots/{id}/restore/.
// Body: { "target_cluster_id": <uuid>, "spec": { ... } }. When
// target_cluster_id is omitted the restore targets the snapshot's own
// cluster (in-place restore).
