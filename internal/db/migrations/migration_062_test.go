package migrations_test

// Static content test for migration 062_image_vulnerabilities.
//
// As with the other migration_*_test.go siblings, we DO NOT run the
// migration against Postgres — CI's helm-test path covers that via the
// migrate-job container. We check the SHAPE of the SQL so future edits
// can't quietly:
//
//   - Drop the FK ON DELETE CASCADE on cluster_id / report_id. Without
//     it, deleting a cluster would leave dangling vulnerability reports
//     and the migration's "decommission also wipes scans" property
//     breaks.
//   - Drop the (cluster_id, report_name) UNIQUE. Without it the
//     upsert path inserts duplicate rows on every re-ingest.
//   - Forget the severity index used by the top-vulnerable-images
//     listing — querying tens of thousands of rows would degrade into
//     a seq-scan.
//   - Forget the seed of the Aqua helm repo / trivy-operator chart.
//     Without those rows the catalog UI cannot install the operator
//     from the chart picker.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMigration062File(t *testing.T, name string) string {
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

func TestMigration_ImageVulnerabilities_UpContent(t *testing.T) {
	up := loadMigration062File(t, "062_image_vulnerabilities.up.sql")
	wants := []string{
		"CREATE TABLE image_vulnerability_reports",
		"cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE",
		"UNIQUE (cluster_id, report_name)",
		"critical_count  INTEGER NOT NULL DEFAULT 0",
		"high_count      INTEGER NOT NULL DEFAULT 0",
		"medium_count    INTEGER NOT NULL DEFAULT 0",
		"low_count       INTEGER NOT NULL DEFAULT 0",
		"unknown_count   INTEGER NOT NULL DEFAULT 0",
		"scanned_at      TIMESTAMPTZ NOT NULL",
		"CREATE INDEX idx_ivr_cluster_severity",
		"CREATE INDEX idx_ivr_cluster_ns",

		"CREATE TABLE image_vulnerabilities",
		"report_id       UUID NOT NULL REFERENCES image_vulnerability_reports(id) ON DELETE CASCADE",
		"vulnerability_id VARCHAR(64) NOT NULL",
		"cvss_score      NUMERIC(4,1)",
		"UNIQUE (report_id, vulnerability_id, pkg_name, installed_version)",
		"CREATE INDEX idx_image_vulns_severity",

		// Catalog seed must be idempotent so re-runs are harmless.
		"INSERT INTO helm_repositories",
		"'aqua'",
		"ON CONFLICT (name) DO NOTHING",
		"INSERT INTO helm_charts",
		"'trivy-operator'",
		"ON CONFLICT (repository_id, name) DO NOTHING",
	}
	for _, w := range wants {
		if !strings.Contains(up, w) {
			t.Errorf("up migration missing required text %q", w)
		}
	}
}

func TestMigration_ImageVulnerabilities_DownContent(t *testing.T) {
	down := loadMigration062File(t, "062_image_vulnerabilities.down.sql")
	wants := []string{
		"DROP INDEX IF EXISTS idx_image_vulns_severity",
		"DROP TABLE IF EXISTS image_vulnerabilities",
		"DROP INDEX IF EXISTS idx_ivr_cluster_ns",
		"DROP INDEX IF EXISTS idx_ivr_cluster_severity",
		"DROP TABLE IF EXISTS image_vulnerability_reports",
	}
	for _, w := range wants {
		if !strings.Contains(down, w) {
			t.Errorf("down migration missing required text %q", w)
		}
	}
	// CVE table must drop before the parent (FK target) for symmetry —
	// not strictly required because DROP TABLE drops dependent indexes
	// + FK metadata, but matching the convention from migration 054
	// keeps the rollback log clean.
	posCVE := strings.Index(down, "DROP TABLE IF EXISTS image_vulnerabilities")
	posIVR := strings.Index(down, "DROP TABLE IF EXISTS image_vulnerability_reports")
	if posCVE < 0 || posIVR < 0 {
		t.Fatalf("missing DROP TABLE statements")
	}
	if posCVE > posIVR {
		t.Errorf("image_vulnerabilities should drop before image_vulnerability_reports")
	}
}
