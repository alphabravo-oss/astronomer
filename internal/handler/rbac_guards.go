package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (h *RBACHandler) getGlobalRole(w http.ResponseWriter, r *http.Request) (sqlc.GlobalRole, bool) {
	id, ok := parseUUIDURLParam(w, r, "id", "role")
	if !ok {
		return sqlc.GlobalRole{}, false
	}
	role, err := h.queries.GetGlobalRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return sqlc.GlobalRole{}, false
	}
	return role, true
}

// guardGlobalBinding enforces the privilege-escalation check for a global role
// binding: it loads the target role and rejects unless the caller already holds
// every rule at global scope (or is a superuser). See enforceNoEscalation.
func (h *RBACHandler) guardGlobalBinding(w http.ResponseWriter, r *http.Request, roleID uuid.UUID) bool {
	if h.engine == nil || h.bindings == nil {
		return true
	}
	role, err := h.queries.GetGlobalRoleByID(r.Context(), roleID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return false
	}
	return h.enforceNoEscalation(w, r, role.Rules, uuid.UUID{}, uuid.UUID{}, "")
}

// guardClusterBinding is the cluster-scoped counterpart of guardGlobalBinding.
// The caller must hold each target rule at the given cluster (and namespace, if
// the binding is namespace-narrowed) scope.
func (h *RBACHandler) guardClusterBinding(w http.ResponseWriter, r *http.Request, roleID, clusterID uuid.UUID, namespace string) bool {
	if h.engine == nil || h.bindings == nil {
		return true
	}
	role, err := h.queries.GetClusterRoleByID(r.Context(), roleID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster role not found")
		return false
	}
	return h.enforceNoEscalation(w, r, role.Rules, clusterID, uuid.UUID{}, namespace)
}

// guardProjectBinding is the project-scoped counterpart of guardGlobalBinding.
func (h *RBACHandler) guardProjectBinding(w http.ResponseWriter, r *http.Request, roleID, projectID uuid.UUID) bool {
	if h.engine == nil || h.bindings == nil {
		return true
	}
	role, err := h.queries.GetProjectRoleByID(r.Context(), roleID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project role not found")
		return false
	}
	return h.enforceNoEscalation(w, r, role.Rules, uuid.UUID{}, projectID, "")
}

// guardGlobalRoleRules is the role-definition counterpart of
// guardGlobalBinding: it gates a write to the role itself rather than to a
// binding. Without it the binding guard is trivially bypassed — instead of
// granting themselves a stronger role, a caller holding rbac:update rewrites
// the rules of a role they are already bound to, and h.invalidateAll() makes it
// effective on the next request. rules is the incoming rule set.
func (h *RBACHandler) guardGlobalRoleRules(w http.ResponseWriter, r *http.Request, id uuid.UUID, rules json.RawMessage) bool {
	existing, err := h.queries.GetGlobalRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Global role not found")
		return false
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return false
	}
	if h.engine == nil || h.bindings == nil {
		return true
	}
	// Global scope (all-zero IDs): a role definition is not attached to a
	// cluster or project — it can be bound anywhere — so the caller must hold
	// each rule everywhere. It is also the scope the route middleware itself
	// resolves for these paths, which carry no cluster_id/project_id param.
	return h.enforceNoEscalation(w, r, rules, uuid.UUID{}, uuid.UUID{}, "")
}

// guardClusterRoleRules is the cluster-role-definition counterpart.
func (h *RBACHandler) guardClusterRoleRules(w http.ResponseWriter, r *http.Request, id uuid.UUID, rules json.RawMessage) bool {
	existing, err := h.queries.GetClusterRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster role not found")
		return false
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return false
	}
	if h.engine == nil || h.bindings == nil {
		return true
	}
	return h.enforceNoEscalation(w, r, rules, uuid.UUID{}, uuid.UUID{}, "")
}

