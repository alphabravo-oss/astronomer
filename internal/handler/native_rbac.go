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
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
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
	// engine + bindings drive the privilege-escalation guard in Create: a
	// caller may only author a native rule for permissions they themselves
	// already hold at the rule's scope (or be a superuser). Optional; when
	// either is nil the guard is skipped, preserving the handler's
	// optional-authorization contract used by unit tests / pre-auth deploys.
	engine   *rbac.Engine
	bindings middleware.RBACQuerier
}

func NewNativeRBACHandler(queries NativeRBACQuerier) *NativeRBACHandler {
	return &NativeRBACHandler{queries: queries}
}

// SetAuthorization wires the RBAC engine + caller-binding lookup used by the
// Create escalation guard. Mirror of the coarse RBAC handler's
// SetAuthorization. Wiring lives in server.go (integrator-owned).
func (h *NativeRBACHandler) SetAuthorization(engine *rbac.Engine, bindings middleware.RBACQuerier) {
	if h == nil {
		return
	}
	h.engine = engine
	h.bindings = bindings
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
	// CSR signing / token minting are cluster-admin-equivalent; keep in sync
	// with rbac.isPrivilegeEscalationGroup so the evaluator refuses them too.
	"certificates.k8s.io":   true,
	"authentication.k8s.io": true,
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

	// Privilege-escalation guard (Kubernetes "you cannot grant permissions you
	// do not hold"): a native rule is an additive allow, so an rbac:create
	// holder who lacks e.g. secrets/pods access must NOT be able to self-author
	// (or author for anyone) a native rule that grants it. For every verb, the
	// CALLER must already hold the mapped coarse permission at the rule's
	// cluster/namespace scope, or be a superuser. Skipped only when
	// authorization is unwired (nil engine/bindings) — see SetAuthorization.
	if !h.enforceNoNativeEscalation(w, r, apiGroup, resource, verbs, clusterID, strings.TrimSpace(req.Namespace)) {
		return
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

// enforceNoNativeEscalation implements the "cannot grant what you do not hold"
// guard for native-rule authoring. It maps the rule's (apiGroup, resource) to
// the coarse RBAC resource the k8s-proxy authz hook would charge the request
// against, then requires the caller's own bindings to already satisfy each verb
// at the rule's scope. Superusers pass via the engine's IsSuperuser
// short-circuit. Returns false (and writes a 4xx) when the rule may not be
// authored; true means Create may proceed. No-op (returns true) when
// authorization is unwired.
func (h *NativeRBACHandler) enforceNoNativeEscalation(w http.ResponseWriter, r *http.Request, apiGroup, resource string, verbs []string, clusterID pgtype.UUID, namespace string) bool {
	if h.engine == nil || h.bindings == nil {
		return true
	}
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to author this rule")
		return false
	}
	callerBindings, err := h.bindings.GetUserBindings(r.Context(), user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load caller bindings")
		return false
	}
	charged := nativeRuleChargedResources(apiGroup, resource)
	var cid uuid.UUID
	if clusterID.Valid {
		cid = uuid.UUID(clusterID.Bytes)
	}
	for _, v := range verbs {
		for _, mapped := range charged {
			if !h.engine.CheckPermission(callerBindings, mapped, rbac.Verb(v), cid, uuid.UUID{}, namespace) {
				RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden,
					"Cannot author a native rule granting permissions you do not already hold at this scope")
				return false
			}
		}
	}
	return true
}

// sensitiveNativeResources is every coarse built-in a core-group wildcard rule
// grants at request time (rbac.NativeAllow honours {apiGroup:"", resource:"*"}
// for every core type, including secrets). Authoring such a rule must therefore
// require the caller to already hold each of these.
func sensitiveNativeResources() []rbac.Resource {
	return []rbac.Resource{
		rbac.ResourceSecrets, rbac.ResourcePods, rbac.ResourceConfigMaps,
		rbac.ResourceStorage, rbac.ResourceNodes, rbac.ResourceServices,
		rbac.ResourceWorkloads, rbac.ResourceIngresses, rbac.ResourceNetworkPolicies,
	}
}

