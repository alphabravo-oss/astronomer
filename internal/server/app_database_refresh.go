package server

import (
	"errors"
	"path/filepath"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"oras.land/oras-go/v2/registry/remote"
)

// Validate typed locator DATA before the restart reduction phase. No DNS,
// snapshot, network client or filesystem staging is constructed here.
func databaseRefreshSettings(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("database refresh configuration unavailable")
	}
	canonical, err := builtinbundles.NormalizeOCISourceURL("oci://" + cfg.DeliveryDatabaseUpdateRepository)
	repository, parseError := remote.NewRepository(cfg.DeliveryDatabaseUpdateRepository)
	if err != nil || canonical != "oci://"+cfg.DeliveryDatabaseUpdateRepository || parseError != nil || repository.Reference.Reference != "" || !filepath.IsAbs(cfg.DeliveryDatabaseCacheDirectory) || filepath.Clean(cfg.DeliveryDatabaseCacheDirectory) != cfg.DeliveryDatabaseCacheDirectory {
		return errors.New("database refresh feed repository or private cache configuration invalid")
	}
	return nil
}
