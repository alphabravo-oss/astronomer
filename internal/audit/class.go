package audit

import "strings"

// Action classes stored on audit_log.action_class (migration 063).
const (
	ClassMutation = "mutation"
	ClassRead     = "read"
	ClassAuth     = "auth"
	ClassSystem   = "system"
)

// ClassifyActionClass maps an action name and source onto the audit class
// vocabulary. Explicit stored classes (read/auth/system) win; otherwise the
// action prefix and source decide. Worker jobs, agent heartbeats, and
// partition maintenance are system — they stay in the trail but are not
// "someone did this".
func ClassifyActionClass(action, source, stored string) string {
	switch stored {
	case ClassRead, ClassAuth, ClassSystem:
		return stored
	}
	a := strings.ToLower(strings.TrimSpace(action))
	s := strings.ToLower(strings.TrimSpace(source))
	switch {
	case strings.HasPrefix(a, "auth.") || strings.HasPrefix(a, "sso."):
		return ClassAuth
	case strings.HasPrefix(a, "read.") || a == "charlie.http.read":
		return ClassRead
	case s == "worker",
		strings.HasPrefix(a, "agent."),
		strings.HasPrefix(a, "audit_log"),
		strings.Contains(a, "sync_failed"),
		strings.Contains(a, "ensure_partition"),
		strings.Contains(a, "enforce_retention"):
		return ClassSystem
	case stored != "":
		return stored
	default:
		return ClassMutation
	}
}

// PeopleActivitySQL is a boolean SQL fragment (alias `a` = audit_log) that
// is true for operator/user actions and false for automated system rows.
// Used so the default audit page is "what people did" even for rows written
// before action_class was classified.
const PeopleActivitySQL = `NOT (
	a.action_class = 'system'
	OR a.source = 'worker'
	OR a.action LIKE 'agent.%'
	OR a.action LIKE 'audit_log%'
	OR a.action LIKE '%sync_failed'
)`

// SystemActivitySQL is the complement of PeopleActivitySQL.
const SystemActivitySQL = `(
	a.action_class = 'system'
	OR a.source = 'worker'
	OR a.action LIKE 'agent.%'
	OR a.action LIKE 'audit_log%'
	OR a.action LIKE '%sync_failed'
)`

// EffectiveClassSQL is a SQL CASE (alias `a`) that reconstructs class for
// historical rows that were stored as the mutation default.
const EffectiveClassSQL = `(CASE
	WHEN a.action LIKE 'auth.%' OR a.action LIKE 'sso.%' THEN 'auth'
	WHEN a.action LIKE 'read.%' OR a.action = 'charlie.http.read' OR a.action_class = 'read' THEN 'read'
	WHEN a.action_class = 'system' OR a.source = 'worker' OR a.action LIKE 'agent.%' OR a.action LIKE 'audit_log%' OR a.action LIKE '%sync_failed' THEN 'system'
	ELSE 'mutation'
END)`
