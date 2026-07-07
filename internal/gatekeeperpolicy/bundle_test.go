package gatekeeperpolicy

import "testing"

// ParseManifest interpolates metadata.name and kind into the SSA API path, so a
// name/kind carrying path characters must be rejected before it can be used.
func TestParseManifest_RejectsPathInjectionInNameAndKind(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"name with slash", "apiVersion: constraints.gatekeeper.sh/v1beta1\nkind: K8sRequiredLabels\nmetadata:\n  name: ../../secrets/steal\n"},
		{"name with space", "apiVersion: constraints.gatekeeper.sh/v1beta1\nkind: K8sRequiredLabels\nmetadata:\n  name: bad name\n"},
		{"kind with slash", "apiVersion: constraints.gatekeeper.sh/v1beta1\nkind: Bad/Kind\nmetadata:\n  name: ok\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.yaml)); err == nil {
				t.Fatalf("expected rejection, got nil error")
			}
		})
	}
	// A well-formed constraint still parses.
	ok := "apiVersion: constraints.gatekeeper.sh/v1beta1\nkind: K8sRequiredLabels\nmetadata:\n  name: require-team-label\n"
	if _, err := ParseManifest([]byte(ok)); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

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
