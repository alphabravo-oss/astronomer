package config

import (
	"strings"
	"testing"
)

// prodBase is a fully-valid production config; individual cases mutate one field
// to prove that field is enforced.
func prodBase() *Config {
	return &Config{
		Env:                                "production",
		SecretKey:                          "a-real-unique-secret",
		EncryptionKey:                      "a-real-unique-encryption-key",
		DatabaseURL:                        "postgres://u:p@db/astronomer?sslmode=require",
		DexBundledEnabled:                  true,
		AuthLocalPasswordOnly:              false,
		ServerURL:                          "https://astronomer.example.com",
		DeliveryEnabled:                    true,
		AgentImageRepository:               "registry.example.test/astronomer-agent@sha256:" + strings.Repeat("a", 64),
		DeliveryFluxDistributionRepository: "registry.example.test/astronomer/system",
		DeliveryFluxDistributionDigest:     "sha256:" + strings.Repeat("b", 64),
		DeliveryFluxDistributionCertificateIdentity: "https://github.com/example/release@refs/tags/v1.0.0",
		DeliveryFluxDistributionOIDCIssuer:          "https://token.actions.githubusercontent.com",
		DeliveryBundleRepository:                    "registry.example.test/astronomer/bundles",
		DeliveryBundleDigest:                        "sha256:" + strings.Repeat("c", 64),
		DeliveryBundleCertificateIdentity:           "https://github.com/example/release@refs/tags/v1.0.0",
		DeliveryBundleOIDCIssuer:                    "https://token.actions.githubusercontent.com",
	}
}

// TestValidateProductionSecurity_WorkerRefusesEmptyOrDevKey is the C-01 regression:
// the worker (and server) must refuse to start in production with an empty or
// known-dev encryption key. ValidateProductionSecurity is the shared fail-fast
// both binaries call; a non-nil error is what triggers os.Exit(1) in cmd/worker.
func TestValidateProductionSecurity_WorkerRefusesEmptyOrDevKey(t *testing.T) {
	empty := prodBase()
	empty.EncryptionKey = ""
	if err := ValidateProductionSecurity(empty, false); err == nil {
		t.Fatal("expected production error for empty encryption key")
	} else if !strings.Contains(err.Error(), "astronomer_encryption_key is empty") {
		t.Fatalf("error did not mention empty key: %v", err)
	}

	dev := prodBase()
	dev.EncryptionKey = devEncryptionKey
	if err := ValidateProductionSecurity(dev, true); err == nil {
		t.Fatal("expected production error for known-dev encryption key")
	} else if !strings.Contains(err.Error(), "known development value") {
		t.Fatalf("error did not flag dev key: %v", err)
	}

	// A non-decodable key (encryptorReady=false) is also rejected.
	badEnc := prodBase()
	if err := ValidateProductionSecurity(badEnc, false); err == nil ||
		!strings.Contains(err.Error(), "could not initialize encryptor") {
		t.Fatalf("expected encryptor-init failure to be rejected, got %v", err)
	}
}

func TestValidateProductionSecurity_HappyPathAndDevNoop(t *testing.T) {
	if err := ValidateProductionSecurity(prodBase(), true); err != nil {
		t.Fatalf("valid production config should pass, got %v", err)
	}

	// Non-production is always a no-op, even with an empty key.
	dev := prodBase()
	dev.Env = "development"
	dev.EncryptionKey = ""
	if err := ValidateProductionSecurity(dev, false); err != nil {
		t.Fatalf("dev config should never fail, got %v", err)
	}
}

func TestValidateProductionSecurity_EnforcesTLSAndURL(t *testing.T) {
	noTLS := prodBase()
	noTLS.DatabaseURL = "postgres://u:p@db/astronomer?sslmode=disable"
	if err := ValidateProductionSecurity(noTLS, true); err == nil ||
		!strings.Contains(err.Error(), "does not enforce TLS") {
		t.Fatalf("expected TLS enforcement error, got %v", err)
	}

	badURL := prodBase()
	badURL.ServerURL = "http://astronomer.example.com"
	if err := ValidateProductionSecurity(badURL, true); err == nil ||
		!strings.Contains(err.Error(), "https URL") {
		t.Fatalf("expected https server_url error, got %v", err)
	}
}

// TestDevSentinelsInUse is the dev-keys-default-and-silent regression: the
// sentinels are published in this repository, so detection must be independent
// of config.env — a "development" install signs the same JWTs and wraps the
// same stored cluster credentials as a production one.
func TestDevSentinelsInUse(t *testing.T) {
	both := &Config{Env: "development", SecretKey: devSecretKey, EncryptionKey: devEncryptionKey}
	got := DevSentinelsInUse(both)
	if len(got) != 2 || got[0] != DevSentinelSecretKey || got[1] != DevSentinelEncryptionKey {
		t.Fatalf("DevSentinelsInUse(both dev keys) = %v, want [%s %s]",
			got, DevSentinelSecretKey, DevSentinelEncryptionKey)
	}

	// Whitespace around a sentinel is still that sentinel — the chart's
	// --set-file recipe leaves a trailing newline.
	padded := &Config{Env: "production", SecretKey: "  " + devSecretKey + "\n"}
	if got := DevSentinelsInUse(padded); len(got) != 1 || got[0] != DevSentinelSecretKey {
		t.Fatalf("DevSentinelsInUse(padded secret key) = %v, want [%s]", got, DevSentinelSecretKey)
	}

	onlyEncryption := &Config{Env: "development", SecretKey: "a-real-unique-secret", EncryptionKey: devEncryptionKey}
	if got := DevSentinelsInUse(onlyEncryption); len(got) != 1 || got[0] != DevSentinelEncryptionKey {
		t.Fatalf("DevSentinelsInUse(dev fernet key only) = %v, want [%s]", got, DevSentinelEncryptionKey)
	}

	if got := DevSentinelsInUse(prodBase()); len(got) != 0 {
		t.Fatalf("DevSentinelsInUse(real keys) = %v, want empty", got)
	}
	if got := DevSentinelsInUse(nil); len(got) != 0 {
		t.Fatalf("DevSentinelsInUse(nil) = %v, want empty", got)
	}
}
