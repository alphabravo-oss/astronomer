package charliequalification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeDriver struct {
	counters CounterSet
	result   ScenarioResult
}

func (f fakeDriver) Counters(context.Context) (CounterSet, error)        { return f.counters, nil }
func (f fakeDriver) Run(context.Context, ScenarioRequest) ScenarioResult { return f.result }

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9443", "[::1]:9443"} {
		if err := ValidateLoopbackAddress(address); err != nil {
			t.Fatalf("%s: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:9443", "localhost:9443", ":9443", "127.0.0.1"} {
		if err := ValidateLoopbackAddress(address); err == nil {
			t.Fatalf("expected %s to be rejected", address)
		}
	}
}

func TestHookRequiresAuthAndNormalizesUnsupportedResult(t *testing.T) {
	token := strings.Repeat("q", 32)
	hook, err := NewHook(token, fakeDriver{result: ScenarioResult{Scenario: "feature_false", Passed: true}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"schema":"charlie.live-scenario/v1","run_id":"qualification-test","scenario":"feature_false","candidate":{}}`
	unauthorized := httptest.NewRecorder()
	hook.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/v1/scenarios/feature_false", strings.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/scenarios/feature_false", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	hook.Handler().ServeHTTP(response, request)
	var result ScenarioResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || result.Passed || len(result.Assertions) != 1 || result.Assertions[0].Passed {
		t.Fatalf("unsupported result was accidentally promoted: %#v", result)
	}
}

func TestHookRejectsUnknownFields(t *testing.T) {
	token := strings.Repeat("q", 32)
	hook, err := NewHook(token, fakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"schema":"charlie.live-scenario/v1","run_id":"qualification-test","scenario":"feature_false","candidate":{},"unexpected":true}`
	request := httptest.NewRequest(http.MethodPost, "/v1/scenarios/feature_false", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	hook.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestCountersRequireCompleteFamilies(t *testing.T) {
	token := strings.Repeat("q", 32)
	hook, err := NewHook(token, fakeDriver{counters: CounterSet{Runtime: map[string]uint64{}, Downstream: map[string]uint64{}}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/counters", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	hook.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}
