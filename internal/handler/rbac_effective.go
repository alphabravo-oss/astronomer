package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type effectivePermissionResponse struct {
	Subject     effectivePermissionSubject `json:"subject"`
	Context     effectivePermissionContext `json:"context"`
	Bindings    []effectiveBinding         `json:"bindings"`
	Permissions []effectivePermissionGrant `json:"permissions"`
}

type effectivePermissionSubject struct {
	UserID string `json:"user_id"`
	Self   bool   `json:"self"`
}

type effectivePermissionContext struct {
	ClusterID                        string   `json:"cluster_id,omitempty"`
	ProjectID                        string   `json:"project_id,omitempty"`
	Namespace                        string   `json:"namespace,omitempty"`
	NamespaceScopedBindingsSupported bool     `json:"namespace_scoped_bindings_supported"`
	Warnings                         []string `json:"warnings,omitempty"`
}

type effectiveBinding struct {
	Scope     string      `json:"scope"`
	BindingID string      `json:"binding_id,omitempty"`
	RoleID    string      `json:"role_id,omitempty"`
	RoleName  string      `json:"role_name,omitempty"`
	Group     string      `json:"group,omitempty"`
	ClusterID string      `json:"cluster_id,omitempty"`
	ProjectID string      `json:"project_id,omitempty"`
	Namespace string      `json:"namespace,omitempty"`
	Rules     []rbac.Rule `json:"rules"`
}

type effectivePermissionGrant struct {
	Resource         string `json:"resource"`
	Verb             string `json:"verb"`
	AppliesToContext bool   `json:"applies_to_context"`
	// Inherited distinguishes a permission a role template contributes through
	// its Inherits chain from one it declares directly; InheritedFrom names the
	// template that declared it. Both are zero-valued (and omitted) for direct
	// grants and for binding-derived permissions, so the response shape is
	// unchanged for callers that never touched template inheritance.
	Inherited     bool                        `json:"inherited,omitempty"`
	InheritedFrom string                      `json:"inherited_from,omitempty"`
	Sources       []effectivePermissionSource `json:"sources"`
}

