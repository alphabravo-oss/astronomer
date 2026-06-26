package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/baseline"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ownedNamespaceSet is the apply/prune boundary for the pull reconcile
// subsystem, derived from deploy/agent.AstronomerOwnedNamespaces. DesiredState
// NEVER emits a manifest targeting a namespace outside this set — the agent
// re-validates before apply, but bounding the rendered set here is the first
// line of the safety boundary.
var ownedNamespaceSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(agenttemplate.AstronomerOwnedNamespaces))
	for _, ns := range agenttemplate.AstronomerOwnedNamespaces {
		set[ns] = struct{}{}
	}
	return set
}()

// isOwnedNamespace reports whether ns is an Astronomer-owned namespace the pull
// applier is permitted to act in.
func isOwnedNamespace(ns string) bool {
	_, ok := ownedNamespaceSet[ns]
	return ok
}

// DesiredState renders the Fleet-style PULL desired state for a single cluster.
//
// The desired set is:
//
//   - (a) the agent's OWN manifest (Deployment + RBAC + config) at the
//     management plane's central agent image/version. The caller renders this
//     via handler.renderAgentInstallManifest (reusing deploy/agent.RenderInstallYAML)
//     and passes it as agentManifest — DesiredState does not reach into the
//     handler package, keeping this rendering unit-testable in isolation.
//   - (b) every ENABLED baseline/platform component, rendered to its
//     astronomer-* namespace. Enablement is read from platform_settings via the
//     same baselineComponentEnabled seam the Argo push path uses, so the two
//     paths agree on which components are on. Only components whose target
//     namespace is Astronomer-owned are emitted (the registry already satisfies
//     this; the guard is defense-in-depth).
//
// The returned Revision is a deterministic SHA-256 over the rendered set so the
// agent can skip re-applying an unchanged revision and report status against a
// concrete version. The render is order-stable (agent first, then baseline in
// registry order), so equal inputs always yield an equal revision.
//
// DesiredState is read-only rendering and is NOT gated by PullReconcileEnabled:
// the responder may always answer. The feature flag gates whether the agent
// runs the loop and whether the server's push path stands down — not whether
// the server can describe the desired state.
func DesiredState(ctx context.Context, clusterID string, agentManifest string, settings platformSettingReader) (protocol.DesiredStateResponsePayload, error) {
	if strings.TrimSpace(agentManifest) == "" {
		return protocol.DesiredStateResponsePayload{}, fmt.Errorf("desired state: empty agent manifest for cluster %q", clusterID)
	}

	manifests := make([]protocol.DesiredManifest, 0, len(baseline.Registry)+1)

	// (a) The agent's own Deployment, in astronomer-system, at the central image.
	// We deliberately ship ONLY the Deployment (not the full install manifest):
	// the cluster-scoped RBAC + Namespaces are bootstrap-only (applied once by the
	// operator's kubectl), and the pull applier — by design — refuses anything
	// cluster-scoped or outside astronomer-*. Self-management going forward is
	// just the Deployment, so a central agent-version bump re-renders here and the
	// agent re-applies its own Deployment in place (Phase 2). Extracting it keeps
	// the desired set inside the applier's namespaced boundary.
	agentDeployment, err := extractAgentDeployment(agentManifest)
	if err != nil {
		return protocol.DesiredStateResponsePayload{}, fmt.Errorf("desired state: %w", err)
	}
	manifests = append(manifests, protocol.DesiredManifest{
		Name:      "astronomer-agent",
		Kind:      "AgentDeployment",
		Namespace: "astronomer-system",
		Content:   agentDeployment,
	})

	// (b) Enabled baseline components, rendered to their astronomer-* namespace
	// in registry order for a deterministic revision. We iterate the FULL
	// registry (not just the DefaultEnabled ApplicationSet set) so an operator
	// who opts INTO an otherwise-off component (e.g. ingress-nginx) gets it in
	// the pull desired state. Enablement is resolved through the same
	// baselineComponentEnabled platform-settings seam the Argo push path uses.
	for _, reg := range baseline.Registry {
		comp := baselineApplicationSetComponent{
			ApplicationSetName: reg.ApplicationSetName,
			ApplicationPrefix:  reg.ApplicationPrefix,
			Slug:               reg.Slug,
			ChartName:          reg.ChartName,
			RepoURL:            reg.RepoURL,
			Namespace:          reg.Namespace,
			ValuesYAML:         reg.ValuesYAML,
			DefaultEnabled:     reg.DefaultEnabled,
		}
		if !isOwnedNamespace(comp.Namespace) {
			// Defense-in-depth: never emit outside the owned set.
			continue
		}
		if !baselineComponentEnabled(ctx, settings, comp) {
			continue
		}
		manifests = append(manifests, protocol.DesiredManifest{
			Name:      "baseline-" + comp.Slug,
			Kind:      "BaselineComponent",
			Namespace: comp.Namespace,
			Content:   renderBaselineComponentManifest(comp),
		})
	}

	return protocol.DesiredStateResponsePayload{
		ClusterID: clusterID,
		Revision:  revisionOf(manifests),
		Manifests: manifests,
	}, nil
}

