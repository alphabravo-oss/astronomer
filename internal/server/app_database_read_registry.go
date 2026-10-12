package server

import (
	"errors"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// Read and push are independent protected roles. These fields carry only an
// exact mounted reference and endpoint/repository DATA, never credentials.
// All-empty preserves the existing anonymous path; partial input fails closed.
func databaseReadRegistryOptions(cfg *config.Config) (*resolver.DatabaseRegistryReadOptions, error) {
	if cfg == nil {
		return nil, errors.New("database registry read configuration unavailable")
	}
	if cfg.DeliveryDatabaseReadCredentialRef == "" && cfg.DeliveryDatabaseReadRegistry == "" && cfg.DeliveryDatabaseReadRealmURL == "" && cfg.DeliveryDatabaseReadService == "" && cfg.DeliveryDatabaseReadRepositories == "" {
		return nil, nil
	}
	if cfg.DeliveryDatabaseReadCredentialRef == "" || cfg.DeliveryDatabaseReadRegistry == "" || cfg.DeliveryDatabaseReadRealmURL == "" || cfg.DeliveryDatabaseReadService == "" || len(cfg.DeliveryDatabaseReadRepositories) > 32<<10 {
		return nil, errors.New("database registry read configuration incomplete")
	}
	var repositories []string
	if jsoncontract.Decode([]byte(cfg.DeliveryDatabaseReadRepositories), &repositories) != nil || repositories == nil || len(repositories) == 0 || len(repositories) > 32 {
		return nil, errors.New("database registry read repositories must be a finite exact array")
	}
	return &resolver.DatabaseRegistryReadOptions{CredentialRef: cfg.DeliveryDatabaseReadCredentialRef, Registry: cfg.DeliveryDatabaseReadRegistry, RealmURL: cfg.DeliveryDatabaseReadRealmURL, Service: cfg.DeliveryDatabaseReadService, Repositories: repositories}, nil
}
