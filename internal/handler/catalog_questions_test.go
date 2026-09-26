package handler

import (
	"encoding/json"
	"testing"
)

func TestEnrichSchemaWithRancherQuestions(t *testing.T) {
	base := inferSchema(map[string]any{
		"manager":  map[string]any{"ingress": map[string]any{"enabled": false, "host": ""}},
		"replicas": float64(1),
	})
	raw := []byte(`questions:
- variable: manager.ingress.enabled
  type: boolean
  label: Enable manager ingress
  group: Networking
  show_subquestion_if: true
  subquestions:
  - variable: manager.ingress.host
    type: hostname
    label: Manager hostname
    min_length: 3
- variable: replicas
  type: int
  label: Controller replicas
  min: 1
  max: 9
  options: [1, 3, 5]
`)
	enriched := enrichSchemaWithRancherQuestions(base, raw)
	if enriched == nil {
		t.Fatal("expected enriched schema")
	}
	var root map[string]any
	if err := json.Unmarshal(enriched, &root); err != nil {
		t.Fatal(err)
	}
	manager := root["properties"].(map[string]any)["manager"].(map[string]any)
	ingress := manager["properties"].(map[string]any)["ingress"].(map[string]any)
	host := ingress["properties"].(map[string]any)["host"].(map[string]any)
	condition := host["x-astronomer-show-when"].(map[string]any)
	if condition["path"] != "manager.ingress.enabled" || condition["equals"] != "true" {
		t.Fatalf("condition=%v", condition)
	}
	if host["format"] != "hostname" || host["minLength"] != float64(3) {
		t.Fatalf("host metadata=%v", host)
	}
	replicas := root["properties"].(map[string]any)["replicas"].(map[string]any)
	if replicas["title"] != "Controller replicas" || replicas["minimum"] != float64(1) || replicas["maximum"] != float64(9) {
		t.Fatalf("replica metadata=%v", replicas)
	}
}

func TestEnrichSchemaWithRancherQuestionRequiredTooltipAndReferenceMetadata(t *testing.T) {
	raw := []byte(`questions:
  - variable: credentials.secretName
    type: secret
    label: Credential Secret
    tooltip: Select a Secret from the release namespace.
    required: true
  - variable: endpoint
    type: ipaddr
    default: 127.0.0.1
`)
	enriched := enrichSchemaWithRancherQuestions(json.RawMessage(`{"type":"object","properties":{}}`), raw)
	var schema map[string]any
	if err := json.Unmarshal(enriched, &schema); err != nil {
		t.Fatal(err)
	}
	credentials := schema["properties"].(map[string]any)["credentials"].(map[string]any)
	if got := credentials["required"].([]any); len(got) != 1 || got[0] != "secretName" {
		t.Fatalf("required=%v", got)
	}
	secret := credentials["properties"].(map[string]any)["secretName"].(map[string]any)
	if secret["format"] != "secret" || secret["description"] != "Select a Secret from the release namespace." {
		t.Fatalf("secret metadata=%v", secret)
	}
	endpoint := schema["properties"].(map[string]any)["endpoint"].(map[string]any)
	if endpoint["format"] != "ip" || endpoint["default"] != "127.0.0.1" {
		t.Fatalf("endpoint metadata=%v", endpoint)
	}
}

func TestEnrichSchemaKeepsNullChartDefaultValid(t *testing.T) {
	base := inferSchema(map[string]any{
		"persistence": map[string]any{
			"backingImage": map[string]any{"dataSourceParameters": nil},
		},
	})
	raw := []byte(`questions:
- variable: persistence.backingImage.dataSourceParameters
  type: string
  label: Backing image parameters
  options:
    - existing
`)
	enriched := enrichSchemaWithRancherQuestions(base, raw)
	var schema map[string]any
	if err := json.Unmarshal(enriched, &schema); err != nil {
		t.Fatal(err)
	}
	node := schema["properties"].(map[string]any)["persistence"].(map[string]any)["properties"].(map[string]any)["backingImage"].(map[string]any)["properties"].(map[string]any)["dataSourceParameters"].(map[string]any)
	types, ok := node["type"].([]any)
	if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Fatalf("nullable question type = %#v", node["type"])
	}
	if _, leaked := node["x-astronomer-default-null"]; leaked {
		t.Fatal("internal null marker leaked into enriched field schema")
	}
	options, ok := node["enum"].([]any)
	if !ok || len(options) != 2 || options[0] != "existing" || options[1] != nil {
		t.Fatalf("nullable question options = %#v", node["enum"])
	}
	if err := validateCatalogValuesSchema(enriched, map[string]any{
		"persistence": map[string]any{
			"backingImage": map[string]any{"dataSourceParameters": nil},
		},
	}); err != nil {
		t.Fatalf("chart default null should validate: %v", err)
	}
}

func TestEnrichSchemaRejectsUnsafeQuestionPath(t *testing.T) {
	base := inferSchema(map[string]any{"safe": "value"})
	enriched := enrichSchemaWithRancherQuestions(base, []byte("questions:\n- variable: __proto__.polluted\n  label: Bad\n"))
	if string(enriched) == "" {
		t.Fatal("safe base schema should remain available")
	}
	var root map[string]any
	_ = json.Unmarshal(enriched, &root)
	properties := root["properties"].(map[string]any)
	if _, exists := properties["__proto__"]; exists {
		t.Fatal("unsafe path was materialized")
	}
}
