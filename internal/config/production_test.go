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
		InternalPSK:                        "a-dedicated-internal-signing-key-with-32-chars",
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

func TestValidateProductionSecurity_RequiresDedicatedInternalPSK(t *testing.T) {
	cfg := prodBase()
	cfg.InternalPSK = ""
	if err := ValidateProductionSecurity(cfg, true); err == nil ||
		!strings.Contains(err.Error(), "astronomer_internal_psk") {
		t.Fatalf("expected dedicated internal PSK validation error, got %v", err)
	}
}

func TestValidateProductionSecurity_InternalPSKRotationWindow(t *testing.T) {
	cfg := prodBase()
	cfg.InternalPSKPrevious = "previous-dedicated-internal-signing-key-with-32-chars"
	if err := ValidateProductionSecurity(cfg, true); err != nil {
		t.Fatalf("valid rotation key rejected: %v", err)
	}

	for name, previous := range map[string]string{
		"too short": "short",
		"same key":  cfg.InternalPSK,
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *cfg
			candidate.InternalPSKPrevious = previous
			if err := ValidateProductionSecurity(&candidate, true); err == nil ||
				!strings.Contains(err.Error(), "astronomer_internal_psk_previous") {
				t.Fatalf("invalid previous key accepted: %v", err)
			}
		})
	}
}

func TestValidateProductionSecurity_InternalPSKMustBeDedicated(t *testing.T) {
	for name, key := range map[string]func(*Config) string{
		"JWT signing key": func(cfg *Config) string { return cfg.SecretKey },
		"Fernet key":      func(cfg *Config) string { return cfg.EncryptionKey },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := prodBase()
			cfg.InternalPSK = key(cfg)
			if err := ValidateProductionSecurity(cfg, true); err == nil || !strings.Contains(err.Error(), "must be independent") {
				t.Fatalf("reused internal key accepted: %v", err)
			}
		})
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

func TestDSNEnforcesTLSParsesEffectivePGXConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		dsn  string
		want bool
	}{
		{name: "url require", dsn: "postgres://u:p@db/astronomer?sslmode=require", want: true},
		{name: "url verify full", dsn: "postgres://u:p@db/astronomer?sslmode=verify-full", want: true},
		{name: "keyword verify ca", dsn: "host=db user=u dbname=astronomer sslmode=verify-ca", want: true},
		{name: "prefer permits plaintext fallback", dsn: "postgres://u:p@db/astronomer?sslmode=prefer", want: false},
		{name: "omitted defaults to prefer", dsn: "postgres://u:p@db/astronomer", want: false},
		{name: "misleading password", dsn: "postgres://u:sslmode%3Drequire@db/astronomer?sslmode=disable", want: false},
		{name: "misleading application name", dsn: "host=db user=u sslmode=disable application_name=sslmode=require", want: false},
		// libpq/pgx keyword names are case-sensitive. An uppercase lookalike must
		// not be mistaken for an effective TLS setting.
		{name: "mixed case keyword is not effective", dsn: "host=db user=u SSLMODE=require", want: false},
		{name: "duplicate URL parameter uses effective last value", dsn: "postgres://u:p@db/astronomer?sslmode=require&sslmode=disable", want: false},
		{name: "invalid", dsn: "postgres://%", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DSNEnforcesTLS(tc.dsn); got != tc.want {
				t.Fatalf("DSNEnforcesTLS() = %t, want %t", got, tc.want)
			}
		})
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
