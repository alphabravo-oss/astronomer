package config

import (
	"crypto/subtle"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

var immutableDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var keywordSSLModePattern = regexp.MustCompile(`(?:^|\s)sslmode\s*=`)

// Known development sentinel values. A production deployment that still carries
// either of these has not been configured with real secrets and must fail fast
// rather than run with a guessable signing/encryption key.
const (
	devSecretKey     = "local-dev-secret-key-change-in-production"
	devEncryptionKey = "RX3rwYkQNmaSq4_UmGs7sPXONIjnB-M6q0gZtB79vQA="
)

// Every other key literal published in this repository. The chart no longer
// ships key material, but the repo's own laptop paths still have to hand the
// install SOMETHING, and a value committed here is exactly as forgeable as the
// two sentinels above — so detection must cover them or the dev loop is
// strictly less observable than it was before the chart defaults were removed.
//
//   - deploy/chart/values-k3d.yaml, scripts/k3d-bootstrap.sh and the Makefile's
//     HELM_* defaults: real installs, on a laptop.
//   - scripts/verify-enterprise.sh, scripts/extract-images.sh and
//     .github/workflows/release.yaml: render-only, never applied — listed
//     anyway so copy-pasting one into an install is caught.
var (
	publishedSecretKeys = []string{
		devSecretKey,
		"k3d-smoke-test-jwt-signing-key-32-chars",
		"make-local-dev-jwt-signing-key-32-chars",
		"verify-enterprise-render-signing-key",
		"extract-images-render-only",
		"release-lint-render-only",
	}
	publishedEncryptionKeys = []string{
		devEncryptionKey,
		"3b4GoQu4Ka-ZH7D28cqSUY8vzDmQDU4vLSbv8aoNWBo=",
		"I2oWSIt6LO68xR6lxhqBpQxhesPuii5R6ubog-Id-yo=",
	}
)

// IsProduction reports whether this process is running in production mode. The
// The caller must pass the startup-resolved configuration.
func IsProduction(cfg *Config) bool {
	return cfg != nil && strings.EqualFold(strings.TrimSpace(cfg.Env), "production")
}

// DSNEnforcesTLS reports whether a Postgres DSN includes an sslmode setting that
// requires TLS. Acceptable values: require, verify-ca, verify-full. Anything
// else (disable/allow/prefer, or omission — which Postgres treats as prefer and
// silently downgrades to plaintext) returns false.
func DSNEnforcesTLS(dsn string) bool {
	dsn = strings.TrimSpace(dsn)
	if !dsnHasUnambiguousSSLMode(dsn) {
		return false
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || cfg.TLSConfig == nil {
		return false
	}
	// sslmode=prefer and sslmode=allow include a plaintext fallback. Inspect
	// pgx's effective connection plan so misleading substrings, encoded values,
	// and duplicate parameters cannot bypass the production gate.
	for _, fallback := range cfg.Fallbacks {
		if fallback.TLSConfig == nil {
			return false
		}
	}
	return true
}

// dsnHasUnambiguousSSLMode rejects duplicate and case-lookalike settings
// before pgx resolves them. Different parsers choosing different duplicate
// values is exactly the kind of configuration ambiguity a production TLS gate
// must not accept.
func dsnHasUnambiguousSSLMode(dsn string) bool {
	if parsed, err := url.Parse(dsn); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		found := 0
		for key, values := range parsed.Query() {
			if strings.EqualFold(key, "sslmode") {
				if key != "sslmode" {
					return false
				}
				found += len(values)
			}
		}
		return found == 1
	}
	return len(keywordSSLModePattern.FindAllStringIndex(dsn, -1)) == 1
}

// Metric/label names for the credentials guarded by the dev sentinels. They
// double as the label values of astronomer_insecure_dev_key_in_use.
const (
	DevSentinelSecretKey     = "secret_key"
	DevSentinelEncryptionKey = "encryption_key"
)

// DevSentinelsInUse reports which credentials are still set to a value
// published in this repository, returning the names above (nil when none are).
// Unlike ValidateProductionSecurity this is env-independent on purpose: a
// "development" install signs real JWTs and wraps real cluster credentials, so
// the server and the worker log + export the result on every boot regardless of
// config.env (dev-keys-default-and-silent).
func DevSentinelsInUse(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var inUse []string
	if slices.Contains(publishedSecretKeys, strings.TrimSpace(cfg.SecretKey)) {
		inUse = append(inUse, DevSentinelSecretKey)
	}
	if slices.Contains(publishedEncryptionKeys, strings.TrimSpace(cfg.EncryptionKey)) {
		inUse = append(inUse, DevSentinelEncryptionKey)
	}
	return inUse
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
		sentinels := DevSentinelsInUse(cfg)
		secretKey := strings.TrimSpace(cfg.SecretKey)
		switch {
		case secretKey == "":
			errs = append(errs, "secret_key is empty")
		case slices.Contains(sentinels, DevSentinelSecretKey):
			errs = append(errs, "secret_key is still the known development value")
		}
		encryptionKey := strings.TrimSpace(cfg.EncryptionKey)
		switch {
		case encryptionKey == "":
			errs = append(errs, "astronomer_encryption_key is empty")
		case slices.Contains(sentinels, DevSentinelEncryptionKey):
			errs = append(errs, "astronomer_encryption_key is still the known development value")
		case !encryptorReady:
			errs = append(errs, "astronomer_encryption_key could not initialize encryptor")
		}
		internalPSK := strings.TrimSpace(cfg.InternalPSK)
		if len(internalPSK) < 32 {
			errs = append(errs, "astronomer_internal_psk must contain at least 32 characters")
		}
		if constantTimeStringEqual(internalPSK, secretKey) || constantTimeStringEqual(internalPSK, encryptionKey) {
			errs = append(errs, "astronomer_internal_psk must be independent from secret_key and astronomer_encryption_key")
		}
		previousInternalPSK := strings.TrimSpace(cfg.InternalPSKPrevious)
		if previousInternalPSK != "" {
			if len(previousInternalPSK) < 32 {
				errs = append(errs, "astronomer_internal_psk_previous must contain at least 32 characters when set")
			}
			if constantTimeStringEqual(previousInternalPSK, internalPSK) {
				errs = append(errs, "astronomer_internal_psk_previous must differ from astronomer_internal_psk")
			}
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
		if !cfg.DeliveryEnabled {
			errs = append(errs, "delivery_enabled must be true")
		}
		if !cfg.DeliveryLocalFluxBootstrap {
			errs = append(errs, "delivery_local_flux_bootstrap must be true")
		}
		validateSignedArtifact := func(name, repository, digest, identity, issuer string) {
			repository = strings.TrimSpace(repository)
			if repository == "" {
				errs = append(errs, name+" repository is empty")
			} else {
				candidate := repository
				if !strings.HasPrefix(candidate, "oci://") {
					candidate = "oci://" + candidate
				}
				if parsed, err := url.Parse(candidate); err != nil || parsed.Scheme != "oci" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
					errs = append(errs, name+" repository must be a credential-free OCI URL")
				}
			}
			if !immutableDigestPattern.MatchString(strings.TrimSpace(digest)) {
				errs = append(errs, name+" digest must be an immutable sha256")
			}
			if strings.TrimSpace(identity) == "" {
				errs = append(errs, name+" certificate identity is empty")
			}
			parsedIssuer, err := url.Parse(strings.TrimSpace(issuer))
			if err != nil || parsedIssuer.Scheme != "https" || parsedIssuer.Host == "" {
				errs = append(errs, name+" OIDC issuer must be https")
			}
		}
		validateSignedArtifact("delivery flux distribution", cfg.DeliveryFluxDistributionRepository, cfg.DeliveryFluxDistributionDigest, cfg.DeliveryFluxDistributionCertificateIdentity, cfg.DeliveryFluxDistributionOIDCIssuer)
		validateSignedArtifact("delivery built-in bundle", cfg.DeliveryBundleRepository, cfg.DeliveryBundleDigest, cfg.DeliveryBundleCertificateIdentity, cfg.DeliveryBundleOIDCIssuer)
		if !strings.Contains(strings.TrimSpace(cfg.AgentImageRepository), "@sha256:") {
			errs = append(errs, "agent_image_repository must be digest-pinned")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("production security config invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

func constantTimeStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
