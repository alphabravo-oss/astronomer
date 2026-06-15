package events

import "encoding/json"

// RawJSON re-marshals an arbitrary event payload into json.RawMessage.
// Event consumers persist or forward the JSON shape, not the original Go type.
func RawJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// DecodeFilterGlobs unmarshals a JSONB array of event filter globs.
// Empty JSONB means no explicit filters.
func DecodeFilterGlobs(raw []byte) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}
