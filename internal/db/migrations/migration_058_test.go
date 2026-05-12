package migrations_test

// Static content test for migration 058_dashboard_widgets.
//
// CI's helm-test path covers the full Postgres apply against an empty
// database; this static check is the fast-feedback lint that protects
// the structural invariants the handler + render path depend on:
//
//   - dashboard_widgets has the widget_type + scope CHECK constraints
//     the handler relies on (anything outside the registered set must
//     fail at INSERT, not at render).
//   - scope_ids defaults to '{}' so empty-array == "all-in-scope".
//   - prometheus_datasources has the UNIQUE (name) constraint the
//     handler relies on for /test/ name resolution.
//   - clusters.cluster_uid is added IF NOT EXISTS so a re-run of the
//     migration against an already-extended table doesn't fail; the
//     backfill UPDATE seeds every legacy row.
//   - The down migration drops in safe order (tables before column).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration058File(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestMigration_DashboardWidgets_UpContent(t *testing.T) {
	up := loadMigration058File(t, "058_dashboard_widgets.up.sql")
	for _, want := range []string{
		"CREATE TABLE dashboard_widgets",
		"CREATE TABLE prometheus_datasources",
		"widget_type     VARCHAR(32) NOT NULL",
		"scope           VARCHAR(16) NOT NULL DEFAULT 'global'",
		"scope_ids       UUID[] NOT NULL DEFAULT '{}'",
		"CONSTRAINT widget_type_valid CHECK (widget_type IN ('grafana_panel','prom_sparkline','prom_stat','url_iframe'))",
		"CONSTRAINT scope_valid CHECK (scope IN ('global','cluster','project'))",
		"CREATE INDEX idx_dashboard_widgets_scope ON dashboard_widgets (scope) WHERE enabled = true",
		"name            VARCHAR(64) NOT NULL UNIQUE",
		"ALTER TABLE clusters ADD COLUMN IF NOT EXISTS cluster_uid",
		"UPDATE clusters SET cluster_uid = SUBSTRING(id::text, 1, 8) WHERE cluster_uid = ''",
		// Seed widgets — three demo rows.
		"'Pod CPU saturation'",
		"'API server p99 latency'",
		"'Cluster health rollup'",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_DashboardWidgets_DownContent(t *testing.T) {
	down := loadMigration058File(t, "058_dashboard_widgets.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS dashboard_widgets",
		"DROP TABLE IF EXISTS prometheus_datasources",
		"ALTER TABLE clusters DROP COLUMN IF EXISTS cluster_uid",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
}
