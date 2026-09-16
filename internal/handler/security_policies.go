package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// publishSecurityPolicyChanged emits the metadata-only security_policy.changed
// event after a successful cluster-security-policy write (P4.9). A Nil
// cluster (delete raced the pre-delete lookup) publishes unscoped rather
// than with a bogus zero-UUID cluster_id.
func (h *SecurityHandler) publishSecurityPolicyChanged(clusterID, policyID uuid.UUID) {
	if h == nil {
		return
	}
	cid := ""
	if clusterID != uuid.Nil {
		cid = clusterID.String()
	}
	events.PublishChanged(h.bus, "security_policy", cid, policyID.String(), nil)
}

// GetPolicy handles GET /api/v1/clusters/{cluster_id}/security/policy/.
func (h *SecurityHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}

	policy, err := h.queries.GetPolicyByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Security policy not found for cluster")
		return
	}

	RespondJSON(w, http.StatusOK, policy)
}

// ListPolicies handles GET /api/v1/security/policies/.
func (h *SecurityHandler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.queries.ListClusterSecurityPolicies(r.Context(), sqlc.ListClusterSecurityPoliciesParams{
		Limit:  int32(queryLimit(r, 20)),
		Offset: int32(queryOffset(r)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list security policies")
		return
	}
	total, err := h.queries.CountClusterSecurityPolicies(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count security policies")
		return
	}
	paging.Write(w, policies, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(policies)))
}

// CreatePolicy handles POST /api/v1/security/policies/.
func (h *SecurityHandler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	// openapi:request ClusterSecurityPolicyCreateRequest
	var req struct {
		ClusterID  uuid.UUID `json:"cluster_id"`
		TemplateID uuid.UUID `json:"template_id"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	params := sqlc.CreateClusterSecurityPolicyParams{
		ClusterID:  req.ClusterID,
		TemplateID: req.TemplateID,
		SyncStatus: "pending",
	}
	var policy sqlc.ClusterSecurityPolicy
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "security transaction runner is not configured")
		return
	}
	err := h.runTx(r.Context(), func(q SecurityMutationTx) error {
		var createErr error
		policy, createErr = q.CreateClusterSecurityPolicy(r.Context(), params)
		if createErr != nil {
			return createErr
		}
		return recordSecurityAuditOutbox(r, q, "security.policy.create", "cluster_security_policy", policy.ID.String(), "", http.StatusCreated, map[string]any{
			"cluster_id": req.ClusterID.String(), "template_id": req.TemplateID.String(),
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create security policy")
		return
	}
	h.publishSecurityPolicyChanged(req.ClusterID, policy.ID)
	w.Header().Set("Location", "/api/v1/security/policies/"+policy.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, policy)
}

// ApplyPolicy handles POST /api/v1/security/policies/{id}/apply/.
func (h *SecurityHandler) ApplyPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid policy ID")
		return
	}
	policy, err := h.queries.GetClusterSecurityPolicyByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Security policy not found")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "security transaction runner is not configured")
		return
	}
	err = h.runTx(r.Context(), func(q SecurityMutationTx) error {
		if updateErr := q.UpdateClusterSecurityPolicyApplied(r.Context(), id); updateErr != nil {
			return updateErr
		}
		return recordSecurityAuditOutbox(r, q, "security.policy.update", "cluster_security_policy", id.String(), "", http.StatusOK, map[string]any{
			"action": "apply", "cluster_id": policy.ClusterID.String(),
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.ApplyError, "Failed to apply security policy")
		return
	}
	policy, err = h.queries.GetClusterSecurityPolicyByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to reload security policy")
		return
	}
	h.publishSecurityPolicyChanged(policy.ClusterID, id)
	RespondJSON(w, http.StatusOK, policy)
}

// DeletePolicy handles DELETE /api/v1/security/policies/{id}/.
func (h *SecurityHandler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid policy ID")
		return
	}
	clusterID := ""
	var clusterUUID uuid.UUID
	if existing, lookupErr := h.queries.GetClusterSecurityPolicyByID(r.Context(), id); lookupErr == nil {
		clusterID = existing.ClusterID.String()
		clusterUUID = existing.ClusterID
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "security transaction runner is not configured")
		return
	}
	err = h.runTx(r.Context(), func(q SecurityMutationTx) error {
		if deleteErr := q.DeleteClusterSecurityPolicy(r.Context(), id); deleteErr != nil {
			return deleteErr
		}
		return recordSecurityAuditOutbox(r, q, "security.policy.delete", "cluster_security_policy", id.String(), "", http.StatusNoContent, map[string]any{"cluster_id": clusterID})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete security policy")
		return
	}
	h.publishSecurityPolicyChanged(clusterUUID, id)
	w.WriteHeader(http.StatusNoContent)
}
