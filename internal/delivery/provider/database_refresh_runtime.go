package provider

import (
	"context"
	"log/slog"
	"time"
)

// Run belongs to the existing fleet leader's joined runtime loop group. The
// synchronous sweep owns all requests and payloads; context cancellation exits
// only after its body/transaction cleanup has run. No goroutine returns while
// an unjoined refresh continues changing desired state.
func (owner *DatabaseRefreshOwner) Run(ctx context.Context, logger *slog.Logger) {
	if ctx == nil || owner == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		err := owner.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if logger != nil {
			if err != nil {
				// Errors may originate from registry or database drivers. Only a
				// fixed diagnostic is logged; no URL, token or response is printed.
				logger.Warn("database update refresh failed", "posture", "unknown")
			} else {
				logger.Info("database update acceptance sweep completed", "scan_used_provenance", "unmeasured")
			}
		}
		timer.Reset(30 * time.Minute)
	}
}
