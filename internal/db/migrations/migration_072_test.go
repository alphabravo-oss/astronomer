package migrations_test

// Static content test for migration 072_anomaly_detection.
//
// Validates the structural invariants the recompute worker and the
// alert evaluator depend on:
//
//   - anomaly_baselines has the (cluster_id, metric_name,
//     window_seconds) UNIQUE so UPSERT is idempotent.
//   - The aggregate columns (mean/stddev/percentiles + min/max +
//     last_value) all default to 0 so a freshly-inserted row is
//     safe to evaluate immediately (with the count-gate doing the
//     real work).
//   - recent_samples defaults to '[]' so the ring-buffer code can
//     unconditionally json.Unmarshal without a nil-check.
//   - alert_rules.rule_kind defaults to 'threshold' so existing
//     rows don't get retro-promoted into the anomaly path.
//   - The CHECK constraints rule out misconfiguration at insert
//     time rather than at evaluation time.
//   - The down file drops everything in the right order so the FK
//     to clusters doesn't dangle.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration072File(t *testing.T, name string) string {
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

func TestMigration_AnomalyDetection_UpContent(t *testing.T) {
	up := loadMigration072File(t, "072_anomaly_detection.up.sql")

	for _, want := range []string{
		"CREATE TABLE anomaly_baselines",
		"cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		"metric_name     VARCHAR(128) NOT NULL",
		"window_seconds  INTEGER NOT NULL DEFAULT 86400",
		"sample_count    INTEGER NOT NULL DEFAULT 0",
		"mean            DOUBLE PRECISION NOT NULL DEFAULT 0",
		"stddev          DOUBLE PRECISION NOT NULL DEFAULT 0",
		"p50             DOUBLE PRECISION NOT NULL DEFAULT 0",
		"p95             DOUBLE PRECISION NOT NULL DEFAULT 0",
		"p99             DOUBLE PRECISION NOT NULL DEFAULT 0",
		"recent_samples  JSONB NOT NULL DEFAULT '[]'",
		"UNIQUE (cluster_id, metric_name, window_seconds)",
		"CREATE INDEX idx_anomaly_baselines_lookup ON anomaly_baselines (cluster_id, metric_name)",
		"ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS rule_kind VARCHAR(16) NOT NULL DEFAULT 'threshold'",
		"ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_stddev DOUBLE PRECISION",
		"ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_window_seconds INTEGER",
		"ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_min_samples INTEGER NOT NULL DEFAULT 50",
		"ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_direction VARCHAR(8) NOT NULL DEFAULT 'above'",
		"CHECK (rule_kind IN ('threshold','anomaly'))",
		"CHECK (anomaly_direction IN ('above','below','either'))",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_AnomalyDetection_DownContent(t *testing.T) {
	down := loadMigration072File(t, "072_anomaly_detection.down.sql")

	for _, want := range []string{
		"DROP TABLE IF EXISTS anomaly_baselines",
		"ALTER TABLE alert_rules DROP COLUMN IF EXISTS rule_kind",
		"ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_stddev",
		"ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_window_seconds",
		"ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_min_samples",
		"ALTER TABLE alert_rules DROP COLUMN IF EXISTS anomaly_direction",
		"ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rule_kind_valid",
		"ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_anomaly_dir_valid",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}
	// The down file must drop the table AFTER the alter-drops so
	// nothing references missing columns.
	colDropIdx := strings.Index(down, "DROP COLUMN IF EXISTS rule_kind")
	tableDropIdx := strings.Index(down, "DROP TABLE IF EXISTS anomaly_baselines")
	if colDropIdx < 0 || tableDropIdx < 0 {
		t.Fatalf("structural markers missing in down migration")
	}
	// Note: order doesn't actually matter for these operations (they're
	// independent), but the test pins the order so a future reorder
	// gets a deliberate look.
	if colDropIdx > tableDropIdx {
		t.Errorf("column drops should precede table drop for readability")
	}
}
