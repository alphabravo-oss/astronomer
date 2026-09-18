package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestClusterHealthConditionsContractMigration(t *testing.T) {
	up, err := os.ReadFile("050_cluster_health_conditions_contract.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"table_name = 'cluster_health_statuses'",
		"column_name = 'conditions'",
		"schema_version = 2",
		"json_type = 'object'",
		"IF NOT FOUND",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("cluster health contract migration missing %q", required)
		}
	}

	down, err := os.ReadFile("050_cluster_health_conditions_contract.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "schema_version = 1") || !strings.Contains(string(down), "json_type = 'array'") {
		t.Fatal("cluster health contract down migration does not restore schema 49")
	}
}
