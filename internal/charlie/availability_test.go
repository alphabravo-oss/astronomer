package charlie

import "testing"

func TestAvailabilityIsFailClosedBeforeActivation(t *testing.T) {
	tests := []struct {
		name            string
		feature, active bool
		want            Availability
		wantConfig      bool
		wantRuntime     bool
	}{
		{"feature unavailable", false, false, AvailabilityUnavailable, false, false},
		{"active cannot override feature", false, true, AvailabilityUnavailable, false, false},
		{"available inactive", true, false, AvailabilityAvailableInactive, true, false},
		{"active", true, true, AvailabilityActive, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAvailability(tc.feature, tc.active)
			if got != tc.want || got.AllowsConfiguration() != tc.wantConfig || got.AllowsRuntime() != tc.wantRuntime {
				t.Fatalf("availability=%s config=%t runtime=%t", got, got.AllowsConfiguration(), got.AllowsRuntime())
			}
			if got.AllowsEvidence() != tc.wantRuntime {
				t.Fatalf("evidence allowance=%t, runtime=%t", got.AllowsEvidence(), tc.wantRuntime)
			}
		})
	}
}
