package handler

import "context"

func (h *MonitoringHandler) stampSharedLokiHealth(ctx context.Context, req SharedLokiRequest) error {
	if h.queries == nil {
		return nil
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(ctx)
	if err != nil {
		return err
	}
	return h.updateSharedLokiMetadata(ctx, backend, req, "healthy")
}
