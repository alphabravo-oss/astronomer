package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
)

func TestRecoveryReferenceRejectsOtherDatabasesAndOverrides(t *testing.T) {
	for _, value := range []string{
		"postgres://user:password@localhost", "postgres://user:password@localhost/production",
		"postgres://user:password@localhost/plan032_w12_recovery_owned?dbname=production",
		"postgres://user:password@localhost/plan032_w12_recovery_owned#fragment",
		"https://localhost/plan032_w12_recovery_owned",
	} {
		path := filepath.Join(t.TempDir(), "reference.json")
		data, _ := json.Marshal(map[string]string{"database_url": value})
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal("create reference")
		}
		if _, _, err := databaseURL(path); err == nil {
			t.Fatal("unowned reference accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "reference.json")
	data := []byte(`{"database_url":"postgres://user:password@localhost/plan032_w12_recovery_owned?sslmode=disable"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal("create private reference")
	}
	if _, name, err := databaseURL(path); err != nil || name != "plan032_w12_recovery_owned" {
		t.Fatal("owned database rejected")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal("change permissions")
	}
	if _, _, err := databaseURL(path); err == nil {
		t.Fatal("public reference accepted")
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal("create symlink")
	}
	if _, _, err := databaseURL(link); err == nil {
		t.Fatal("symlink reference accepted")
	}
}

func TestRecoveryDocumentsRejectUnknownAndTrailingFields(t *testing.T) {
	for _, data := range []string{`{"owner":"Plan032 W12","unrecognized":true}`, `{"owner":"Plan032 W12"} {}`} {
		if decodeClosed([]byte(data), new(manifest)) == nil {
			t.Fatal("unrecognized manifest accepted")
		}
	}
}

func TestRecoveryRequiresBothIndependentKeysAndUsableCharlieTLS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "independent-key-reference.json")
	k, digest, err := createKeys(path)
	if err != nil {
		t.Fatal("create disposable key reference")
	}
	for _, source := range []string{"w12-old:" + k.Old, "w12-current:" + k.Current} {
		encryptor, err := auth.NewEncryptor(source)
		if err != nil {
			t.Fatal("create source encryptor")
		}
		trust, err := charlie.GenerateLocalTrust(encryptor, charlie.LocalTrustConfig{InstallationID: "3c608d44-848c-45d6-bd86-246be0b880af", BridgeServerDNS: "bridge.w12.svc", MCPServerDNS: "mcp.w12.svc"})
		if err != nil {
			t.Fatal("generate production trust")
		}
		_, recovered, err := recoveredKeys(path, digest, "")
		if err != nil {
			t.Fatal("recover protected reference")
		}
		plaintext, err := recovered.Decrypt(trust.EncryptedLocalTrust)
		if err != nil || verifyTLS(trust.Public, []byte(plaintext)) != nil {
			t.Fatal("production trust key recovery failed")
		}
		_, wrong, err := recoveredKeys(path, digest, "wrong")
		if err != nil {
			t.Fatal("build negative key ring")
		}
		if _, err := wrong.Decrypt(trust.EncryptedLocalTrust); err == nil {
			t.Fatal("wrong key recovered trust")
		}
		if strings.HasPrefix(source, "w12-old:") {
			_, missing, err := recoveredKeys(path, digest, "missing-old")
			if err != nil {
				t.Fatal("build incomplete key ring")
			}
			if _, err := missing.Decrypt(trust.EncryptedLocalTrust); err == nil {
				t.Fatal("missing old key recovered trust")
			}
		}
		if verifyTLS(trust.Public, []byte(`{}`)) == nil {
			t.Fatal("missing recovered private keys accepted")
		}
	}
	if _, _, err := recoveredKeys(path, "sha256:"+strings.Repeat("0", 64), ""); err == nil {
		t.Fatal("different protected reference accepted")
	}
	if _, _, err := createKeys(path); err == nil {
		t.Fatal("existing protected reference overwritten")
	}
}
