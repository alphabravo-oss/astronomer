package server

import (
	"os"
	"strings"
	"testing"
)

func TestWorkloadOperationReceiptRouteDefersRowAwareAuthorizationToHandler(t *testing.T) {
	source, err := os.ReadFile("routes_resources_workloads.go")
	if err != nil {
		t.Fatal(err)
	}
	route := `r.Get("/workloads/operations/{id}/", deps.Workloads.GetOperation)`
	if !strings.Contains(string(source), route) {
		t.Fatalf("receipt route must be authenticated by the protected router and authorized after loading its row")
	}
	legacy := `requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceWorkloads, rbac.VerbRead)).Get("/workloads/operations/{id}/"`
	if strings.Contains(string(source), legacy) {
		t.Fatalf("upfront workloads:read gate blocks mutation-only creators from polling their own receipt")
	}
}