type effectivePermissionSource struct {
	Scope     string `json:"scope"`
	BindingID string `json:"binding_id,omitempty"`
	RoleID    string `json:"role_id,omitempty"`
	RoleName  string `json:"role_name,omitempty"`
	ClusterID string `json:"cluster_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type permissionPreviewRequest struct {
	Scope        string          `json:"scope"`
	RoleID       string          `json:"role_id"`
	TemplateName string          `json:"template_name"`
	Rules        json.RawMessage `json:"rules"`
	ClusterID    string          `json:"cluster_id"`
	ProjectID    string          `json:"project_id"`
}

type permissionPreviewResponse struct {
	Scope          string                     `json:"scope"`
	RoleID         string                     `json:"role_id,omitempty"`
	RoleName       string                     `json:"role_name,omitempty"`
	TemplateName   string                     `json:"template_name,omitempty"`
	RiskLevel      string                     `json:"risk_level"`
	Warnings       []string                   `json:"warnings,omitempty"`
	Permissions    []effectivePermissionGrant `json:"permissions"`
	SensitiveFlags permissionSensitiveFlags   `json:"sensitive_flags"`
}

type permissionSensitiveFlags struct {
	Wildcard       bool `json:"wildcard"`
	CanMutate      bool `json:"can_mutate"`
	CanDelete      bool `json:"can_delete"`
	CanExec        bool `json:"can_exec"`
	CanProxy       bool `json:"can_proxy"`
	CanReadSecrets bool `json:"can_read_secrets"`
	CanManageRBAC  bool `json:"can_manage_rbac"`
	CanRestore     bool `json:"can_restore"`
}

func (h *RBACHandler) MyEffectivePermissions(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	h.respondEffectivePermissions(w, r, user.ID, true)
}

func (h *RBACHandler) EffectivePermissionsForUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	if userID == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "User ID is required")
		return
	}
	if _, err := uuid.Parse(userID); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	current, _ := middleware.GetAuthenticatedUser(r.Context())
	self := current != nil && current.ID == userID
	h.respondEffectivePermissions(w, r, userID, self)
}

func (h *RBACHandler) PermissionPreview(w http.ResponseWriter, r *http.Request) {
	var req permissionPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = string(rbac.ScopeGlobal)
	}
	if !rbac.IsValidScope(rbac.Scope(scope)) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidScope, "scope must be global, cluster, or project")
		return
	}

	rules, roleName, grants, err := h.previewRules(r.Context(), req, scope)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.PreviewError, err.Error())
		return
	}
	source := effectivePermissionSource{Scope: scope, RoleID: req.RoleID, RoleName: roleName}
	// A template preview returns provenance-annotated grants (so the UI can flag
	// inherited permissions); role/raw-rule previews have no inheritance and
	// fall back to the flat rule expansion.
	permissions := grantsFromRules(rules, source)
	if grants != nil {
		permissions = grantsFromEffective(grants, source)
	}
	warnings := previewWarnings(scope, req, rules)
	RespondJSON(w, http.StatusOK, permissionPreviewResponse{
		Scope:          scope,
		RoleID:         req.RoleID,
		RoleName:       roleName,
		TemplateName:   req.TemplateName,
		RiskLevel:      rbac.RiskLevelForRules(rules),
		Warnings:       warnings,
		Permissions:    permissions,
		SensitiveFlags: sensitiveFlagsForRules(rules),
	})
}

func (h *RBACHandler) respondEffectivePermissions(w http.ResponseWriter, r *http.Request, userID string, self bool) {
	if h == nil || h.bindings == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RBACUnavailable, "RBAC binding lookup is not configured")
		return
	}
	selectedContext, err := effectivePermissionContextFromRequest(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidContext, err.Error())
		return
	}
	bindings, err := h.bindings.GetUserBindings(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LoadError, "Failed to load user bindings")
		return
	}
	response := effectivePermissionResponse{
		Subject:  effectivePermissionSubject{UserID: userID, Self: self},
		Context:  selectedContext,
		Bindings: make([]effectiveBinding, 0, len(bindings)),
	}
	for _, binding := range bindings {
		response.Bindings = append(response.Bindings, effectiveBinding{
			Scope:     binding.Scope,
			BindingID: binding.BindingID,
			RoleID:    binding.RoleID,
			RoleName:  binding.RoleName,
			Group:     binding.Group,
			ClusterID: binding.ClusterID,
			ProjectID: binding.ProjectID,
			Namespace: binding.Namespace,
			Rules:     binding.RoleRules,
		})
		source := effectivePermissionSource{
			Scope:     binding.Scope,
			BindingID: binding.BindingID,
			RoleID:    binding.RoleID,
			RoleName:  binding.RoleName,
			ClusterID: binding.ClusterID,
			ProjectID: binding.ProjectID,
			Namespace: binding.Namespace,
		}
		response.Permissions = mergeGrants(response.Permissions, grantsFromRules(binding.RoleRules, source))
	}
	for i := range response.Permissions {
		response.Permissions[i].AppliesToContext = grantAppliesToContext(response.Permissions[i], selectedContext)
	}
	sortEffectivePermissions(response.Permissions)
	RespondJSON(w, http.StatusOK, response)
}

func effectivePermissionContextFromRequest(r *http.Request) (effectivePermissionContext, error) {
	values := r.URL.Query()
	out := effectivePermissionContext{
		ClusterID: strings.TrimSpace(values.Get("cluster_id")),
		ProjectID: strings.TrimSpace(values.Get("project_id")),
		Namespace: strings.TrimSpace(values.Get("namespace")),
	}
	if out.ClusterID != "" {
		if _, err := uuid.Parse(out.ClusterID); err != nil {
			return out, errString("cluster_id must be a UUID")
		}
	}
	if out.ProjectID != "" {
		if _, err := uuid.Parse(out.ProjectID); err != nil {
			return out, errString("project_id must be a UUID")
		}
	}
	if out.Namespace != "" {
		if errs := k8svalidation.IsDNS1123Label(out.Namespace); len(errs) > 0 {
			return out, errString("namespace must be a valid Kubernetes namespace")
		}
		out.Warnings = append(out.Warnings, "namespace context is enforced for namespace-scoped bindings when present; binding storage and assignment UI are still pending")
	}
	return out, nil
}

// previewRules resolves the rule set to preview. The third return value is the
// flattened, provenance-annotated grant list; it is non-nil only for template
// previews (where inheritance can contribute grants) and lets the caller render
// direct vs inherited permissions. Role and raw-rule previews return nil grants
// and the caller expands their flat rules instead.
func (h *RBACHandler) previewRules(ctx context.Context, req permissionPreviewRequest, scope string) ([]rbac.Rule, string, []rbac.EffectiveGrant, error) {
	switch {
	case req.TemplateName != "":
		if h.templates == nil {
			return nil, "", nil, errString("RBAC template catalog not loaded")
		}
		tmpl, ok := h.templates.Get(req.TemplateName)
		if !ok {
			return nil, "", nil, errString("template not found")
		}
		if string(tmpl.Scope) != scope {
			return nil, "", nil, errString("template scope does not match requested scope")
		}
		// EffectiveRules flattens the Inherits chain so risk/flags reflect the
		// full permission set; EffectiveGrants carries the direct/inherited
		// provenance for the rendered permissions.
		return tmpl.EffectiveRules(), tmpl.DisplayName, tmpl.EffectiveGrants(), nil
	case req.RoleID != "":
		if h.queries == nil {
			return nil, "", nil, errString("RBAC role lookup is not configured")
		}
		id, err := uuid.Parse(req.RoleID)
		if err != nil {
			return nil, "", nil, errString("invalid role_id")
		}
		rules, name, err := h.rulesForRoleID(ctx, scope, id)
		return rules, name, nil, err
	case len(req.Rules) > 0:
		var rules []rbac.Rule
		if err := json.Unmarshal(req.Rules, &rules); err != nil {
			return nil, "", nil, errString("invalid rules")
		}
		if len(rules) == 0 {
			return nil, "", nil, errString("rules must contain at least one entry")
		}
		return rules, "", nil, nil
	default:
		return nil, "", nil, errString("template_name, role_id, or rules is required")
	}
}

func (h *RBACHandler) rulesForRoleID(ctx context.Context, scope string, id uuid.UUID) ([]rbac.Rule, string, error) {
	var raw json.RawMessage
	name := ""
	switch rbac.Scope(scope) {
	case rbac.ScopeGlobal:
		role, err := h.queries.GetGlobalRoleByID(ctx, id)
		if err != nil {
			return nil, "", errString("global role not found")
		}
		raw = role.Rules
		name = role.Name
	case rbac.ScopeCluster:
		role, err := h.queries.GetClusterRoleByID(ctx, id)
		if err != nil {
			return nil, "", errString("cluster role not found")
		}
		raw = role.Rules
		name = role.Name
	case rbac.ScopeProject:
		role, err := h.queries.GetProjectRoleByID(ctx, id)
		if err != nil {
			return nil, "", errString("project role not found")
		}
		raw = role.Rules
		name = role.Name
	}
	var rules []rbac.Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, "", errString("role rules are invalid")
	}
	return rules, name, nil
}

func grantsFromRules(rules []rbac.Rule, source effectivePermissionSource) []effectivePermissionGrant {
	grants := make([]effectivePermissionGrant, 0)
	for _, rule := range rules {
		for _, verb := range rule.Verbs {
			grants = append(grants, effectivePermissionGrant{
				Resource:         rule.Resource,
				Verb:             verb,
				AppliesToContext: true,
				Sources:          []effectivePermissionSource{source},
			})
		}
	}
	sortEffectivePermissions(grants)
	return grants
}

// grantsFromEffective renders a template's flattened, provenance-annotated
// grant set into the preview response, preserving the direct/inherited
// distinction so the UI can tell which permissions come from an inherited
// template.
func grantsFromEffective(grants []rbac.EffectiveGrant, source effectivePermissionSource) []effectivePermissionGrant {
	out := make([]effectivePermissionGrant, 0, len(grants))
	for _, g := range grants {
		out = append(out, effectivePermissionGrant{
			Resource:         g.Resource,
			Verb:             g.Verb,
			AppliesToContext: true,
			Inherited:        g.Inherited,
			InheritedFrom:    g.InheritedFrom,
			Sources:          []effectivePermissionSource{source},
		})
	}
	sortEffectivePermissions(out)
	return out
}

func mergeGrants(existing, incoming []effectivePermissionGrant) []effectivePermissionGrant {
	index := make(map[string]int, len(existing))
	for i, grant := range existing {
		index[grant.Resource+"\x00"+grant.Verb] = i
	}
	for _, grant := range incoming {
		key := grant.Resource + "\x00" + grant.Verb
		if i, ok := index[key]; ok {
			existing[i].Sources = append(existing[i].Sources, grant.Sources...)
			continue
		}
		index[key] = len(existing)
		existing = append(existing, grant)
	}
	return existing
}

func grantAppliesToContext(grant effectivePermissionGrant, context effectivePermissionContext) bool {
	if context.ClusterID == "" && context.ProjectID == "" {
		return true
	}
	for _, source := range grant.Sources {
		if sourceAppliesToContext(source, context) {
			return true
		}
	}
	return false
}

func sourceAppliesToContext(source effectivePermissionSource, context effectivePermissionContext) bool {
	if source.Namespace != "" {
		if context.Namespace == "" || source.Namespace != context.Namespace {
			return false
		}
		if source.ClusterID == "" && source.ProjectID == "" {
			return false
		}
	}
	if source.ClusterID == "" && source.ProjectID == "" {
		return true
	}
	if context.ClusterID != "" && source.ClusterID == context.ClusterID {
		return true
	}
	if context.ProjectID != "" && source.ProjectID == context.ProjectID {
		return true
	}
	return false
}

func sortEffectivePermissions(grants []effectivePermissionGrant) {
	sort.SliceStable(grants, func(i, j int) bool {
		if grants[i].Resource != grants[j].Resource {
			return grants[i].Resource < grants[j].Resource
		}
		return grants[i].Verb < grants[j].Verb
	})
}

func sensitiveFlagsForRules(rules []rbac.Rule) permissionSensitiveFlags {
	var flags permissionSensitiveFlags
	for _, rule := range rules {
		resource := rule.Resource
		for _, verb := range rule.Verbs {
			if resource == "*" || verb == "*" {
				flags.Wildcard = true
				flags.CanMutate = true
			}
			switch verb {
			case string(rbac.VerbCreate), string(rbac.VerbUpdate), string(rbac.VerbDelete), string(rbac.VerbScale), string(rbac.VerbRestart), string(rbac.VerbManage), string(rbac.VerbSync):
				flags.CanMutate = true
			}
			if verb == string(rbac.VerbDelete) || verb == "*" {
				flags.CanDelete = true
			}
			if resource == string(rbac.ResourcePods) && (verb == string(rbac.VerbExec) || verb == "*") {
				flags.CanExec = true
			}
			if verb == string(rbac.VerbProxy) || verb == "*" {
				flags.CanProxy = true
			}
			if resource == string(rbac.ResourceSecrets) && (verb == string(rbac.VerbRead) || verb == string(rbac.VerbList) || verb == "*") {
				flags.CanReadSecrets = true
			}
			if resource == string(rbac.ResourceRBAC) && (verb == string(rbac.VerbCreate) || verb == string(rbac.VerbUpdate) || verb == string(rbac.VerbDelete) || verb == "*") {
				flags.CanManageRBAC = true
			}
			if resource == string(rbac.ResourceBackups) && (verb == string(rbac.VerbManage) || verb == "*") {
				flags.CanRestore = true
			}
		}
	}
	return flags
}

func previewWarnings(scope string, req permissionPreviewRequest, rules []rbac.Rule) []string {
	warnings := make([]string, 0, 4)
	if scope == string(rbac.ScopeCluster) && req.ClusterID == "" {
		warnings = append(warnings, "cluster_id was not provided; preview is scope-only")
	}
	if scope == string(rbac.ScopeProject) && req.ProjectID == "" {
		warnings = append(warnings, "project_id was not provided; preview is scope-only")
	}
	flags := sensitiveFlagsForRules(rules)
	if flags.Wildcard {
		warnings = append(warnings, "wildcard permissions grant broad access")
	}
	if flags.CanExec {
		warnings = append(warnings, "pod exec can expose workload credentials and runtime state")
	}
	if flags.CanReadSecrets {
		warnings = append(warnings, "secret read access can expose credentials")
	}
	if flags.CanRestore {
		warnings = append(warnings, "restore permissions can overwrite live resources")
	}
	return warnings
}

type errString string

func (e errString) Error() string { return string(e) }
