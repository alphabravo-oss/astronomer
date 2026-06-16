// Package cloudcreds is the provider catalog for the cloud-credentials
// feature (migration 053). It defines:
//
//   - ProviderSpec: the static description of a credential type (AWS,
//     GCP, Azure, Generic) — which fields are required vs optional, which
//     are secret, and how each field renders into a k8s Secret's data map.
//   - The built-in provider registry: an immutable map of provider name
//     → spec, used by the handler at validate-on-write time and by the
//     materialization worker at apply time.
//   - Validate + RedactSecrets helpers that the handler calls.
//
// Why a registry rather than a per-provider package: every cloud creds
// flow is the same shape (validate, encrypt, materialize) — only the
// fieldset differs. A registry keeps the worker + handler completely
// provider-agnostic; adding a new provider (Cloudflare, OCI, Linode, ...)
// is one PR adding one entry to BuiltinProviders.
package cloudcreds

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SecretSentinel is the redaction marker the handler returns instead of
// the cleartext for any key listed in SecretKeys on GET. The PUT handler
// treats an incoming value equal to the sentinel as "preserve the stored
// value" — same pattern the SMTP / cluster-registry handlers use for
// their respective passwords. Keep this in sync with the documented UI
// contract (see docs/cloud-credentials.md once that's added).
const SecretSentinel = "<set>"

// ProviderSpec is the static description of a cloud credential provider.
//
// RequiredKeys + OptionalKeys taken together form the legal-key set for
// non-Generic providers; any key on the operator payload that isn't in
// that union is rejected at write time. Generic accepts arbitrary keys
// because its whole purpose is "shove arbitrary {k:v} pairs through the
// same encryption + materialization pipeline as the typed providers".
//
// SecretShape maps blob-key → k8s-Secret-data-key for the
// materialization worker. Most entries pass through unchanged, but
// the GCP provider re-keys "service_account_json" to "key.json"
// because that's the filename Google's SDKs expect.
type ProviderSpec struct {
	// Name is the lookup key (and the value stored in the provider
	// column). Lowercase ASCII, no spaces.
	Name string `json:"name"`
	// DisplayName is the human-readable label for the UI wizard.
	DisplayName string `json:"display_name"`
	// RequiredKeys must all be present on create / full-PUT. Missing
	// any one is a 400.
	RequiredKeys []string `json:"required_keys"`
	// OptionalKeys MAY be present on create / PUT. Unknown keys are
	// rejected for non-Generic providers; for Generic, every key is
	// effectively "optional" and AllowUnknownKeys=true lets the
	// validator pass.
	OptionalKeys []string `json:"optional_keys"`
	// SecretKeys is the subset of (RequiredKeys ∪ OptionalKeys) that
	// gets redacted on GET. e.g. for AWS, access_key_id is technically
	// not as sensitive as secret_access_key but both round-trip
	// redacted because AWS treats the pair as a single credential.
	SecretKeys []string `json:"secret_keys"`
	// SecretShape is the map blob_key → k8s_secret_data_key the
	// materialization worker uses to render the in-cluster Secret.
	// Missing entries default to the blob_key itself.
	SecretShape map[string]string `json:"secret_shape"`
	// AllowUnknownKeys is true for Generic; the validator accepts any
	// key/value pair instead of rejecting non-allowlisted keys.
	AllowUnknownKeys bool `json:"allow_unknown_keys"`
}

// allKeysSet returns the union of RequiredKeys + OptionalKeys as a set.
func (p ProviderSpec) allKeysSet() map[string]struct{} {
	out := make(map[string]struct{}, len(p.RequiredKeys)+len(p.OptionalKeys))
	for _, k := range p.RequiredKeys {
		out[k] = struct{}{}
	}
	for _, k := range p.OptionalKeys {
		out[k] = struct{}{}
	}
	return out
}

// secretKeysSet returns SecretKeys as a set for O(1) redaction lookup.
func (p ProviderSpec) secretKeysSet() map[string]struct{} {
	out := make(map[string]struct{}, len(p.SecretKeys))
	for _, k := range p.SecretKeys {
		out[k] = struct{}{}
	}
	return out
}

