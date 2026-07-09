package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SEC-R03: hydrate chart download must reject loopback/private chart URLs.
func TestFetchHTTPChartArchive_RejectsLoopbackURL(t *testing.T) {
	h := NewCatalogHandler(nil)
	urls, _ := json.Marshal([]string{"http://127.0.0.1/charts/evil.tgz"})
	version := sqlc.HelmChartVersion{
		ID:   uuid.New(),
		Urls: urls,
	}
	repo := sqlc.HelmRepository{
		ID:  uuid.New(),
		Url: "https://charts.example.com",
	}
	_, err := h.fetchHTTPChartArchive(context.Background(), repo, version)
	if err == nil {
		t.Fatal("expected loopback chart URL to be rejected")
	}
	if !strings.Contains(err.Error(), "blocked") && !strings.Contains(err.Error(), "disallowed") {
		t.Fatalf("error should mention SSRF block, got: %v", err)
	}
}

// SEC-R03: CGNAT / private absolute chart URLs are rejected the same way.
func TestFetchHTTPChartArchive_RejectsCGNATURL(t *testing.T) {
	h := NewCatalogHandler(nil)
	urls, _ := json.Marshal([]string{"http://100.64.0.1/charts/evil.tgz"})
	version := sqlc.HelmChartVersion{ID: uuid.New(), Urls: urls}
	repo := sqlc.HelmRepository{ID: uuid.New(), Url: "https://charts.example.com"}
	_, err := h.fetchHTTPChartArchive(context.Background(), repo, version)
	if err == nil {
		t.Fatal("expected CGNAT chart URL to be rejected")
	}
}
