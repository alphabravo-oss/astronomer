package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type visibilityConnectionReader struct{ connection sqlc.CharlieConnection }

func (r *visibilityConnectionReader) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return r.connection, nil
}

type visibilityNextExecutor struct{}

func (visibilityNextExecutor) Execute(_ context.Context, _ CapabilityDescriptor, _ map[string]json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}
func (visibilityNextExecutor) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}
func (visibilityNextExecutor) SupportsCapability(context.Context, string) bool { return true }

func TestKubernetesVisibilityExecutorProfilesAndDisclosure(t *testing.T) {
	reader := &visibilityConnectionReader{connection: sqlc.CharlieConnection{
		Active: true, McpServiceName: "astronomer-charlie-mcp.astronomer.svc",
		KubernetesVisibilityProfile: string(KubernetesVisibilityDisabled),
	}}
	executor, err := NewKubernetesVisibilityExecutor(visibilityNextExecutor{}, reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if executor.SupportsCapability(ctx, "astronomer.management.pods") {
		t.Fatal("disabled profile disclosed Kubernetes pods")
	}
	if executor.SupportsCapability(ctx, "astronomer.management.workload_scale") {
		t.Fatal("disabled profile disclosed a Kubernetes write")
	}
	if !executor.SupportsCapability(ctx, "astronomer.database.health") {
		t.Fatal("Kubernetes profile hid a non-Kubernetes capability")
	}

	reader.connection.KubernetesVisibilityProfile = string(KubernetesVisibilityProductNamespace)
	if !executor.SupportsCapability(ctx, "astronomer.management.pods") ||
		!executor.SupportsCapability(ctx, "astronomer.management.workload_scale") ||
		executor.SupportsCapability(ctx, "astronomer.management.nodes") || executor.SupportsCapability(ctx, "astronomer.management.pod_logs") {
		t.Fatal("product-namespace profile did not enforce namespaced metadata without content")
	}
	// Scope disclosure does not itself grant the write. The independent
	// ActionGuard still applies read-only, approval, or automatic authority to
	// this catalogued operation at execution time.
	writeConnector, ok := executor.ConnectorMetadata(ctx, "astronomer.management.workload_scale")
	if !ok || writeConnector.Profile != string(KubernetesVisibilityProductNamespace) || writeConnector.ClusterScoped {
		t.Fatalf("namespaced write connector provenance was incomplete: %#v", writeConnector)
	}
	productDigest := capabilityDisclosureDigest(mcpToolsFor(ctx, executor))

	reader.connection.KubernetesVisibilityProfile = string(KubernetesVisibilityClusterDiagnostics)
	reader.connection.KubernetesVisibilityPodLogs = true
	if !executor.SupportsCapability(ctx, "astronomer.management.nodes") || !executor.SupportsCapability(ctx, "astronomer.management.pod_logs") {
		t.Fatal("cluster-diagnostics profile did not expose opted-in diagnostics")
	}
	connector, ok := executor.ConnectorMetadata(ctx, "astronomer.management.pods")
	if !ok || connector.Profile != string(KubernetesVisibilityClusterDiagnostics) || !connector.ClusterScoped || connector.DownstreamTargets || len(connector.ContentAccess) != 1 || connector.ContentAccess[0] != "pod_logs" {
		t.Fatalf("unexpected connector metadata: %#v", connector)
	}
	clusterDigest := capabilityDisclosureDigest(mcpToolsFor(ctx, executor))
	if productDigest == "" || clusterDigest == "" || productDigest == clusterDigest {
		t.Fatal("Kubernetes profile expansion did not change capability disclosure digest")
	}
}

func TestKubernetesVisibilityHardBoundaryIsClosed(t *testing.T) {
	view := safeKubernetesVisibility(sqlc.CharlieConnection{
		McpServiceName:              "astronomer-charlie-mcp.astronomer.svc",
		KubernetesVisibilityProfile: string(KubernetesVisibilityClusterDiagnostics),
		KubernetesVisibilityPodLogs: true,
	})
	if view.DownstreamTargets || view.SecretValues || view.Exec || view.Attach || view.PortForward || view.APIProxy || !view.ProductOwnedOnly {
		t.Fatalf("hard Kubernetes boundary opened: %#v", view)
	}
	if len(view.Namespaces) != 1 || view.Namespaces[0] != "astronomer" {
		t.Fatalf("management namespace was not derived safely: %#v", view.Namespaces)
	}
}

func TestKubernetesVisibilitySeparatesRediscoveryAndBothAcknowledgements(t *testing.T) {
	connection := sqlc.CharlieConnection{
		KubernetesVisibilityProfile:          string(KubernetesVisibilityProductNamespace),
		KubernetesVisibilityRediscoveryState: "required",
	}
	view := safeKubernetesVisibility(connection)
	if !view.RequiresRediscovery || view.RequiresCentralReview || view.RequiresProductAcknowledgement {
		t.Fatalf("required rediscovery state was ambiguous: %+v", view)
	}
	connection.KubernetesVisibilityRediscoveryState = "review_required"
	connection.KubernetesVisibilityCandidateDigest = strings.Repeat("a", 64)
	view = safeKubernetesVisibility(connection)
	if view.RequiresRediscovery || !view.RequiresCentralReview || view.RequiresProductAcknowledgement {
		t.Fatalf("central review state was ambiguous: %+v", view)
	}
	// Central review may deliberately change mode or allowlists, so its active
	// disclosure need not equal the original rediscovery candidate.
	connection.DisclosureDigest = strings.Repeat("b", 64)
	view = safeKubernetesVisibility(connection)
	if view.RequiresCentralReview || !view.RequiresProductAcknowledgement {
		t.Fatalf("product acknowledgement state was ambiguous: %+v", view)
	}
	connection.AcknowledgedDisclosureDigest = connection.DisclosureDigest
	connection.KubernetesVisibilityRediscoveryState = "ready"
	connection.KubernetesVisibilityCandidateDigest = ""
	view = safeKubernetesVisibility(connection)
	if view.RequiresRediscovery || view.RequiresCentralReview || view.RequiresProductAcknowledgement {
		t.Fatalf("acknowledged visibility remained pending: %+v", view)
	}
}

func TestKubernetesVisibilityBlocksAuthorityOnlyDuringIncompleteLifecycle(t *testing.T) {
	connection := sqlc.CharlieConnection{KubernetesVisibilityRediscoveryState: "ready"}
	if kubernetesVisibilityAuthorityPending(connection) {
		t.Fatal("ready connector blocked normal authority management")
	}
	connection.KubernetesVisibilityRediscoveryState = "required"
	if !kubernetesVisibilityAuthorityPending(connection) {
		t.Fatal("unperformed rediscovery did not block authority")
	}
	connection.KubernetesVisibilityRediscoveryState = "review_required"
	connection.DisclosureDigest = strings.Repeat("b", 64)
	if !kubernetesVisibilityAuthorityPending(connection) {
		t.Fatal("unacknowledged central disclosure did not block authority")
	}
	connection.AcknowledgedDisclosureDigest = connection.DisclosureDigest
	if kubernetesVisibilityAuthorityPending(connection) {
		t.Fatal("exact product acknowledgement did not release authority management")
	}
}
