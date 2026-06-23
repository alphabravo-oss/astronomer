package baseline

import "testing"

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
