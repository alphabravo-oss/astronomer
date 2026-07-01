package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// NativeRBACQuerier is the DB surface the native-rule CRUD handler needs.
type NativeRBACQuerier interface {
	CreateNativeRBACRule(ctx context.Context, arg sqlc.CreateNativeRBACRuleParams) (sqlc.NativeRbacRule, error)
	GetNativeRBACRuleByID(ctx context.Context, id uuid.UUID) (sqlc.NativeRbacRule, error)
	ListNativeRBACRulesByUser(ctx context.Context, userID uuid.UUID) ([]sqlc.NativeRbacRule, error)
	ListNativeRBACRules(ctx context.Context, arg sqlc.ListNativeRBACRulesParams) ([]sqlc.NativeRbacRule, error)
	DeleteNativeRBACRule(ctx context.Context, id uuid.UUID) error
}

// NativeRBACHandler serves CRUD for native per-CRD RBAC rules. Rules are an
// ADDITIVE allow layer consulted by the k8s-proxy authz hook after a coarse
// deny; see internal/rbac/native.go and the migration 126 comment.
type NativeRBACHandler struct {
	queries    NativeRBACQuerier
	invalidate func(userID string)
}

func NewNativeRBACHandler(queries NativeRBACQuerier) *NativeRBACHandler {
	return &NativeRBACHandler{queries: queries}
}

// SetInvalidator wires a callback fired after a rule mutation so the authz
// hook's per-user cache drops immediately instead of waiting out its TTL.
func (h *NativeRBACHandler) SetInvalidator(fn func(userID string)) {
	if h != nil {
		h.invalidate = fn
	}
}

// allowedNativeVerbs is the coarse verb vocabulary a native rule may grant.
// exec and logs are deliberately excluded — the evaluator refuses them anyway,
// but rejecting them at authoring time gives a clear error. "*" means all of
// these (still never exec/logs).
var allowedNativeVerbs = map[string]bool{
	"read": true, "list": true, "watch": true,
	"create": true, "update": true, "delete": true, "*": true,
}

// escalationGroups keeps native rules out of the privilege-escalation api
// groups at authoring time (the evaluator also refuses them at request time).
var escalationGroups = map[string]bool{
	"rbac.authorization.k8s.io":    true,
	"admissionregistration.k8s.io": true,
	"apiregistration.k8s.io":       true,
	"apiextensions.k8s.io":         true,
}

type nativeRuleResponse struct {
	ID        string   `json:"id"`
	UserID    string   `json:"userId"`
	ClusterID string   `json:"clusterId,omitempty"`
	Namespace string   `json:"namespace"`
	APIGroup  string   `json:"apiGroup"`
	Resource  string   `json:"resource"`
	Verbs     []string `json:"verbs"`
	CreatedAt string   `json:"createdAt"`
	CreatedBy string   `json:"createdBy,omitempty"`
}

