package migrations_test

// Static content test for migration 066_cluster_groups.
//
// CI's helm-test path runs the full Postgres apply against an empty
// database; this static lint protects the structural invariants the
// handler + the fleet selector expander depend on:
//
//   - cluster_groups has the self-referencing parent_id with ON DELETE
//     CASCADE so deleting a subtree is a single operation.
//   - The (parent_id, slug) UNIQUE + the partial unique index on
//     (slug) WHERE parent_id IS NULL together enforce slug uniqueness
//     within a parent AND globally for top-level groups.
//   - clusters.group_id has ON DELETE SET NULL so deleting a group
//     unparents the clusters but leaves them in place.
//   - The down file drops the per-cluster column + the cluster_groups
//     table in the right order so the FK doesn't dangle.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration066File(t *testing.T, name string) string {
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

func TestMigration_ClusterGroups_UpContent(t *testing.T) {
	up := loadMigration066File(t, "066_cluster_groups.up.sql")

	for _, want := range []string{
		"CREATE TABLE cluster_groups",
		"name            VARCHAR(128) NOT NULL",
		"slug            VARCHAR(128) NOT NULL",
		"parent_id       UUID REFERENCES cluster_groups(id) ON DELETE CASCADE",
		"color           VARCHAR(16) NOT NULL DEFAULT '#6b7280'",
		"icon            VARCHAR(64) NOT NULL DEFAULT 'folder'",
		"enabled         BOOLEAN NOT NULL DEFAULT true",
		"created_by      UUID REFERENCES users(id) ON DELETE SET NULL",
		"UNIQUE (parent_id, slug)",
		"CREATE INDEX idx_cluster_groups_parent ON cluster_groups (parent_id) WHERE enabled = true",
		"CREATE UNIQUE INDEX idx_cluster_groups_toplevel_slug ON cluster_groups (slug) WHERE parent_id IS NULL",
		"ALTER TABLE clusters ADD COLUMN IF NOT EXISTS group_id UUID",
		"REFERENCES cluster_groups(id) ON DELETE SET NULL",
		"CREATE INDEX IF NOT EXISTS idx_clusters_group ON clusters (group_id) WHERE group_id IS NOT NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_ClusterGroups_DownContent(t *testing.T) {
	down := loadMigration066File(t, "066_cluster_groups.down.sql")

	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_clusters_group",
		"ALTER TABLE clusters DROP COLUMN IF EXISTS group_id",
		"DROP TABLE IF EXISTS cluster_groups",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// The clusters.group_id column must be dropped BEFORE cluster_groups
	// (the FK would block the parent drop otherwise).
	posColumn := strings.Index(down, "ALTER TABLE clusters DROP COLUMN IF EXISTS group_id")
	posTable := strings.Index(down, "DROP TABLE IF EXISTS cluster_groups")
	if posColumn < 0 || posTable < 0 {
		t.Fatalf("missing expected DROP statements")
	}
	if posColumn > posTable {
		t.Errorf("clusters.group_id dropped AFTER cluster_groups; FK would block rollback")
	}
}
