package migrations_test

import (
	"strings"
	"testing"
)

func TestCertManagerStartupCheckMigration(t *testing.T) {
	up := readMigration(t, "070_enable_cert_manager_startup_check.up.sql")
	for _, want := range []string{"slug = 'cert-manager'", "startupapicheck", "enabled: true"} {
		if !strings.Contains(up, want) {
			t.Fatalf("up migration missing %q", want)
		}
	}
	down := readMigration(t, "070_enable_cert_manager_startup_check.down.sql")
	if !strings.Contains(down, "enabled: false") {
		t.Fatal("down migration does not restore the previous startup check default")
	}
}
