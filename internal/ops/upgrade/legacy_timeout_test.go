package upgrade

import (
	"context"
	"testing"
	"time"
)

func TestLegacyPositiveTimeoutIsNotClippedToOneHour(t *testing.T) {
	machine, driver, _ := testMachine()
	plan := testPlan()
	plan.Timeout = 2 * time.Hour
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	result := machine.Run(context.Background(), plan, nil)
	if result.ExitCode != 0 || len(driver.calls) == 0 {
		t.Fatal("representable legacy timeout was rejected or prevented stages")
	}
	if plan.Timeout != 2*time.Hour {
		t.Fatal("requested timeout clipped")
	}
}
