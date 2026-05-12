package cloudcreds

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLookupProvider_Known exercises the four built-in providers — the
// "is this provider known?" check is the gate every handler write
// shares so a regression here breaks every PUT/POST.
func TestLookupProvider_Known(t *testing.T) {
	for _, name := range []string{"aws", "gcp", "azure", "generic"} {
		t.Run(name, func(t *testing.T) {
			p, ok := LookupProvider(name)
			if !ok {
				t.Fatalf("expected provider %q to be registered", name)
			}
			if p.Name != name {
				t.Fatalf("expected Name=%q, got %q", name, p.Name)
			}
		})
	}
}

// TestLookupProvider_Unknown returns ok=false so the handler can map to
// 400 invalid_provider.
func TestLookupProvider_Unknown(t *testing.T) {
	if _, ok := LookupProvider("digital_ocean"); ok {
		t.Fatalf("expected unknown provider lookup to fail")
	}
}

// TestLookupProvider_CaseInsensitive lets the UI accept "AWS" / "aws" /
// "Aws" without an extra normalize step on the wire.
func TestLookupProvider_CaseInsensitive(t *testing.T) {
	if _, ok := LookupProvider("AWS"); !ok {
		t.Fatalf("expected case-insensitive AWS lookup to succeed")
	}
}

func TestListProviders_StableOrder(t *testing.T) {
	got := ListProviders()
	if len(got) != 4 {
		t.Fatalf("expected 4 providers, got %d", len(got))
	}
	want := []string{"aws", "azure", "gcp", "generic"}
	for i, p := range got {
		if p.Name != want[i] {
			t.Fatalf("provider[%d]: expected %q, got %q", i, want[i], p.Name)
		}
	}
}

