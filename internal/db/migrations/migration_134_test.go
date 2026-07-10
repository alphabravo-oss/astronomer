package migrations

import (
	"os"
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
		"runtime_secret_name", "public_clients_encrypted", "Legacy migration input only",
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
}
