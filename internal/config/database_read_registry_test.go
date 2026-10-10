package config

import "testing"

func TestDatabaseReadRegistryConfigurationCapturesOnlyTypedReferences(t *testing.T) {
	t.Setenv("ENV", "development")
	t.Setenv("RELEASE_MANIFEST_PATH", "")
	t.Setenv("DELIVERY_DATABASE_UPDATES_ENABLED", "false")
	t.Setenv("DELIVERY_DATABASE_READ_CREDENTIAL_REF", "owned-pull")
	t.Setenv("DELIVERY_DATABASE_READ_REGISTRY", "registry.corp")
	t.Setenv("DELIVERY_DATABASE_READ_REALM_URL", "https://auth.corp/token")
	t.Setenv("DELIVERY_DATABASE_READ_SERVICE", "owned-service")
	t.Setenv("DELIVERY_DATABASE_READ_REPOSITORIES", `["registry.corp/distribution","registry.corp/database-updates"]`)
	cfg, err := Load()
	if err != nil {
		t.Fatal("typed read registry configuration unavailable")
	}
	if cfg.DeliveryDatabaseUpdatesEnabled || cfg.DeliveryDatabaseReadCredentialRef != "owned-pull" || cfg.DeliveryDatabaseReadRegistry != "registry.corp" || cfg.DeliveryDatabaseReadRealmURL != "https://auth.corp/token" || cfg.DeliveryDatabaseReadService != "owned-service" || cfg.DeliveryDatabaseReadRepositories != `["registry.corp/distribution","registry.corp/database-updates"]` {
		t.Fatal("typed read reference DATA did not bind or implicitly enabled database updates")
	}
}

func TestDatabaseReadRegistryConfigurationPreservesEmptyDefaults(t *testing.T) {
	t.Setenv("ENV", "development")
	t.Setenv("RELEASE_MANIFEST_PATH", "")
	t.Setenv("DELIVERY_DATABASE_UPDATES_ENABLED", "")
	for _, name := range []string{"DELIVERY_DATABASE_READ_CREDENTIAL_REF", "DELIVERY_DATABASE_READ_REGISTRY", "DELIVERY_DATABASE_READ_REALM_URL", "DELIVERY_DATABASE_READ_SERVICE", "DELIVERY_DATABASE_READ_REPOSITORIES"} {
		t.Setenv(name, "")
	}
	cfg, err := Load()
	if err != nil || cfg.DeliveryDatabaseUpdatesEnabled || cfg.DeliveryDatabaseReadCredentialRef != "" || cfg.DeliveryDatabaseReadRegistry != "" || cfg.DeliveryDatabaseReadRealmURL != "" || cfg.DeliveryDatabaseReadService != "" || cfg.DeliveryDatabaseReadRepositories != "" {
		t.Fatal("ordinary configuration acquired a mounted read role")
	}
}
