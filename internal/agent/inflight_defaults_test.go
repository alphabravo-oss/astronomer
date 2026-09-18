package agent

import "testing"

func TestDefaultInflightLimitsCoverDashboardFanout(t *testing.T) {
	cfg := &AgentConfig{}
	if got := inflightLimit(cfg, dispatchBuffered); got != 64 {
		t.Fatalf("buffered in-flight default = %d, want 64", got)
	}
	if got := inflightLimit(cfg, dispatchStream); got != 256 {
		t.Fatalf("stream in-flight default = %d, want 256", got)
	}
}
