package builtin

import (
	"context"
	"errors"
	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"log/slog"
)

// CatalogLoader supplies either verified release assets or explicitly selected
// source-development fixtures. Reconcile cannot silently choose another path.
type CatalogLoader interface {
	Load(context.Context) (builtinbundles.Catalog, error)
}

func (p *Provisioner) loadCatalog(ctx context.Context) (builtinbundles.Catalog, error) {
	if p.catalog == nil {
		return builtinbundles.Catalog{}, errors.New("built-in catalog loader is not configured")
	}
	catalog, err := p.catalog.Load(ctx)
	if provider, ok := p.catalog.(interface{ Status() distribution.Status }); ok {
		status := provider.Status()
		slog.InfoContext(ctx, "distribution status", "mode", status.Mode, "release", status.Release, "source_digest", status.SourceDigest, "location", status.Location, "verification", status.Verification, "inventory_complete", status.InventoryComplete, "last_successful_sync", status.LastSuccessfulSync, "blocked_category", status.BlockedCategory)
	}
	return catalog, err
}
