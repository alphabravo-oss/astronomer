package migrations_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestDeliveryDurableJSONGovernance(t *testing.T) {
	upgrade := readMigration(t, "061_delivery_durable_json_governance.up.sql")

	contracts := map[string][]string{
		"catalog_blessed_charts":           {"artifact", "compatibility", "lifecycle", "presentation", "raw_entry", "resources", "storage"},
		"cluster_deployments":              {"desired_renderer_spec"},
		"delivery_catalogs":                {"trust_policy"},
		"delivery_configuration_templates": {"patches", "secret_refs", "values_document"},
		"delivery_controller_inventory":    {"system_components"},
		"delivery_override_sets":           {"patches", "values_document"},
	}
	for table, columns := range contracts {
		for _, column := range columns {
			needle := fmt.Sprintf("'public', '%s', '%s'", table, column)
			if !strings.Contains(upgrade, needle) {
				t.Errorf("migration 061 is missing durable JSON contract %s.%s", table, column)
			}
		}
	}

	for _, table := range []string{
		"catalog_blessed_charts",
		"delivery_catalogs",
		"delivery_configuration_templates",
		"delivery_override_sets",
	} {
		needle := "BEFORE INSERT OR UPDATE ON public." + table
		if !strings.Contains(upgrade, needle) {
			t.Errorf("migration 061 is missing writer validation trigger for %s", table)
		}
	}
}
