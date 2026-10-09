package jsoncontract

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadTaskPayloadV1CompatibilityAndSafeFailures(t *testing.T) {
	for _, raw := range []string{`{}`, `{"provider":{"future":[1,null,{"x":true}]},"_traceparent":"invalid-but-inert-trace"}`, `{"_correlation_id":"","_tracestate":"vendor=value"}`} {
		got, err := ReadTaskPayloadV1([]byte(raw))
		if err != nil || string(got) != raw {
			t.Fatalf("v1 document not preserved: %v", err)
		}
	}
	got, err := ReadTaskPayloadV1(nil)
	if err != nil || string(got) != `{}` {
		t.Fatal("missing legacy empty adapter")
	}
	for _, raw := range []string{`null`, `[]`, `42`, ` `, `{"secret":"private-fixture"`, `{"_correlation_id":null}`, `{"_traceparent":[]}`, `{} {}`} {
		_, err := ReadTaskPayloadV1([]byte(raw))
		if err == nil {
			t.Fatalf("invalid document accepted")
		}
		if strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("error exposed payload")
		}
	}
	_, err = ReadTaskPayloadV1(bytes.Repeat([]byte("x"), MaxDocumentBytes+1))
	if err == nil || err.Error() != "invalid task_payload: too_large" {
		t.Fatalf("missing size bound: %v", err)
	}
}
