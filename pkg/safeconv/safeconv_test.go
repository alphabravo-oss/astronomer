package safeconv

import (
	"math"
	"testing"
)

func TestSaturatingConversions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"int32 in range", Int32(int64(42)), int32(42)},
		{"int32 negative in range", Int32(-7), int32(-7)},
		{"int32 above", Int32(int64(math.MaxInt32) + 1), int32(math.MaxInt32)},
		{"int32 below", Int32(int64(math.MinInt32) - 1), int32(math.MinInt32)},
		{"int32 from huge uint64", Int32(uint64(math.MaxUint64)), int32(math.MaxInt32)},
		{"uint32 negative", Uint32(-1), uint32(0)},
		{"uint32 above", Uint32(int64(math.MaxUint32) + 5), uint32(math.MaxUint32)},
		{"uint32 in range", Uint32(1000), uint32(1000)},
		{"uint64 negative", Uint64(int64(-9)), uint64(0)},
		{"uint64 in range", Uint64(9), uint64(9)},
		{"int64 from huge uint64", Int64(uint64(math.MaxUint64)), int64(math.MaxInt64)},
		{"int64 in range", Int64(uint64(12)), int64(12)},
		{"int from uint64", Int(uint64(5)), 5},
		{"byte above", Byte(300), byte(255)},
		{"byte negative", Byte(-3), byte(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("got %v (%T), want %v (%T)", tc.got, tc.got, tc.want, tc.want)
			}
		})
	}
}
