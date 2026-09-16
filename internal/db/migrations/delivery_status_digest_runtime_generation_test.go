package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestDeliveryStatusDigestAndRuntimeGenerationMigration(t *testing.T) {
	up, err := os.ReadFile("036_delivery_status_digest_runtime_generation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"status_digest varchar(80) NOT NULL DEFAULT",
		"semantic_sequence bigint NOT NULL DEFAULT 1",
		"runtime_generation bigint NOT NULL DEFAULT 1",
		"bump_delivery_rollout_runtime_from_cluster",
		"bump_delivery_rollout_runtime_from_deployment",
		"bump_delivery_rollout_runtime_from_connection",
		"AFTER UPDATE OF labels ON public.clusters",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration is missing %q", required)
		}
	}
}
