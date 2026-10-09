package jsoncontract

import (
	"strings"
	"testing"
)

func TestDecodeOneClosedInterpretation(t *testing.T) {
	type document struct {
		Value int `json:"value"`
	}
	for _, test := range []struct {
		name, input string
		accepted    bool
	}{
		{"valid", `{"value":1}`, true},
		{"duplicate", `{"value":1,"value":2}`, false},
		{"unknown", `{"value":1,"other":2}`, false},
		{"second document", `{"value":1} {"value":2}`, false},
		{"malformed tail", `{"value":1} invalid`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var target document
			if err := Decode([]byte(test.input), &target); (err == nil) != test.accepted {
				t.Fatalf("unexpected contract result: %v", err)
			}
		})
	}
	var nested any
	if err := Decode([]byte(strings.Repeat("[", 65)+"0"+strings.Repeat("]", 65)), &nested); err == nil {
		t.Fatal("unbounded nesting accepted")
	}
	if err := Decode([]byte(strings.Repeat("[", 64)+"0"+strings.Repeat("]", 64)), &nested); err != nil {
		t.Fatal(err)
	}
}
