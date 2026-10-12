package jsoncontract

import (
	"bytes"
	"encoding/json"
)

// ReadObjectV1 preserves additive domain properties while rejecting a corrupt
// object envelope. Values are opaque; this is not a replacement for a domain's
// nested typed validator.
func ReadObjectV1(field string, raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) > MaxDocumentBytes {
		return nil, &Error{field, "too_large"}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &Error{field, "invalid_shape"}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, &Error{field, "invalid_json"}
	}
	return object, nil
}

// ReadStringMapV1 owns labels/annotations' stable map<string,string> shape.
// Key syntax and authorization belong to the corresponding domain. Empty maps
// are valid, and nested provider payloads must not be passed to this reader.
func ReadStringMapV1(field string, raw []byte) (map[string]string, error) {
	object, err := ReadObjectV1(field, raw)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(object))
	for key, rawValue := range object {
		var value string
		if !readString(rawValue, &value) {
			return nil, &Error{field, "invalid_item"}
		}
		result[key] = value
	}
	return result, nil
}

// ReadAuditDetailV1 retains opaque sanitized domain values, including numbers
// outside float64's range. Only the stable object envelope is constrained;
// before/after snapshots and a legacy domain-owned tags key remain additive.
func ReadAuditDetailV1(raw []byte) (map[string]any, error) {
	if _, err := ReadObjectV1("audit_detail", raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var detail map[string]any
	if err := decoder.Decode(&detail); err != nil {
		return nil, &Error{"audit_detail", "invalid_json"}
	}
	return detail, nil
}
