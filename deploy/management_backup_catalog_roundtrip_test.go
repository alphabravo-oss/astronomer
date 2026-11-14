package deploy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementBackupCatalogTrustActualCLIRoundTrip(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "dr-crypto")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/dr-crypto")
	build.Dir = repoRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual crypto CLI: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		name                 string
		ca, signature, empty bool
	}{
		{name: "flat"}, {name: "ca", ca: true}, {name: "signature", signature: true},
		{name: "both", ca: true, signature: true}, {name: "empty trust fails closed", ca: true, empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			staging := filepath.Join(root, "staging")
			if err := os.Mkdir(staging, 0700); err != nil {
				t.Fatal(err)
			}
			projectedBackupSecret(t, filepath.Join(root, "source"), map[string]string{"SECRET_KEY": "synthetic signing key", "ASTRONOMER_ENCRYPTION_KEY": "synthetic encryption key"})
			sets := []string{"managementBackup.s3.bucket=test-backups", "managementBackup.s3.credentialsSecretRef.name=test-credentials", "managementBackup.encryption.sourceIdentity=test-installation", "managementBackup.encryption.wrappingSecretRef.name=test-wrap"}
			trust := map[string]string{"ca.pem": "synthetic public trust", ".extra.pem": "synthetic extra trust"}
			if tc.empty {
				trust = map[string]string{}
			}
			for _, item := range []struct {
				enabled            bool
				directory, setting string
			}{
				{tc.ca, "catalog-ca", "catalog.ca.existingSecret=test-ca"},
				{tc.signature, "catalog-signature-trust", "catalog.signature.trustKeys.existingSecret=test-signature"},
			} {
				if item.enabled {
					sets = append(sets, item.setting)
					projectedBackupSecret(t, filepath.Join(root, item.directory), trust)
				}
			}
			cron := findRenderedDoc(t, parseRenderedDocs(t, helmTemplate(t, sets...)), "CronJob", "astronomer-management-backup")
			container := findContainer(t, podSpecFor(cron), "containers", "pgdump-s3")
			script := container["args"].([]any)[0].(string)
			start, end := strings.Index(script, "# Top-level,"), strings.Index(script, "# Manifest upload")
			if start < 0 || end <= start {
				t.Fatal("backup staging/encryption boundary missing")
			}
			script = script[start:end]
			script = strings.NewReplacer(
				"/var/run/astronomer-keys-source", filepath.Join(root, "source"),
				"/var/run/astronomer-keys", staging,
				"/var/run/catalog-ca", filepath.Join(root, "catalog-ca"),
				"/var/run/catalog-signature-trust", filepath.Join(root, "catalog-signature-trust"),
			).Replace(script)
			passphrase := filepath.Join(root, "passphrase")
			if err := os.WriteFile(passphrase, bytes.Repeat([]byte("p"), 32), 0600); err != nil {
				t.Fatal(err)
			}
			context := `{"release":"test","source":"fixture","stamp":"now"}`
			envelope := filepath.Join(root, "keys.aead")
			command := exec.CommandContext(t.Context(), "sh", "-eu", "-c", script)
			command.Env = append(os.Environ(), "PATH="+filepath.Dir(binary)+":"+os.Getenv("PATH"), "KEY_BUNDLE="+filepath.Join(root, "keys.tar"), "KEY_ENVELOPE="+envelope, "PASSPHRASE_FILE="+passphrase, "CONTEXT="+context)
			output, err := command.CombinedOutput()
			if tc.empty {
				if err == nil {
					t.Fatal("empty configured trust was silently omitted")
				}
				if _, err := os.Stat(envelope); !os.IsNotExist(err) {
					t.Fatal("failed bundle produced a publishable envelope")
				}
				return
			}
			if err != nil {
				t.Fatalf("rendered backup staging: %v\n%s", err, output)
			}
			plain := filepath.Join(root, "decrypted.tar")
			runBackupCryptoCLI(t, binary, "decrypt", "--input", envelope, "--output", plain, "--passphrase-file", passphrase, "--expect-purpose", "key-bundle", "--expect-context", context)
			restored := filepath.Join(root, "restored")
			runBackupCryptoCLI(t, binary, "key-bundle-extract", "--input", plain, "--output", restored)
			for _, name := range []string{"SECRET_KEY", "ASTRONOMER_ENCRYPTION_KEY"} {
				if _, err := os.Stat(filepath.Join(restored, name)); err != nil {
					t.Fatal(err)
				}
			}
			for _, item := range []struct {
				enabled   bool
				directory string
			}{{tc.ca, "catalog-ca"}, {tc.signature, "catalog-signature-trust"}} {
				if !item.enabled {
					continue
				}
				entries, err := os.ReadDir(filepath.Join(restored, item.directory))
				if err != nil || len(entries) != len(trust) {
					t.Fatalf("wrong restored trust inventory: %v", err)
				}
				for name, want := range trust {
					got, err := os.ReadFile(filepath.Join(restored, item.directory, name))
					if err != nil || string(got) != want {
						t.Fatalf("trust file %s changed: %v", name, err)
					}
				}
			}
		})
	}
}

func projectedBackupSecret(t *testing.T, directory string, files map[string]string) {
	t.Helper()
	version := filepath.Join(directory, "..version")
	if err := os.MkdirAll(version, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..version", filepath.Join(directory, "..data")); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(version, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..data", name), filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func runBackupCryptoCLI(t *testing.T, binary string, args ...string) {
	t.Helper()
	if output, err := exec.CommandContext(t.Context(), binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("crypto CLI failed: %v\n%s", err, output)
	}
}
