package config

import (
	"strings"
	"testing"
)

// prodBase is a fully-valid production config; individual cases mutate one field
// to prove that field is enforced.
func prodBase() *Config {
	return &Config{
		Env:                   "production",
		SecretKey:             "a-real-unique-secret",
		EncryptionKey:         "a-real-unique-encryption-key",
		DatabaseURL:           "postgres://u:p@db/astronomer?sslmode=require",
		DexBundledEnabled:     true,
		AuthLocalPasswordOnly: false,
		ServerURL:             "https://astronomer.example.com",
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
