package server

import (
	"os"
	"strings"
	"testing"
)

func TestAdminQueuesProductionWiresExactTransactionRunner(t *testing.T) {
	source, err := os.ReadFile("app_router_composition.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	constructor := strings.Index(text, "h := handler.NewAdminQueuesHandler(asynq.NewInspector(redisOpt), queries)")
	if constructor < 0 {
		t.Fatal("AdminQueues production constructor not found")
	}
	end := strings.Index(text[constructor:], "return h")
	if end < 0 {
		t.Fatal("AdminQueues production constructor terminator not found")
	}
	block := text[constructor : constructor+end]
	want := "h.SetRunTx(sqlcMutationTxRunner[handler.AdminQueueMutationTx](database))"
	if !strings.Contains(block, want) {
		t.Fatalf("AdminQueues production constructor does not wire exact transaction runner: %s", block)
	}
}

func TestAdminQueueOperationStatusRouteIsRegistered(t *testing.T) {
	source, err := os.ReadFile("routes_api_entry.go")
	if err != nil {
		t.Fatal(err)
	}
	want := `Get("/admin/queues/operations/{id}/", deps.AdminQueues.GetOperation)`
	if !strings.Contains(string(source), want) {
		t.Fatalf("durable admin queue operation receipt route %q is not registered", want)
	}
}
