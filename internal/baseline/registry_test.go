package baseline

import (
	"testing"

	semver "github.com/Masterminds/semver/v3"
)

// TestDeliveryPathRoutesOnlyDefaultEnabledToApplicationSets is the dispatcher
// seam's contract test: exactly the two metrics exporters ride the baseline
// ApplicationSet lifecycle; every other catalog component is opt-in via the
// tool_operations path. If the DefaultEnabled set or the routing logic drifts,
// this fails.
func TestDeliveryPathRoutesOnlyDefaultEnabledToApplicationSets(t *testing.T) {
	wantAppSet := map[string]bool{
		"kube-state-metrics":       true,
		"prometheus-node-exporter": true,
	}

	seen := map[string]bool{}
	for _, c := range Registry {
		seen[c.Slug] = true
		got := c.DeliveryPath()
		if wantAppSet[c.Slug] {
			if got != PathApplicationSet {
				t.Errorf("%s: DeliveryPath = %q, want %q", c.Slug, got, PathApplicationSet)
			}
			if !c.DefaultEnabled {
				t.Errorf("%s: expected DefaultEnabled for an ApplicationSet component", c.Slug)
			}
		} else {
			if got != PathToolOperation {
				t.Errorf("%s: DeliveryPath = %q, want %q", c.Slug, got, PathToolOperation)
			}
			if c.DefaultEnabled {
				t.Errorf("%s: opt-in component must not be DefaultEnabled", c.Slug)
			}
		}
	}

	for slug := range wantAppSet {
		if !seen[slug] {
			t.Errorf("registry missing expected ApplicationSet component %q", slug)
		}
	}

	appSet := ApplicationSetComponents()
	if len(appSet) != len(wantAppSet) {
		t.Fatalf("ApplicationSetComponents len = %d, want %d", len(appSet), len(wantAppSet))
	}
	for _, c := range appSet {
		if !wantAppSet[c.Slug] {
			t.Errorf("ApplicationSetComponents returned unexpected %q", c.Slug)
		}
		// Delivery path components carry the chart coordinates the server needs.
		if c.ChartName == "" || c.RepoURL == "" {
			t.Errorf("%s: ApplicationSet component missing chart coordinates", c.Slug)
		}
	}
}

// TestBaselineRegistryPinsEveryDefault is the fence on the floating-version
// fix: anything routed to the ApplicationSet lifecycle is auto-delivered to
// every adopted cluster with prune and selfHeal on, so it must name the exact
// chart version it delivers. Before the fix every one of them rendered
// targetRevision "*" and took whatever upstream published last.
func TestBaselineRegistryPinsEveryDefault(t *testing.T) {
	for _, c := range ApplicationSetComponents() {
		if c.ChartVersion == "" {
			t.Errorf("%s: ApplicationSet component has no ChartVersion pin", c.Slug)
			continue
		}
		if c.ChartVersion == "*" {
			t.Errorf("%s: ChartVersion %q floats to upstream-latest", c.Slug, c.ChartVersion)
			continue
		}
		// ArgoCD parses a Helm targetRevision as a semver constraint, so the pin
		// has to be one; an exact version is the constraint that matches only
		// itself.
		if _, err := semver.NewVersion(c.ChartVersion); err != nil {
			t.Errorf("%s: ChartVersion %q is not an exact semver version: %v", c.Slug, c.ChartVersion, err)
		}
	}
}
