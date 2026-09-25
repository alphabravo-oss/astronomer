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

// Cluster rows are retained as tombstones for audit and recovery evidence.
// Project navigation must follow the active-cluster boundary or every
// decommissioned test cluster leaves its generated system/default projects in
// selectors and fleet pages until the tombstone retention sweep runs.
func TestProjectLists_GuardDecommissionedClusters(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "projects.sql"))
	if err != nil {
		t.Fatalf("read projects.sql: %v", err)
	}
	queryFile := string(body)
	for _, name := range []string{
		"ListProjects",
		"CountProjectsFiltered",
		"ListProjectsForScopes",
		"CountProjectsForScopes",
		"ListProjectsByCluster",
		"CountProjectsByClusterFiltered",
		"CountProjects",
		"CountProjectsByCluster",
	} {
		t.Run(name, func(t *testing.T) {
			query := namedProjectQuery(t, queryFile, name)
			if !strings.Contains(query, "JOIN clusters") {
				t.Errorf("query must join clusters to filter tombstones; body:\n%s", query)
			}
			if !strings.Contains(query, "decommissioned_at IS NULL") {
				t.Errorf("query must exclude decommissioned clusters; body:\n%s", query)
			}
		})
	}
}

func namedProjectQuery(t *testing.T, queryFile, name string) string {
	t.Helper()
	header := "-- name: " + name + " "
	start := strings.Index(queryFile, header)
	if start < 0 {
		t.Fatalf("%s query missing from projects.sql", name)
	}
	query := queryFile[start+len(header):]
	if next := strings.Index(query, "-- name:"); next >= 0 {
		query = query[:next]
	}
	return query
}
