package server

import (
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func TestPodDeleteProductionWiresExactTransactionRunner(t *testing.T) {
	source, err := os.ReadFile("app_core_handlers.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	constructor := strings.Index(text, "workloadHandler := handler.NewWorkloadHandlerWithDeps(queries, requester)")
	if constructor < 0 {
		t.Fatal("workload handler production constructor not found")
	}
	end := strings.Index(text[constructor:], "workloadHandler.SetLogger(logger)")
	if end < 0 {
		t.Fatal("workload handler production constructor terminator not found")
	}
	block := text[constructor : constructor+end]
	want := "workloadHandler.SetRunTx(sqlcMutationTxRunner[handler.WorkloadMutationTx](database))"
	if !strings.Contains(block, want) {
		t.Fatalf("pod delete production constructor does not wire %q: %s", want, block)
	}
}

func TestPodDeleteRouteRetainsWriteScopeAndPodDeleteAuthorization(t *testing.T) {
	source, err := os.ReadFile("routes_resources_workloads.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "r.Use(mutationWriteScope)") || !strings.Contains(text, "r.Use(idem)") {
		t.Fatal("workload mutation group must retain write-scope and idempotency middleware")
	}
	if got := routePermissions["DELETE /api/v1/workloads/pods/{cluster_id}/{namespace}/{pod}/"]; got.Resource != rbac.ResourcePods || got.Verb != rbac.VerbDelete || got.Scope != ScopeRoute {
		t.Fatalf("pod delete route must retain pods:delete authorization, table has %s", got)
	}
}
