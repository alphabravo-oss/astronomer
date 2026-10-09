// Package jsoncontract owns versioned product JSON schemas shared by HTTP,
// persistence, and worker readers. Opaque provider documents are not these DTOs.
package jsoncontract

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// MaxDocumentBytes matches the existing durable JSON registry ceiling.
const MaxDocumentBytes = 16 * 1024 * 1024

// Error contains only a fixed schema field and category, never stored values.
type Error struct{ Field, Category string }

func (e *Error) Error() string { return "invalid " + e.Field + ": " + e.Category }

type InhibitionMatcher struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	IsRegex bool   `json:"is_regex"`
	pattern *regexp.Regexp
}

// Matches uses the regex prepared by the reader once, without recompiling per
// target alert. Missing labels and unprepared regex values fail closed.
func (m InhibitionMatcher) Matches(labels map[string]string) bool {
	value, exists := labels[m.Label]
	if !exists {
		return false
	}
	if m.IsRegex {
		return m.pattern != nil && m.pattern.MatchString(value)
	}
	return value == m.Value
}

type InhibitionV1 struct {
	Source []InhibitionMatcher
	Target []InhibitionMatcher
	Equal  []string
}

// ReadInhibitionV1 is the explicit additive v1 reader. Unknown object keys are
// ignored as before, absent is_regex means false, and empty arrays remain valid.
// JSON null is not an array and was never a valid non-null durable column value.
// New-request minimum matcher counts are deliberately separate from this reader.
func ReadInhibitionV1(source, target, equal json.RawMessage) (InhibitionV1, error) {
	var result InhibitionV1
	var err error
	if result.Source, err = readMatchersV1("source_matchers", source); err != nil {
		return result, err
	}
	if result.Target, err = readMatchersV1("target_matchers", target); err != nil {
		return result, err
	}
	items, err := readArray("equal_labels", equal)
	if err != nil {
		return result, err
	}
	result.Equal = make([]string, 0, len(items))
	for _, item := range items {
		var label string
		if !readString(item, &label) || strings.TrimSpace(label) == "" {
			return result, &Error{"equal_labels", "invalid_item"}
		}
		result.Equal = append(result.Equal, label)
	}
	return result, nil
}

func readArray(field string, raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) > MaxDocumentBytes {
		return nil, &Error{field, "too_large"}
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, &Error{field, "invalid_shape"}
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, &Error{field, "invalid_json"}
	}
	return items, nil
}

func readMatchersV1(field string, raw json.RawMessage) ([]InhibitionMatcher, error) {
	items, err := readArray(field, raw)
	if err != nil {
		return nil, err
	}
	result := make([]InhibitionMatcher, 0, len(items))
	for _, item := range items {
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(item, &properties); err != nil || properties == nil {
			return nil, &Error{field, "invalid_item"}
		}
		var matcher InhibitionMatcher
		if !readString(properties["label"], &matcher.Label) || strings.TrimSpace(matcher.Label) == "" || !readString(properties["value"], &matcher.Value) {
			return nil, &Error{field, "invalid_item"}
		}
		if rawRegex, exists := properties["is_regex"]; exists {
			rawRegex = bytes.TrimSpace(rawRegex)
			if !bytes.Equal(rawRegex, []byte("true")) && !bytes.Equal(rawRegex, []byte("false")) {
				return nil, &Error{field, "invalid_item"}
			}
			matcher.IsRegex = bytes.Equal(rawRegex, []byte("true"))
		}
		if matcher.IsRegex {
			matcher.pattern, err = regexp.Compile("^(?:" + matcher.Value + ")$")
			if err != nil {
				return nil, &Error{field, "invalid_regex"}
			}
		}
		result = append(result, matcher)
	}
	return result, nil
}

func readString(raw json.RawMessage, value *string) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, value) == nil
}
