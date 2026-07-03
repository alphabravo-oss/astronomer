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

// TestStringRedactsKubeconfigUnderNonSensitiveKey guards the fix for the
// case-folding bug that disabled the kubeconfig string detector: a full
// kubeconfig YAML sitting in a value (not under a "kubeconfig"-named key) must
// still be redacted, since it carries certificate-authority-data / client-key
// material.
func TestStringRedactsKubeconfigUnderNonSensitiveKey(t *testing.T) {
	kubeconfig := "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: SECRETCA\nusers:\n- user:\n    client-key-data: SECRETKEY\n"
	got := String(kubeconfig)
	if got != KubeconfigMarker {
		t.Fatalf("kubeconfig string should be redacted to %q, got %q", KubeconfigMarker, got)
	}
	// And via Payload under a non-sensitive key.
	out := Payload(map[string]any{"note": kubeconfig}).(map[string]any)
	if s, _ := out["note"].(string); strings.Contains(s, "SECRETCA") || strings.Contains(s, "SECRETKEY") {
		t.Fatalf("kubeconfig leaked under non-sensitive key: %v", out["note"])
	}
}
