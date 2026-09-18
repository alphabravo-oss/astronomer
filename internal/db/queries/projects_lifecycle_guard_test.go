package queries_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Decommissioned clusters are retained as tombstones, so their project
// namespace rows remain present. The periodic enforcement sweep must not keep
// claiming those rows and sending work to an agent that can never reconnect.
func TestListAllProjectNamespaces_GuardsDecommissioned(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "projects.sql"))
	if err != nil {
		t.Fatalf("read projects.sql: %v", err)
	}
	query := string(body)
	const header = "-- name: ListAllProjectNamespaces :many"
	start := strings.Index(query, header)
	if start < 0 {
		t.Fatal("ListAllProjectNamespaces query missing from projects.sql")
	}
	query = query[start+len(header):]
	if next := strings.Index(query, "-- name:"); next >= 0 {
		query = query[:next]
	}
	if !strings.Contains(query, "JOIN clusters") {
		t.Errorf("query must join clusters to filter tombstones; body:\n%s", query)
	}
	if !strings.Contains(query, "decommissioned_at IS NULL") {
		t.Errorf("query must exclude decommissioned cluster rows; body:\n%s", query)
	}
	if !strings.Contains(query, "c.status = 'active'") {
		t.Errorf("query must defer enforcement for disconnected clusters; body:\n%s", query)
	}
}
