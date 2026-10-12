//go:build unix

package server

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestDatabaseStartupReadRegistryScopeRemainsDataBeforeMountedCredentials(t *testing.T) {
	cfg, _ := databaseStartupConfigFixture(t)
	cfg.DeliveryDatabaseReadCredentialRef = "pull"
	cfg.DeliveryDatabaseReadRegistry = "registry.corp"
	cfg.DeliveryDatabaseReadRealmURL = "https://registry.corp/token"
	cfg.DeliveryDatabaseReadService = "registry.corp"
	cfg.DeliveryDatabaseReadRepositories = `["registry.corp/distribution","registry.corp/database-updates"]`
	options, err := databaseStartupOptionsForBuild(t.Context(), cfg, "1.2.1", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal("scope validation required absent credential/key/CA before restart reduction")
	}
	if options.Registry == nil || options.Registry.Read == nil || options.Registry.Read.Registry != cfg.DeliveryDatabaseReadRegistry || options.Registry.Read.CredentialRef != "pull" || len(options.Registry.Read.Repositories) != 2 || options.Distribution.Client != nil {
		t.Fatal("startup scope manufactured or omitted read authority")
	}
	cfg.DeliveryDatabaseReadRepositories = `["registry.corp/changed"]`
	if options.Registry.Read.Repositories[0] != "registry.corp/distribution" {
		t.Fatal("configured read repository aliases caller DATA")
	}
	var ordinary config.Config
	read, err := databaseReadRegistryOptions(&ordinary)
	if err != nil || read != nil {
		t.Fatal("all-empty read scope changed anonymous behavior")
	}
}

func TestDatabaseStartupReadRegistryRefusesPartialOrUnboundedScope(t *testing.T) {
	for _, mutation := range []string{"partial", "null", "scalar", "non-string", "empty-array", "unknown-object", "unapproved-realm", "tag", "excessive"} {
		t.Run(mutation, func(t *testing.T) {
			cfg, _ := databaseStartupConfigFixture(t)
			cfg.DeliveryDatabaseReadCredentialRef = "pull"
			cfg.DeliveryDatabaseReadRegistry = "registry.corp"
			cfg.DeliveryDatabaseReadRealmURL = "https://registry.corp/token"
			cfg.DeliveryDatabaseReadService = "registry.corp"
			cfg.DeliveryDatabaseReadRepositories = `["registry.corp/distribution"]`
			switch mutation {
			case "partial":
				cfg.DeliveryDatabaseReadCredentialRef = ""
			case "null":
				cfg.DeliveryDatabaseReadRepositories = `null`
			case "scalar":
				cfg.DeliveryDatabaseReadRepositories = `"registry.corp/distribution"`
			case "non-string":
				cfg.DeliveryDatabaseReadRepositories = `[1]`
			case "empty-array":
				cfg.DeliveryDatabaseReadRepositories = `[]`
			case "unknown-object":
				cfg.DeliveryDatabaseReadRepositories = `{"repositories":["registry.corp/distribution"],"grant":true}`
			case "unapproved-realm":
				cfg.DeliveryDatabaseReadRealmURL = "https://unapproved.example.test/token"
			case "tag":
				cfg.DeliveryDatabaseReadRepositories = `["registry.corp/distribution:mutable"]`
			case "excessive":
				cfg.DeliveryDatabaseReadRepositories = strings.Repeat("x", (32<<10)+1)
			}
			if _, err := databaseStartupOptionsForBuild(t.Context(), cfg, "1.2.1", strings.Repeat("a", 40)); err == nil {
				t.Fatal("partial or malformed read configuration accepted")
			}
		})
	}
}
