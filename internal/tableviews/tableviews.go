// Package tableviews owns the validation rules for per-user saved DataTable
// views. Persistence and transport layers use this package instead of keeping
// their own copies of the limits.
package tableviews

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxViewsPerTable caps saved views per user and table key.
	MaxViewsPerTable = 20
	MaxNameRunes     = 64
	MaxTableKeyBytes = 128
	// MaxStateBytes bounds one saved state document.
	MaxStateBytes = 32 * 1024
)

var tableKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9:._/-]*$`)

// allowedStateKeys is the closed set of top-level keys a saved view may carry.
var allowedStateKeys = map[string]struct{}{
	"v": {}, "search": {}, "filters": {}, "sort": {},
	"hidden": {}, "order": {}, "pinning": {},
}

// ValidateTableKey checks a table identifier such as "pods" or "cluster:nodes".
func ValidateTableKey(key string) error {
	if key == "" || len(key) > MaxTableKeyBytes || !tableKeyPattern.MatchString(key) {
		return errors.New("table_key must be 1-128 characters of a-z, 0-9, : . _ / -")
	}
	return nil
}

// NormalizeName trims and validates a view name.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return "", fmt.Errorf("name must be 1-%d characters", MaxNameRunes)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("name must not contain control characters")
		}
	}
	return name, nil
}

// NormalizeState validates a saved state document and returns it re-encoded.
func NormalizeState(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("state is required")
	}
	if len(raw) > MaxStateBytes {
		return nil, fmt.Errorf("state must be at most %d bytes", MaxStateBytes)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("state must be a JSON object")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil, errors.New("state must be a JSON object")
	}
	for key := range doc {
		if _, ok := allowedStateKeys[key]; !ok {
			return nil, fmt.Errorf("state contains unsupported key %q", key)
		}
	}
	return json.RawMessage(trimmed), nil
}
