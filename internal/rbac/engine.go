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
