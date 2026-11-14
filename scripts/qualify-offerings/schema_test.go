package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPreflightSchemaRejectsLifecycleAndUnboundPass(t *testing.T) {
	t.Parallel()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	for _, name := range []string{"offering-qualification", "offering-estate-preflight"} {
		raw, err := os.ReadFile("../../deploy/release/" + name + ".schema.json")
		if err != nil {
			t.Fatal(err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		suffix := "v1"
		if name == "offering-estate-preflight" {
			suffix = "v2"
		}
		if err := compiler.AddResource("https://astronomer.dev/schemas/"+name+"-"+suffix+".json", document); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile("https://astronomer.dev/schemas/offering-estate-preflight-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	c, _, base, now := estateFixture(t)
	report := estatePreflightReport{SchemaVersion: estatePreflightSchema, RunID: "preflight-test", GeneratedAt: now, Candidate: candidate{Commit: c.ExpectedCommit, StagedDiffSHA: digest(nil), UnstagedDiffSHA: digest(nil), UntrackedSHA: digest(nil)}, BaseOrigin: base.String(), Status: "BLOCKED", Reason: "not qualified", RequiredMemberClusters: 2, Members: []memberTargetResult{}}
	validate := func(report estatePreflightReport) error {
		raw, err := json.Marshal(report)
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		return schema.Validate(doc)
	}
	if err := validate(report); err != nil {
		t.Fatal(err)
	}
	report.Status = "PASS"
	if validate(report) == nil {
		t.Fatal("unbound pass accepted")
	}
	report.EvidenceSHA256 = strings.Repeat("a", 64)
	report.QualificationID = c.QualificationID
	report.ConfiguredMemberTargets = 2
	for _, target := range c.MemberTargets {
		report.Members = append(report.Members, memberTargetResult{Name: target.Name, ClusterID: target.ClusterID, ProjectID: target.ProjectID, Namespace: target.Namespace, ExpectedPrivilegeProfile: "admin", Status: "PASS", Reasons: []string{"corroborated"}})
	}
	if err := validate(report); err != nil {
		t.Fatal(err)
	}
	report.LifecycleQualified = true
	if validate(report) == nil {
		t.Fatal("readiness promoted to lifecycle qualification")
	}
	report.LifecycleQualified = false
	report.Candidate.Dirty = true
	if validate(report) == nil {
		t.Fatal("dirty candidate accepted")
	}
}
