package tasks

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
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
	// Mirror canonical durable writers rather than constructing JSON null.
	if source == nil {
		source = []inhibitionMatcher{}
	}
	if target == nil {
		target = []inhibitionMatcher{}
	}
	if equal == nil {
		equal = []string{}
	}
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

func TestInhibition_CorruptJSONCannotSuppress(t *testing.T) {
	for _, tc := range []struct {
		name, column, value string
	}{
		{"equal labels syntax", "equal", `not-json`},
		{"equal labels object", "equal", `{}`},
		{"equal labels number", "equal", `42`},
		{"equal labels null", "equal", `null`},
		{"source wrong boolean", "source", `[{"label":"severity","value":"critical","is_regex":"false"}]`},
		{"target wrong boolean", "target", `[{"label":"severity","value":"warning","is_regex":"false"}]`},
		{"source null value", "source", `[{"label":"severity","value":null}]`},
		{"target null value", "target", `[{"label":"severity","value":null}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inh := inhibitionRule(t, []inhibitionMatcher{{Label: "severity", Value: "critical"}}, []inhibitionMatcher{{Label: "severity", Value: "warning"}}, []string{"cluster_id"})
			switch tc.column {
			case "equal":
				inh.EqualLabels = []byte(tc.value)
			case "source":
				inh.SourceMatchers = []byte(tc.value)
			case "target":
				inh.TargetMatchers = []byte(tc.value)
			}
			source := map[string]string{"severity": "critical", "cluster_id": "c1"}
			target := map[string]string{"severity": "warning", "cluster_id": "c1"}
			if tc.name == "source null value" {
				source["severity"] = ""
			}
			if tc.name == "target null value" {
				target["severity"] = ""
			}
			if alertInhibited([]sqlc.AlertInhibition{inh}, []map[string]string{source}, target) {
				t.Fatal("corrupt stored inhibition suppressed the target")
			}
		})
	}
}

func TestInhibition_ValidRuleContinuesAfterCorruptRule(t *testing.T) {
	valid := inhibitionRule(t, []inhibitionMatcher{{Label: "severity", Value: "critical"}}, []inhibitionMatcher{{Label: "severity", Value: "warning"}}, []string{"cluster_id"})
	corrupt := valid
	corrupt.SourceMatchers = []byte(`{}`)
	if !alertInhibited([]sqlc.AlertInhibition{corrupt, valid}, []map[string]string{{"severity": "critical", "cluster_id": "c1"}}, map[string]string{"severity": "warning", "cluster_id": "c1"}) {
		t.Fatal("valid inhibition after corrupt rule was not evaluated")
	}
}

func TestInhibition_HistoricalEmptyArrays(t *testing.T) {
	source := []map[string]string{{"severity": "critical"}}
	target := map[string]string{"severity": "warning"}
	for _, equal := range [][]string{nil, {}} {
		valid := inhibitionRule(t, []inhibitionMatcher{{Label: "severity", Value: "critical"}}, []inhibitionMatcher{{Label: "severity", Value: "warning"}}, equal)
		if !alertInhibited([]sqlc.AlertInhibition{valid}, source, target) {
			t.Fatal("empty equal-label set must preserve unrestricted equality")
		}
		valid.SourceMatchers = []byte(`[]`)
		if alertInhibited([]sqlc.AlertInhibition{valid}, source, target) {
			t.Fatal("historical empty source matcher set must remain inert")
		}
		valid.SourceMatchers = []byte(`[{"label":"severity","value":"critical"}]`)
		valid.TargetMatchers = []byte(`[]`)
		if alertInhibited([]sqlc.AlertInhibition{valid}, source, target) {
			t.Fatal("historical empty target matcher set must remain inert")
		}
	}
}

// Unit fixtures pass through the same canonical reader as the sweep loader.
func alertInhibited(rows []sqlc.AlertInhibition, firing []map[string]string, target map[string]string) bool {
	prepared := make([]preparedInhibition, 0, len(rows))
	for _, row := range rows {
		parsed, err := jsoncontract.ReadInhibitionV1(row.SourceMatchers, row.TargetMatchers, row.EqualLabels)
		if err == nil {
			prepared = append(prepared, preparedInhibition{ID: row.ID, InhibitionV1: parsed})
		}
	}
	return preparedAlertInhibited(prepared, firing, target)
}
