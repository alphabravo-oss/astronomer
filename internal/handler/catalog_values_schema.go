package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type catalogSchemaCacheEntry struct {
	schema *jsonschema.Schema
	err    error
}

var catalogSchemaCache sync.Map

func validateCatalogValuesSchema(raw json.RawMessage, values map[string]any) error {
	if len(raw) == 0 || string(raw) == "{}" {
		return nil
	}
	digest := sha256.Sum256(raw)
	key := hex.EncodeToString(digest[:])
	cached, ok := catalogSchemaCache.Load(key)
	if !ok {
		compiler := jsonschema.NewCompiler()
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err == nil {
			err = compiler.AddResource("chart-values.schema.json", document)
		}
		var compiled *jsonschema.Schema
		if err == nil {
			compiled, err = compiler.Compile("chart-values.schema.json")
		}
		cached, _ = catalogSchemaCache.LoadOrStore(key, catalogSchemaCacheEntry{schema: compiled, err: err})
	}
	entry := cached.(catalogSchemaCacheEntry)
	if entry.err != nil || entry.schema == nil {
		return errors.New("the selected chart's values schema could not be evaluated")
	}
	if err := entry.schema.Validate(values); err != nil {
		var validation *jsonschema.ValidationError
		if errors.As(err, &validation) {
			if location := catalogValidationLocation(validation); len(location) > 0 {
				return fmt.Errorf("value at %s does not satisfy the selected chart schema", strings.Join(location, "."))
			}
		}
		return errors.New("values do not satisfy the selected chart schema")
	}
	return nil
}

func catalogValidationLocation(validation *jsonschema.ValidationError) []string {
	if validation == nil {
		return nil
	}
	for _, cause := range validation.Causes {
		if location := catalogValidationLocation(cause); len(location) > 0 {
			return location
		}
	}
	return validation.InstanceLocation
}
