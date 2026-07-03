// Package redaction centralizes best-effort credential redaction for
// diagnostics, support bundles, and other shareable operational exports.
package redaction

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	Marker              = "[redacted]"
	PrivateKeyMarker    = "[redacted private key]"
	KubeconfigMarker    = "[redacted kubeconfig]"
	SensitiveLineMarker = "[redacted sensitive log line]"
)

var stringRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*)[^\s,;]+`),
	regexp.MustCompile(`(?i)\b(cookie\s*[:=]\s*)[^\n;]+`),
	regexp.MustCompile(`(?i)\b(set-cookie\s*[:=]\s*)[^\n;]+`),
	regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)\b(access[_-]?token|refresh[_-]?token|id[_-]?token|token|password|secret|client[_-]?secret)\s*[:=]\s*[^,\s;]+`),
	regexp.MustCompile(`(?i)(://)[^/@\s:]+:[^/@\s]+@`),
}

// Payload returns a JSON-shaped copy of payload with sensitive keys and
// credential-shaped strings redacted. If payload cannot be JSON encoded, the
// original value is returned.
func Payload(payload any) any {
	raw, err := json.Marshal(payload)
	if err != nil {
		return payload
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return payload
	}
	return Value(decoded)
}

// Value recursively redacts a JSON-like value.
func Value(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if IsSensitiveKey(key) {
				out[key] = Marker
				continue
			}
			out[key] = Value(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = Value(item)
		}
		return out
	case string:
		return String(typed)
	default:
		return typed
	}
}

// IsSensitiveKey reports whether a JSON/object key should be fully redacted.
func IsSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
	switch normalized {
	case "authorization", "cookie", "setcookie", "token", "tokenhash", "authtoken", "bearertoken",
		"accesstoken", "refreshtoken", "idtoken", "password", "secret", "clientsecret",
		"authtokenencrypted", "clientsecretencrypted", "upstreamidtokenencrypted",
		"secretkey", "privatekey", "clientkey", "kubeconfig", "cacertificate",
		"certificateauthoritydata":
		return true
	default:
		return false
	}
}

// String redacts credential-shaped substrings while preserving useful context.
func String(value string) string {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "-----begin ") && strings.Contains(lower, " private key-----") {
		return PrivateKeyMarker
	}
	// `lower` is already lowercased, so the needle must be too — the previous
	// mixed-case "apiVersion:" literal never matched, disabling this branch.
	if strings.Contains(lower, "apiversion:") && strings.Contains(lower, "clusters:") && strings.Contains(lower, "users:") {
		return KubeconfigMarker
	}
	out := value
	for _, redactor := range stringRedactors {
		out = redactor.ReplaceAllString(out, "$1"+Marker)
	}
	return out
}

// SensitiveLine redacts entire log lines that are likely to contain
// credentials. Non-sensitive long lines are truncated to keep bundles small.
func SensitiveLine(line string) string {
	lower := strings.ToLower(line)
	for _, marker := range []string{"token", "secret", "authorization", "bearer ", "password"} {
		if strings.Contains(lower, marker) {
			return SensitiveLineMarker
		}
	}
	if len(line) > 500 {
		return line[:500] + "...[truncated]"
	}
	return line
}

// ByteCount redacts a string while retaining its byte length for diagnostics.
func ByteCount(value string) string {
	if value == "" {
		return ""
	}
	return fmt.Sprintf("[redacted %d bytes]", len(value))
}
