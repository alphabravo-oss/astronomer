package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestInventoryRegistryFollowsEveryCanonicalPage(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret-token" {
			t.Fatalf("authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("offset") == "1" {
			_, _ = w.Write([]byte(`{"data":[{"slug":"two"}],"pagination":{"limit":1,"offset":1,"has_more":false,"next_offset":null,"total":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"slug":"one"}],"pagination":{"limit":1,"offset":0,"has_more":true,"next_offset":1,"total":2}}`))
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result := inventoryRegistry(context.Background(), server.Client(), base, "test-secret-token", registryContract{
		ID: "tools", Path: "/api/v1/tools/?limit=1&offset=0", ItemsPath: "data", IdentityField: "slug", ExpectedIdentities: []string{"one", "two"},
	}, nil, nil)
	if result.Status != "PASS" || result.Pages != 2 || requests != 2 {
		t.Fatalf("inventory result = %#v, requests = %d", result, requests)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "test-secret-token") {
		t.Fatal("evidence contained the bearer token")
	}
}

func TestInventoryRegistryFailsClosedOnUnknownOffering(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"type":"known"},{"type":"new-provider"}]`))
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := inventoryRegistry(context.Background(), server.Client(), base, "token", registryContract{
		ID: "providers", Path: "/api/v1/providers/", IdentityField: "type", ExpectedIdentities: []string{"known"},
	}, nil, nil)
	if result.Status != "FAIL" || !slices.Equal(result.AdditionalIdentities, []string{"new-provider"}) {
		t.Fatalf("inventory result = %#v", result)
	}
}

func TestInventoryRegistryRejectsCrossOriginPagination(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"next":"https://attacker.invalid/api/v1/steal"}`))
	}))
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	result := inventoryRegistry(context.Background(), server.Client(), base, "token", registryContract{
		ID: "tools", Path: "/api/v1/tools/", ItemsPath: "data", IdentityField: "slug",
	}, nil, nil)
	if result.Status != "FAIL" || result.Reason != "pagination continuation escaped the configured API origin" {
		t.Fatalf("inventory result = %#v", result)
	}
}

func TestInventoryClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	t.Cleanup(destination.Close)
	source := httptest.NewServer(http.RedirectHandler(destination.URL+"/api/v1/steal", http.StatusFound))
	t.Cleanup(source.Close)
	base, _ := url.Parse(source.URL)
	result := inventoryRegistry(context.Background(), inventoryHTTPClient(), base, "token", registryContract{
		ID: "tools", Path: "/api/v1/tools/", ItemsPath: "data", IdentityField: "slug",
	}, nil, nil)
	if result.Status != "FAIL" || result.HTTPStatuses[0] != http.StatusFound || redirected {
		t.Fatalf("redirect was not rejected before follow: result=%#v redirected=%t", result, redirected)
	}
}

func TestInventoryRegistryAcceptsExplicitlyDisabledFeatureRoute(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	base, _ := url.Parse(server.URL)
	contract := registryContract{
		ID: "extensions", Path: "/api/v1/extensions/", ItemsPath: "data.items", IdentityField: "name",
		AllowAdditional: true, AbsentWhenFeatureDisabled: "feature.extensions",
	}
	result := inventoryRegistry(context.Background(), server.Client(), base, "token", contract, nil, map[string]bool{"feature.extensions": false})
	if result.Status != "PASS" || result.HTTPStatuses[0] != http.StatusNotFound {
		t.Fatalf("inventory result = %#v", result)
	}
	withoutEvidence := inventoryRegistry(context.Background(), server.Client(), base, "token", contract, nil, nil)
	if withoutEvidence.Status != "FAIL" {
		t.Fatalf("404 without a disabled feature flag was accepted: %#v", withoutEvidence)
	}
}

