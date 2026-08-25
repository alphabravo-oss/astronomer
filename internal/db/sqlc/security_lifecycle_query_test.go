package sqlc

import (
	"strings"
	"testing"
)

func TestCreateCISScanWithOutboxIsOneStatement(t *testing.T) {
	want := []string{
		"WITH scan AS (",
		"INSERT INTO security_scan_results",
		"INSERT INTO task_outbox",
		"INSERT INTO audit_outbox",
		"'security.scan.create'",
		"'security:ingest_scan_results'",
		"'tunnel'",
		"SELECT scan.id",
	}
	for _, fragment := range want {
		if !strings.Contains(createCISScanWithOutbox, fragment) {
			t.Errorf("atomic create query missing %q", fragment)
		}
	}
}

func TestSecurityScanTerminalUpdatesAreCompareAndSwap(t *testing.T) {
	for name, query := range map[string]string{
		"complete": finalizeSecurityScanReport,
		"fail":     failSecurityScanPoll,
	} {
		for _, guard := range []string{"poll_generation =", "status IN ('pending', 'running', 'in_progress')", "cancel_requested_at IS NULL"} {
			if !strings.Contains(query, guard) {
				t.Errorf("%s terminal query missing guard %q", name, guard)
			}
		}
	}
	if !strings.Contains(finalizeSecurityScanReport, "poll_owner =") {
		t.Error("completion must require the active lease owner")
	}
}

func TestSecurityScanClaimRejectsUnexpiredLease(t *testing.T) {
	if !strings.Contains(claimSecurityScanPoll, "poll_lease_expires_at <=") {
		t.Fatal("claim query must require an expired lease")
	}
	if strings.Contains(claimSecurityScanPoll, "OR poll_owner =") {
		t.Fatal("same-replica duplicate deliveries must not bypass an unexpired lease")
	}
}

func TestSecurityScanReadQueriesScopeBeforePaginationAndHideDecommissionedClusters(t *testing.T) {
	for name, query := range map[string]string{
		"estate list":    listSecurityScanResults,
		"scoped list":    listSecurityScanResultsForScopes,
		"cluster list":   listScansByCluster,
		"cluster object": getSecurityScanResultByClusterAndID,
		"estate object":  getActiveSecurityScanResultByID,
		"scoped object":  getActiveSecurityScanResultByIDForScopes,
		"estate count":   countSecurityScanResults,
		"scoped count":   countSecurityScanResultsForScopes,
		"cluster count":  countSecurityScanResultsByCluster,
	} {
		if !strings.Contains(query, "JOIN clusters c") || !strings.Contains(query, "c.decommissioned_at IS NULL") {
			t.Errorf("%s does not exclude decommissioned cluster rows", name)
		}
	}
	for name, query := range map[string]string{
		"scoped list":   listSecurityScanResultsForScopes,
		"scoped object": getActiveSecurityScanResultByIDForScopes,
		"scoped count":  countSecurityScanResultsForScopes,
	} {
		if !strings.Contains(query, "s.cluster_id = ANY(") {
			t.Errorf("%s does not apply the authorized cluster set in SQL", name)
		}
	}
	if strings.Index(listSecurityScanResultsForScopes, "s.cluster_id = ANY(") > strings.Index(listSecurityScanResultsForScopes, "LIMIT") {
		t.Fatal("scoped scan authorization predicate must be applied before LIMIT")
	}
}