// renderBaselineComponentManifest renders one enabled baseline component into a
// bounded, applyable manifest for the pull applier. The MVP emits the target
// Namespace object (labeled managed-by so the agent's prune pass keeps it) plus
// a HelmRelease-shaped descriptor comment carrying the chart coordinates the
// agent's local applier resolves. The Namespace is always within
// AstronomerOwnedNamespaces, so the manifest is safe to apply by definition.
func renderBaselineComponentManifest(comp baselineApplicationSetComponent) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("kind: Namespace\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + comp.Namespace + "\n")
	b.WriteString("  labels:\n")
	b.WriteString("    " + argolabels.ManagedByLabelKey + ": " + argolabels.ManagedByLabelValue + "\n")
	b.WriteString("    astronomer.io/baseline-component: " + comp.Slug + "\n")
	if comp.ChartName != "" {
		b.WriteString("  annotations:\n")
		b.WriteString("    astronomer.io/chart-name: " + comp.ChartName + "\n")
		if comp.RepoURL != "" {
			b.WriteString("    astronomer.io/chart-repo: " + comp.RepoURL + "\n")
		}
	}
	return b.String()
}

// extractAgentDeployment pulls the single `kind: Deployment` document out of the
// full multi-document agent install manifest. The pull applier is namespaced and
// refuses cluster-scoped resources, so we self-manage only the Deployment (the
// RBAC/Namespaces are bootstrap-only). Returns an error if no Deployment doc is
// found so a malformed manifest never yields an empty desired set.
func extractAgentDeployment(manifest string) (string, error) {
	for _, doc := range strings.Split(manifest, "\n---") {
		trimmed := strings.TrimSpace(doc)
		if trimmed == "" {
			continue
		}
		// Match the document kind at line start (avoid matching kind: refs in
		// rules/subjects of other documents).
		isDeployment := false
		for _, line := range strings.Split(trimmed, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "kind:") {
				if strings.TrimSpace(strings.TrimPrefix(l, "kind:")) == "Deployment" {
					isDeployment = true
				}
				break
			}
		}
		if isDeployment {
			return trimmed + "\n", nil
		}
	}
	return "", fmt.Errorf("no Deployment document found in agent manifest")
}

// revisionOf computes a deterministic content hash over the rendered manifest
// set. The hash covers each manifest's Name, Namespace, and Content so a change
// to any rendered byte (or to which components are enabled) changes the
// revision, while re-rendering identical inputs is stable. Manifests are sorted
// by Name before hashing so ordering can never perturb the revision.
func revisionOf(manifests []protocol.DesiredManifest) string {
	sorted := make([]protocol.DesiredManifest, len(manifests))
	copy(sorted, manifests)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	h := sha256.New()
	for _, m := range sorted {
		h.Write([]byte(m.Name))
		h.Write([]byte{0})
		h.Write([]byte(m.Namespace))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
