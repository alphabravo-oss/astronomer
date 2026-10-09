package jsoncontract

import (
	"bytes"
	"encoding/json"
)

// ReadTaskPayloadV1 validates the additive object envelope used by durable task
// payloads. Task-specific fields remain owned by their typed task handlers.
// Historically payload-less tasks used empty bytes; this explicit v1 adapter
// supplies an empty object without rewriting an existing stored row. JSON null
// and whitespace-only documents are corruption, not that legacy representation.
func ReadTaskPayloadV1(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	if len(raw) > MaxDocumentBytes {
		return nil, &Error{"task_payload", "too_large"}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &Error{"task_payload", "invalid_shape"}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, &Error{"task_payload", "invalid_json"}
	}
	// These reserved metadata fields are the stable observability envelope.
	// Unknown domain properties are retained under the existing v1 contract.
	for _, key := range []string{"_correlation_id", "_traceparent", "_tracestate"} {
		if field, exists := object[key]; exists {
			var value string
			if !readString(field, &value) {
				return nil, &Error{"task_payload", "invalid_metadata"}
			}
		}
	}
	return raw, nil
}
