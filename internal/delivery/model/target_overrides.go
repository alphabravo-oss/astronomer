package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	MaxTargetOverridesBytes = 64 << 10
	MaxTargetPatches        = 16
	MaxTargetPatchBytes     = 16 << 10
	MaxTargetValuesDepth    = 12
)

// TargetOverrides is frozen with each rollout and deployment assignment. It
// customizes an immutable bundle version without ever rewriting that version.
type TargetOverrides struct {
	HelmValues json.RawMessage `json:"helm_values,omitempty"`
	Patches    []string        `json:"patches,omitempty"`
}

func (o *TargetOverrides) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("overrides must be an object")
	}
	type plain TargetOverrides
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode overrides: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("overrides contain trailing JSON")
	}
	*o = TargetOverrides(value)
	return o.Validate()
}

func (o TargetOverrides) Validate() error {
	encoded, err := json.Marshal(o)
	if err != nil || len(encoded) > MaxTargetOverridesBytes {
		return fmt.Errorf("overrides must be at most %d bytes", MaxTargetOverridesBytes)
	}
	if len(o.Patches) > MaxTargetPatches {
		return fmt.Errorf("overrides.patches supports at most %d entries", MaxTargetPatches)
	}
	for i, patch := range o.Patches {
		if strings.TrimSpace(patch) == "" || len(patch) > MaxTargetPatchBytes || strings.ContainsRune(patch, '\x00') {
			return fmt.Errorf("overrides.patches[%d] is empty or exceeds %d bytes", i, MaxTargetPatchBytes)
		}
		var document any
		if err := yaml.Unmarshal([]byte(patch), &document); err != nil {
			return fmt.Errorf("overrides.patches[%d] is invalid YAML: %w", i, err)
		}
		object, ok := document.(map[string]any)
		if !ok || len(object) == 0 {
			return fmt.Errorf("overrides.patches[%d] must be a Kubernetes object or patch", i)
		}
	}
	if len(o.HelmValues) > 0 {
		var values any
		decoder := json.NewDecoder(bytes.NewReader(o.HelmValues))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return fmt.Errorf("overrides.helm_values must be JSON: %w", err)
		}
		if _, ok := values.(map[string]any); !ok {
			return errors.New("overrides.helm_values must be an object")
		}
		if err := validateTargetValue(values, 0); err != nil {
			return fmt.Errorf("overrides.helm_values: %w", err)
		}
		canonical, _ := json.Marshal(values)
		o.HelmValues = canonical
	}
	return nil
}

func validateTargetValue(value any, depth int) error {
	if depth > MaxTargetValuesDepth {
		return fmt.Errorf("nesting exceeds %d levels", MaxTargetValuesDepth)
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 256 {
			return errors.New("an object exceeds 256 keys")
		}
		for key, child := range typed {
			if key == "" || len(key) > 253 || strings.ContainsAny(key, "\r\n\x00") {
				return errors.New("contains an invalid key")
			}
			if err := validateTargetValue(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(typed) > 256 {
			return errors.New("an array exceeds 256 items")
		}
		for _, child := range typed {
			if err := validateTargetValue(child, depth+1); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > 8192 || strings.ContainsRune(typed, '\x00') {
			return errors.New("contains an oversized or invalid string")
		}
	case json.Number, bool, nil:
	default:
		return fmt.Errorf("contains unsupported value type %T", value)
	}
	return nil
}

func (o TargetOverrides) Canonical() (TargetOverrides, error) {
	if err := o.Validate(); err != nil {
		return TargetOverrides{}, err
	}
	canonical := TargetOverrides{Patches: append([]string(nil), o.Patches...)}
	if len(o.HelmValues) > 0 {
		var values any
		decoder := json.NewDecoder(bytes.NewReader(o.HelmValues))
		decoder.UseNumber()
		_ = decoder.Decode(&values)
		canonical.HelmValues, _ = json.Marshal(values)
	}
	return canonical, nil
}

func (o TargetOverrides) CanonicalDigest() (Digest, error) {
	canonical, err := o.Canonical()
	if err != nil {
		return "", err
	}
	return CanonicalDigest(canonical)
}

func MergeHelmValues(base, override json.RawMessage) (json.RawMessage, error) {
	var left, right map[string]any
	if len(base) > 0 {
		if err := json.Unmarshal(base, &left); err != nil {
			return nil, err
		}
	}
	if left == nil {
		left = map[string]any{}
	}
	if len(override) > 0 {
		if err := json.Unmarshal(override, &right); err != nil {
			return nil, err
		}
	}
	mergeTargetMaps(left, right)
	return json.Marshal(left)
}

func mergeTargetMaps(destination, override map[string]any) {
	for key, value := range override {
		if nested, ok := value.(map[string]any); ok {
			current, _ := destination[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
			}
			mergeTargetMaps(current, nested)
			destination[key] = current
			continue
		}
		destination[key] = value
	}
}
