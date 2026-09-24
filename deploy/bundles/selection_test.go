package builtinbundles

import "testing"

func TestRegistrationImageScanningDefaultAndOptOut(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, metrics := range []bool{false, true} {
		for _, disabled := range []bool{false, true} {
			selected := catalog.ForRegistration(metrics, disabled)
			for _, component := range selected.Components {
				want := metrics
				if component.Slug == "trivy-operator" {
					want = !disabled
				}
				if component.DefaultEnabled != want {
					t.Fatalf("%s enabled=%v, want=%v (metrics=%v scanning opt-out=%v)", component.Slug, component.DefaultEnabled, want, metrics, disabled)
				}
			}
		}
	}
	for _, component := range catalog.Components {
		if !component.DefaultEnabled {
			t.Fatal("selection mutated embedded defaults")
		}
	}
}
