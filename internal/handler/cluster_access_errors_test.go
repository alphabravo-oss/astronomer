package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRespondClusterAccessErrorPreservesAgentOverload(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/test/pods/", nil)

	respondClusterAccessError(recorder, request, &kubernetesResponseError{
		StatusCode: http.StatusTooManyRequests,
		Body:       `{"message":"agent at its in-flight request limit; retry"}`,
		Headers:    map[string]string{"Retry-After": "1"},
	})

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusTooManyRequests, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"code":"agent_overloaded"`) {
		t.Fatalf("body does not contain stable overload code: %s", body)
	}
	if body := recorder.Body.String(); strings.Contains(body, "in-flight request limit") {
		t.Fatalf("body leaked upstream agent detail: %s", body)
	}
}
