package builtin

import (
	"testing"
	"time"
)

func TestStableIDIsDeterministicAndDomainSeparated(t *testing.T) {
	first := stableID("target", "cluster-a", "kube-state-metrics")
	if first != stableID("target", "cluster-a", "kube-state-metrics") {
		t.Fatal("stable ID changed for identical identity parts")
	}
	if first == stableID("bundle", "cluster-a", "kube-state-metrics") {
		t.Fatal("stable ID did not separate resource domains")
	}
}

func TestRetryAfterRequiresNewerExplicitRequest(t *testing.T) {
	created := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	before, equal, after := created.Add(-time.Second), created, created.Add(time.Second)
	for name, tc := range map[string]struct {
		requested *time.Time
		want      bool
	}{
		"none":   {requested: nil, want: false},
		"before": {requested: &before, want: false},
		"equal":  {requested: &equal, want: false},
		"after":  {requested: &after, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := retryAfter(tc.requested, created); got != tc.want {
				t.Fatalf("retryAfter() = %v, want %v", got, tc.want)
			}
		})
	}
}