// BuiltinProviders is the registry of providers shipped with Astronomer.
// Stable list order (sorted alphabetically by name) — the public
// /api/v1/cloud-credentials/providers/ endpoint returns this verbatim so
// a UI form-builder can render the wizard without any client-side
// knowledge of the provider set.
var BuiltinProviders = map[string]ProviderSpec{
	"aws": {
		Name:         "aws",
		DisplayName:  "Amazon Web Services",
		RequiredKeys: []string{"access_key_id", "secret_access_key"},
		OptionalKeys: []string{"region", "assume_role_arn"},
		SecretKeys:   []string{"access_key_id", "secret_access_key"},
		SecretShape: map[string]string{
			"access_key_id":     "access_key_id",
			"secret_access_key": "secret_access_key",
			"region":            "region",
			"assume_role_arn":   "assume_role_arn",
		},
	},
	"gcp": {
		Name:         "gcp",
		DisplayName:  "Google Cloud Platform",
		RequiredKeys: []string{"service_account_json"},
		OptionalKeys: []string{"project_id"},
		SecretKeys:   []string{"service_account_json"},
		SecretShape: map[string]string{
			// The whole service-account JSON file is rendered under
			// the canonical "key.json" filename that Google's SDKs
			// (GOOGLE_APPLICATION_CREDENTIALS, External Secrets,
			// kaniko, etc.) auto-discover when mounted from a Secret.
			"service_account_json": "key.json",
			"project_id":           "project_id",
		},
	},
	"azure": {
		Name:         "azure",
		DisplayName:  "Microsoft Azure",
		RequiredKeys: []string{"client_id", "client_secret", "tenant_id", "subscription_id"},
		OptionalKeys: []string{},
		SecretKeys:   []string{"client_secret"},
		SecretShape: map[string]string{
			"client_id":       "client_id",
			"client_secret":   "client_secret",
			"tenant_id":       "tenant_id",
			"subscription_id": "subscription_id",
		},
	},
	"generic": {
		Name:             "generic",
		DisplayName:      "Generic (arbitrary key/value)",
		RequiredKeys:     []string{},
		OptionalKeys:     []string{},
		SecretKeys:       []string{}, // see RedactGeneric — all values redact
		SecretShape:      map[string]string{},
		AllowUnknownKeys: true,
	},
}

// LookupProvider returns the registered ProviderSpec for the given name
// (case-insensitive) and an "ok" bool. The handler short-circuits on a
// not-found with a 400 invalid_provider response.
func LookupProvider(name string) (ProviderSpec, bool) {
	p, ok := BuiltinProviders[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// ListProviders returns every registered provider in deterministic order.
// Used by the GET /cloud-credentials/providers/ endpoint.
func ListProviders() []ProviderSpec {
	out := make([]ProviderSpec, 0, len(BuiltinProviders))
	for _, p := range BuiltinProviders {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Validate checks that the operator-supplied blob satisfies the provider
// spec. Returns nil on success or a descriptive error on the first
// failure. Validation rules:
//
//  1. Every required key must be present AND non-empty.
//  2. For non-Generic providers, no unknown keys allowed.
//  3. All values must be strings (the registry only handles string-typed
//     blobs; nested objects / numeric values are out of scope for now
//     because the materialized Secret renders every value as base64
//     string anyway).
//
// The blob is the post-decode map: handler calls json.Unmarshal of the
// request "data" field into a map[string]any then passes it here.
func Validate(provider string, blob map[string]any) error {
	spec, ok := LookupProvider(provider)
	if !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	// (1) Required keys present + non-empty + string-typed.
	for _, key := range spec.RequiredKeys {
		raw, present := blob[key]
		if !present {
			return fmt.Errorf("required key %q missing", key)
		}
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("required key %q must be a string, got %T", key, raw)
		}
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("required key %q must not be empty", key)
		}
	}
	// (2) Unknown-key rejection for typed providers.
	if !spec.AllowUnknownKeys {
		allowed := spec.allKeysSet()
		for key := range blob {
			if _, ok := allowed[key]; !ok {
				return fmt.Errorf("unknown key %q for provider %q", key, spec.Name)
			}
		}
	}
	// (3) Every value must be a string.
	for key, raw := range blob {
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("key %q must be a string, got %T", key, raw)
		}
	}
	return nil
}

// EncodeBlob canonicalises the blob to JSON bytes the handler then
// encrypts. Sorted keys so the ciphertext is stable across operator
// reorderings — useful for the materialization worker's "did this
// credential change?" diff in the future.
func EncodeBlob(blob map[string]any) ([]byte, error) {
	asStrings := make(map[string]string, len(blob))
	for k, v := range blob {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("key %q is not a string", k)
		}
		asStrings[k] = s
	}
	// Use json.Marshal on a stable type rather than reflecting through
	// `any` so JSON-encoder ordering is deterministic.
	keys := make([]string, 0, len(asStrings))
	for k := range asStrings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(keys))
	for _, k := range keys {
		ordered[k] = asStrings[k]
	}
	return json.Marshal(ordered)
}

