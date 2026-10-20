package handler

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestMonitoringRetryAttemptsRejectsOverflowAndFractions(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  int32
		valid bool
	}{
		{nil, 1, true}, {float64(3), 3, true}, {int32(4), 4, true}, {int(5), 5, true}, {float64(math.MaxInt32), math.MaxInt32, true},
		{float64(4294967297), 0, false}, {int(4294967297), 0, false}, {3.5, 0, false}, {float64(0), 0, false}, {-1, 0, false}, {"3", 0, false}, {math.Inf(1), 0, false}, {math.NaN(), 0, false},
	} {
		got, err := monitoringRetryAttempts(tc.value)
		if (err == nil) != tc.valid || got != tc.want {
			t.Fatalf("value=%v got=%d err=%v", tc.value, got, err)
		}
	}
	for _, raw := range []string{`{"operationPolicies":{"maxRetryAttempts":3}}`, `{"operationPolicies":{"maxRetryAttempts":3.0}}`, `{"operationPolicies":{"maxRetryAttempts":3e0}}`} {
		if got, err := monitoringRetryPolicy(json.RawMessage(raw)); err != nil || got != 3 {
			t.Fatalf("valid policy %s: %d %v", raw, got, err)
		}
	}
	handler := &MonitoringHandler{}
	for _, raw := range []string{`{"operationPolicies":{"maxRetryAttempts":4294967297}}`, `{"operationPolicies":{"maxRetryAttempts":3.5}}`, `{"operationPolicies":{"maxRetryAttempts":2147483647.0000001}}`, `{"operationPolicies":{"maxRetryAttempts":3.0000000000000000001}}`, `{"operationPolicies":{"maxRetryAttempts":1e999999999}}`} {
		if _, err := handler.resolveMaxRetryAttempts(sqlc.MonitoringBackend{AuthConfig: json.RawMessage(raw)}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
