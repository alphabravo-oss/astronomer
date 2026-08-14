package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestFreshPlatformConfigurationSeedsBaselineWithoutOverridingOperators(t *testing.T) {
	raw, err := os.ReadFile("159_seed_fresh_platform_configuration.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	for _, required := range []string{
		"INSERT INTO platform_configuration (id, default_cluster_template_id)",
		"FROM cluster_templates",
		"WHERE name = 'Platform baseline'",
		"ON CONFLICT (id) DO NOTHING",
	} {
		if !strings.Contains(up, required) {
			t.Errorf("migration 159 is missing %q", required)
		}
	}
	if strings.Contains(up, "DO UPDATE") {
		t.Error("migration 159 must not overwrite an existing operator-managed platform configuration")
	}
}
