package tunnel

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// mockRBACQuerier implements middleware.RBACQuerier for the consumer authz tests.
type mockRBACQuerier struct {
	bindings []rbac.RoleBinding
	err      error
}

func (m *mockRBACQuerier) GetUserBindings(_ context.Context, _ string) ([]rbac.RoleBinding, error) {
	return m.bindings, m.err
}

// namespaceBinding grants (resource wildcard) on a single cluster, narrowed to
// one Kubernetes namespace.
func namespaceBinding(clusterID uuid.UUID, namespace string) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		Namespace: namespace,
		RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"*"}}},
	}}
}

// clusterWideBinding grants (resource wildcard) across the whole cluster,
// regardless of namespace.
func clusterWideBinding(clusterID uuid.UUID) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"*"}}},
	}}
}

// TestExecConsumer_AuthorizeCluster_NamespaceScoped verifies that a
// namespace-scoped user is allowed to exec in their authorized namespace and
// denied in another, while a cluster-wide user is allowed anywhere.
func TestExecConsumer_AuthorizeCluster_NamespaceScoped(t *testing.T) {
	engine := rbac.NewEngine()
	clusterID := uuid.New()
	userID := uuid.New()
	ctx := context.Background()

	ec := NewExecConsumer(nil, nil)
	ec.SetAuthorization(engine, &mockRBACQuerier{bindings: namespaceBinding(clusterID, "team-a")})

	if !ec.authorizeCluster(ctx, userID, clusterID, "team-a") {
		t.Error("namespace-scoped user should be allowed to exec in team-a")
	}
	if ec.authorizeCluster(ctx, userID, clusterID, "team-b") {
		t.Error("namespace-scoped user must be denied exec in team-b")
	}
}

func TestExecConsumer_AuthorizeCluster_ClusterWide(t *testing.T) {
	engine := rbac.NewEngine()
	clusterID := uuid.New()
	userID := uuid.New()
	ctx := context.Background()

	ec := NewExecConsumer(nil, nil)
	ec.SetAuthorization(engine, &mockRBACQuerier{bindings: clusterWideBinding(clusterID)})

	for _, ns := range []string{"team-a", "team-b", ""} {
		if !ec.authorizeCluster(ctx, userID, clusterID, ns) {
			t.Errorf("cluster-wide user should be allowed to exec in namespace %q", ns)
		}
	}
}

// TestLogsConsumer_AuthorizeCluster_NamespaceScoped mirrors the exec test for
// the logs consumer (clusters:read gate).
func TestLogsConsumer_AuthorizeCluster_NamespaceScoped(t *testing.T) {
	engine := rbac.NewEngine()
	clusterID := uuid.New()
	userID := uuid.New()
	ctx := context.Background()

	lc := NewLogsConsumer(nil, nil)
	lc.SetAuthorization(engine, &mockRBACQuerier{bindings: namespaceBinding(clusterID, "team-a")})

	if !lc.authorizeCluster(ctx, userID, clusterID, "team-a") {
		t.Error("namespace-scoped user should be allowed to stream logs in team-a")
	}
	if lc.authorizeCluster(ctx, userID, clusterID, "team-b") {
		t.Error("namespace-scoped user must be denied logs in team-b")
	}
}

func TestLogsConsumer_AuthorizeCluster_ClusterWide(t *testing.T) {
	engine := rbac.NewEngine()
	clusterID := uuid.New()
	userID := uuid.New()
	ctx := context.Background()

	lc := NewLogsConsumer(nil, nil)
	lc.SetAuthorization(engine, &mockRBACQuerier{bindings: clusterWideBinding(clusterID)})

	for _, ns := range []string{"team-a", "team-b", ""} {
		if !lc.authorizeCluster(ctx, userID, clusterID, ns) {
			t.Errorf("cluster-wide user should be allowed to stream logs in namespace %q", ns)
		}
	}
}
