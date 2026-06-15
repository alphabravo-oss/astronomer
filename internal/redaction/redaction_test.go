package redaction

import (
	"strings"
	"testing"
)

func TestPayloadRedactsSensitiveKeysAndCredentialStrings(t *testing.T) {
	payload := map[string]any{
		"kubeconfig":               "apiVersion: v1\nclusters: []\nusers: []\n",
		"message":                  "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
		"url":                      "postgres://user:password@example.com/db",
		"auth_token_encrypted":     "encrypted-token-material",
		"upstreamIdTokenEncrypted": "encrypted-id-token",
		"nested": []any{
			map[string]any{"authorization": "Bearer abc123"},
			map[string]any{"note": "client_secret=hidden"},
		},
	}

	redacted, ok := Payload(payload).(map[string]any)
	if !ok {
		t.Fatalf("redacted payload type = %T", redacted)
	}
	if redacted["kubeconfig"] != Marker {
		t.Fatalf("kubeconfig = %v", redacted["kubeconfig"])
	}
	if redacted["message"] != PrivateKeyMarker {
		t.Fatalf("message = %v", redacted["message"])
	}
	if redacted["auth_token_encrypted"] != Marker || redacted["upstreamIdTokenEncrypted"] != Marker {
		t.Fatalf("encrypted token keys not redacted: %#v", redacted)
	}
	if strings.Contains(redacted["url"].(string), "password") {
		t.Fatalf("url not redacted: %v", redacted["url"])
	}
	nested := redacted["nested"].([]any)
	first := nested[0].(map[string]any)
	second := nested[1].(map[string]any)
	if first["authorization"] != Marker {
		t.Fatalf("authorization = %v", first["authorization"])
	}
	if strings.Contains(second["note"].(string), "hidden") {
		t.Fatalf("credential-shaped string not redacted: %v", second["note"])
	}
}

func TestSensitiveLineRedactsLikelyCredentialLines(t *testing.T) {
	if got := SensitiveLine("Authorization: Bearer abc123"); got != SensitiveLineMarker {
		t.Fatalf("authorization line = %q", got)
	}
	if got := SensitiveLine("normal log line"); got != "normal log line" {
		t.Fatalf("normal line = %q", got)
	}
	longLine := strings.Repeat("x", 600)
	if got := SensitiveLine(longLine); len(got) > 520 || !strings.Contains(got, "...[truncated]") {
		t.Fatalf("long line was not truncated: len=%d %q", len(got), got)
	}
}

func TestByteCount(t *testing.T) {
	if got := ByteCount(""); got != "" {
		t.Fatalf("empty byte count = %q", got)
	}
	if got := ByteCount("secret"); got != "[redacted 6 bytes]" {
		t.Fatalf("byte count = %q", got)
	}
}
