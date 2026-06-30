package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeAgentManifest is a minimal stand-in for the rendered agent install
// manifest. DesiredState only requires it to be non-empty; the real content is
// produced by deploy/agent.RenderInstallYAML via the cluster handler.
const fakeAgentManifest = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: astronomer-agent\n  namespace: astronomer-system\n"

func TestDesiredStateRendersAgentPlusDefaultBaseline(t *testing.T) {
	ctx := context.Background()
	// nil settings => every component falls back to DefaultEnabled. Only the two
	// metrics exporters are DefaultEnabled, both in astronomer-monitoring.
	resp, err := DesiredState(ctx, "11111111-1111-1111-1111-111111111111", fakeAgentManifest, nil)
	if err != nil {
		t.Fatalf("DesiredState returned error: %v", err)
	}

	if resp.Revision == "" || !strings.HasPrefix(resp.Revision, "sha256:") {
		t.Fatalf("expected a sha256 revision, got %q", resp.Revision)
	}

	byName := map[string]bool{}
	for _, m := range resp.Manifests {
		byName[m.Name] = true
		// Safety boundary: every manifest must target an Astronomer-owned ns.
		if !isOwnedNamespace(m.Namespace) {
			t.Fatalf("manifest %q targets non-owned namespace %q", m.Name, m.Namespace)
		}
	}

	// (a) the agent's own manifest.
	if !byName["astronomer-agent"] {
		t.Fatalf("expected agent manifest in desired state, got %v", names(resp.Manifests))
	}

	// (b) the two default-enabled baseline components, both in monitoring.
	for _, want := range []string{"baseline-kube-state-metrics", "baseline-prometheus-node-exporter"} {
		if !byName[want] {
			t.Fatalf("expected default baseline %q in desired state, got %v", want, names(resp.Manifests))
		}
	}
	for _, m := range resp.Manifests {
		if m.Name == "baseline-kube-state-metrics" {
			if m.Namespace != "astronomer-monitoring" {
				t.Fatalf("kube-state-metrics namespace = %q, want astronomer-monitoring", m.Namespace)
			}
			if !strings.Contains(m.Content, argolabels.ManagedByLabelKey+": "+argolabels.ManagedByLabelValue) {
				t.Fatalf("baseline manifest missing managed-by label:\n%s", m.Content)
			}
		}
	}

	// (c) The pull desired set must now carry REAL, NAMESPACED workloads — not a
	// Namespace stub. ksm = ServiceAccount + Deployment + Service; node-exporter =
	// ServiceAccount + DaemonSet + Service. And critically: NO cluster-scoped
	// resources (ClusterRole/ClusterRoleBinding/Namespace) may leak into the pull
	// set — the applier refuses them, and ksm's cluster RBAC is bootstrap-only.
	content := map[string]string{}
	for _, m := range resp.Manifests {
		content[m.Name] = m.Content
	}
	assertContainsAll(t, "kube-state-metrics", content["baseline-kube-state-metrics"], []string{
		"kind: ServiceAccount",
		"kind: Deployment",
		"kind: Service",
		"name: kube-state-metrics",
		"namespace: astronomer-monitoring",
	})
	assertContainsAll(t, "prometheus-node-exporter", content["baseline-prometheus-node-exporter"], []string{
		"kind: ServiceAccount",
		"kind: DaemonSet",
		"kind: Service",
		"name: prometheus-node-exporter",
		"namespace: astronomer-monitoring",
		// host access is pod-level (no cluster RBAC needed).
		"hostNetwork: true",
		"hostPID: true",
	})
	for name, c := range content {
		if !strings.HasPrefix(name, "baseline-") {
			continue
		}
		if name == "baseline-kube-state-metrics" || name == "baseline-prometheus-node-exporter" {
			for _, forbidden := range []string{"kind: ClusterRole", "kind: ClusterRoleBinding", "kind: Namespace"} {
				if strings.Contains(c, forbidden) {
					t.Fatalf("default baseline %q leaked cluster-scoped resource %q into the pull set:\n%s", name, forbidden, c)
				}
			}
		}
		// Every doc in every baseline manifest must declare the owned namespace.
		for _, doc := range strings.Split(c, "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			if name == "baseline-kube-state-metrics" || name == "baseline-prometheus-node-exporter" {
				if !strings.Contains(doc, "namespace: astronomer-monitoring") {
					t.Fatalf("default baseline %q has a doc without an owned namespace:\n%s", name, doc)
				}
			}
		}
	}
}

func assertContainsAll(t *testing.T, label, content string, wants []string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(content, w) {
			t.Fatalf("%s manifest missing %q:\n%s", label, w, content)
		}
	}
}

func TestDesiredStateRevisionIsDeterministic(t *testing.T) {
	ctx := context.Background()
	a, err := DesiredState(ctx, "cid", fakeAgentManifest, nil)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	b, err := DesiredState(ctx, "cid", fakeAgentManifest, nil)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if a.Revision != b.Revision {
		t.Fatalf("revision not stable across identical renders: %q != %q", a.Revision, b.Revision)
	}

	// A changed agent manifest must change the revision.
	c, err := DesiredState(ctx, "cid", fakeAgentManifest+"\n# drift\n", nil)
	if err != nil {
		t.Fatalf("drift render: %v", err)
	}
	if c.Revision == a.Revision {
		t.Fatalf("revision did not change when the agent manifest changed")
	}
}

