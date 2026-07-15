package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration134DexRuntimeSecretStorageContract(t *testing.T) {
	up, err := os.ReadFile("134_dex_runtime_secret.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"runtime_secret_name", "public_clients_encrypted", "compatibility release", "public_clients_cutover_at",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"pgcrypto", "clientSecret", "bindPW"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must not embed or invent credential transform %q", forbidden)
		}
	}
	down, err := os.ReadFile("134_dex_runtime_secret.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	lowerDown := strings.ToLower(string(down))
	if strings.Contains(lowerDown, "drop column") || !strings.Contains(lowerDown, "forward-fix rollback stub") {
		t.Fatal("migration 134 down must retain the only encrypted credential copy")
	}
	for _, required := range []string{"public_clients_cutover_at is null", "case when dex_settings.public_clients_cutover_at is null"} {
		query, err := os.ReadFile(filepath.Join("..", "queries", "sso.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(string(query)), required) {
			t.Fatalf("mixed-version query contract missing %q", required)
		}
	}
}
