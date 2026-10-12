package config

import (
	"strings"
	"testing"
)

func databaseUpdateEnvironmentFixture(t *testing.T, enabled string) {
	t.Helper()
	t.Setenv("ENV", "development")
	t.Setenv("RELEASE_MANIFEST_PATH", "")
	t.Setenv("DELIVERY_DATABASE_UPDATES_ENABLED", enabled)
	t.Setenv("DELIVERY_DATABASE_REGISTRY_ROOTS_REF", "registry")
	t.Setenv("DELIVERY_DATABASE_SCANNER_VERSION", "0.70.0")
	t.Setenv("DELIVERY_DATABASE_SCANNER_DIGEST", "sha256:"+strings.Repeat("a", 64))
}

func TestDatabaseUpdateConfigurationBindsExactTypedEnvironment(t *testing.T) {
	for _, enabled := range []string{"true", "false"} {
		t.Run(enabled, func(t *testing.T) {
			databaseUpdateEnvironmentFixture(t, enabled)
			cfg, err := Load()
			if err != nil {
				t.Fatal("typed database configuration did not load")
			}
			if cfg.DeliveryDatabaseUpdatesEnabled != (enabled == "true") || cfg.DeliveryDatabaseRegistryRootsRef != "registry" || cfg.DeliveryDatabaseScannerVersion != "0.70.0" || cfg.DeliveryDatabaseScannerDigest != "sha256:"+strings.Repeat("a", 64) {
				t.Fatal("database environment did not bind exact typed configuration")
			}
		})
	}
}

func TestDatabaseUpdateLocatorDataCannotEnableComposition(t *testing.T) {
	databaseUpdateEnvironmentFixture(t, "")
	cfg, err := Load()
	if err != nil {
		t.Fatal("database configuration defaults did not load")
	}
	if cfg.DeliveryDatabaseUpdatesEnabled || cfg.DeliveryDatabaseRegistryRootsRef != "registry" || cfg.DeliveryDatabaseScannerDigest == "" {
		t.Fatal("locator/scanner data implicitly enabled database authority")
	}
	t.Setenv("DELIVERY_DATABASE_REGISTRY_ROOTS_REF", "")
	t.Setenv("DELIVERY_DATABASE_SCANNER_VERSION", "")
	t.Setenv("DELIVERY_DATABASE_SCANNER_DIGEST", "")
	cfg, err = Load()
	if err != nil || cfg.DeliveryDatabaseUpdatesEnabled || cfg.DeliveryDatabaseRegistryRootsRef != "" || cfg.DeliveryDatabaseScannerVersion != "" || cfg.DeliveryDatabaseScannerDigest != "" {
		t.Fatal("optional database configuration does not retain its zero-value defaults")
	}
}
