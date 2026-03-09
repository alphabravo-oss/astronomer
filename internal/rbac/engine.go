package rbac

import "github.com/google/uuid"

// Engine evaluates permissions across the three-tier RBAC model.
type Engine struct{}

// NewEngine creates a new RBAC permission engine.
func NewEngine() *Engine {
	return &Engine{}
}

// CheckPermission evaluates if the given bindings grant access to resource+verb
// at the specified scope (global, cluster, or project).
//
// Check order (first match wins):
//  1. Global roles (apply everywhere)
//  2. Cluster roles (if clusterID provided)
//  3. Project roles (if projectID provided)
func (e *Engine) CheckPermission(bindings []RoleBinding, resource Resource, verb Verb, clusterID, projectID uuid.UUID) bool {
	for _, b := range bindings {
		if !e.bindingApplies(b, clusterID, projectID) {
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

// bindingApplies checks whether a binding is applicable at the given scope.
// - Global bindings (no ClusterID, no ProjectID) always apply.
// - Cluster bindings apply when the clusterID matches.
// - Project bindings apply when the projectID matches.
func (e *Engine) bindingApplies(b RoleBinding, clusterID, projectID uuid.UUID) bool {
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