func nativeRuleToResponse(r sqlc.NativeRbacRule) nativeRuleResponse {
	out := nativeRuleResponse{
		ID:        r.ID.String(),
		UserID:    r.UserID.String(),
		Namespace: r.Namespace,
		APIGroup:  r.ApiGroup,
		Resource:  r.Resource,
		Verbs:     r.Verbs,
		CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if r.ClusterID.Valid {
		out.ClusterID = uuid.UUID(r.ClusterID.Bytes).String()
	}
	if r.CreatedByID.Valid {
		out.CreatedBy = uuid.UUID(r.CreatedByID.Bytes).String()
	}
	return out
}

type createNativeRuleRequest struct {
	UserID    string   `json:"userId"`
	ClusterID string   `json:"clusterId,omitempty"`
	Namespace string   `json:"namespace,omitempty"`
	APIGroup  string   `json:"apiGroup,omitempty"`
	Resource  string   `json:"resource"`
	Verbs     []string `json:"verbs"`
}

// Create authors a native rule. POST /native-rbac-rules/
func (h *NativeRBACHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createNativeRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid request body")
		return
	}

	userID, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "userId must be a valid user UUID")
		return
	}
	resource := strings.TrimSpace(req.Resource)
	if resource == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "resource is required (a plural resource name, or '*' for all in the group)")
		return
	}
	apiGroup := strings.TrimSpace(req.APIGroup)
	if escalationGroups[strings.ToLower(apiGroup)] {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody,
			"native rules cannot target privilege-escalation api groups (rbac/admission/apiregistration/apiextensions); use a coarse RBAC grant for those")
		return
	}
	if len(req.Verbs) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "at least one verb is required")
		return
	}
	verbs := make([]string, 0, len(req.Verbs))
	for _, v := range req.Verbs {
		v = strings.ToLower(strings.TrimSpace(v))
		if !allowedNativeVerbs[v] {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody,
				"verb must be one of read|list|watch|create|update|delete|* (exec/logs are not grantable via native rules)")
			return
		}
		verbs = append(verbs, v)
	}

	var clusterID pgtype.UUID
	if s := strings.TrimSpace(req.ClusterID); s != "" {
		cid, err := uuid.Parse(s)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "clusterId must be a valid cluster UUID or omitted for all clusters")
			return
		}
		clusterID = pgtype.UUID{Bytes: cid, Valid: true}
	}

	row, err := h.queries.CreateNativeRBACRule(r.Context(), sqlc.CreateNativeRBACRuleParams{
		UserID:      userID,
		ClusterID:   clusterID,
		Namespace:   strings.TrimSpace(req.Namespace),
		ApiGroup:    apiGroup,
		Resource:    resource,
		Verbs:       verbs,
		CreatedByID: currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create native rule (does the user exist?)")
		return
	}
	if h.invalidate != nil {
		h.invalidate(userID.String())
	}
	recordAudit(r, h.queries, "rbac.native_rule.created", "native_rbac_rule", row.ID.String(), resource, map[string]any{
		"target_user": userID.String(),
		"api_group":   apiGroup,
		"resource":    resource,
		"verbs":       verbs,
	})
	RespondJSON(w, http.StatusCreated, nativeRuleToResponse(row))
}

// List returns rules, optionally filtered by ?userId=. GET /native-rbac-rules/
func (h *NativeRBACHandler) List(w http.ResponseWriter, r *http.Request) {
	if uid := strings.TrimSpace(r.URL.Query().Get("userId")); uid != "" {
		userID, err := uuid.Parse(uid)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "userId must be a valid UUID")
			return
		}
		rows, err := h.queries.ListNativeRBACRulesByUser(r.Context(), userID)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list native rules")
			return
		}
		RespondJSON(w, http.StatusOK, nativeRulesToResponses(rows))
		return
	}
	limit, offset := parseLimitOffset(r, 100, 500)
	rows, err := h.queries.ListNativeRBACRules(r.Context(), sqlc.ListNativeRBACRulesParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list native rules")
		return
	}
	RespondJSON(w, http.StatusOK, nativeRulesToResponses(rows))
}

// Delete removes a rule. DELETE /native-rbac-rules/{id}/
func (h *NativeRBACHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid rule ID")
		return
	}
	row, err := h.queries.GetNativeRBACRuleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Native rule not found")
		return
	}
	if err := h.queries.DeleteNativeRBACRule(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete native rule")
		return
	}
	if h.invalidate != nil {
		h.invalidate(row.UserID.String())
	}
	recordAudit(r, h.queries, "rbac.native_rule.deleted", "native_rbac_rule", id.String(), row.Resource, map[string]any{
		"target_user": row.UserID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

func nativeRulesToResponses(rows []sqlc.NativeRbacRule) []nativeRuleResponse {
	out := make([]nativeRuleResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, nativeRuleToResponse(r))
	}
	return out
}
