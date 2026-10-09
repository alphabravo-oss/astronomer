package jsoncontract

import (
	"errors"
	"strings"
	"testing"
)

func TestInhibitionV1AdditiveReader(t *testing.T) {
	parsed, err := ReadInhibitionV1([]byte(`[{"label":"severity","value":"","future":{"secret":"sensitive-fixture"}}]`), []byte(`[]`), []byte(`[]`))
	if err != nil || len(parsed.Source) != 1 || parsed.Source[0].IsRegex || parsed.Source[0].Value != "" || len(parsed.Target) != 0 {
		t.Fatalf("v1 additive/optional/empty compatibility changed: parsed=%+v err=%v", parsed, err)
	}
}

func TestInhibitionV1InvalidNestedAndBoundedErrors(t *testing.T) {
	for _, source := range []string{`null`, `{}`, `[null]`, `[42]`, `[{}]`, `[{"label":null,"value":"sensitive-fixture"}]`, `[{"label":"severity"}]`, `[{"label":"severity","value":1}]`, `[{"label":"severity","value":"sensitive-fixture","is_regex":null}]`, `[{"label":"severity","value":"[","is_regex":true}]`, strings.Repeat(" ", MaxDocumentBytes) + `[]`} {
		_, err := ReadInhibitionV1([]byte(source), []byte(`[]`), []byte(`[]`))
		var schemaErr *Error
		if !errors.As(err, &schemaErr) || strings.Contains(err.Error(), "sensitive-fixture") || len(err.Error()) > 100 {
			t.Fatalf("expected bounded typed schema error, got %v", err)
		}
	}
	for _, equal := range []string{`null`, `{}`, `[null]`, `[42]`, `[""]`, `["  "]`} {
		if _, err := ReadInhibitionV1([]byte(`[]`), []byte(`[]`), []byte(equal)); err == nil {
			t.Fatalf("accepted invalid equal_labels %s", equal)
		}
	}
}