func TestVerifyRejectsEveryFalseGreenState(t *testing.T) {
	t.Parallel()
	manifest := testManifest()
	base := passingReport(manifest)
	tests := map[string]func(*report){
		"inventory-only mode": func(value *report) { value.Mode = "inventory" },
		"dirty candidate":     func(value *report) { value.Candidate.Dirty = true },
		"not run":             func(value *report) { value.Cases[0].State = "NOT_RUN" },
		"blocked":             func(value *report) { value.Cases[0].State = "BLOCKED" },
		"failed cleanup":      func(value *report) { value.Cleanup.State = "FAIL" },
		"missing case":        func(value *report) { value.Cases = value.Cases[:1] },
		"duplicate case": func(value *report) {
			value.Cases[1].ID = value.Cases[0].ID
		},
		"unknown registry":      func(value *report) { value.Inventory[0].ID = "other" },
		"wrong registry path":   func(value *report) { value.Inventory[0].Path = "/api/v1/other/" },
		"forged expectations":   func(value *report) { value.Inventory[0].ExpectedIdentities = []string{"forged"} },
		"forged observations":   func(value *report) { value.Inventory[0].ObservedIdentities = []string{"forged"} },
		"missing page evidence": func(value *report) { value.Inventory[0].Pages = 0 },
		"invalid source digest": func(value *report) { value.Candidate.StagedDiffSHA = "not-a-sha256" },
		"unmatched offering": func(value *report) {
			value.Inventory[0].AdditionalIdentities = []string{"surprise"}
		},
		"false summary": func(value *report) { value.Summary.Total++ },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Cases = append([]caseResult(nil), base.Cases...)
			value.Inventory = append([]registryResult(nil), base.Inventory...)
			value.Summary = resultSummary{Total: base.Summary.Total, InventoryOK: base.Summary.InventoryOK, InventoryBad: base.Summary.InventoryBad, ByState: cloneIntMap(base.Summary.ByState)}
			mutate(&value)
			if err := verifyReport(manifest, value); err == nil {
				t.Fatal("false-green report was accepted")
			}
		})
	}
}

func TestVerifyAcceptsCompleteEvidence(t *testing.T) {
	t.Parallel()
	manifest := testManifest()
	evidence := passingReport(manifest)
	if err := verifyReport(manifest, evidence); err != nil {
		t.Fatal(err)
	}
}

func TestReportsMatchClosedEvidenceSchema(t *testing.T) {
	t.Parallel()
	rawSchema, err := os.ReadFile("../../deploy/release/offering-qualification.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawSchema))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("offering-qualification.schema.json", schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("offering-qualification.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	rawReport, err := json.Marshal(passingReport(testManifest()))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawReport))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatal(err)
	}
}

func testManifest() caseManifest {
	return caseManifest{
		SchemaVersion: casesSchema,
		Cases: []caseDefinition{
			{ID: "APP-01", Category: "app", Title: "One", Section: "Apps", Required: true},
			{ID: "TOOL-01", Category: "tool", Title: "One", Section: "Tools", Required: true},
		},
		RuntimeRegistries: []registryContract{{ID: "tools", Path: "/api/v1/tools/"}},
	}
}

func passingReport(manifest caseManifest) report {
	value := report{
		SchemaVersion: reportSchema,
		RunID:         "run-1",
		GeneratedAt:   time.Now().UTC(),
		Mode:          "qualification",
		Candidate: candidate{
			Commit: "0123456789abcdef0123456789abcdef01234567", StagedDiffSHA: digest(nil), UnstagedDiffSHA: digest(nil), UntrackedSHA: digest(nil),
		},
		Target: target{BaseOrigin: "https://astronomer.example.com", TargetIDs: map[string]string{}},
		Inventory: []registryResult{{
			ID: "tools", Path: "/api/v1/tools/", Pages: 1, HTTPStatuses: []int{200}, ObservedIdentities: []string{},
			ExpectedIdentities: []string{}, MissingIdentities: []string{}, AdditionalIdentities: []string{}, Status: "PASS", Reason: "matched",
		}},
		Cleanup: cleanupResult{State: "PASS", Reason: "removed run-owned resources"},
	}
	for _, definition := range manifest.Cases {
		value.Cases = append(value.Cases, caseResult{ID: definition.ID, State: "PASS"})
	}
	value.Summary = summarize(value)
	return value
}

func cloneIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
