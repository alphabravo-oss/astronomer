package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestDeliveryTargetOverridesMigrationFreezesDeploymentConfiguration(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("038_delivery_target_overrides.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"ADD COLUMN overrides jsonb",
		"ADD COLUMN desired_overrides jsonb",
		"ADD COLUMN previous_overrides jsonb",
		"pg_column_size(overrides) <= 65536",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration missing %q", want)
		}
	}
}
