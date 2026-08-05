// Command specprep creates the oapi-codegen input from the immutable bridge pin.
// It only inlines JSON Schema $defs because oapi-codegen v2.5 cannot generate a
// Go type from a nested $defs reference in an external OpenAPI 3.1 schema.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 4 {
		panic("usage: specprep BRIDGE FINDING OUTPUT")
	}
	bridgeRaw, err := os.ReadFile(os.Args[1])
	check(err)
	findingRaw, err := os.ReadFile(os.Args[2])
	check(err)

	var bridge map[string]any
	check(yaml.Unmarshal(bridgeRaw, &bridge))
	var finding map[string]any
	check(json.Unmarshal(findingRaw, &finding))

	components := bridge["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	defs := finding["$defs"].(map[string]any)
	delete(finding, "$schema")
	delete(finding, "$id")
	delete(finding, "$defs")
	rewriteRefs(finding)
	schemas["FindingEnvelope"] = finding
	for name, definition := range defs {
		rewriteRefs(definition)
		schemas["Finding"+upperFirst(name)] = definition
	}

	generated, err := yaml.Marshal(bridge)
	check(err)
	check(os.MkdirAll(filepath.Dir(os.Args[3]), 0o755))
	check(os.WriteFile(os.Args[3], generated, 0o644))
}

func rewriteRefs(value any) {
	switch value := value.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/$defs/") {
			value["$ref"] = "#/components/schemas/Finding" + upperFirst(strings.TrimPrefix(ref, "#/$defs/"))
		}
		for _, child := range value {
			rewriteRefs(child)
		}
	case []any:
		for _, child := range value {
			rewriteRefs(child)
		}
	}
}

func upperFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func check(err error) {
	if err != nil {
		panic(fmt.Sprintf("prepare Product Bridge schema: %v", err))
	}
}
