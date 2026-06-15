package sqlc

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// These tests exercise the SQL strings + helper functions in the
// hand-rolled image_vulns_ext.sql.go without spinning up Postgres.
// They are intentionally narrow:
//
//   - Verify the SQL string for each query carries the columns / ORDER
//     BY / GROUP BY / aggregate operations the handler relies on. If a
//     future regeneration drops one of these clauses, the matching
//     handler-level behaviour would silently degrade — these tests
//     surface that as a failing build before a deployment ships.
//   - Verify the typed Go helpers (CVSSScoreFloat) decode pgtype.Numeric
//     the way the handler renderer expects.

func TestAggregateCluster_SumsByCritical(t *testing.T) {
	// The handler asserts the cluster aggregate query sums each severity
	// AND surfaces the report_count + last_scanned_at. If any of those
	// projections is dropped the per-cluster summary endpoint returns
	// zeroes everywhere.
	wants := []string{
		"COALESCE(SUM(critical_count), 0)::bigint AS critical",
		"COALESCE(SUM(high_count), 0)::bigint AS high",
		"COALESCE(SUM(medium_count), 0)::bigint AS medium",
		"COALESCE(SUM(low_count), 0)::bigint AS low",
		"COALESCE(SUM(unknown_count), 0)::bigint AS unknown",
		"COUNT(*)::bigint AS report_count",
		"COALESCE(MAX(scanned_at), '1970-01-01T00:00:00Z'::timestamptz) AS last_scanned_at",
		"WHERE cluster_id = $1",
	}
	for _, w := range wants {
		if !strings.Contains(aggregateClusterVulnerabilities, w) {
			t.Errorf("aggregateClusterVulnerabilities missing %q", w)
		}
	}
}

func TestAggregateFleet_IncludesClusterCount(t *testing.T) {
	if !strings.Contains(aggregateFleetVulnerabilities, "COUNT(DISTINCT cluster_id)::bigint AS cluster_count") {
		t.Errorf("aggregateFleetVulnerabilities missing cluster_count distinct-count")
	}
	// Must NOT have a WHERE cluster_id clause — this is the fleet view.
	if strings.Contains(aggregateFleetVulnerabilities, "WHERE cluster_id") {
		t.Errorf("aggregateFleetVulnerabilities should not filter by cluster_id")
	}
}

func TestTopVulnerableImages_Order(t *testing.T) {
	// Critical desc THEN high desc THEN scanned_at desc. The third
	// fallback is to keep the order deterministic across re-ingests
	// that don't change the severity tuple.
	if !strings.Contains(topVulnerableImages,
		"ORDER BY critical_count DESC, high_count DESC, scanned_at DESC") {
		t.Errorf("topVulnerableImages missing required ORDER BY")
	}
	if !strings.Contains(topVulnerableImages, "WHERE cluster_id = $1") {
		t.Errorf("topVulnerableImages must be cluster-scoped")
	}
}

func TestListVulnerabilitiesForReport_SeverityOrder(t *testing.T) {
	// CRITICAL → HIGH → MEDIUM → LOW → other. The handler relies on
	// this stable severity rank to render severity-coded rows.
	for _, sev := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW"} {
		if !strings.Contains(listVulnerabilitiesForReport, "'"+sev+"'") {
			t.Errorf("severity ranking missing %s", sev)
		}
	}
	if !strings.Contains(listVulnerabilitiesForReport, "cvss_score DESC NULLS LAST") {
		t.Errorf("CVE list must order by cvss_score desc nulls last")
	}
}

func TestTopClustersByVulnerability_GroupAndOrder(t *testing.T) {
	wants := []string{
		"GROUP BY cluster_id",
		"ORDER BY critical DESC, high DESC",
		"LIMIT $1",
	}
	for _, w := range wants {
		if !strings.Contains(topClustersByVulnerability, w) {
			t.Errorf("topClustersByVulnerability missing %q", w)
		}
	}
}

func TestUpsertImageVulnerabilityReport_OnConflictUpdatesAggregates(t *testing.T) {
	if !strings.Contains(upsertImageVulnerabilityReport, "ON CONFLICT (cluster_id, report_name) DO UPDATE") {
		t.Errorf("upsert missing the (cluster_id, report_name) ON CONFLICT clause")
	}
	for _, col := range []string{
		"critical_count  = EXCLUDED.critical_count",
		"high_count      = EXCLUDED.high_count",
		"medium_count    = EXCLUDED.medium_count",
		"low_count       = EXCLUDED.low_count",
		"unknown_count   = EXCLUDED.unknown_count",
		"scanned_at      = EXCLUDED.scanned_at",
		"updated_at      = now()",
	} {
		if !strings.Contains(upsertImageVulnerabilityReport, col) {
			t.Errorf("upsert must refresh column on conflict: %q", col)
		}
	}
}

func TestInsertImageVulnerability_OnConflictIsDoNothing(t *testing.T) {
	// The (report_id, vulnerability_id, pkg_name, installed_version)
	// uniqueness lets a re-batch be a no-op for duplicates without
	// blowing up the whole transaction.
	if !strings.Contains(insertImageVulnerability,
		"ON CONFLICT (report_id, vulnerability_id, pkg_name, installed_version) DO NOTHING") {
		t.Errorf("CVE insert must be ON CONFLICT DO NOTHING for idempotency")
	}
}

func TestCVSSScoreFloat_NullAndValue(t *testing.T) {
	var v ImageVulnerability
	if _, ok := v.CVSSScoreFloat(); ok {
		t.Fatalf("expected CVSSScoreFloat=false on zero (Valid=false) Numeric")
	}
	var n pgtype.Numeric
	if err := n.Scan("7.5"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	v.CvssScore = n
	got, ok := v.CVSSScoreFloat()
	if !ok {
		t.Fatalf("expected ok=true after setting 7.5")
	}
	if got != 7.5 {
		t.Fatalf("expected 7.5, got %v", got)
	}
}
