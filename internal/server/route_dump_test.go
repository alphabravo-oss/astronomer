package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"
)

// routeDumpEntry is the minimal method+path shape consumed by
// scripts/openapi-coverage.mjs. Patterns are normalized to the clean
// public form (no /api/v1 star-mount artifact, brace params, no trailing
// slash beyond the root) via normalizeRoutePattern.
type routeDumpEntry struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
}

// TestRouteDumpCanBeGenerated walks the fully-wired chi router and, when
// DUMP_ROUTES=1, writes the complete method+path table to docs/routes.json.
// Without the env var it just asserts the dump is non-trivial so the test
// stays meaningful in normal CI runs.
//
//	DUMP_ROUTES=1 go test ./internal/server/ -run TestRouteDumpCanBeGenerated
func TestRouteDumpCanBeGenerated(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)

	var entries []routeDumpEntry
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		entries = append(entries, routeDumpEntry{
			Method:  method,
			Pattern: normalizeRoutePattern(route),
		})
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}

	if len(entries) < 100 {
		t.Fatalf("route dump entries = %d, want at least 100", len(entries))
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Pattern == entries[j].Pattern {
			return entries[i].Method < entries[j].Method
		}
		return entries[i].Pattern < entries[j].Pattern
	})

	t.Logf("route dump entries = %d", len(entries))
	if os.Getenv("DUMP_ROUTES") != "1" {
		return
	}

	path := filepath.Join("..", "..", "docs", "routes.json")
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal route dump: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write route dump: %v", err)
	}
	t.Logf("wrote %d route entries to %s", len(entries), path)
}
