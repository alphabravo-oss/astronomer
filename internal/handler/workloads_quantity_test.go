package handler

import "testing"

func TestParseKubernetesResourceQuantities(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		got  int
		want int
	}{
		{name: "fractional CPU", got: parseCPU("0.5"), want: 500},
		{name: "millicpu", got: parseCPU("250m"), want: 250},
		{name: "fractional binary memory", got: parseMemory("1.5Gi"), want: 1610612736},
		{name: "tebibytes", got: parseMemory("1Ti"), want: 1099511627776},
		{name: "decimal gigabytes", got: parseMemory("2G"), want: 2000000000},
		{name: "invalid CPU", got: parseCPU("not-a-quantity"), want: 0},
		{name: "invalid memory", got: parseMemory("not-a-quantity"), want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("got %d, want %d", test.got, test.want)
			}
		})
	}
}