// guardProjectRoleRules is the project-role-definition counterpart.
func (h *RBACHandler) guardProjectRoleRules(w http.ResponseWriter, r *http.Request, id uuid.UUID, rules json.RawMessage) bool {
	existing, err := h.queries.GetProjectRoleByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project role not found")
		return false
	}
	if rejectBuiltinRoleWrite(w, r, existing.IsBuiltin) {
		return false
	}
	if h.engine == nil || h.bindings == nil {
		return true
	}
	return h.enforceNoEscalation(w, r, rules, uuid.UUID{}, uuid.UUID{}, "")
}

// rejectBuiltinRoleWrite freezes the seeded built-in roles against update and
// delete. Returns true (and writes the 403) when the write must be refused.
//
// is_builtin is MIGRATION-OWNED and must stay that way: this freeze is
// permanent and unrecoverable through the API, so the create handlers
// deliberately do not read the flag off the request body (see roleRequest).
// Otherwise any caller holding rbac:create could plant roles nobody — superuser
// included — can ever edit or delete.
//
// Rancher's equivalent (pkg/api/norman/customization/globalrole/validator.go)
// strips the mutable fields from a PUT to a builtin instead of failing, because
// one field (newUserDefault) legitimately stays writable there. Our role rows
// have no such field — name, display_name, description, permissions and rules
// are the whole record — so stripping would turn every PUT into a 200 that
// changed nothing while recording a role.update audit event that never
// happened. We reject instead, so the operator learns the truth. 403 rather
// than 409: the refusal is permanent and independent of the request body, and
// it matches how the rest of this surface reports "not allowed". Built-ins are
// reconciled by migrations (see 141) so an accepted edit would not survive the
// next upgrade anyway.
//
// Deliberately NOT here: an escalation check on delete. Deleting a role hands
// the caller no permission, and requiring the caller to hold the deleted role's
// rules would take an ability away from a role that ships today — the seeded
// 'RBAC Administrator' (migration 032: rbac:* plus users:read/list) could no
// longer clean up any operator-authored role carrying permissions beyond its
// own, with no migration able to give that back. Kubernetes draws the same line:
// its escalate check applies only to writes that create rules. The lockout half
// of the finding is closed by the built-in freeze above — the cascade from
// global_role_bindings can no longer take out the seeded 'Administrator' row.
func rejectBuiltinRoleWrite(w http.ResponseWriter, r *http.Request, isBuiltin bool) bool {
	if !isBuiltin {
		return false
	}
	RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden,
		"Built-in roles cannot be modified or deleted")
	return true
}

// enforceNoEscalation implements Kubernetes' "you cannot grant permissions you
// do not hold" escalate/bind guard. For every (resource, verb) in the target
// role's rules, the CALLER must already hold that permission at the binding's
// scope (clusterID/projectID/namespace identify the scope; all zero means
// global). Superusers bypass via the engine's IsSuperuser short-circuit. It
// writes a 403 and returns false on denial; true means the binding may proceed.
//
// Wildcard semantics come straight from the engine: a caller holding only
// rbac:* is NOT allowed to grant a role carrying resource "*" — the engine only
// matches a request for resource "*" against a caller rule whose resource is
// itself "*". So self-escalation to full admin requires the caller to already
// be full admin.
//
// Callers gate this behind an engine/bindings nil-check (see the guard*Binding
// wrappers), preserving the handler's optional-authorization contract used by
// unit tests and pre-authorization deployments.
func (h *RBACHandler) enforceNoEscalation(w http.ResponseWriter, r *http.Request, rawRules json.RawMessage, clusterID, projectID uuid.UUID, namespace string) bool {
	targetRules, err := decodeRoleRules(rawRules)
	if err != nil {
		// A role whose rules we cannot parse cannot be safely granted; fail closed.
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to decode target role rules")
		return false
	}
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to grant this role")
		return false
	}
	callerBindings, err := h.bindings.GetUserBindings(r.Context(), user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load caller bindings")
		return false
	}
	for _, rule := range targetRules {
		if rule.IsCRDGrant() {
			for _, group := range rule.CRDAPIGroups() {
				if escalationGroups[strings.ToLower(strings.TrimSpace(group))] {
					RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden,
						"CRD grants cannot target privilege-escalation API groups")
					return false
				}
				for _, resource := range rule.ResourceNames() {
					charged := nativeRuleChargedResources(group, resource)
					for _, verb := range rbac.NormalizeNativeVerbs(rule.Verbs) {
						for _, mapped := range charged {
							if !h.engine.CheckPermission(callerBindings, mapped, rbac.Verb(verb), clusterID, projectID, namespace) {
								RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Cannot grant a role that includes permissions you do not hold")
								return false
							}
						}
					}
				}
			}
			continue
		}
		for _, verb := range rule.Verbs {
			if !h.engine.CheckPermission(callerBindings, rbac.Resource(rule.Resource), rbac.Verb(verb), clusterID, projectID, namespace) {
				RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Cannot grant a role that includes permissions you do not hold")
				return false
			}
		}
	}
	return true
}

