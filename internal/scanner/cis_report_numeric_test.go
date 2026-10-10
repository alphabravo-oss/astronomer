package scanner

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func TestNumericFieldCheckedInt32Boundary(t *testing.T) {
	type numericCase struct {
		name  string
		value any
		want  int32
		valid bool
	}
	cases := []numericCase{
		{"float max", float64(math.MaxInt32), math.MaxInt32, true},
		{"float min", float64(math.MinInt32), math.MinInt32, true},
		{"int max", int(math.MaxInt32), math.MaxInt32, true},
		{"int min", int(math.MinInt32), math.MinInt32, true},
		{"int64 max", int64(math.MaxInt32), math.MaxInt32, true},
		{"int64 min", int64(math.MinInt32), math.MinInt32, true},
		{"number max", json.Number("2147483647"), math.MaxInt32, true},
		{"number min", json.Number("-2147483648"), math.MinInt32, true},
		{"float overflow", float64(math.MaxInt32) + 1, 0, false},
		{"float underflow", float64(math.MinInt32) - 1, 0, false},
		{"fraction", 1.5, 0, false},
		{"negative fraction", -1.5, 0, false},
		{"nan", math.NaN(), 0, false},
		{"positive infinity", math.Inf(1), 0, false},
		{"negative infinity", math.Inf(-1), 0, false},
		{"int64 overflow", int64(math.MaxInt32) + 1, 0, false},
		{"int64 underflow", int64(math.MinInt32) - 1, 0, false},
		{"number overflow", json.Number("2147483648"), 0, false},
		{"number underflow", json.Number("-2147483649"), 0, false},
		{"number fractional", json.Number("1.5"), 0, false},
		{"number int64 overflow", json.Number("9223372036854775808"), 0, false},
	}
	if strconv.IntSize == 64 {
		overflow, underflow := int64(math.MaxInt32)+1, int64(math.MinInt32)-1
		cases = append(cases, numericCase{"int overflow", int(overflow), 0, false}, numericCase{"int underflow", int(underflow), 0, false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := NumericField(map[string]any{"count": tc.value}, "count")
			if got != tc.want || valid != tc.valid {
				t.Fatalf("got (%d, %v), want (%d, %v)", got, valid, tc.want, tc.valid)
			}
		})
	}
}
