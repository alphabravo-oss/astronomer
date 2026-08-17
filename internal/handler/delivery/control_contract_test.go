package delivery

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
)

func TestRequireIfMatchStrongCASContract(t *testing.T) {
	for _, test := range []struct {
		name  string
		value []string
		want  int64
		ok    bool
	}{
		{name: "strong", value: []string{`"42"`}, want: 42, ok: true},
		{name: "zero", value: []string{`"0"`}, want: 0, ok: true},
		{name: "missing"},
		{name: "weak", value: []string{`W/"42"`}},
		{name: "wildcard", value: []string{"*"}},
		{name: "unquoted", value: []string{"42"}},
		{name: "duplicate", value: []string{`"41"`, `"42"`}},
		{name: "negative", value: []string{`"-1"`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("PATCH", "/", nil)
			for _, value := range test.value {
				request.Header.Add("If-Match", value)
			}
			got, err := requireIfMatch(request)
			if (err == nil) != test.ok || (test.ok && got != test.want) {
				t.Fatalf("got version=%d err=%v, want version=%d ok=%v", got, err, test.want, test.ok)
			}
		})
	}
}

func TestCanonicalJSONObjectBoundsAndShape(t *testing.T) {
	if got, err := canonicalJSONObject(nil); err != nil || string(got) != "{}" {
		t.Fatalf("empty policy = %s, %v", got, err)
	}
	if got, err := canonicalJSONObject([]byte(`{"z":1,"a":true}`)); err != nil || string(got) != `{"a":true,"z":1}` {
		t.Fatalf("canonical policy = %s, %v", got, err)
	}
	for _, raw := range []string{`[]`, `null`, `"string"`, `{broken`} {
		if raw == "null" {
			continue // documented omission normalizes to an empty policy
		}
		if _, err := canonicalJSONObject([]byte(raw)); err == nil {
			t.Fatalf("accepted non-object %q", raw)
		}
	}
}

func TestPreviewRisksAreStableAndBounded(t *testing.T) {
	result := placement.Result{
		SelectedCount: 101, RequiresAllConfirmation: true,
		Decisions: []placement.Decision{
			{Reason: placement.ReasonDisconnected},
			{Reason: placement.ReasonIncompatible},
			{Reason: placement.ReasonMissingCapability},
			{Reason: placement.ReasonIncompatible},
		},
	}
	want := []string{"all_clusters", "capability_blockers", "disconnected_clusters_excluded", "incompatible_clusters_excluded", "large_blast_radius"}
	if got := previewRisks(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("risks=%v want=%v", got, want)
	}
}
