package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func TestDexConnectorCiphertextRotationDoesNotStageLogicalRuntime(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "set_config('astronomer.dex_connector_stage_bypass','1',true)") || !strings.Contains(text, "UPDATE dex_connectors SET config = $1::jsonb") || strings.Contains(text, "UPDATE dex_connectors SET runtime_generation") {
		t.Fatal("Dex connector key rotation is not a ciphertext-only CAS")
	}
	migration, err := os.ReadFile("../../internal/db/migrations/137_dex_advisory_lock_connector_lifecycle.up.sql")
	if err != nil || !strings.Contains(string(migration), "DROP TRIGGER IF EXISTS dex_connectors_runtime_generation") {
		t.Fatalf("generic runtime trigger would take keyrotate offline: %v", err)
	}
}

// TEST-04 / CORR-04: shipped SQL builders for batch SELECT + CAS UPDATE.
func TestSelectBatchSQL_HonorsLimitOffsetPlaceholders(t *testing.T) {
	sql := selectBatchSQL(target{table: "cloud_credentials", idCol: "id", column: "data_encrypted"})
	for _, want := range []string{
		"FROM cloud_credentials",
		"data_encrypted",
		"ORDER BY id",
		"LIMIT $1 OFFSET $2",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("select SQL missing %q: %s", want, sql)
		}
	}
}

func TestRotateDexConnectorConfigRewritesFallbackCiphertext(t *testing.T) {
	newKey, _ := auth.GenerateKey()
	oldKey, _ := auth.GenerateKey()
	oldOnly, _ := auth.NewEncryptor(oldKey)
	multi, _ := auth.NewEncryptor(newKey + "," + oldKey)
	newOnly, _ := auth.NewEncryptor(newKey)
	oldCipher, _ := oldOnly.Encrypt("synthetic-connector-secret")
	raw, _ := json.Marshal(map[string]any{"issuer": "https://idp.example", "clientID": "id", "clientSecret": oldCipher})
	rotated, changed, err := rotateDexConnectorConfig(string(raw), "oidc", multi)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var config map[string]any
	_ = json.Unmarshal([]byte(rotated), &config)
	value, _ := config["clientSecret"].(string)
	if plain, err := newOnly.Decrypt(value); err != nil || plain != "synthetic-connector-secret" {
		t.Fatalf("new primary cannot decrypt rotated value")
	}
	if _, err := oldOnly.Decrypt(value); err == nil {
		t.Fatal("rotated value still decrypts with removed fallback only")
	}
}

func TestRotateDexConnectorConfigFailsClosedForUnknownTypeOrBadCiphertext(t *testing.T) {
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	if _, _, err := rotateDexConnectorConfig(`{}`, "future", enc); err == nil {
		t.Fatal("unknown connector type accepted")
	}
	if _, _, err := rotateDexConnectorConfig(`{"clientSecret":"plaintext"}`, "oidc", enc); err == nil {
		t.Fatal("plaintext connector secret accepted")
	}
}

func TestPrimaryOnlyVerificationRejectsFallbackConnectorAndStaticClientDrift(t *testing.T) {
	primaryKey, _ := auth.GenerateKey()
	fallbackKey, _ := auth.GenerateKey()
	primary, _ := auth.NewEncryptor(primaryKey)
	fallback, _ := auth.NewEncryptor(fallbackKey)
	fallbackCipher, _ := fallback.Encrypt("synthetic")
	connector, _ := json.Marshal(map[string]any{"issuer": "https://idp.example", "clientID": "id", "clientSecret": fallbackCipher})
	if err := verifyDexConnectorPrimary(string(connector), "oidc", primary); err == nil {
		t.Fatal("fallback-only connector passed primary-only verification")
	}
	clients := `[{"id":"app","redirectURIs":["https://platform.example/callback"],"secret":"synthetic"}]`
	fallbackEnvelope, _ := fallback.Encrypt(clients)
	if err := verifyDexStaticClientRow(clients, fallbackEnvelope, false, primary); err == nil {
		t.Fatal("fallback-only static-client envelope passed primary-only verification")
	}
	primaryEnvelope, _ := primary.Encrypt(clients)
	if err := verifyDexStaticClientRow(`[{"id":"different","redirectURIs":["https://platform.example/callback"],"secret":"synthetic"}]`, primaryEnvelope, false, primary); err == nil {
		t.Fatal("stale pre-cutover envelope passed compatibility equality verification")
	}
	if err := verifyDexStaticClientRow(`[]`, primaryEnvelope, true, primary); err != nil {
		t.Fatalf("valid cutover envelope failed: %v", err)
	}
}

func TestCASUpdateSQL_RequiresOldCiphertext(t *testing.T) {
	sql := casUpdateSQL(target{table: "sso_configurations", idCol: "id", column: "client_secret_encrypted"})
	// Must CAS on both primary key and previous ciphertext.
	if !strings.Contains(sql, "WHERE id = $2 AND client_secret_encrypted = $3") {
		t.Fatalf("CAS UPDATE must pin old ciphertext: %s", sql)
	}
	if strings.Contains(sql, "WHERE id = $2;") || !strings.Contains(sql, "$3") {
		t.Fatalf("CAS UPDATE must not be id-only: %s", sql)
	}
}

func TestRequireCASUpdateTreatsEveryMissAsFatal(t *testing.T) {
	if requireCASUpdate(1) != nil {
		t.Fatal("single-row CAS update rejected")
	}
	for _, affected := range []int64{0, 2} {
		if requireCASUpdate(affected) == nil {
			t.Fatalf("rows affected %d was not fatal", affected)
		}
	}
}

func TestRewriteEncryptorRoundTrip(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := enc.Encrypt("rotation-secret")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatal(err)
	}
	newCT, err := enc.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	again, err := enc.Decrypt(newCT)
	if err != nil || again != "rotation-secret" {
		t.Fatalf("got %q err %v", again, err)
	}
	// CAS would compare ct != newCT for concurrent writer detection.
	if newCT == "" || ct == "" {
		t.Fatal("empty ciphertext")
	}
}
