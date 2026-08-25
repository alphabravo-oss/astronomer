package server

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

// startProductionRuntime preserves the runtime startup order while delegating
// each cohesive composition phase to a typed helper.
func (c *productionComposition) startProductionRuntime(_ context.Context, cfg *config.Config, logger *slog.Logger, routed *routerComposition) (*Server, error) {
	lifecycles, err := c.composeCharlieLifecycles(cfg, logger, routed.deps)
	if err != nil {
		return nil, err
	}
	foundation, err := c.startRuntimeFoundation(cfg, logger, routed, lifecycles)
	if err != nil {
		return nil, err
	}
	runtimeTasks, err := c.composeRuntimeTasks(cfg, logger, routed, foundation)
	if err != nil {
		return nil, err
	}
	if err := c.startRuntimeServices(cfg, logger, foundation, runtimeTasks); err != nil {
		return nil, err
	}
	return foundation.server, nil
}
