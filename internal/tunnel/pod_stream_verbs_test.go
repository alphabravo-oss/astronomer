package tunnel

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// The WebSocket exec/logs consumers authorize on pods:exec / pods:logs — the
// same vocabulary the HTTP k8s-proxy path uses — rather than the clusters:update
// / clusters:read they used to. These tests pin both directions of that move
// against the EXACT grant sets shipped roles carry, so neither the over-grant
// nor the parity fix can silently regress.

// platformOperatorRules is the exact grant set of the 'Platform Operator'
// built-in in the canonical initial schema and of
// internal/rbac/templates/platform-operator.yaml. Note what it does NOT have:
// any pods rule at all.
func platformOperatorRules() []rbac.Rule {
	return []rbac.Rule{
		{Resource: "clusters", Verbs: []string{"create", "read", "update", "list"}},
		{Resource: "agents", Verbs: []string{"create", "read", "update", "list"}},
		{Resource: "projects", Verbs: []string{"create", "read", "update", "list"}},
		{Resource: "workloads", Verbs: []string{"read", "list"}},
		{Resource: "monitoring", Verbs: []string{"read", "list"}},
		{Resource: "alerts", Verbs: []string{"create", "read", "update", "list"}},
		{Resource: "catalog", Verbs: []string{"read", "list"}},
		{Resource: "backups", Verbs: []string{"create", "read", "update", "list"}},
		{Resource: "audit_logs", Verbs: []string{"read", "list"}},
	}
}

// clusterTroubleshooterRules is the exact grant set of the 'Cluster
// Troubleshooter' built-in in the canonical initial schema. pods:exec and
// pods:logs, clusters:read — and deliberately no clusters:update.
func clusterTroubleshooterRules() []rbac.Rule {
	return []rbac.Rule{
		{Resource: "clusters", Verbs: []string{"read"}},
		{Resource: "workloads", Verbs: []string{"read", "list"}},
		{Resource: "pods", Verbs: []string{"read", "list", "watch", "logs", "exec", "proxy"}},
		{Resource: "monitoring", Verbs: []string{"read", "list"}},
		{Resource: "alerts", Verbs: []string{"read", "list"}},
	}
}

// monitoringViewerRules is the exact grant set of the 'Monitoring Viewer'
// built-in (098). It is the canonical clusters:read-without-pods shape shared
// by the audit / GitOps / catalog viewer roles.
func monitoringViewerRules() []rbac.Rule {
	return []rbac.Rule{
		{Resource: "monitoring", Verbs: []string{"read", "list"}},
		{Resource: "alerts", Verbs: []string{"read", "list"}},
		{Resource: "clusters", Verbs: []string{"read", "list"}},
		{Resource: "projects", Verbs: []string{"read", "list"}},
	}
}

// loggingViewerRules is the exact grant set of the 'Logging Viewer' built-in
// (098) — the catalog's designated pod-log reader.
func loggingViewerRules() []rbac.Rule {
	return []rbac.Rule{
		{Resource: "logging", Verbs: []string{"read", "list"}},
		{Resource: "clusters", Verbs: []string{"read", "list"}},
		{Resource: "projects", Verbs: []string{"read", "list"}},
		{Resource: "pods", Verbs: []string{"read", "list", "logs"}},
	}
}

// supportEngineerRules mirrors internal/rbac/templates/support-engineer.yaml:
// read-everything plus pods:[logs,exec], and no clusters:update.
func supportEngineerRules() []rbac.Rule {
	return []rbac.Rule{
		{Resource: "clusters", Verbs: []string{"read", "list"}},
		{Resource: "projects", Verbs: []string{"read", "list"}},
		{Resource: "workloads", Verbs: []string{"read", "list"}},
		{Resource: "pods", Verbs: []string{"read", "list", "watch", "logs", "exec"}},
		{Resource: "audit_logs", Verbs: []string{"read", "list"}},
	}
}

func globalRoleBinding(rules []rbac.Rule) []rbac.RoleBinding {
	return []rbac.RoleBinding{{RoleRules: rules}}
}

func clusterRoleBinding(clusterID uuid.UUID, rules []rbac.Rule) []rbac.RoleBinding {
	return []rbac.RoleBinding{{ClusterID: clusterID.String(), RoleRules: rules}}
}

