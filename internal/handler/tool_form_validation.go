package handler

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// validateToolFormValues enforces the curated field types even when the caller
// uses the YAML editor or API directly. Uncurated chart values remain available.
func validateToolFormValues(slug, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values map[string]any
	if err := yaml.UnmarshalStrict([]byte(raw), &values); err != nil {
		return fmt.Errorf("invalid tool values: %w", err)
	}
	schema := toolFormSchemaFor(slug)
	if schema == nil {
		return nil
	}
	for _, field := range schema.Fields {
		var value any = values
		present := true
		for _, segment := range strings.Split(field.Path, ".") {
			object, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must be nested under an object", field.Path)
			}
			value, present = object[segment]
			if !present || value == nil {
				break
			}
		}
		if !present || value == nil {
			continue
		}
		valid := false
		switch field.Type {
		case toolFieldBoolean:
			_, valid = value.(bool)
		case toolFieldNumber:
			n, ok := value.(float64)
			valid = ok
			if valid && field.Minimum != nil {
				valid = n >= *field.Minimum
			}
			if valid && field.Maximum != nil {
				valid = n <= *field.Maximum
			}
			if valid && field.Step != nil && *field.Step > 0 {
				origin := 0.0
				if field.Minimum != nil {
					origin = *field.Minimum
				}
				steps := (n - origin) / *field.Step
				valid = math.Abs(steps-math.Round(steps)) < 1e-9
			}
		case toolFieldSelect:
			s, ok := value.(string)
			valid = ok && slices.Contains(field.Options, s)
		case toolFieldString, toolFieldMultiline, toolFieldStorage:
			_, valid = value.(string)
		}
		if !valid {
			return fmt.Errorf("invalid value for %s (%s)", field.Path, field.Type)
		}
	}
	return nil
}
