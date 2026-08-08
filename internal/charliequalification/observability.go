package charliequalification

import (
	"context"
	"log/slog"
)

type HookLifecycleEvent uint8

const (
	HookStarted HookLifecycleEvent = iota + 1
	HookStoppedWithFailure
)

// LogHookLifecycle is the sole qualification-tool operational sink. The hook
// handles credentials and live-effect errors, so caller text and remote errors
// deliberately have no representation in this closed schema.
func LogHookLifecycle(logger *slog.Logger, event HookLifecycleEvent) {
	if logger == nil {
		logger = slog.Default()
	}
	switch event {
	case HookStarted:
		logger.LogAttrs(context.Background(), slog.LevelInfo, "Charlie qualification event",
			slog.String("event", "charlie.qualification_hook_started"),
			slog.String("outcome_code", "ready"),
			slog.String("transport", "tls"))
	case HookStoppedWithFailure:
		logger.LogAttrs(context.Background(), slog.LevelError, "Charlie qualification event",
			slog.String("event", "charlie.qualification_hook_stopped"),
			slog.String("outcome_code", "failed"),
			slog.String("failure_code", "charlie.qualification_hook_failed"))
	}
}