// TestLogsConsumer_ClustersReadWithoutPodsLogsIsDenied closes the over-grant:
// holding a cluster read no longer streams every container's log (which carries
// bearer tokens and PII) in every namespace.
func TestLogsConsumer_ClustersReadWithoutPodsLogsIsDenied(t *testing.T) {
	clusterID := uuid.New()
	lc := NewLogsConsumer(nil, nil)
	lc.SetAuthorization(rbac.NewEngine(), &mockRBACQuerier{bindings: globalRoleBinding(monitoringViewerRules())})

	if lc.authorizeCluster(context.Background(), uuid.New(), clusterID, "default") {
		t.Fatal("clusters:read with no pods grant must not stream pod logs")
	}
}

// TestLogsConsumer_PodsLogsIsAllowed is the other half: the granular grant the
// catalog actually hands out for log reading is now the one consulted.
func TestLogsConsumer_PodsLogsIsAllowed(t *testing.T) {
	clusterID := uuid.New()
	lc := NewLogsConsumer(nil, nil)
	lc.SetAuthorization(rbac.NewEngine(), &mockRBACQuerier{bindings: globalRoleBinding(loggingViewerRules())})

	if !lc.authorizeCluster(context.Background(), uuid.New(), clusterID, "default") {
		t.Fatal("'Logging Viewer' holds pods:logs and must keep log streaming")
	}
}

// TestExecConsumer_PlatformOperatorIsDeniedExec pins the AUTHORIZED privilege
// reduction. 'Platform Operator' holds clusters:update and zero pods rules, so
// it used to get an interactive shell in any container on any adopted cluster.
// It must not any more. This is a deliberate behaviour change — if this test
// ever starts failing, the reduction has been reverted, not fixed.
func TestExecConsumer_PlatformOperatorIsDeniedExec(t *testing.T) {
	clusterID := uuid.New()
	ec := NewExecConsumer(nil, nil)
	ec.SetAuthorization(rbac.NewEngine(), &mockRBACQuerier{bindings: globalRoleBinding(platformOperatorRules())})

	if ec.authorizeCluster(context.Background(), uuid.New(), clusterID, "default") {
		t.Fatal("'Platform Operator' holds no pods grant and must not get an interactive shell")
	}
}

// TestLogsConsumer_PlatformOperatorIsDeniedLogs is the logs half of the same
// reduction.
func TestLogsConsumer_PlatformOperatorIsDeniedLogs(t *testing.T) {
	clusterID := uuid.New()
	lc := NewLogsConsumer(nil, nil)
	lc.SetAuthorization(rbac.NewEngine(), &mockRBACQuerier{bindings: globalRoleBinding(platformOperatorRules())})

	if lc.authorizeCluster(context.Background(), uuid.New(), clusterID, "default") {
		t.Fatal("'Platform Operator' holds no pods grant and must not stream pod logs")
	}
}

// TestExecConsumer_ClusterTroubleshooterIsAllowedExec is the parity half: a role
// built for pod diagnostics holds pods:exec but not clusters:update, so the
// terminal used to 403 for its intended persona.
func TestExecConsumer_ClusterTroubleshooterIsAllowedExec(t *testing.T) {
	clusterID := uuid.New()
	ec := NewExecConsumer(nil, nil)
	ec.SetAuthorization(rbac.NewEngine(), &mockRBACQuerier{bindings: clusterRoleBinding(clusterID, clusterTroubleshooterRules())})

	if !ec.authorizeCluster(context.Background(), uuid.New(), clusterID, "default") {
		t.Fatal("'Cluster Troubleshooter' holds pods:exec and must be able to exec")
	}
}

// TestExecConsumer_SupportEngineerIsAllowedExec covers the same parity gap for
// the global-scope support template.
func TestExecConsumer_SupportEngineerIsAllowedExec(t *testing.T) {
	clusterID := uuid.New()
	ec := NewExecConsumer(nil, nil)
	ec.SetAuthorization(rbac.NewEngine(), &mockRBACQuerier{bindings: globalRoleBinding(supportEngineerRules())})

	if !ec.authorizeCluster(context.Background(), uuid.New(), clusterID, "default") {
		t.Fatal("'Support Engineer' holds pods:exec and must be able to exec")
	}
}
