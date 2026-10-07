package migrations_test

import (
	"strings"
	"testing"
)

func TestClusterToolReleaseVersionsMigration(t *testing.T) {
	up := readMigration(t, "067_pin_cluster_tool_releases.up.sql")
	down := readMigration(t, "067_pin_cluster_tool_releases.down.sql")

	versions := map[string]string{
		"cis-operator":             "106.8.0+up8.10.0",
		"fluent-bit":               "0.58.1",
		"trivy-operator":           "0.36.0",
		"cert-manager":             "v1.21.1",
		"gatekeeper":               "3.23.0",
		"kube-state-metrics":       "8.4.0",
		"prometheus-node-exporter": "4.56.1",
		"ingress-nginx":            "4.15.1",
		"longhorn":                 "1.12.1",
		"neuvector":                "2.11.1",
	}
	for slug, version := range versions {
		if !strings.Contains(up, "('"+slug+"', '"+version+"')") {
			t.Errorf("migration does not pin %s to %s", slug, version)
		}
		if !strings.Contains(down, "'"+slug+"'") {
			t.Errorf("rollback does not scope %s", slug)
		}
	}
	for _, required := range []string{"version_constraint = pinned.version", "jsonb_set(tool.charts, '{0,version}'", "charts = charts #- '{0,version}'"} {
		if !strings.Contains(up+down, required) {
			t.Errorf("migration pair lacks %q", required)
		}
	}
	if strings.Contains(up, "('dex',") || strings.Contains(down, "'dex'") {
		t.Fatal("Dex must remain management-plane-only and unpinned in the member-cluster Tool catalog")
	}
}
