package deployment

import "testing"

func TestResolveActionDoesNotExposeRawFluxOperations(t *testing.T) {
	for _, test := range []struct {
		action     Action
		wantAction string
		wantEvent  string
	}{
		{ActionReconcile, "apply", "deployment_reconcile_requested"},
		{ActionSuspend, "suspend", "deployment_suspend_requested"},
		{ActionResume, "apply", "deployment_resume_requested"},
	} {
		action, event, ok := resolveAction(test.action)
		if !ok || action != test.wantAction || event != test.wantEvent {
			t.Fatalf("%s resolved to %q/%q ok=%v", test.action, action, event, ok)
		}
	}
	if _, _, ok := resolveAction("raw_flux_object"); ok {
		t.Fatal("accepted unsupported raw Flux operation")
	}
}
