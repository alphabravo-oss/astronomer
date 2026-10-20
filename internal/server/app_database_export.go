package server

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	digest "github.com/opencontainers/go-digest"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

type DatabaseExportCommandOptions struct{ OutputDirectory, FeedDigest string }

// ExportDatabaseUpdate collects an existing exact signed decision using only
// the configured guarded pull client/current mounted trust. It never signs,
// records a publication/import disposition, mutates a cluster or creates NewApp.
func ExportDatabaseUpdate(ctx context.Context, cfg *config.Config, command DatabaseExportCommandOptions) (distribution.DatabaseOfflineExportResult, error) {
	var result distribution.DatabaseOfflineExportResult
	if ctx == nil || cfg == nil || !cfg.DeliveryDatabaseUpdatesEnabled || !filepath.IsAbs(command.OutputDirectory) || filepath.Clean(command.OutputDirectory) != command.OutputDirectory || (digest.Digest(command.FeedDigest).Validate() != nil || digest.Digest(command.FeedDigest).Algorithm() != digest.SHA256) {
		return result, errors.New("offline database export feature or input invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Minute)
	defer cancel()
	resources, err := prepareDatabaseTransferCommand(bounded, cfg)
	if err != nil {
		return result, err
	}
	defer resources.Close()
	return resources.authority.ExportOfflineUpdateKit(bounded, resources.proof, cfg.DeliveryDatabaseUpdateRepository, command.FeedDigest, cfg.DeliveryDatabaseCacheDirectory, command.OutputDirectory, time.Now().UTC())
}
