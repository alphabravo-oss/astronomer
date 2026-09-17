package handler

import "context"

// These methods expose one narrow domain capability. The control-plane service
// depends on this interface instead of retaining concrete sibling handlers.
func (h *MonitoringHandler) ControlPlaneSummary(ctx context.Context) (map[string]any, error) {
	return h.controllerSummary(ctx)
}

func (h *ToolHandler) ControlPlaneSummary(ctx context.Context) (map[string]any, error) {
	return h.controllerSummary(ctx)
}

func (h *CatalogHandler) ControlPlaneSummary(ctx context.Context) (map[string]any, error) {
	return h.controllerSummary(ctx)
}

func (h *BackupHandler) ControlPlaneSummary(ctx context.Context) (map[string]any, error) {
	return h.controllerSummary(ctx)
}

func (h *LoggingHandler) ControlPlaneSummary(ctx context.Context) (map[string]any, error) {
	return h.controllerSummary(ctx)
}

func (h *SecurityHandler) ControlPlaneSummary(ctx context.Context) (map[string]any, error) {
	return h.controllerSummary(ctx)
}
