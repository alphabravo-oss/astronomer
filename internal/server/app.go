package server

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

// NewApp creates a fully wired production server in explicit phases. Each
// phase owns one dependency domain; productionComposition is the typed hand-off
// into route composition and background-runtime startup.
func NewApp(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Server, error) {
	composition := &productionComposition{}
	phases := []func(context.Context, *config.Config, *slog.Logger) error{
		composition.initializePersistence,
		composition.initializeCoreHandlers,
		composition.initializeTenantHandlers,
		composition.initializeClusterHandlers,
		composition.initializeIdentityAndClusterHandlers,
		composition.initializeIntegrations,
		composition.initializeCharlie,
		composition.initializeDelivery,
	}
	for _, initialize := range phases {
		if err := initialize(ctx, cfg, logger); err != nil {
			return nil, err
		}
	}
	routed, err := composition.composeRouter(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}
	return composition.startProductionRuntime(ctx, cfg, logger, routed)
}
