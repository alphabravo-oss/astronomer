package migrations_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var classifiedSecretColumns = map[string]string{
	"001_initial.up.sql:access_key":                    "deprecated blank-only object-store compatibility field",
	"001_initial.up.sql:agent_secret_hmac":             "keyed digest of Kubernetes Secret content",
	"001_initial.up.sql:agent_secret_name":             "Kubernetes Secret name reference",
	"001_initial.up.sql:client_secret_encrypted":       "encrypted SSO client secret",
	"001_initial.up.sql:credential_content_digest":     "non-secret delivery credential epoch digest",
	"001_initial.up.sql:credential_encrypted":          "encrypted delivery source credential envelope",
	"001_initial.up.sql:credential_id":                 "foreign key reference",
	"001_initial.up.sql:credential_state":              "non-secret credential lifecycle enum",
	"001_initial.up.sql:encrypted_credentials":         "encrypted object-store credentials",
	"002_management_backup_destinations.up.sql:encrypted_credentials": "encrypted management-plane backup object-store credentials",
	"001_initial.up.sql:object_storage_secret_name":    "Kubernetes Secret name reference",
	"001_initial.up.sql:password":                      "hashed bcrypt user password",
	"001_initial.up.sql:password_encrypted":            "encrypted SMTP password",
	"001_initial.up.sql:password_hash_at_issue":        "password hash snapshot",
	"001_initial.up.sql:previous_token_hash":           "hashed previous cluster agent token",
	"001_initial.up.sql:registry_credential_encrypted": "encrypted project registry credential",
	"001_initial.up.sql:registry_password":             "deprecated blank-only cluster registry compatibility field",
	"001_initial.up.sql:registry_password_encrypted":   "encrypted cluster registry password",
	"001_initial.up.sql:runtime_secret_name":           "Kubernetes Secret name reference",
	"001_initial.up.sql:secret_encrypted":              "encrypted TOTP or webhook shared secret",
	"001_initial.up.sql:secret_key":                    "deprecated blank-only object-store compatibility field",
	"001_initial.up.sql:secret_name":                   "Kubernetes Secret name reference",
	"001_initial.up.sql:token":                         "deprecated blank-only registration or agent token field",
	"001_initial.up.sql:token_hash":                    "hashed bearer token",
	"001_initial.up.sql:upstream_id_token_encrypted":   "encrypted upstream identity token",
}

var migrationColumnDecl = regexp.MustCompile(`(?i)\b(?:ADD\s+COLUMN(?:\s+IF\s+NOT\s+EXISTS)?\s+)?([a-z][a-z0-9_]*)\s+(TEXT|CHARACTER\s+VARYING(?:\([^)]+\))?|VARCHAR(?:\([^)]+\))?|CHAR(?:\([^)]+\))?|JSONB|UUID|BYTEA)\b`)

func TestSecretLikeMigrationColumnsAreClassified(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, rawLine := range strings.Split(string(b), "\n") {
			line := strings.SplitN(rawLine, "--", 2)[0]
			matches := migrationColumnDecl.FindAllStringSubmatch(line, -1)
			for _, match := range matches {
				col := strings.ToLower(match[1])
				if !isSecretLikeColumn(col) {
					continue
				}
				key := name + ":" + col
				if classifiedSecretColumns[key] == "" {
					t.Fatalf("secret-like migration column %s is not classified; update docs/secret-column-inventory.md and classifiedSecretColumns", key)
				}
			}
		}
	}
}

func isSecretLikeColumn(name string) bool {
	if name == "access_key" || name == "secret_key" {
		return true
	}
	for _, part := range []string{"password", "secret", "token", "credential", "private_key", "client_secret"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}
