//go:build linux

package upgradecli

import (
	"context"
	"testing"
)

func TestLegacyBootstrapHelpAndUsageHaveNoPreparationEffects(t *testing.T) {
	calls := 0
	input := LegacyBootstrapInputs{Lookup: func(string) (string, bool) {
		calls++
		t.Fatal("early argv decision read operator environment")
		return "", false
	}}
	result, err := BootstrapLegacyUpgrade(nil, []string{"--yes", "--help"}, input) //nolint:staticcheck // Deliberately tests rejection of a missing context.
	if err != nil || !result.Help || result.ExitCode != 0 || calls != 0 {
		t.Fatal("legacy help acquired bootstrap resources")
	}
	result, err = BootstrapLegacyUpgrade(context.Background(), []string{"--unknown"}, input)
	if err == nil || result.ExitCode != 2 || calls != 0 {
		t.Fatal("legacy usage acquired bootstrap resources")
	}
}
