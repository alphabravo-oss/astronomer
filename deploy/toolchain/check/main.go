// Command check validates the version authority with the repository's pinned
// JSON Schema implementation. It never fetches metadata or rewrites consumers.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"sigs.k8s.io/yaml"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--yaml-json" {
		data, err := os.ReadFile("/dev/stdin")
		if err == nil {
			data, err = yaml.YAMLToJSON(data)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var document any
		if err := json.Unmarshal(data, &document); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(document); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: check schema.json inventory.json")
		os.Exit(2)
	}
	if err := validate(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validate(schemaPath, inventoryPath string) error {
	compiler := jsonschema.NewCompiler()
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		return err
	}
	if err := compiler.AddResource("urn:astronomer:toolchain", schemaDocument); err != nil {
		return err
	}
	schema, err := compiler.Compile("urn:astronomer:toolchain")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(inventoryPath)
	if err != nil {
		return err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(document)
}