// TestValidate_AWS_OK feeds a happy-path AWS blob: required keys present
// plus an optional region.
func TestValidate_AWS_OK(t *testing.T) {
	blob := map[string]any{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
		"region":            "us-east-1",
	}
	if err := Validate("aws", blob); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidate_AWS_MissingRequired bites when the operator forgets the
// secret_access_key.
func TestValidate_AWS_MissingRequired(t *testing.T) {
	blob := map[string]any{
		"access_key_id": "AKIAFAKE",
	}
	err := Validate("aws", blob)
	if err == nil {
		t.Fatalf("expected error on missing required key")
	}
	if !strings.Contains(err.Error(), "secret_access_key") {
		t.Fatalf("expected error to name the missing key, got %v", err)
	}
}

// TestValidate_AWS_EmptyRequired distinguishes "key present but empty
// string" from "key absent" — both are rejected so the encrypted blob
// never persists empty creds.
func TestValidate_AWS_EmptyRequired(t *testing.T) {
	blob := map[string]any{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "   ",
	}
	if err := Validate("aws", blob); err == nil {
		t.Fatalf("expected error on whitespace-only required key")
	}
}

// TestValidate_AWS_UnknownKey rejects extras for typed providers.
func TestValidate_AWS_UnknownKey(t *testing.T) {
	blob := map[string]any{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
		"surprise":          "value",
	}
	err := Validate("aws", blob)
	if err == nil {
		t.Fatalf("expected error on unknown key")
	}
	if !strings.Contains(err.Error(), "surprise") {
		t.Fatalf("expected error to name the unknown key, got %v", err)
	}
}

// TestValidate_GCP_OK on the single required key.
func TestValidate_GCP_OK(t *testing.T) {
	blob := map[string]any{
		"service_account_json": `{"type": "service_account"}`,
	}
	if err := Validate("gcp", blob); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidate_Azure_OK on the full four-key tuple.
func TestValidate_Azure_OK(t *testing.T) {
	blob := map[string]any{
		"client_id":       "client",
		"client_secret":   "secret",
		"tenant_id":       "tenant",
		"subscription_id": "sub",
	}
	if err := Validate("azure", blob); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidate_Generic_AcceptsArbitrary should not reject any key/value
// shape — that's the whole point of the Generic provider.
func TestValidate_Generic_AcceptsArbitrary(t *testing.T) {
	blob := map[string]any{
		"cloudflare_api_token": "x",
		"random_thing":         "y",
		"another":              "z",
	}
	if err := Validate("generic", blob); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidate_UnknownProvider always fails.
func TestValidate_UnknownProvider(t *testing.T) {
	if err := Validate("digital_ocean", map[string]any{}); err == nil {
		t.Fatalf("expected error on unknown provider")
	}
}

// TestValidate_NonStringValue rejects nested objects / numbers — the
// blob is string-only.
func TestValidate_NonStringValue(t *testing.T) {
	blob := map[string]any{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": 12345, // not a string
	}
	if err := Validate("aws", blob); err == nil {
		t.Fatalf("expected error on non-string required value")
	}
}

// TestEncodeBlob_DecodesBack round-trips through json.
func TestEncodeBlob_DecodesBack(t *testing.T) {
	in := map[string]any{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
	}
	enc, err := EncodeBlob(in)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeBlob(enc)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out["access_key_id"] != "AKIAFAKE" || out["secret_access_key"] != "shhh" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

// TestEncodeBlob_StableOrdering is the property the future "did this
// credential change?" diff relies on.
func TestEncodeBlob_StableOrdering(t *testing.T) {
	a, _ := EncodeBlob(map[string]any{"b": "2", "a": "1", "c": "3"})
	b, _ := EncodeBlob(map[string]any{"a": "1", "c": "3", "b": "2"})
	if string(a) != string(b) {
		t.Fatalf("ordering is not stable: %s vs %s", a, b)
	}
}

// TestRedactSecrets_AWS redacts the secret + the access key (the spec
// flags both because AWS treats the pair as one credential) and leaves
// non-secret fields alone.
func TestRedactSecrets_AWS(t *testing.T) {
	in := map[string]string{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
		"region":            "us-east-1",
	}
	got := RedactSecrets("aws", in)
	if got["access_key_id"] != SecretSentinel || got["secret_access_key"] != SecretSentinel {
		t.Fatalf("expected secret keys to redact, got %+v", got)
	}
	if got["region"] != "us-east-1" {
		t.Fatalf("expected region to pass through, got %+v", got)
	}
}

// TestRedactSecrets_Generic redacts everything.
func TestRedactSecrets_Generic(t *testing.T) {
	in := map[string]string{
		"api_token": "real",
		"some_id":   "id",
	}
	got := RedactSecrets("generic", in)
	for k, v := range got {
		if v != SecretSentinel {
			t.Fatalf("expected key %q to redact, got %q", k, v)
		}
	}
}

// TestRedactSecrets_EmptyValuesPassThrough lets the UI distinguish
// "field set but redacted" from "field never set" — a redacted "<set>"
// could otherwise mislead the operator into thinking they configured a
// blank value.
func TestRedactSecrets_EmptyValuesPassThrough(t *testing.T) {
	in := map[string]string{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "",
	}
	got := RedactSecrets("aws", in)
	if got["secret_access_key"] != "" {
		t.Fatalf("expected empty value to pass through, got %q", got["secret_access_key"])
	}
}

// TestMergePatch_SentinelPreservesStored is the PUT-loop UX guarantee:
// "GET → edit description → PUT" must not blank the credential.
func TestMergePatch_SentinelPreservesStored(t *testing.T) {
	existing := map[string]string{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
	}
	patch := map[string]string{
		"access_key_id":     SecretSentinel,
		"secret_access_key": SecretSentinel,
	}
	merged := MergePatch("aws", existing, patch)
	if merged["secret_access_key"] != "shhh" {
		t.Fatalf("expected stored value preserved, got %q", merged["secret_access_key"])
	}
}

// TestMergePatch_NewValueOverwrites is the same loop with a real
// rotation: operator sends a new value, sentinel rule doesn't apply.
func TestMergePatch_NewValueOverwrites(t *testing.T) {
	existing := map[string]string{"secret_access_key": "old"}
	patch := map[string]string{"secret_access_key": "new"}
	merged := MergePatch("aws", existing, patch)
	if merged["secret_access_key"] != "new" {
		t.Fatalf("expected overwrite, got %q", merged["secret_access_key"])
	}
}

// TestRenderSecretData_GCPRemapsServiceAccount checks the GCP-specific
// "service_account_json" → "key.json" filename rule.
func TestRenderSecretData_GCPRemapsServiceAccount(t *testing.T) {
	in := map[string]string{
		"service_account_json": `{"type": "service_account"}`,
		"project_id":           "my-project",
	}
	got := RenderSecretData("gcp", in)
	if _, ok := got["key.json"]; !ok {
		t.Fatalf("expected key.json output, got keys %v", mapKeys(got))
	}
	if got["project_id"] != "my-project" {
		t.Fatalf("expected project_id to pass through, got %+v", got)
	}
	if _, ok := got["service_account_json"]; ok {
		t.Fatalf("unexpected unmapped service_account_json key in output")
	}
}

// TestRenderSecretData_GenericPassThrough preserves the operator's
// chosen keys verbatim — the Generic provider doesn't transform.
func TestRenderSecretData_GenericPassThrough(t *testing.T) {
	in := map[string]string{
		"cloudflare_api_token": "x",
		"random_thing":         "y",
	}
	got := RenderSecretData("generic", in)
	if got["cloudflare_api_token"] != "x" || got["random_thing"] != "y" {
		t.Fatalf("expected pass-through, got %+v", got)
	}
}

// TestRenderSecretData_AWS preserves every key as-is.
func TestRenderSecretData_AWS(t *testing.T) {
	in := map[string]string{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
		"region":            "us-east-1",
	}
	got := RenderSecretData("aws", in)
	for k, v := range in {
		if got[k] != v {
			t.Fatalf("expected %q to pass through, got %q", k, got[k])
		}
	}
}

// TestListProviders_PublicShape checks the wire shape the
// /providers/ endpoint will emit — JSON keys snake_case, no surprises.
func TestListProviders_PublicShape(t *testing.T) {
	got := ListProviders()
	raw, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	for _, want := range []string{"\"name\"", "\"display_name\"", "\"required_keys\"", "\"optional_keys\"", "\"secret_keys\"", "\"secret_shape\""} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("expected JSON to contain %q, got %s", want, raw)
		}
	}
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
