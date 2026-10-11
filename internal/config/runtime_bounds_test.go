package config

import "testing"

func TestLoadRejectsRuntimeIntegerOverflow(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{"LOGIN_FAILURE_THRESHOLD", "5", true}, {"LOGIN_FAILURE_THRESHOLD", "2147483647", true}, {"LOGIN_FAILURE_THRESHOLD", "4294967297", false},
		{"WORKER_CONCURRENCY", "32", true}, {"WORKER_CONCURRENCY", "2147483639", true}, {"WORKER_CONCURRENCY", "2147483640", false}, {"WORKER_CONCURRENCY", "4294967297", false},
	} {
		t.Run(tc.key+"_"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if (err == nil) != tc.valid {
				t.Fatalf("%v", err)
			}
		})
	}
}
