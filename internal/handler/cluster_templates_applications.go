package handler

import (
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// publishTemplateBindingChanged emits the metadata-only
// template_binding.changed event after a successful application-row write.
func (h *ClusterTemplateHandler) publishTemplateBindingChanged(clusterID uuid.UUID, status string) {
	if h == nil {
		return
	}
	extra := map[string]any{}
	if status != "" {
		extra["status"] = status
	}
	events.PublishChanged(h.bus, "template_binding", clusterID.String(), clusterID.String(), extra)
}

// Status constants for cluster_template_applications.status. Kept in
// lockstep with the worker's transitions.
const (
	ClusterTemplateStatusPending  = "pending"
	ClusterTemplateStatusApplying = "applying"
	ClusterTemplateStatusApplied  = "applied"
	ClusterTemplateStatusFailed   = "failed"
)

// ────────────────────────────────────────────────────────────────────────
// Per-cluster apply / detach / status endpoints
// ────────────────────────────────────────────────────────────────────────

// Apply handles POST /api/v1/clusters/{cluster_id}/template/. Binds the
// template to the cluster (replacing any previous binding) and enqueues
// the convergence task. Returns 202 Accepted with the current status row.
func (h *ClusterTemplateHandler) Apply(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	// Migration 057: maintenance window gate on cluster_template.apply.
	if EnforceMaintenanceWindow(w, r, h.maintenanceGate, "cluster_template.apply",
		MaintenanceGateClusterLabels(cluster),
		pgtype.UUID{Bytes: clusterID, Valid: true}, pgtype.UUID{}) {
		return
	}
	var req ApplyClusterTemplateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	templateID, err := uuid.Parse(req.TemplateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid template_id")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), templateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster template not found")
		return
	}

	// Migration 067 — pre-flight every ${vault://...} reference in the
	// spec so a bad template (missing key / wrong connection) fails the
	// API call instead of the worker. We DELIBERATELY DO NOT persist
	// the resolved values: the spec snapshot keeps the original
	// ${vault://...} markers, and the worker re-resolves at install
	// time. Cluster-scoped apply, no project context, so unqualified
	// refs require the explicit ${vault://<connection>/...} form.
	if _, vaultErr := vaultResolveBlob(r.Context(), h.vaultResolver, uuid.Nil, string(tmpl.Spec)); vaultErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.VaultResolveFailed, vaultErr.Error())
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "cluster template transaction runner is not configured")
		return
	}

	params := sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    clusterID,
		TemplateID:   tmpl.ID,
		SpecSnapshot: tmpl.Spec,
	}
	r = r.WithContext(withOperationIdempotency(r, "cluster_template_apply"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID  string `json:"cluster_id"`
		TemplateID string `json:"template_id"`
	}{ClusterID: clusterID.String(), TemplateID: tmpl.ID.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode template application request")
		return
	}
	var receipt ClusterTemplateApplicationResponse
	replayed := false
	app, err := executeMutation(r, h.runTx,
		func(q ClusterTemplateMutationTx) (sqlc.ClusterTemplateApplication, error) {
			idemQ, ok := q.(resourceOperationIdempotencyQuerier)
			if !ok {
				return sqlc.ClusterTemplateApplication{}, errors.New("cluster template idempotency store is not configured")
			}
			_, stored, replay, claimErr := claimOperationReceipt[ClusterTemplateApplicationResponse](r.Context(), idemQ, "cluster_template_applications", digest)
			if claimErr != nil {
				return sqlc.ClusterTemplateApplication{}, claimErr
			}
			if replay {
				receipt, replayed = stored, true
				return sqlc.ClusterTemplateApplication{}, nil
			}
			row, mutationErr := upsertClusterTemplateApplicationAndTask(r, q, params)
			if mutationErr != nil {
				return sqlc.ClusterTemplateApplication{}, mutationErr
			}
			receipt = applicationToResponse(row, tmpl.Name)
			if attachErr := attachOperationReceipt(r.Context(), idemQ, "cluster_template_applications", clusterID, digest, receipt); attachErr != nil {
				return sqlc.ClusterTemplateApplication{}, attachErr
			}
			return row, nil
		},
		func(row sqlc.ClusterTemplateApplication) mutationAuditEvent {
			if replayed {
				return mutationAuditEvent{}
			}
			return mutationAuditEvent{
				action: "cluster.template_applied", resourceType: "cluster",
				resourceID: clusterID.String(), resourceName: cluster.Name, status: http.StatusAccepted,
				detail: map[string]any{"template_id": tmpl.ID.String(), "template_name": tmpl.Name},
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different template application")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.ApplyError, "Failed to bind template to cluster")
		return
	}

	if !replayed {
		h.publishTemplateBindingChanged(clusterID, app.Status)
	}
	RespondAcceptedOperation(w, "/api/v1/clusters/"+clusterID.String()+"/template/", receipt)
}

// GetApplication handles GET /api/v1/clusters/{cluster_id}/template/.
func (h *ClusterTemplateHandler) GetApplication(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	app, err := h.queries.GetClusterTemplateApplication(r.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No template applied to this cluster")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to load template application")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), app.TemplateID)
	templateName := ""
	if err == nil {
		templateName = tmpl.Name
	}
	RespondJSON(w, http.StatusOK, applicationToResponse(app, templateName))
}

