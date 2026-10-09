package jsoncontract

import (
	"encoding/json"
	"testing"
)

func TestReadStringMapV1NestedShape(t *testing.T) {
	for _, raw := range []string{`{}`, `{"team":"platform","empty":""}`} {
		if _, err := ReadStringMapV1("labels", []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"team":null}`, `{"team":{"private-fixture":true}}`, `{"team":7}`} {
		if _, err := ReadStringMapV1("labels", []byte(raw)); err == nil {
			t.Fatal("invalid map accepted")
		}
	}
}

func TestReadAuditDetailV1PreservesLegacyDomainValues(t *testing.T) {
	detail, err := ReadAuditDetailV1([]byte(`{"tags":["historical-domain-value"],"before":{"provider":{"future":true}},"number":1e999}`))
	if err != nil {
		t.Fatal(err)
	}
	if detail["number"] != json.Number("1e999") {
		t.Fatal("opaque numeric value lost")
	}
	if _, err := json.Marshal(detail); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `[]`, `{} {}`, `{"value":`} {
		if _, err := ReadAuditDetailV1([]byte(raw)); err == nil {
			t.Fatal("invalid audit detail accepted")
		}
	}
}
