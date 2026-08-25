package server

import (
	"os"
	"strings"
	"testing"
)

func TestChartRatingsProductionWiresTransactionRunner(t *testing.T) {
	source, err := os.ReadFile("app_router_dependencies_core.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	constructor := strings.Index(text, "h := handler.NewChartRatingsHandler(queries)")
	if constructor < 0 {
		t.Fatal("ChartRatings production constructor not found")
	}
	end := strings.Index(text[constructor:], "return h")
	if end < 0 {
		t.Fatal("ChartRatings production constructor terminator not found")
	}
	block := text[constructor : constructor+end]
	want := "h.SetRunTx(sqlcMutationTxRunner[handler.ChartRatingMutationTx](database))"
	if !strings.Contains(block, want) {
		t.Fatalf("ChartRatings production constructor does not wire the exact transaction runner: %s", block)
	}
}
