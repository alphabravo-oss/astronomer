package server

import (
	"os"
	"strings"
	"testing"
)

func TestResourceSettingsSSOProductionWiresExactTransactionRunner(t *testing.T) {
	source, err := os.ReadFile("app_integrations.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	constructor := strings.Index(text, "resourceHandler := handler.NewResourceHandlerWithQueries(queries, requester)")
	if constructor < 0 {
		t.Fatal("ResourceHandler production constructor not found")
	}
	end := strings.Index(text[constructor:], "resourceHandler.SetEncryptor(encryptor)")
	if end < 0 {
		t.Fatal("ResourceHandler production wiring block terminator not found")
	}
	block := text[constructor : constructor+end]
	want := "resourceHandler.SetRunTx(sqlcMutationTxRunner[handler.ResourceSettingsMutationTx](database))"
	if !strings.Contains(block, want) {
		t.Fatalf("ResourceHandler production constructor does not wire the exact settings/SSO transaction runner: %s", block)
	}
}
