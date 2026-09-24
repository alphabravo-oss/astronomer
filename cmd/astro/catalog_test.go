package main

import (
	"strings"
	"testing"
)

func TestCatalogInstallRequiresProjectAndImmutableVersion(t *testing.T) {
	cmd := newCatalogInstallCmd()
	cmd.SetArgs([]string{
		"--cluster", "11111111-1111-4111-8111-111111111111",
		"--namespace", "payments",
		"--release-name", "payments",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--project") || !strings.Contains(err.Error(), "--chart-version") {
		t.Fatalf("expected project and immutable chart-version validation, got %v", err)
	}
}

func TestCatalogOptionalUUID(t *testing.T) {
	if got, err := catalogOptionalUUID("", "project"); err != nil || got != nil {
		t.Fatalf("empty optional UUID = %v, %v; want nil, nil", got, err)
	}
	if _, err := catalogOptionalUUID("not-a-uuid", "project"); err == nil || !strings.Contains(err.Error(), "invalid --project") {
		t.Fatalf("invalid optional UUID error = %v", err)
	}
}

func TestCatalogReadbackCommandsUnwrapCanonicalAPIEnvelopes(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name           string
		exchange       operatorExchange
		response, want string
	}{
		{"values", operatorExchange{args: []string{"catalog", "installed", "get-values", id}, method: "GET", path: "/api/v1/catalog/installed/" + id + "/values/", status: 200}, `{"data":{"release_name":"demo","namespace":"default","values_override":"replicas: 2"}}`, "replicas: 2"},
		{"operation", operatorExchange{args: []string{"catalog", "operations", "get", id}, method: "GET", path: "/api/v1/catalog/operations/" + id + "/", status: 200}, `{"data":{"id":"` + id + `","status":"running","journalStatus":"completed","deliveryPhase":"pending","events":[{"message":"queued"}]}}`, "queued"},
		{"retry", operatorExchange{args: []string{"catalog", "operations", "retry", id}, method: "POST", path: "/api/v1/catalog/operations/" + id + "/retry/", status: 202, idempotent: true}, `{"data":{"id":"` + id + `","status":"pending","journalStatus":"pending"}}`, "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := runOperatorExchange(t, tc.exchange, tc.exchange.status, tc.response)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, tc.want) || strings.Contains(output, `"data"`) {
				t.Fatalf("command did not unwrap canonical envelope: %s", output)
			}
		})
	}
}
