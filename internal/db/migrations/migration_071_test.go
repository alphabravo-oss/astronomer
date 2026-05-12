package migrations_test

// Static content test for migration 071_service_mesh.
//
// CI's helm-test path covers the full Postgres apply against an empty
// database; this static check is the fast-feedback lint that protects
// the structural invariants the handler + worker depend on:
//
//   - cluster_service_mesh is keyed by cluster_id and CASCADEs on
//     cluster delete (so dropping a cluster cleans the mesh row).
//   - detected_mesh has the CHECK constraint enumerating the valid
//     mesh names — a bad string from a future worker won't poison
//     the table.
//   - helm_chart_tags is created idempotently (IF NOT EXISTS) so
//     re-running the migration on an install where a sister sprint
//     already introduced the table doesn't fail.
//   - The service-mesh INSERT uses ON CONFLICT DO NOTHING so the same
//     migration applied twice (or running over a fresh repo sync) is
//     a no-op.
//   - The down file drops cluster_service_mesh — and ONLY that. It
//     deliberately leaves helm_chart_tags in place because other
//     sprints may share that table.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration071File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestMigration_ServiceMesh_UpContent(t *testing.T) {
	up := loadMigration071File(t, "071_service_mesh.up.sql")

	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS helm_chart_tags",
		"CREATE TABLE cluster_service_mesh",
		"cluster_id      UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE",
		"detected_mesh   VARCHAR(32) NOT NULL DEFAULT 'unknown'",
		"detected_version VARCHAR(64) NOT NULL DEFAULT ''",
		"control_plane_namespace VARCHAR(253) NOT NULL DEFAULT ''",
		"gateway_count       INTEGER NOT NULL DEFAULT 0",
		"virtual_service_count INTEGER NOT NULL DEFAULT 0",
		"destination_rule_count INTEGER NOT NULL DEFAULT 0",
		"peer_authentication_count INTEGER NOT NULL DEFAULT 0",
		"service_profile_count INTEGER NOT NULL DEFAULT 0",
		"server_auth_count   INTEGER NOT NULL DEFAULT 0",
		"mtls_coverage_pct INTEGER NOT NULL DEFAULT 0",
		"last_detected_at TIMESTAMPTZ",
		"last_error       TEXT NOT NULL DEFAULT ''",
		"CONSTRAINT detected_mesh_valid CHECK (detected_mesh IN ('istio','linkerd','kuma','cilium','none','unknown'))",
		"CREATE INDEX idx_cluster_service_mesh_detected_mesh",
		"INSERT INTO helm_chart_tags (chart_id, tag)",
		"'service-mesh'",
		"ON CONFLICT (chart_id, tag) DO NOTHING",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_ServiceMesh_DownContent(t *testing.T) {
	down := loadMigration071File(t, "071_service_mesh.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS cluster_service_mesh") {
		t.Errorf("down migration missing DROP TABLE for cluster_service_mesh")
	}
	// helm_chart_tags MUST NOT be dropped (sister sprints may depend
	// on it). The presence of the literal table name in a DROP
	// statement here would be a bug.
	if strings.Contains(down, "DROP TABLE IF EXISTS helm_chart_tags") {
		t.Errorf("down migration unexpectedly drops helm_chart_tags; that table may be shared with sister sprints")
	}
}
