package baseline

import "testing"

func TestComponentBySlugReturnsSecurityMetadata(t *testing.T) {
	component, ok := ComponentBySlug("kube-state-metrics")
	if !ok || !component.RequiresClusterRBAC {
		t.Fatalf("kube-state-metrics metadata = %+v, found=%v", component, ok)
	}
	if _, ok := ComponentBySlug("unknown"); ok {
		t.Fatal("unknown component unexpectedly found")
	}
}
