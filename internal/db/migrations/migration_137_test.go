package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration137DexLockAndConnectorLifecycleContract(t *testing.T) {
	up, err := os.ReadFile("137_dex_advisory_lock_connector_lifecycle.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	upText := strings.ToLower(string(up))
	for _, required := range []string{"create trigger dex_connectors_runtime_generation", "pg_advisory_xact_lock(742193440558879931)", "current_setting('astronomer.dex_connector_stage_bypass'", "set_config('astronomer.dex_connector_stage_bypass', '', true)"} {
		if !strings.Contains(upText, required) {
			t.Fatalf("compatibility trigger missing %q", required)
		}
	}
	sso, _ := os.ReadFile("../queries/sso.sql")
	auth, _ := os.ReadFile("../queries/auth.sql")
	all := strings.ToLower(string(sso) + string(auth))
	for _, query := range []string{"stagedexsettingsanddisablesso", "stagecreatedexconnector", "stageupdatedexconnector", "stagedeletedexconnector", "restoredexssoforgeneration", "enabledexssoforgeneration"} {
		at := strings.Index(all, strings.ToLower(query))
		if at < 0 {
			t.Fatalf("missing %s", query)
		}
		end := at + 2500
		if end > len(all) {
			end = len(all)
		}
		if !strings.Contains(all[at:end], "pg_advisory_xact_lock(742193440558879931)") {
			t.Fatalf("%s lacks shared advisory lock", query)
		}
	}
	for _, required := range []string{"runtime_applied_generation=sqlc.arg(runtime_generation)", "runtime_phase in ('fresh','cutover')", "for update of settings"} {
		if !strings.Contains(strings.ReplaceAll(all, " ", ""), strings.ReplaceAll(required, " ", "")) {
			t.Fatalf("missing activation guard %q", required)
		}
	}

	down, err := os.ReadFile("137_dex_advisory_lock_connector_lifecycle.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration136, err := os.ReadFile("136_dex_runtime_saga_phase.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	functionBody := func(sql string) string {
		start := strings.Index(sql, "CREATE OR REPLACE FUNCTION bump_dex_runtime_generation()")
		if start < 0 {
			t.Fatal("missing bump_dex_runtime_generation function")
		}
		end := strings.Index(sql[start:], "$$;")
		if end < 0 {
			t.Fatal("unterminated bump_dex_runtime_generation function")
		}
		return strings.Join(strings.Fields(strings.ToLower(sql[start:start+end+3])), " ")
	}
	if got, want := functionBody(string(down)), functionBody(string(migration136)); got != want {
		t.Fatalf("migration 137 down does not restore migration 136 trigger function\ngot:  %s\nwant: %s", got, want)
	}
	downText := strings.ToLower(string(down))
	for _, forbidden := range []string{"pg_advisory_xact_lock", "dex_connector_stage_bypass"} {
		if strings.Contains(downText, forbidden) {
			t.Fatalf("migration 137 down retained 137-only behavior %q", forbidden)
		}
	}
	for _, required := range []string{"drop trigger if exists dex_connectors_runtime_generation", "create trigger dex_connectors_runtime_generation"} {
		if !strings.Contains(downText, required) {
			t.Fatalf("migration 137 down missing %q", required)
		}
	}
}
