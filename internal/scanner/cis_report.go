package scanner

import "encoding/json"

// DecodeCISReportJSON pulls the actual CIS report payload out of
// spec.reportJSON (string-encoded) or spec.report (already an object),
// tolerating both shapes emitted by cis-operator versions.
func DecodeCISReportJSON(spec map[string]any) map[string]any {
	if spec == nil {
		return map[string]any{}
	}
	if raw, ok := spec["reportJSON"].(string); ok && raw != "" {
		var out map[string]any
		if err := json.Unmarshal([]byte(raw), &out); err == nil {
			return out
		}
	}
	if obj, ok := spec["report"].(map[string]any); ok {
		return obj
	}
	if obj, ok := spec["reportJSON"].(map[string]any); ok {
		return obj
	}
	return map[string]any{}
}

// StringField returns the first non-empty string value for the supplied keys.
func StringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// NumericField returns a numeric report value as int32 when present.
func NumericField(m map[string]any, key string) (int32, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int32(n), true
	case int:
		return int32(n), true
	case int64:
		return int32(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int32(i), true
	}
	return 0, false
}
