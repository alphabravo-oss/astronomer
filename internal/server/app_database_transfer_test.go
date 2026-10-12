package server

import (
	"context"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestDatabaseOfflineTransferRejectsMissingFeatureAndClosedCommandInputsBeforeDatabase(t *testing.T) {
	for _, cfg := range []*config.Config{nil, {DeliveryDatabaseUpdatesEnabled: false}} {
		if _, err := ImportDatabaseUpdate(t.Context(), cfg, DatabaseImportCommandOptions{KitDirectory: "/private/kit"}); err == nil {
			t.Fatal("disabled import reached database")
		}
		if _, err := ExportDatabaseUpdate(t.Context(), cfg, DatabaseExportCommandOptions{OutputDirectory: "/private/new-kit", FeedDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err == nil {
			t.Fatal("disabled export reached database")
		}
	}
	enabled := &config.Config{DeliveryDatabaseUpdatesEnabled: true}
	if _, err := ImportDatabaseUpdate(context.Background(), enabled, DatabaseImportCommandOptions{KitDirectory: "relative"}); err == nil {
		t.Fatal("relative import root accepted")
	}
	if _, err := ExportDatabaseUpdate(t.Context(), enabled, DatabaseExportCommandOptions{OutputDirectory: "/private/new-kit", FeedDigest: "latest"}); err == nil {
		t.Fatal("mutable export feed accepted")
	}
	if _, err := ImportDatabaseUpdate(nil, enabled, DatabaseImportCommandOptions{KitDirectory: "/private/kit"}); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil command context accepted")
	}
}
