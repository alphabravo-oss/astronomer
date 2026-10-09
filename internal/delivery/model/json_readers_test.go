package model

import (
	"strings"
	"testing"
)

func TestReadPlacementV1CompatibilityAndIntegrity(t *testing.T) {
	for _, raw := range []string{`{}`, `{"match_labels":null,"match_expressions":null,"future_v1":true}`, `{"match_labels":{"namespace":""}}`} {
		if _, err := ReadPlacementV1([]byte(raw)); err != nil {
			t.Fatalf("v1 selector adapter rejected historical shape: %v", err)
		}
	}
	for _, raw := range []string{`null`, `{} {}`, `{"match_labels":{"namespace":42}}`, `{"all_clusters":true,"match_labels":{"namespace":"private-fixture"}}`, `{"match_expressions":[{"key":"namespace","operator":"future-operator"}]}`} {
		if _, err := ReadPlacementV1([]byte(raw)); err == nil || strings.Contains(err.Error(), "private-fixture") {
			t.Fatalf("invalid selector not rejected with safe category: %v", err)
		}
	}
}
