package gatekeeperpolicy

import "testing"

func TestManifests(t *testing.T) {
	ms, err := Manifests()
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(ms) != 6 {
		t.Fatalf("got %d manifests, want 6", len(ms))
	}
	// Templates must come before constraints (filename order) so the constraint
	// CRDs exist by the time their constraints apply on a later sweep.
	if ms[0].Kind != "ConstraintTemplate" {
		t.Errorf("first manifest kind = %q, want ConstraintTemplate", ms[0].Kind)
	}

	byName := map[string]Manifest{}
	for _, m := range ms {
		if m.Name == "" || m.Group == "" || m.Version == "" || m.Resource == "" || len(m.JSON) == 0 {
			t.Errorf("incomplete manifest: %+v", m)
		}
		byName[m.Name] = m
	}

	// ConstraintTemplate resolves to the constrainttemplates API.
	tmpl := byName["k8spspprivilegedcontainer"]
	if got, want := tmpl.APIPath(), "/apis/templates.gatekeeper.sh/v1/constrainttemplates/k8spspprivilegedcontainer"; got != want {
		t.Errorf("template APIPath = %q, want %q", got, want)
	}
	// A constraint resolves to its lowercased-kind plural under constraints.gatekeeper.sh.
	c := byName["no-privileged-containers"]
	if got, want := c.APIPath(), "/apis/constraints.gatekeeper.sh/v1beta1/k8spspprivilegedcontainer/no-privileged-containers"; got != want {
		t.Errorf("constraint APIPath = %q, want %q", got, want)
	}
}
