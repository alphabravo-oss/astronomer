package migrations_test

// Content tests for migration 086_cluster_condition_remediation.
//
// We don't run the SQL against Postgres here — helm-test covers that.
// What we pin is the shape: a future edit can't drop the indexes the
// reconciler's backoff lookup depends on, drop the CHECK on outcome,
// or break the cascade that lets cluster decommissions also tidy
// these rows.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMig086(t *testing.T, name string) string {
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

func TestConditionRemediation_UpContent(t *testing.T) {
	up := loadMig086(t, "086_cluster_condition_remediation.up.sql")
	mustContain := []string{
		// Table + key fields.
		"CREATE TABLE cluster_condition_remediation_attempts",
		"cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		"condition_type  VARCHAR(64) NOT NULL",
		"action          VARCHAR(64) NOT NULL",
		"outcome         VARCHAR(16) NOT NULL CHECK (outcome IN ('success', 'failed', 'skipped'))",
		"detail          JSONB       NOT NULL DEFAULT '{}'::jsonb",
		"attempted_at    TIMESTAMPTZ NOT NULL DEFAULT now()",
		// Two required indexes. Names are load-bearing — the down
		// migration drops by name.
		"CREATE INDEX idx_ccra_cluster_type_attempted",
		"ON cluster_condition_remediation_attempts (cluster_id, condition_type, attempted_at DESC)",
		"CREATE INDEX idx_ccra_attempted_at",
	}
	for _, s := range mustContain {
		if !strings.Contains(up, s) {
			t.Errorf("up file missing required content:\n  %q", s)
		}
	}
}

func TestConditionRemediation_DownDropsByName(t *testing.T) {
	down := loadMig086(t, "086_cluster_condition_remediation.down.sql")
	// Indexes drop first (Postgres tolerates either order, but the
	// convention in this codebase is index-then-table for clarity).
	if !strings.Contains(down, "DROP INDEX IF EXISTS idx_ccra_attempted_at") {
		t.Errorf("down should drop idx_ccra_attempted_at by name")
	}
	if !strings.Contains(down, "DROP INDEX IF EXISTS idx_ccra_cluster_type_attempted") {
		t.Errorf("down should drop idx_ccra_cluster_type_attempted by name")
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS cluster_condition_remediation_attempts") {
		t.Errorf("down should drop the table by name with IF EXISTS")
	}
}
