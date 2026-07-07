package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Known development sentinel values. A production deployment that still carries
// either of these has not been configured with real secrets and must fail fast
// rather than run with a guessable signing/encryption key.
const (
	devSecretKey     = "local-dev-secret-key-change-in-production"
	devEncryptionKey = "RX3rwYkQNmaSq4_UmGs7sPXONIjnB-M6q0gZtB79vQA="
)

// IsProduction reports whether this process is running in production mode. The
// config value wins, with ASTRONOMER_ENV / ENV as fall-backs so the check still
// fires for binaries (e.g. the worker) that read the same environment but a
// leaner config surface.
func IsProduction(cfg *Config) bool {
	if cfg != nil && strings.EqualFold(strings.TrimSpace(cfg.Env), "production") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ASTRONOMER_ENV")), "production") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ENV")), "production")
}

// DSNEnforcesTLS reports whether a Postgres DSN includes an sslmode setting that
// requires TLS. Acceptable values: require, verify-ca, verify-full. Anything
// else (disable/allow/prefer, or omission — which Postgres treats as prefer and
// silently downgrades to plaintext) returns false.
func DSNEnforcesTLS(dsn string) bool {
	d := strings.ToLower(dsn)
	return strings.Contains(d, "sslmode=require") ||
		strings.Contains(d, "sslmode=verify-ca") ||
		strings.Contains(d, "sslmode=verify-full")
}

// ValidateProductionSecurity fails fast when a production deployment is misconfigured
// in a way that is unsafe: an empty/dev secret or encryption key, a non-decodable
// encryption key (encryptorReady=false), a DSN that does not enforce TLS, an
// un-acknowledged local-only auth stance, or a missing/non-https server URL. It is
// a no-op (returns nil) outside production so dev/local stacks come up unchanged.
//
// encryptorReady lets the caller keep the auth-package dependency out of this
// package: pass true when auth.NewEncryptor(cfg.EncryptionKey) succeeded.
//
// Both the server (internal/server) and the worker (cmd/worker) call this so a
// typo'd key or dirty config crashes BOTH processes loudly instead of leaving the
// worker Running while it silently no-ops its credential-migration/email tasks.
func ValidateProductionSecurity(cfg *Config, encryptorReady bool) error {
	if !IsProduction(cfg) {
		return nil
	}
	var errs []string
	if cfg == nil {
		errs = append(errs, "config is nil")
	} else {
		secretKey := strings.TrimSpace(cfg.SecretKey)
		switch secretKey {
		case "":
			errs = append(errs, "secret_key is empty")
		case devSecretKey:
			errs = append(errs, "secret_key is still the known development value")
		}
		encryptionKey := strings.TrimSpace(cfg.EncryptionKey)
		switch {
		case encryptionKey == "":
			errs = append(errs, "astronomer_encryption_key is empty")
		case encryptionKey == devEncryptionKey:
			errs = append(errs, "astronomer_encryption_key is still the known development value")
		case !encryptorReady:
			errs = append(errs, "astronomer_encryption_key could not initialize encryptor")
		}
		if !DSNEnforcesTLS(cfg.DatabaseURL) {
			errs = append(errs, "database_url does not enforce TLS")
		}
		if !cfg.DexBundledEnabled && !cfg.AuthLocalPasswordOnly {
			errs = append(errs, "dex_bundled_enabled is false and auth_local_password_only is not acknowledged")
		}
		serverURL := strings.TrimSpace(cfg.ServerURL)
		if serverURL == "" {
			errs = append(errs, "server_url is empty")
		} else if u, err := url.Parse(serverURL); err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, "server_url must be an external https URL")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("production security config invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}
