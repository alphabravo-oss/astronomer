package tasks

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func inhJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// inhibitionRule builds an enabled inhibition with the given matchers.
func inhibitionRule(t *testing.T, source, target []inhibitionMatcher, equal []string) sqlc.AlertInhibition {
	return sqlc.AlertInhibition{
		SourceMatchers: inhJSON(t, source),
		TargetMatchers: inhJSON(t, target),
		EqualLabels:    inhJSON(t, equal),
		Enabled:        true,
	}
}

// TestInhibition_SourceFiresSuppressesTarget — a firing source matching
// source_matchers, sharing the equal-label value, suppresses the target.
func TestInhibition_SourceFiresSuppressesTarget(t *testing.T) {
	inh := inhibitionRule(t,
		[]inhibitionMatcher{{Label: "severity", Value: "critical"}},
		[]inhibitionMatcher{{Label: "severity", Value: "warning"}},
		[]string{"cluster_id"},
	)
	firing := []map[string]string{
		{"severity": "critical", "cluster_id": "c1", "alertname": "node-down"},
	}
	target := map[string]string{"severity": "warning", "cluster_id": "c1", "alertname": "high-cpu"}

	if !alertInhibited([]sqlc.AlertInhibition{inh}, firing, target) {
		t.Fatalf("expected target to be suppressed while source is firing")
	}
}

// TestInhibition_SourceResolvesReleasesTarget — with no matching source alert
// in the firing set (source resolved), the target is NOT suppressed.
func TestInhibition_SourceResolvesReleasesTarget(t *testing.T) {
	inh := inhibitionRule(t,
		[]inhibitionMatcher{{Label: "severity", Value: "critical"}},
		[]inhibitionMatcher{{Label: "severity", Value: "warning"}},
		[]string{"cluster_id"},
	)
	// Source resolved: firing set no longer contains the critical alert.
	firing := []map[string]string{
		{"severity": "warning", "cluster_id": "c1", "alertname": "high-cpu"},
	}
	target := map[string]string{"severity": "warning", "cluster_id": "c1", "alertname": "high-cpu"}

	if alertInhibited([]sqlc.AlertInhibition{inh}, firing, target) {
		t.Fatalf("expected target to be released once the source resolved")
	}
}

// TestInhibition_EqualLabelsMismatchDoesNotSuppress — a firing source that
// matches source_matchers but differs on an equal-label does NOT suppress.
func TestInhibition_EqualLabelsMismatchDoesNotSuppress(t *testing.T) {
	inh := inhibitionRule(t,
		[]inhibitionMatcher{{Label: "severity", Value: "critical"}},
		[]inhibitionMatcher{{Label: "severity", Value: "warning"}},
		[]string{"cluster_id"},
	)
	// Source is on a different cluster than the target.
	firing := []map[string]string{
		{"severity": "critical", "cluster_id": "c2", "alertname": "node-down"},
	}
	target := map[string]string{"severity": "warning", "cluster_id": "c1", "alertname": "high-cpu"}

	if alertInhibited([]sqlc.AlertInhibition{inh}, firing, target) {
		t.Fatalf("expected NO suppression when equal-label values differ")
	}
}

// TestInhibition_RegexSourceMatcher — an is_regex source matcher matches by
// anchored regex.
func TestInhibition_RegexSourceMatcher(t *testing.T) {
	inh := inhibitionRule(t,
		[]inhibitionMatcher{{Label: "severity", Value: "crit.*", IsRegex: true}},
		[]inhibitionMatcher{{Label: "severity", Value: "warning"}},
		nil,
	)
	firing := []map[string]string{{"severity": "critical", "cluster_id": "c1"}}
	target := map[string]string{"severity": "warning", "cluster_id": "c1"}
	if !alertInhibited([]sqlc.AlertInhibition{inh}, firing, target) {
		t.Fatalf("expected regex source matcher to suppress")
	}
}

// TestInhibition_NoSelfInhibition — an alert whose labels satisfy BOTH source
// and target matchers must not inhibit itself.
func TestInhibition_NoSelfInhibition(t *testing.T) {
	inh := inhibitionRule(t,
		[]inhibitionMatcher{{Label: "team", Value: "sre"}},
		[]inhibitionMatcher{{Label: "team", Value: "sre"}},
		[]string{"team"},
	)
	labels := map[string]string{"team": "sre", "cluster_id": "c1"}
	firing := []map[string]string{labels}
	if alertInhibited([]sqlc.AlertInhibition{inh}, firing, labels) {
		t.Fatalf("an alert must not inhibit itself")
	}
}
