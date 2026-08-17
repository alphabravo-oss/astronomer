package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDeliveryCommandTreeCoversContinuousDeliverySurface(t *testing.T) {
	root := newDeliveryCmd()
	root.SetArgs([]string{"--help"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{"Continuous Delivery", "source", "bundle", "target", "rollout", "deployment"} {
		if !strings.Contains(help, want) {
			t.Fatalf("delivery help missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(strings.ToLower(help), "argo"+"cd") || strings.Contains(help, "fl"+"eet") {
		t.Fatalf("delivery help listed a leftover delivery engine:\n%s", help)
	}

	cases := map[string][]string{
		"source":     {"list", "get", "create", "update", "delete", "verify", "rotate-credential"},
		"bundle":     {"list", "get", "create", "delete", "version-create", "version-list"},
		"target":     {"list", "get", "apply", "delete", "preview"},
		"rollout":    {"list", "get", "start", "pause", "resume", "approve", "abort", "retry", "rollback", "watch"},
		"deployment": {"list", "get", "reconcile", "suspend", "resume", "events"},
	}
	for parent, children := range cases {
		cmd, _, err := root.Find([]string{parent})
		if err != nil || cmd == nil {
			t.Fatalf("missing delivery %s: %v", parent, err)
		}
		have := map[string]bool{}
		for _, child := range cmd.Commands() {
			have[strings.Fields(child.Use)[0]] = true
		}
		for _, child := range children {
			if !have[child] {
				t.Fatalf("delivery %s missing %s", parent, child)
			}
		}
	}
}

func TestClusterAgentCommandTreeIsFirstParty(t *testing.T) {
	root := newClusterAgentCmd()
	root.SetArgs([]string{"--help"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	if !strings.Contains(help, "Cluster Agents") {
		t.Fatalf("cluster-agent help missing Cluster Agents:\n%s", help)
	}
	for _, want := range []string{"list", "get", "diagnostics", "upgrade"} {
		if !strings.Contains(help, want) {
			t.Fatalf("cluster-agent help missing %s:\n%s", want, help)
		}
	}
	if strings.Contains(strings.ToLower(help), "argo"+"cd") || strings.Contains(help, "fl"+"eet-operations") {
		t.Fatalf("cluster-agent help listed a leftover delivery engine:\n%s", help)
	}
}

func TestParseDeliveryTargetDocumentAcceptsYAMLAndKindWrapper(t *testing.T) {
	doc, err := parseDeliveryTargetDocument([]byte(`
kind: DeliveryTarget
metadata:
  name: production-ingress
  project_id: 18fc69f4-5763-4541-bafb-1ef22192bcfa
spec:
  bundle_version_id: 2bfdd32f-713e-4c03-8e7c-968aed474a65
  placement:
    all_clusters: false
    match_labels:
      environment: production
`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "production-ingress" || doc.ProjectID != "18fc69f4-5763-4541-bafb-1ef22192bcfa" {
		t.Fatalf("parsed identity = %#v", doc)
	}
	if doc.Body["bundle_version_id"] != "2bfdd32f-713e-4c03-8e7c-968aed474a65" {
		t.Fatalf("spec was not unwrapped: %#v", doc.Body)
	}
	if _, ok := doc.Body["placement"].(map[string]any); !ok {
		t.Fatalf("placement not parsed: %#v", doc.Body["placement"])
	}
}

func TestParseDeliveryTargetDocumentRejectsEmptySelectorIdentity(t *testing.T) {
	if _, err := parseDeliveryTargetDocument([]byte(`name: only`)); err == nil {
		t.Fatal("accepted a document without project_id")
	}
	if _, err := parseDeliveryTargetDocument(nil); err == nil {
		t.Fatal("accepted empty document")
	}
}

func TestDeliveryTargetPatchOmitsImmutableName(t *testing.T) {
	patch := deliveryTargetPatchBody(map[string]any{
		"name": "keep-server-side", "project_id": "18fc69f4-5763-4541-bafb-1ef22192bcfa",
		"description": "next", "bundle_version_id": "2bfdd32f-713e-4c03-8e7c-968aed474a65",
	})
	if _, ok := patch["name"]; ok {
		t.Fatalf("patch must not rewrite name: %#v", patch)
	}
	if patch["description"] != "next" || patch["project_id"] == nil {
		t.Fatalf("patch dropped mutable fields: %#v", patch)
	}
}

func TestDeliveryRolloutTerminalStates(t *testing.T) {
	for _, state := range []string{"succeeded", "failed", "aborted", "rejected", "rolled_back", "rollback_failed"} {
		if !deliveryRolloutTerminal(state) {
			t.Fatalf("%s should be terminal", state)
		}
	}
	if deliveryRolloutTerminal("progressing") {
		t.Fatal("progressing is not terminal")
	}
}

func TestQuotedEntityTagIsStrong(t *testing.T) {
	if got := quotedEntityTag(7); got != `"7"` {
		t.Fatalf("If-Match tag = %q", got)
	}
}
