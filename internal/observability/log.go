package observability

import "log/slog"

func WithEvent(log *slog.Logger, event string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if event == "" {
		return log
	}
	return log.With("event", event)
}

func WithCorrelationID(log *slog.Logger, correlationID string) *slog.Logger {
	if log == nil {
		log = slog.Default()
	}
	if correlationID == "" {
		return log
	}
	return log.With("correlation_id", correlationID)
}
