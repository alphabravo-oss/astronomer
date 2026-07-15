package handler

import (
	"strings"
	"testing"
)

func TestArgoSyncAndOperationOpenAPIContractIsServed(t *testing.T) {
	raw, err := docsFS.ReadFile("assets/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	for _, required := range []string{
		"ArgoSyncRequest:",
		"ArgoOperation:",
		"/api/v1/argocd/operations/:",
		"/api/v1/argocd/operations/{id}/:",
		"/api/v1/argocd/applications/{id}/sync/:",
		"/api/v1/argocd/instances/{id}/applications/{name}/sync/:",
		"A 404 means the durable operation row does not exist; it never indicates completion.",
		"No Application spec payload is accepted.",
	} {
		if !strings.Contains(spec, required) {
			t.Fatalf("served OpenAPI is missing %q", required)
		}
	}
}
