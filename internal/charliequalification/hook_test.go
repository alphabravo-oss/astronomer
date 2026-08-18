package charliequalification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeDriver struct {
	counters CounterSet
	result   ScenarioResult
	runs     int
}

func (f *fakeDriver) Counters(context.Context) (CounterSet, error)        { return f.counters, nil }
func (f *fakeDriver) Run(context.Context, ScenarioRequest) ScenarioResult { f.runs++; return f.result }

const candidateJSON = `{"ref":"refs/heads/qualification","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","version":"1.0.21","central_image_digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","agent_image_digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","central_chart_digest":"sha256:3333333333333333333333333333333333333333333333333333333333333333","agent_chart_digest":"sha256:4444444444444444444444444444444444444444444444444444444444444444"}`

func scenarioBody(runID, scenario string) string {
	return `{"schema":"charlie.live-scenario/v1","run_id":"` + runID + `","scenario":"` + scenario + `","candidate":` + candidateJSON + `}`
}

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

func TestHTTPServerDoesNotSetConnectionReadTimeout(t *testing.T) {
	server, err := NewHTTPServer("127.0.0.1:9443", http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	if server.ReadTimeout != 0 {
		t.Fatalf("ReadTimeout = %s; a connection-wide read deadline cancels candidate deploys", server.ReadTimeout)
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s", server.ReadHeaderTimeout)
	}
}

func TestHookRequiresAuthAndNormalizesUnsupportedResult(t *testing.T) {
	token := strings.Repeat("q", 32)
	driver := &fakeDriver{result: ScenarioResult{Scenario: "feature_false", Passed: true}}
	hook, err := NewHook(token, driver)
	if err != nil {
		t.Fatal(err)
	}
	body := scenarioBody("qualification-test", "feature_false")
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
	if response.Code != http.StatusOK || result.Passed || len(result.Assertions) != len(requiredAssertions["feature_false"]) {
		t.Fatalf("unsupported result was accidentally promoted: %#v", result)
	}
	for _, assertion := range result.Assertions {
		if assertion.Passed {
			t.Fatalf("unsupported assertion was accidentally promoted: %#v", result)
		}
	}
}

func TestHookRejectsUnknownFields(t *testing.T) {
	token := strings.Repeat("q", 32)
	hook, err := NewHook(token, &fakeDriver{})
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSuffix(scenarioBody("qualification-test", "feature_false"), "}") + `,"unexpected":true}`
	request := httptest.NewRequest(http.MethodPost, "/v1/scenarios/feature_false", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	hook.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHookBindsCandidateAndReplaysScenarioWithoutEffects(t *testing.T) {
	token := strings.Repeat("q", 32)
	driver := &fakeDriver{result: Passed("feature_false", "state_applied", "runtime_counters_unchanged", "downstream_counters_unchanged")}
	hook, err := NewHook(token, driver)
	if err != nil {
		t.Fatal(err)
	}
	call := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/scenarios/feature_false", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		hook.Handler().ServeHTTP(response, request)
		return response
	}
	body := scenarioBody("qualification-bound", "feature_false")
	if response := call(body); response.Code != http.StatusOK {
		t.Fatalf("first status = %d", response.Code)
	}
	if response := call(body); response.Code != http.StatusOK || driver.runs != 1 {
		t.Fatalf("replay status=%d runs=%d", response.Code, driver.runs)
	}
	substituted := strings.Replace(body, strings.Repeat("1", 64), strings.Repeat("9", 64), 1)
	if response := call(substituted); response.Code != http.StatusConflict || driver.runs != 1 {
		t.Fatalf("substitution status=%d runs=%d", response.Code, driver.runs)
	}
	if response := call(scenarioBody("qualification-other", "feature_false")); response.Code != http.StatusConflict || driver.runs != 1 {
		t.Fatalf("run substitution status=%d runs=%d", response.Code, driver.runs)
	}
}

func TestHookRejectsIncompleteCandidateBeforeDriver(t *testing.T) {
	token := strings.Repeat("q", 32)
	driver := &fakeDriver{}
	hook, err := NewHook(token, driver)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/scenarios/feature_false", strings.NewReader(`{"schema":"charlie.live-scenario/v1","run_id":"qualification-test","scenario":"feature_false","candidate":{}}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	hook.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || driver.runs != 0 {
		t.Fatalf("status=%d runs=%d", response.Code, driver.runs)
	}
}

func TestQualificationCatalogKeepsApprovalPathInAutomation(t *testing.T) {
	if _, legacy := requiredAssertions["auto_nonallowlisted_denial"]; legacy {
		t.Fatal("qualification catalog still treats a safe non-auto write as terminal denial")
	}
	assertions, ok := requiredAssertions["auto_nonallowlisted_approval"]
	if !ok || len(assertions) != 2 || assertions[0] != "approval_pending" || assertions[1] != "product_calls_zero" {
		t.Fatalf("automation approval fallback assertions = %#v", assertions)
	}
}

func TestCountersRequireCompleteFamilies(t *testing.T) {
	token := strings.Repeat("q", 32)
	hook, err := NewHook(token, &fakeDriver{counters: CounterSet{Runtime: map[string]uint64{}, Downstream: map[string]uint64{}}})
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
