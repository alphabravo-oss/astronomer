package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestToolDriftLeaseMigrationSupportsDistributedClaims(t *testing.T) {
	raw, err := os.ReadFile("046_tool_drift_leases.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"ADD COLUMN drift_locked_until timestamptz",
		"ADD COLUMN drift_claim_token uuid",
		"installed_charts_drift_claim_idx",
		"WHERE status IN ('installed', 'deployed', 'upgraded')",
		"ADD COLUMN reconcile_claim_token uuid",
		"ADD COLUMN decommission_claim_token uuid",
		"ADD COLUMN decommission_lease_until timestamptz",
		"cluster_decommissions_claim_idx",
		"WHERE status IN ('pending', 'failed', 'running')",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("tool drift lease migration missing %q", required)
		}
	}
}
