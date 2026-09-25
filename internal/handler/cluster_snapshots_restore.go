package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
)

func (h *ClusterSnapshotsHandler) CreateRestore(w http.ResponseWriter, r *http.Request) {
	clusterID, snapshotID, ok := parseClusterAndSnapshotIDs(w, r)
	if !ok {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "cluster-snapshot-restore"))
	snapshot, err := h.queries.GetClusterSnapshotByID(r.Context(), snapshotID)
	if err != nil || snapshot.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Snapshot not found")
		return
	}
	if snapshot.Phase != "Completed" && snapshot.Phase != "PartiallyFailed" {
		RespondRequestError(w, r, http.StatusConflict, apierror.SnapshotNotReady, "Snapshot is not yet Completed; cannot restore")
		return
	}

	// openapi:request SnapshotRestoreRequest
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
	if !h.authz.authorizeClusterAction(w, r, targetID, rbac.ResourceClusters, rbac.VerbUpdate) {
		return
	}
	target, err := h.queries.GetClusterByID(r.Context(), targetID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Target cluster not found")
		return
	}

	namespace := strings.TrimSpace(req.Namespace)
	if namespace == "" {
		namespace = snapshot.VeleroNamespace
		if namespace == "" {
			namespace = defaultVeleroNamespace
		}
	}

	// Cross-cluster restore pre-flight. The target cluster must have
	// Velero installed AND have a BackupStorageLocation pointing at
	// the same store as the snapshot's source cluster — otherwise
	// Velero on the target would have nothing to read. We surface a
	// clear 409 rather than 500ing later in the poller.
	if targetID != clusterID {
		bsls, vErr := listVeleroBSLs(r.Context(), h.requester, targetID.String(), namespace)
		if vErr != nil {
			RespondRequestError(w, r, http.StatusBadGateway, apierror.VeleroUnreachable, fmt.Sprintf("could not check Velero on target cluster: %v", vErr))
			return
		}
		if len(bsls) == 0 {
			RespondRequestError(w, r, http.StatusConflict, apierror.VeleroMissingOnTarget, "Target cluster has no Velero BackupStorageLocation; install Velero before cross-cluster restore")
			return
		}

		// Same-store validation: it isn't enough for the target to have
		// SOME BSL — it must have one pointing at the SAME object store
		// (provider + bucket) the snapshot was written to, or Velero on
		// the target would have nothing to read and the restore would only
		// fail later in the poller. Resolve the source snapshot's backing
		// store from the source cluster's BSLs and require a target match.
		srcNamespace := strings.TrimSpace(snapshot.VeleroNamespace)
		if srcNamespace == "" {
			srcNamespace = defaultVeleroNamespace
		}
		srcBSLs, sErr := listVeleroBSLs(r.Context(), h.requester, clusterID.String(), srcNamespace)
		if sErr != nil {
			RespondRequestError(w, r, http.StatusBadGateway, apierror.VeleroUnreachable, fmt.Sprintf("could not check Velero on source cluster: %v", sErr))
			return
		}
		src, resolved := resolveBSLStore(srcBSLs, decodeSpec(snapshot.Spec).StorageLocation)
		if !resolved || strings.TrimSpace(src.Bucket) == "" {
			RespondRequestError(w, r, http.StatusConflict, apierror.VeleroMissingOnTarget, "Cannot verify the source snapshot object store; restore was not queued")
			return
		}
		if !targetHasMatchingStore(bsls, src) {
			RespondRequestError(w, r, http.StatusConflict, apierror.VeleroMissingOnTarget, "Target cluster has no BackupStorageLocation matching the source snapshot object store")
			return
		}

	}

	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "snapshot transaction runner is not configured")
		return
	}

	veleroName := newVeleroRestoreName(snapshot.VeleroName)
	params := sqlc.CreateClusterRestoreParams{
		SnapshotID:      snapshotID,
		TargetClusterID: targetID,
		VeleroName:      veleroName,
		VeleroNamespace: namespace,
		Spec:            encodeRestoreSpec(req.Spec),
		Phase:           "New",
		CreatedBy:       currentUserUUID(r),
	}
	type createRestoreResult struct {
		row       sqlc.ClusterRestore
		remoteErr error
		replay    bool
	}
	result, err := executeMutation(r, h.runTx,
		func(q ClusterSnapshotMutationTx) (createRestoreResult, error) {
			existingID, replay, err := claimResourceOperation(r.Context(), q, "cluster_restores")
			if err != nil {
				return createRestoreResult{}, err
			}
			if replay {
				row, getErr := q.GetClusterRestoreByID(r.Context(), existingID)
				if getErr != nil || row.SnapshotID != snapshotID || row.TargetClusterID != targetID || row.VeleroNamespace != namespace || !bytes.Equal(row.Spec, params.Spec) {
					return createRestoreResult{}, errOperationIdempotencyConflict
				}
				return createRestoreResult{row: row, replay: true}, nil
			}
			row, err := q.CreateClusterRestore(r.Context(), params)
			if err != nil {
				return createRestoreResult{}, err
			}
			if err := enqueueClusterSnapshotOperation(r.Context(), q, tasks.ClusterSnapshotOperationPayload{
				Operation: tasks.ClusterSnapshotOperationRestore, RestoreID: row.ID.String(),
			}); err != nil {
				return createRestoreResult{}, err
			}
			if err := attachResourceOperation(r.Context(), q, "cluster_restores", row.ID, restoreToResponse(row)); err != nil {
				return createRestoreResult{}, err
			}
			return createRestoreResult{row: row}, nil
		},
		func(result createRestoreResult) mutationAuditEvent {
			if result.replay {
				return mutationAuditEvent{}
			}
			detail := map[string]any{
				"cluster_id": targetID.String(), "snapshot_id": snapshotID.String(),
				"snapshot_cluster": clusterID.String(), "velero_name": result.row.VeleroName,
			}
			if result.remoteErr != nil {
				detail["remote_submission"] = "failed"
			}
			return mutationAuditEvent{
				action: "cluster.snapshot.restore_requested", resourceType: "cluster_restore",
				resourceID: result.row.ID.String(), resourceName: target.Name,
				status: http.StatusAccepted, detail: detail,
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different snapshot restore")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create restore intent")
		return
	}
	row := result.row
	h.publishSnapshotChanged(row.TargetClusterID, row.ID, "restore")
	out := restoreToResponse(row)
	out.SourceClusterID = clusterID
	if result.remoteErr != nil {
		out.LastPollError = result.remoteErr.Error()
	}
	RespondAcceptedOperation(w, fmt.Sprintf("/api/v1/clusters/%s/snapshot-restores/%s/", row.TargetClusterID, row.ID), out)
}

// ----------------------------------------------------------------------
// /snapshot-schedules/ — list / get / create / update / delete
// ----------------------------------------------------------------------
