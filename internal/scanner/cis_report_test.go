package scanner

import (
	"encoding/json"
	"testing"
)

func TestDecodeCISReportJSON(t *testing.T) {
	encoded := `{"total":3,"tests":[{"id":"1"}]}`
	got := DecodeCISReportJSON(map[string]any{"reportJSON": encoded})
	if got["total"] != float64(3) {
		t.Fatalf("decoded report total = %#v, want 3", got["total"])
	}

	inline := map[string]any{"total": 2}
	if got := DecodeCISReportJSON(map[string]any{"report": inline}); got["total"] != 2 {
		t.Fatalf("inline report total = %#v, want 2", got["total"])
	}

	if got := DecodeCISReportJSON(map[string]any{"reportJSON": map[string]any{"total": 1}}); got["total"] != 1 {
		t.Fatalf("object reportJSON total = %#v, want 1", got["total"])
	}

	if got := DecodeCISReportJSON(nil); len(got) != 0 {
		t.Fatalf("nil spec should decode to empty map: %#v", got)
	}
}

func TestCISReportFieldHelpers(t *testing.T) {
	if got := StringField(map[string]any{"a": "", "b": "value"}, "a", "b"); got != "value" {
		t.Fatalf("StringField = %q, want value", got)
	}
	if got := StringField(map[string]any{"a": 1}, "a"); got != "" {
		t.Fatalf("StringField non-string = %q, want empty", got)
	}

	for _, tc := range []struct {
		name string
		in   any
		want int32
		ok   bool
	}{
		{name: "float64", in: float64(7), want: 7, ok: true},
		{name: "int", in: int(8), want: 8, ok: true},
		{name: "int64", in: int64(9), want: 9, ok: true},
		{name: "number", in: json.Number("10"), want: 10, ok: true},
		{name: "bad number", in: json.Number("bad"), ok: false},
		{name: "string", in: "11", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NumericField(map[string]any{"value": tc.in}, "value")
			if ok != tc.ok || got != tc.want {
				t.Fatalf("NumericField = (%d, %v), want (%d, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
