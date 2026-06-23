package migrations_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var classifiedSecretColumns = map[string]string{
	"001_initial.up.sql:password":                "hashed bcrypt user password",
	"001_initial.up.sql:client_secret_encrypted": "encrypted SSO client secret",
	"001_initial.up.sql:token_hash":              "hashed API token",
	"001_initial.up.sql:token":                   "legacy plaintext registration/agent token",
	"001_initial.up.sql:registry_password":       "legacy plaintext registry password",
	"001_initial.up.sql:access_key":              "legacy plaintext object-store access key",
	"001_initial.up.sql:secret_key":              "legacy plaintext object-store secret key",
	"001_initial.up.sql:auth_token_encrypted":    "encrypted ArgoCD auth token",

	"003_monitoring_object_storage.up.sql:object_storage_secret_name":            "Kubernetes Secret name reference",
	"019_argocd_managed_clusters.up.sql:cluster_secret_name":                     "Kubernetes Secret name reference",
	"020_velero_backup_engine.up.sql:encrypted_credentials":                      "encrypted object-store credentials",
	"029_cluster_agent_tokens.up.sql:token":                                      "legacy plaintext agent token",
	"043_two_factor_auth.up.sql:secret_encrypted":                                "encrypted TOTP shared secret",
	"047_smtp_email.up.sql:password_encrypted":                                   "encrypted SMTP password",
	"047_smtp_email.up.sql:token_hash":                                           "hashed password reset token",
	"047_smtp_email.up.sql:password_hash_at_issue":                               "password hash snapshot",
	"048_webhook_subscriptions.up.sql:secret_encrypted":                          "encrypted webhook signing secret",
	"050_cluster_registry_extensions.up.sql:secret_name":                         "Kubernetes Secret name reference",
	"053_cloud_credentials.up.sql:credential_id":                                 "foreign key reference",
	"053_cloud_credentials.up.sql:secret_name":                                   "Kubernetes Secret name reference",
	"054_sso_sessions.up.sql:upstream_id_token_encrypted":                        "encrypted upstream identity token",
	"090_argocd_cluster_proxy_tokens.up.sql:token_hash":                          "hashed ArgoCD proxy token",
	"090_argocd_cluster_proxy_tokens.up.sql:token_prefix":                        "non-secret token prefix",
	"090_argocd_cluster_proxy_tokens.up.sql:token_encrypted":                     "encrypted ArgoCD proxy token",
	"093_cluster_registration_token_hash.up.sql:token_hash":                      "hashed cluster registration token",
	"094_cluster_agent_token_hash.up.sql:token_hash":                             "hashed cluster agent token",
	"095_cluster_registry_password_encrypted.up.sql:registry_password_encrypted": "encrypted cluster registry password",
	"114_scim_tokens.up.sql:token_hash":                                         "hashed SCIM provisioning token",
}

var migrationColumnDecl = regexp.MustCompile(`(?i)\b(?:ADD\s+COLUMN(?:\s+IF\s+NOT\s+EXISTS)?\s+)?([a-z][a-z0-9_]*)\s+(TEXT|VARCHAR(?:\([^)]+\))?|CHAR(?:\([^)]+\))?|JSONB|UUID|BYTEA)\b`)

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
