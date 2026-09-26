package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateCatalogValuesSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}},"additionalProperties":true}`)
	if err := validateCatalogValuesSchema(schema, map[string]any{"replicas": float64(2)}); err != nil {
		t.Fatalf("valid values rejected: %v", err)
	}
	err := validateCatalogValuesSchema(schema, map[string]any{"replicas": float64(0)})
	if err == nil || !strings.Contains(err.Error(), "replicas") {
		t.Fatalf("expected path-only validation error, got %v", err)
	}
}

func TestValidateCatalogValuesSchemaDoesNotEchoSecretValue(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"password":{"type":"string","minLength":20}}}`)
	secret := "short-secret"
	err := validateCatalogValuesSchema(schema, map[string]any{"password": secret})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("validation must fail without echoing value: %v", err)
	}
}
