//go:build linux

package process

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLegacyLongTimeoutRetainsEarlierCallerDeadlineAndJoinedChild(t *testing.T) {
	spec := processFixture(t, "wait")
	spec.Timeout = 2 * time.Hour
	ctx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	result, err := Run(ctx, spec)
	if !errors.Is(err, context.DeadlineExceeded) || result.Kind != TimedOut || result.ExitCode != 124 || !result.LeaderReaped {
		t.Fatal("earlier caller deadline lost or child was not joined", result, err)
	}
}
