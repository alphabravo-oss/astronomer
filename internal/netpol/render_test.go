package netpol

import (
	"strings"
	"testing"
)

const denyAllIngressSpec = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: deny_all_ingress
spec:
  podSelector: {}
  policyTypes: [Ingress]
`

const projectIsolatedSpec = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels:
              astronomer.io/project: {{.Project}}
`

func TestRender_DenyAllIngress(t *testing.T) {
	got, err := Render(denyAllIngressSpec, Context{
		Namespace:  "team-a",
		PolicyName: PolicyName("deny_all_ingress"),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"name: astronomer-np-deny_all_ingress",
		"namespace: team-a",
		"policyTypes: [Ingress]",
		"podSelector: {}",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered output missing %q\n---\n%s", want, s)
		}
	}
}

func TestRender_ProjectIsolated_SubstitutesProject(t *testing.T) {
	got, err := Render(projectIsolatedSpec, Context{
		Namespace:  "team-a",
		Project:    "billing",
		PolicyName: PolicyName("project_isolated"),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "astronomer.io/project: billing") {
		t.Errorf("expected project substitution to billing, got:\n%s", s)
	}
}

func TestRender_RejectsEmptySpec(t *testing.T) {
	if _, err := Render("", Context{Namespace: "x"}); err == nil {
		t.Fatal("expected error for empty spec")
	}
}

func TestRender_ReportsTemplateParseError(t *testing.T) {
	_, err := Render("{{ bogus", Context{})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got %v", err)
	}
}

func TestPolicyName(t *testing.T) {
	if got := PolicyName("deny_all_ingress"); got != "astronomer-np-deny_all_ingress" {
		t.Errorf("PolicyName: got %q, want astronomer-np-deny_all_ingress", got)
	}
}