func rejectGlobalCRDGrants(w http.ResponseWriter, r *http.Request, raw json.RawMessage) bool {
	rules, err := decodeRoleRules(raw)
	if err != nil {
		return false
	}
	for _, rule := range rules {
		if rule.IsCRDGrant() {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody,
				"CRD grants belong on cluster or project roles, not global roles")
			return true
		}
	}
	return false
}

// decodeRoleRules parses a role's rules JSONB into the RBAC rule slice. Empty
// input yields no rules (a role that grants nothing).
func decodeRoleRules(raw json.RawMessage) ([]rbac.Rule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rules []rbac.Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func parseUUIDURLParam(w http.ResponseWriter, r *http.Request, param, label string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid "+label+" ID")
		return uuid.UUID{}, false
	}
	return id, true
}

func defaultJSONArray(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}

func roleDocuments(w http.ResponseWriter, r *http.Request, req roleRequest) (json.RawMessage, json.RawMessage, bool) {
	permissions := defaultJSONObject(req.Permissions)
	rules := defaultJSONArray(req.Rules)

	var permissionObject map[string]json.RawMessage
	if err := json.Unmarshal(permissions, &permissionObject); err != nil || permissionObject == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Role permissions must be a JSON object")
		return nil, nil, false
	}
	var ruleArray []json.RawMessage
	if err := json.Unmarshal(rules, &ruleArray); err != nil || ruleArray == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Role rules must be a JSON array")
		return nil, nil, false
	}
	return permissions, rules, true
}

// rejectGroupBinding blocks the manual role-binding API from creating
// group-scoped (or user-less) bindings. Group-scoped bindings are stored
// and indexed but never expanded at authorization time — GetUserBindings /
// ListUserBindingsWithRoles resolve strictly by user_id, so a binding with
// no user_id (or a "group" set) silently grants nothing. Group membership
// is driven by identity group mappings, not this endpoint. We fail closed
// with a 400 so operators notice instead of trusting a no-op grant.
//
// Returns true (and writes the 400) when the request must be rejected;
// false means the binding carries a concrete user_id and may proceed.
func rejectGroupBinding(w http.ResponseWriter, r *http.Request, userID, group string) bool {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(group) != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
			"group bindings are managed via identity group mappings, not the manual binding API")
		return true
	}
	return false
}

func parseBindingRefs(w http.ResponseWriter, r *http.Request, roleIDValue, userIDValue string) (uuid.UUID, pgtype.UUID, bool) {
	roleID, err := uuid.Parse(roleIDValue)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Role ID is required")
		return uuid.UUID{}, pgtype.UUID{}, false
	}
	if userIDValue == "" {
		return roleID, pgtype.UUID{}, true
	}
	userID, err := uuid.Parse(userIDValue)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return uuid.UUID{}, pgtype.UUID{}, false
	}
	return roleID, pgtype.UUID{Bytes: userID, Valid: true}, true
}