// Reapply handles POST /api/v1/clusters/{cluster_id}/template/reapply/.
// Used for drift correction — resets the application status to pending
// and re-enqueues the apply task. Spec snapshot is refreshed from the
// current template body so the convergence target tracks the latest.
func (h *ClusterTemplateHandler) Reapply(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	app, err := h.queries.GetClusterTemplateApplication(r.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No template applied to this cluster")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to load template application")
		return
	}
	tmpl, err := h.queries.GetClusterTemplateByID(r.Context(), app.TemplateID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Template no longer exists")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "cluster template transaction runner is not configured")
		return
	}
	params := sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    clusterID,
		TemplateID:   tmpl.ID,
		SpecSnapshot: tmpl.Spec,
	}
	r = r.WithContext(withOperationIdempotency(r, "cluster_template_reapply"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID string `json:"cluster_id"`
	}{ClusterID: clusterID.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode template reapply request")
		return
	}
	var receipt ClusterTemplateApplicationResponse
	replayed := false
	app, err = executeMutation(r, h.runTx,
		func(q ClusterTemplateMutationTx) (sqlc.ClusterTemplateApplication, error) {
			idemQ, ok := q.(resourceOperationIdempotencyQuerier)
			if !ok {
				return sqlc.ClusterTemplateApplication{}, errors.New("cluster template idempotency store is not configured")
			}
			_, stored, replay, claimErr := claimOperationReceipt[ClusterTemplateApplicationResponse](r.Context(), idemQ, "cluster_template_reapplications", digest)
			if claimErr != nil {
				return sqlc.ClusterTemplateApplication{}, claimErr
			}
			if replay {
				receipt, replayed = stored, true
				return sqlc.ClusterTemplateApplication{}, nil
			}
			row, mutationErr := upsertClusterTemplateApplicationAndTask(r, q, params)
			if mutationErr != nil {
				return sqlc.ClusterTemplateApplication{}, mutationErr
			}
			receipt = applicationToResponse(row, tmpl.Name)
			if attachErr := attachOperationReceipt(r.Context(), idemQ, "cluster_template_reapplications", clusterID, digest, receipt); attachErr != nil {
				return sqlc.ClusterTemplateApplication{}, attachErr
			}
			return row, nil
		},
		func(row sqlc.ClusterTemplateApplication) mutationAuditEvent {
			if replayed {
				return mutationAuditEvent{}
			}
			return mutationAuditEvent{
				action: "cluster.template_reapplied", resourceType: "cluster",
				resourceID: clusterID.String(), resourceName: cluster.Name, status: http.StatusAccepted,
				detail: map[string]any{"template_id": tmpl.ID.String(), "template_name": tmpl.Name},
			}
		})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different template reapply request")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.ApplyError, "Failed to reset template application")
		return
	}
	if !replayed {
		h.publishTemplateBindingChanged(clusterID, app.Status)
	}
	RespondAcceptedOperation(w, "/api/v1/clusters/"+clusterID.String()+"/template/", receipt)
}

// Detach handles DELETE /api/v1/clusters/{cluster_id}/template/. Removes
// the binding (and the associated registration-policy row) but leaves
// any tools/projects the apply task installed in place — the operator
// can clean those up via the individual handlers if a full teardown is
// desired. This conservative behavior matches the user expectation that
// "unbind" not destroy operator-installed workloads.
func (h *ClusterTemplateHandler) Detach(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q ClusterTemplateMutationTx) (struct{}, error) {
			if deleteErr := q.DeleteClusterTemplateApplication(r.Context(), clusterID); deleteErr != nil {
				return struct{}{}, deleteErr
			}
			if policyErr := q.DeleteClusterRegistrationPolicy(r.Context(), clusterID); policyErr != nil {
				return struct{}{}, policyErr
			}
			return struct{}{}, nil
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.template_detached", resourceType: "cluster",
				resourceID: clusterID.String(), resourceName: cluster.Name, status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DetachError, "Failed to detach template")
		return
	}
	h.publishTemplateBindingChanged(clusterID, "detached")
	w.WriteHeader(http.StatusNoContent)
}

// newClusterTemplateApplyTask builds the durable apply intent committed with
// the application row by upsertClusterTemplateApplicationAndTask.
func newClusterTemplateApplyTask(r *http.Request, clusterID uuid.UUID) (*asynq.Task, error) {
	task, err := tasks.NewClusterTemplateApplyTask(clusterID)
	if err != nil {
		return nil, err
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), reqctx.CorrelationID(r.Context()))
	return asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3)), nil
}

func upsertClusterTemplateApplicationAndTask(r *http.Request, q ClusterTemplateMutationTx, params sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	task, err := newClusterTemplateApplyTask(r, params.ClusterID)
	if err != nil {
		return sqlc.ClusterTemplateApplication{}, err
	}
	app, atomic, err := upsertClusterTemplateApplicationWithTaskOutbox(r.Context(), q, q, params, task, tasks.TaskOutboxOptions{
		DedupeKey:           clusterTemplateRequestDedupeKey(r, params.ClusterID),
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
	})
	if !atomic {
		return sqlc.ClusterTemplateApplication{}, errors.New("cluster template task outbox transaction is unavailable")
	}
	return app, err
}
