package handler

import (
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sync handles POST /api/v1/admin/gitops-sources/{id}/sync/.
func (h *GitOpsHandler) Sync(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps transaction runner not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "admin_gitops_source_sync"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action   string `json:"action"`
		SourceID string `json:"source_id"`
	}{Action: "sync", SourceID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode GitOps sync request")
		return
	}
	var receipt GitOpsSyncReceipt
	err = h.runTx(r.Context(), func(q GitOpsMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("GitOps sync idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[GitOpsSyncReceipt](r.Context(), idemQ, "gitops_source_syncs", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt = stored
			return nil
		}
		result, enqueueErr := enqueueGitOpsSourceSync(r, q, q, id)
		if enqueueErr != nil {
			return enqueueErr
		}
		receipt = GitOpsSyncReceipt{SourceID: result.row.ID.String(), TaskID: result.task.ID.String(), Status: "queued"}
		if auditErr := recordAuditOutbox(r, q, "admin.gitops_source.sync_requested", "gitops_source", result.row.ID.String(), result.row.Name, http.StatusAccepted, map[string]any{
			"trigger": "manual", "task_id": result.task.ID.String(),
		}); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "gitops_source_syncs", result.task.ID, digest, receipt)
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different GitOps source sync")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SyncError, "Failed to queue GitOps source sync")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/admin/gitops-sources/"+receipt.SourceID+"/", receipt)
}

type GitOpsSyncReceipt struct {
	SourceID string `json:"source_id"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
}
