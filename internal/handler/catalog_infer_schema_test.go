package handler

import (
	"encoding/json"
	"testing"
)

func TestInferSchema_TypesAndTitles(t *testing.T) {
	values := map[string]interface{}{
		"replicaCount": float64(3),
		"enabled":      true,
		"image":        map[string]interface{}{"repository": "nginx", "tag": "1.0"},
		"ports":        []interface{}{float64(80)},
		"nothing":      nil,
	}
	raw := inferSchema(values)
	if raw == nil {
		t.Fatal("inferSchema returned nil for non-empty values")
	}
	var s map[string]interface{}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("inferred schema not valid JSON: %v", err)
	}
	if s["type"] != "object" || s["x-astronomer-inferred"] != true {
		t.Fatalf("root should be inferred object, got %v", s)
	}
	props := s["properties"].(map[string]interface{})
	rc := props["replicaCount"].(map[string]interface{})
	if rc["type"] != "number" || rc["title"] != "replicaCount" {
		t.Errorf("replicaCount node wrong: %v", rc)
	}
	if props["enabled"].(map[string]interface{})["type"] != "boolean" {
		t.Error("enabled should be boolean")
	}
	img := props["image"].(map[string]interface{})
	if img["type"] != "object" {
		t.Error("image should be object")
	}
	if props["ports"].(map[string]interface{})["type"] != "array" {
		t.Error("ports should be array")
	}
	// nil value => no type constraint (opaque), but still present
	if _, ok := props["nothing"]; !ok {
		t.Error("nil-valued key should still appear as a field")
	}
}

func TestInferSchema_EmptyIsNil(t *testing.T) {
	if inferSchema(map[string]interface{}{}) != nil {
		t.Error("empty values should infer nil (no form)")
	}
}
