package rbac

import "github.com/google/uuid"

// Engine evaluates permissions across the three-tier RBAC model.
type Engine struct{}

// NewEngine creates a new RBAC permission engine.
func NewEngine() *Engine {
	return &Engine{}
}

// CheckPermission evaluates if the given bindings grant access to resource+verb
// at the specified scope (global, cluster, project, and optional namespace).
//
// Check order (first match wins):
//  1. Superuser binding (IsSuperuser=true) short-circuits to true
//  2. Global roles (apply everywhere)
//  3. Cluster roles (if clusterID provided)
//  4. Project roles (if projectID provided)
func (e *Engine) CheckPermission(bindings []RoleBinding, resource Resource, verb Verb, clusterID, projectID uuid.UUID, namespace ...string) bool {
	requestNamespace := ""
	if len(namespace) > 0 {
		requestNamespace = namespace[0]
	}
	for _, b := range bindings {
		if b.IsSuperuser {
			return true
		}
	}
	for _, b := range bindings {
		if !e.bindingApplies(b, clusterID, projectID, requestNamespace) {
			continue
		}
		for _, rule := range b.RoleRules {
			if e.matchRule(rule, resource, verb) {
				return true
			}
		}
	}
	return false
}

// CheckExplicitPermission evaluates resource+verb at the given scope WITHOUT
// the superuser short-circuit: only a matching role rule grants access. This
// is for break-glass guards (e.g. the compliance deletion guard) where the
// implicit superuser bypass would defeat the check — a superuser must hold an
// explicit grant of the override permission, not merely be a superuser.
func (e *Engine) CheckExplicitPermission(bindings []RoleBinding, resource Resource, verb Verb, clusterID, projectID uuid.UUID, namespace ...string) bool {
	requestNamespace := ""
	if len(namespace) > 0 {
		requestNamespace = namespace[0]
	}
	for _, b := range bindings {
		if !e.bindingApplies(b, clusterID, projectID, requestNamespace) {
			continue
		}
		for _, rule := range b.RoleRules {
			if e.matchRule(rule, resource, verb) {
				return true
			}
		}
	}
	return false
}

// CheckSuperuser returns true if any binding marks the user as a superuser.
// This is a convenience helper for callers that only need the bypass check.
func (e *Engine) CheckSuperuser(bindings []RoleBinding) bool {
	for _, b := range bindings {
		if b.IsSuperuser {
			return true
		}
	}
	return false
}

// HasAnyNamespaceAccess reports whether the bindings grant (resource, verb) at
// the given cluster either cluster-wide (a global or cluster-scoped binding with
// no namespace narrowing) OR within at least one namespace-scoped binding on the
// cluster. It exists for the "list" gate: a namespace- or project-scoped user
// who cannot pass a bare cluster-wide CheckPermission must still be allowed to
// reach the list handler (which then filters the results down to their
// authorized namespaces). Superuser short-circuits to true.
func (e *Engine) HasAnyNamespaceAccess(bindings []RoleBinding, resource Resource, verb Verb, clusterID uuid.UUID) bool {
	all, names := e.AuthorizedNamespaces(bindings, resource, verb, clusterID)
	return all || len(names) > 0
}

// AuthorizedNamespaces computes the namespace visibility a set of bindings grants
// for (resource, verb) at a cluster.
//
//   - all==true means the caller may see every namespace: a superuser, or a
//     global/cluster-wide binding (Namespace=="") that grants the permission.
//     When all is true the returned names map is nil and must be ignored.
//   - all==false means the caller is namespace-restricted: names is the exact
//     allow-set of namespaces whose resources they may see. An empty names map
//     with all==false means "no namespaces" — the caller must see nothing.
//
// This is a strict allow-list: only namespaces from a matching namespace-scoped
// binding on this cluster are included. Bindings that carry a namespace but no
// cluster scope are ignored (fail closed), mirroring bindingApplies.
func (e *Engine) AuthorizedNamespaces(bindings []RoleBinding, resource Resource, verb Verb, clusterID uuid.UUID) (bool, map[string]struct{}) {
	for _, b := range bindings {
		if b.IsSuperuser {
			return true, nil
		}
	}
	names := make(map[string]struct{})
	for _, b := range bindings {
		// Determine whether this binding scopes to the target cluster at all.
		// A global binding only counts when it is not narrowed to a namespace
		// (a namespace-only global binding is invalid and fails closed, same as
		// bindingApplies). A cluster binding must match the cluster ID; it may
		// or may not carry a namespace.
		isGlobal := b.ClusterID == "" && b.ProjectID == ""
		clusterMatch := b.ClusterID != "" && b.ClusterID == clusterID.String()
		switch {
		case isGlobal && b.Namespace == "":
			// cluster-wide, eligible
		case clusterMatch:
			// cluster-scoped (namespace optional), eligible
		default:
			continue
		}

		granted := false
		for _, rule := range b.RoleRules {
			if e.matchRule(rule, resource, verb) {
				granted = true
				break
			}
		}
		if !granted {
			continue
		}
		if b.Namespace == "" {
			// A cluster-wide grant subsumes any per-namespace narrowing.
			return true, nil
		}
		names[b.Namespace] = struct{}{}
	}
	return false, names
}

// bindingApplies checks whether a binding is applicable at the given scope.
//   - Global bindings (no ClusterID, no ProjectID) always apply.
//   - Cluster bindings apply when the clusterID matches.
//   - Project bindings apply when the projectID matches.
//   - Namespace-scoped bindings only apply when the request carries the same
//     namespace. They do not apply to broad cluster/project requests.
func (e *Engine) bindingApplies(b RoleBinding, clusterID, projectID uuid.UUID, namespace string) bool {
	if b.Namespace != "" && b.Namespace != namespace {
		return false
	}
	if b.Namespace != "" && b.ClusterID == "" && b.ProjectID == "" {
		return false
	}

	isGlobal := b.ClusterID == "" && b.ProjectID == ""
	if isGlobal {
		return true
	}

	if b.ClusterID != "" && b.ClusterID == clusterID.String() {
		return true
	}

	if b.ProjectID != "" && b.ProjectID == projectID.String() {
		return true
	}

	return false
}

// matchRule checks if a single rule grants the requested permission.
func (e *Engine) matchRule(rule Rule, resource Resource, verb Verb) bool {
	// Check resource match
	if rule.Resource != "*" && rule.Resource != string(resource) {
		return false
	}

	// Check verb match
	for _, v := range rule.Verbs {
		if v == "*" || v == string(verb) {
			return true
		}
	}

	return false
}
