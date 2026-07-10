package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration136DexSagaPhaseAndAtomicSQLContract(t *testing.T) {
	up, err := os.ReadFile("136_dex_runtime_saga_phase.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(up))
	for _, required := range []string{
		"runtime_phase", "runtime_staged_generation", "saga_previous_sso_enabled",
		"runtime_applied_generation <= runtime_staged_generation",
		"runtime_staged_generation <= runtime_generation",
		"create or replace function bump_dex_runtime_generation",
		"with previous_sso as materialized", "update sso_configurations",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing saga invariant %q", required)
		}
	}

	query, err := os.ReadFile("../queries/sso.sql")
	if err != nil {
		t.Fatal(err)
	}
	q := strings.ToLower(string(query))
	for _, required := range []string{
		"stagedexsettingsanddisablesso", "with previous_sso as materialized",
		"saga_previous_sso_enabled = excluded.saga_previous_sso_enabled",
		"runtime_generation = dex_settings.runtime_generation + 1",
		"restoredexssoforgeneration", "settings.runtime_generation = sqlc.arg(runtime_generation)",
		"markdexruntimestaged", "markdexruntimeapplied",
	} {
		if !strings.Contains(q, required) {
			t.Fatalf("Dex saga query contract missing %q", required)
		}
	}
	if strings.Contains(q, "upsertdexsettings") {
		t.Fatal("non-atomic Dex settings upsert remains callable")
	}
}