// nativeRuleChargedResources returns the coarse RBAC resources a native rule's
// (apiGroup, resource) must be authorized against. A precisely-named built-in
// maps to its single coarse resource. A wildcard or unrecognised *core* resource
// is charged against the full sensitive set (fail closed) so it cannot be
// authored for the price of clusters:<verb> alone — the escalation the previous
// mapNativeRuleResource("", "*") -> ResourceClusters fallback allowed. Keep the
// per-type mapping in sync with knownNativeK8sResource / the k8s-proxy hook.
func nativeRuleChargedResources(apiGroup, resource string) []rbac.Resource {
	res := strings.ToLower(strings.TrimSpace(resource))
	grp := strings.TrimSpace(apiGroup)
	wildcardResource := res == "*" || res == ""
	coreGroup := grp == ""
	allGroups := grp == "*"

	withClusters := func(rs []rbac.Resource) []rbac.Resource {
		return append(rs, rbac.ResourceClusters)
	}

	if !wildcardResource {
		if mapped, ok := knownNativeK8sResource(res); ok {
			return []rbac.Resource{mapped}
		}
		// Unrecognised resource under a named CRD group → a custom resource.
		if !coreGroup && !allGroups {
			return []rbac.Resource{rbac.ResourceCustomResources}
		}
		// Unrecognised *core* resource (e.g. serviceaccounts): cannot prove it
		// harmless, so fail closed against the full sensitive set.
		return withClusters(sensitiveNativeResources())
	}

	// Wildcard resource: grants every resource in the group's scope.
	switch {
	case coreGroup:
		return withClusters(sensitiveNativeResources())
	case allGroups:
		// apiGroup "*" + resource "*" is cluster-admin equivalent.
		return withClusters(append(sensitiveNativeResources(), rbac.ResourceCustomResources))
	default:
		// "*" within a single named CRD group.
		return []rbac.Resource{rbac.ResourceCustomResources}
	}
}

// knownNativeK8sResource mirrors server.knownK8sProxyResource (which lives in an
// integrator-owned file this handler may not import). Keep the two in sync.
func knownNativeK8sResource(resourceType string) (rbac.Resource, bool) {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "services", "service", "endpoints", "endpoint":
		return rbac.ResourceServices, true
	case "ingresses", "ingress",
		"gateways", "gateway",
		"httproutes", "httproute",
		"gatewayclasses", "gatewayclass",
		"grpcroutes", "grpcroute",
		"tcproutes", "tcproute",
		"udproutes", "udproute",
		"tlsroutes", "tlsroute",
		"referencegrants", "referencegrant":
		return rbac.ResourceIngresses, true
	case "networkpolicies", "networkpolicy":
		return rbac.ResourceNetworkPolicies, true
	case "persistentvolumes", "persistentvolume", "pv",
		"persistentvolumeclaims", "persistentvolumeclaim", "pvc",
		"storageclasses", "storageclass":
		return rbac.ResourceStorage, true
	case "configmaps", "configmap":
		return rbac.ResourceConfigMaps, true
	case "secrets", "secret":
		return rbac.ResourceSecrets, true
	case "pods", "pod":
		return rbac.ResourcePods, true
	case "nodes", "node":
		return rbac.ResourceNodes, true
	case "deployments", "deployment",
		"daemonsets", "daemonset",
		"statefulsets", "statefulset",
		"replicasets", "replicaset",
		"jobs", "job",
		"cronjobs", "cronjob",
		"hpa", "horizontalpodautoscalers", "horizontalpodautoscaler",
		"poddisruptionbudgets", "poddisruptionbudget":
		return rbac.ResourceWorkloads, true
	default:
		return "", false
	}
}

func nativeRulesToResponses(rows []sqlc.NativeRbacRule) []nativeRuleResponse {
	out := make([]nativeRuleResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, nativeRuleToResponse(r))
	}
	return out
}
