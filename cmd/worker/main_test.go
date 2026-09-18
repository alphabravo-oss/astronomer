package main

import "testing"

func TestWorkerDBMaxConns(t *testing.T) {
	tests := []struct {
		name        string
		configured  int32
		concurrency int
		want        int32
	}{
		{name: "explicit", configured: 17, concurrency: 64, want: 17},
		{name: "base minimum", concurrency: 8, want: 25},
		{name: "worker headroom", concurrency: 32, want: 40},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := workerDBMaxConns(test.configured, test.concurrency); got != test.want {
				t.Fatalf("workerDBMaxConns(%d, %d) = %d, want %d", test.configured, test.concurrency, got, test.want)
			}
		})
	}
}
