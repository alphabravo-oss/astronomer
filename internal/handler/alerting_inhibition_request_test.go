package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInhibitionRequestValidateNestedBeforeMutation(t *testing.T) {
	for _, tc := range []struct{ source, equal, field string }{
		{`[{"label":"severity"}]`, `[]`, "source_matchers"},
		{`[{"label":"severity","value":null}]`, `[]`, "source_matchers"},
		{`[{"label":"severity","value":"sensitive-fixture","is_regex":null}]`, `[]`, "source_matchers"},
		{`[{"label":"severity","value":"sensitive-fixture[","is_regex":true}]`, `[]`, "source_matchers"},
		{`[{"label":"severity","value":"critical"}]`, `[null]`, "equal_labels"},
		{`[{"label":"severity","value":"critical"}]`, `[""]`, "equal_labels"},
	} {
		h := NewAlertingHandler(nil)
		h.SetRunTx(func(context.Context, func(AlertingMutationTx) error) error {
			t.Fatal("invalid nested JSON reached mutation transaction")
			return nil
		})
		body := `{"name":"valid","source_matchers":` + tc.source + `,"target_matchers":[{"label":"severity","value":"warning"}],"equal_labels":` + tc.equal + `}`
		w := httptest.NewRecorder()
		h.CreateInhibition(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"field":"`+tc.field+`"`) || strings.Contains(w.Body.String(), "sensitive-fixture") {
			t.Fatalf("invalid nested item lacks safe field error: status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestInhibitionRequestV1OptionalNullEmptyAndFalse(t *testing.T) {
	for _, optional := range []string{"", `,"equal_labels":null,"enabled":null`, `,"equal_labels":[],"enabled":false`} {
		body := `{"name":"valid","source_matchers":[{"label":"severity","value":""}],"target_matchers":[{"label":"severity","value":"warning"}]` + optional + `}`
		var req InhibitionRequest
		w := httptest.NewRecorder()
		if !decodeAndValidate(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &req) || len(req.SourceMatchers) != 1 || req.SourceMatchers[0].IsRegex || req.SourceMatchers[0].Value != "" || string(marshalEqualLabels(req.EqualLabels)) != "[]" {
			t.Fatalf("v1 optional/empty behavior changed: status=%d body=%s", w.Code, w.Body.String())
		}
		if strings.Contains(optional, "false") && (req.Enabled == nil || *req.Enabled) {
			t.Fatal("explicit false was lost")
		}
	}
}
