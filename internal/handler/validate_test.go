package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAndValidateRejectsUnknownAndTrailingJSON(t *testing.T) {
	type request struct {
		Name string `json:"name" validate:"required"`
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"name":"valid","retired":true}`},
		{name: "trailing document", body: `{"name":"valid"}{"name":"second"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			httpRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			var decoded request
			if decodeAndValidate(recorder, httpRequest, &decoded) {
				t.Fatal("decodeAndValidate accepted an ambiguous request body")
			}
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestUpdateGeneralSettingsRejectsRetiredSnakeCaseAlias(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/settings/general/",
		strings.NewReader(`{"platform_name":"legacy"}`),
	)

	(&ResourceHandler{}).UpdateGeneralSettings(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}