// DecodeBlob inverts EncodeBlob into a map[string]string.
func DecodeBlob(data []byte) (map[string]string, error) {
	out := map[string]string{}
	if len(data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode credential blob: %w", err)
	}
	return out, nil
}

// RedactSecrets returns a copy of the blob with every key listed in
// SecretKeys replaced by SecretSentinel. For Generic, every value
// redacts (because we can't know which fields the operator considers
// secret — assume they all are). Empty values pass through unchanged
// so the UI can distinguish "no value set" from "set but redacted".
func RedactSecrets(provider string, blob map[string]string) map[string]string {
	out := make(map[string]string, len(blob))
	spec, ok := LookupProvider(provider)
	if !ok {
		// Unknown provider — be conservative: redact everything.
		for k, v := range blob {
			if v == "" {
				out[k] = ""
				continue
			}
			out[k] = SecretSentinel
		}
		return out
	}
	if spec.AllowUnknownKeys {
		// Generic: every populated value is a secret.
		for k, v := range blob {
			if v == "" {
				out[k] = ""
				continue
			}
			out[k] = SecretSentinel
		}
		return out
	}
	secretSet := spec.secretKeysSet()
	for k, v := range blob {
		if _, isSecret := secretSet[k]; !isSecret {
			out[k] = v
			continue
		}
		if v == "" {
			out[k] = ""
			continue
		}
		out[k] = SecretSentinel
	}
	return out
}

// MergePatch produces the new blob after a PUT, honoring the
// SecretSentinel preserve-stored-value rule. patch is the operator's
// incoming map[string]string after Validate; existing is the decrypted
// stored blob. Result is the blob to re-encrypt and persist.
//
// Rules:
//   - A patch value equal to SecretSentinel preserves existing[key]
//     (and a missing key in existing is treated as "" — preserves to
//     empty string, which the validator will then reject on a
//     required field).
//   - A patch value of any other shape overwrites existing[key].
//   - Keys present in existing but absent from patch are PRESERVED
//     when their counterpart in the spec is a secret (typical
//     "operator only sends the field they want to change") but
//     DROPPED when they're non-secret (operator omitted the optional
//     field on purpose). For Generic, every key is treated as a
//     secret so omitted keys are preserved.
func MergePatch(provider string, existing, patch map[string]string) map[string]string {
	spec, ok := LookupProvider(provider)
	if !ok {
		// Unknown — apply the patch verbatim, no preserve magic.
		merged := make(map[string]string, len(existing)+len(patch))
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range patch {
			if v == SecretSentinel {
				continue
			}
			merged[k] = v
		}
		return merged
	}
	secretSet := spec.secretKeysSet()
	merged := make(map[string]string, len(existing)+len(patch))
	// 1) Seed with existing keys that should be preserved on omission.
	for k, v := range existing {
		if spec.AllowUnknownKeys {
			merged[k] = v
			continue
		}
		if _, isSecret := secretSet[k]; isSecret {
			merged[k] = v
			continue
		}
		// Non-secret: presence in `patch` decides — if missing, drop;
		// if present, copy from patch below. We seed defensively so
		// the loop below can overwrite cleanly.
		if _, present := patch[k]; present {
			merged[k] = v
		}
	}
	// 2) Apply the patch on top.
	for k, v := range patch {
		if v == SecretSentinel {
			if existVal, ok := existing[k]; ok {
				merged[k] = existVal
			}
			continue
		}
		merged[k] = v
	}
	return merged
}

// RenderSecretData renders the cleartext blob into the k8s Secret
// data section (map[string]string of cleartext values; the worker
// base64-encodes each entry when constructing the SSA payload). Keys
// are re-mapped per ProviderSpec.SecretShape so e.g. GCP's
// "service_account_json" becomes "key.json".
//
// Unknown providers and Generic fall back to passing the blob through
// unchanged.
func RenderSecretData(provider string, blob map[string]string) map[string]string {
	out := make(map[string]string, len(blob))
	spec, ok := LookupProvider(provider)
	if !ok || spec.AllowUnknownKeys {
		for k, v := range blob {
			out[k] = v
		}
		return out
	}
	for k, v := range blob {
		mapped := spec.SecretShape[k]
		if mapped == "" {
			mapped = k
		}
		out[mapped] = v
	}
	return out
}
