package migrations_test

import (
	"strings"
	"testing"
)

func TestCISOperatorReleasePlanIncludesCRDsFirst(t *testing.T) {
	up := readMigration(t, "068_add_cis_operator_crd_release.up.sql")
	down := readMigration(t, "068_add_cis_operator_crd_release.down.sql")

	for _, required := range []string{
		"rancher-cis-benchmark-crd",
		"cis-operator-crd",
		"'order', 0",
		"rancher-cis-benchmark",
		"'release_name', 'cis-operator'",
		"'order', 1",
		"106.8.0+up8.10.0",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("CIS release plan migration lacks %q", required)
		}
	}
	if strings.Contains(down, "rancher-cis-benchmark-crd") || !strings.Contains(down, "rancher-cis-benchmark") {
		t.Fatal("rollback must restore the single operator chart used before migration 068")
	}
}