func TestDesiredStateRespectsBaselineEnablement(t *testing.T) {
	ctx := context.Background()
	// Opt INTO ingress-nginx (default off) and opt OUT of node-exporter.
	q := baselineAppSetQuerierStub{settings: map[string]json.RawMessage{
		platformSettingBaselineComponentPrefix + "ingress-nginx":            json.RawMessage("true"),
		platformSettingBaselineComponentPrefix + "prometheus-node-exporter": json.RawMessage("false"),
	}}
	resp, err := DesiredState(ctx, "cid", fakeAgentManifest, q)
	if err != nil {
		t.Fatalf("DesiredState: %v", err)
	}
	got := map[string]bool{}
	for _, m := range resp.Manifests {
		got[m.Name] = true
	}
	if !got["baseline-ingress-nginx"] {
		t.Fatalf("expected ingress-nginx to be enabled, got %v", names(resp.Manifests))
	}
	if got["baseline-prometheus-node-exporter"] {
		t.Fatalf("expected node-exporter to be disabled, got %v", names(resp.Manifests))
	}
	if !got["baseline-kube-state-metrics"] {
		t.Fatalf("expected kube-state-metrics (default-on, unchanged), got %v", names(resp.Manifests))
	}
}

func TestDesiredStateRejectsEmptyAgentManifest(t *testing.T) {
	if _, err := DesiredState(context.Background(), "cid", "   ", nil); err == nil {
		t.Fatalf("expected error for empty agent manifest")
	}
}

// TestDesiredStateAgentManifestIsDeploymentOnly locks Phase 2: from the full
// multi-doc install manifest (Namespace + ClusterRole + Deployment), the desired
// state must emit ONLY the namespaced Deployment — the cluster-scoped resources
// are bootstrap-only and the pull applier refuses them. This is what lets a
// central version bump roll the agent in place.
func TestDesiredStateAgentManifestIsDeploymentOnly(t *testing.T) {
	fullInstall := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: astronomer-system\n" +
		"---\napiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: astronomer-agent\n" +
		"---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: astronomer-agent\n  namespace: astronomer-system\nspec:\n  replicas: 1\n"
	out, err := DesiredState(context.Background(), "cid", fullInstall, nil)
	if err != nil {
		t.Fatalf("DesiredState: %v", err)
	}
	var agent *protocol.DesiredManifest
	for i := range out.Manifests {
		if out.Manifests[i].Name == "astronomer-agent" {
			agent = &out.Manifests[i]
		}
	}
	if agent == nil {
		t.Fatalf("no astronomer-agent manifest emitted")
	}
	if !strings.Contains(agent.Content, "kind: Deployment") {
		t.Fatalf("agent manifest is not a Deployment:\n%s", agent.Content)
	}
	if strings.Contains(agent.Content, "ClusterRole") || strings.Contains(agent.Content, "kind: Namespace") {
		t.Fatalf("agent manifest leaked cluster-scoped resources (must be Deployment-only):\n%s", agent.Content)
	}
}

// ownershipSettingsStub is a platformSettingReader that also implements
// ownershipDecisionReader, so DesiredState honors per-cluster leave_local
// decisions. nil settings map => every component falls back to DefaultEnabled.
type ownershipSettingsStub struct {
	decisions []sqlc.ArgocdBaselineOwnershipDecision
}

func (ownershipSettingsStub) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	return sqlc.PlatformSetting{}, pgx.ErrNoRows
}

func (s ownershipSettingsStub) ListArgoCDBaselineOwnershipDecisions(_ context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdBaselineOwnershipDecision, error) {
	out := []sqlc.ArgocdBaselineOwnershipDecision{}
	for _, d := range s.decisions {
		if d.ClusterID == clusterID {
			out = append(out, d)
		}
	}
	return out, nil
}

// TestDesiredStateHonorsLeaveLocalOwnership: a leave_local decision for
// kube-state-metrics on this cluster drops it from the pull desired set, while
// node-exporter (no decision) still renders. A different cluster's decision
// must not affect this cluster.
func TestDesiredStateHonorsLeaveLocalOwnership(t *testing.T) {
	ctx := context.Background()
	clusterID := "55555555-5555-5555-5555-555555555555"
	settings := ownershipSettingsStub{
		decisions: []sqlc.ArgocdBaselineOwnershipDecision{
			{ClusterID: uuid.MustParse(clusterID), ComponentSlug: "kube-state-metrics", Decision: "leave_local"},
			{ClusterID: uuid.MustParse("66666666-6666-6666-6666-666666666666"), ComponentSlug: "prometheus-node-exporter", Decision: "leave_local"},
		},
	}
	resp, err := DesiredState(ctx, clusterID, fakeAgentManifest, settings)
	if err != nil {
		t.Fatalf("DesiredState returned error: %v", err)
	}
	byName := map[string]bool{}
	for _, m := range resp.Manifests {
		byName[m.Name] = true
	}
	if byName["baseline-kube-state-metrics"] {
		t.Fatalf("leave_local kube-state-metrics must be excluded, got %v", names(resp.Manifests))
	}
	// node-exporter has no decision for THIS cluster (the other row is a
	// different cluster) → still rendered.
	if !byName["baseline-prometheus-node-exporter"] {
		t.Fatalf("node-exporter must still render, got %v", names(resp.Manifests))
	}
}

func names(manifests []protocol.DesiredManifest) []string {
	out := make([]string, 0, len(manifests))
	for _, m := range manifests {
		out = append(out, m.Name)
	}
	return out
}
