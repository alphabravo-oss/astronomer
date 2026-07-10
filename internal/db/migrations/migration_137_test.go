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
	if !strings.Contains(strings.ToLower(string(up)), "drop trigger if exists dex_connectors_runtime_generation") {
		t.Fatal("generic connector trigger remains")
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
}
