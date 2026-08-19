// Monitoring-backend credential envelope (migration 146).
//
// `monitoring_backends.auth_config` was bare JSONB from 002 until 146: the
// bearer token or basic-auth password this platform uses to authenticate to
// Thanos / Prometheus / Alertmanager sat in the clear in the platform database
// and in every backup of it. It is the same finding migration 145 fixed for
// helm_repositories, deliberately deferred because this column is harder.
//
// What makes it harder is that it is not only a credential. It doubles as the
// monitoring subsystem's operator-config bag, and four separate paths do a
// read-modify-write on it to update NON-secret keys:
//
//   - internal/handler/monitoring_stack_shared.go   updateSharedThanosMetadata
//   - internal/handler/monitoring_stack_shared.go   updateSharedAlertmanagerMetadata
//   - internal/handler/monitoring_stack_grafana.go  updateSharedGrafanaMetadata
//   - internal/handler/alerting.go     persistSharedAlertingAssetHashes
//   - internal/worker/tasks/monitoring_reconcile.go  reconcileMonitoringBackend
//
// plus the operator-facing writer (UpdateBackendConfig). Every one of them has
// to become resolve → mutate → re-seal. One of them left as
// decode-JSONB → mutate → marshal would write the stripped projection back as
// if it were the whole document, and the credential would vanish during an
// unrelated edit — an operator changes a retry policy and monitoring stops
// authenticating, with nothing pointing at the cause.
//
// Two rules follow, and they are why the seal helper never takes a bare
// document from a caller that has not resolved first:
//
//  1. a resolve failure must ABORT the write, never fall through to a write of
//     what was readable;
//  2. the split between the columns is decided HERE, not at the call site.
package monitoring

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/alphabravocompany/astronomer-go/internal/credential"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Decryptor unwraps the Fernet envelope; Encryptor seals it. *auth.Encryptor
// satisfies both. They are interfaces so this package keeps depending on
// nothing but the standard library and the leaf envelope package.
type (
	Decryptor = credential.Decryptor
	Encryptor = credential.Encryptor
)

// ErrAuthConfigUnavailable reports that a backend's stored credential could
// not be recovered. A reader must answer by sending NO Authorization header; a
// writer must answer by not writing at all.
var ErrAuthConfigUnavailable = credential.ErrUnavailable

// NonSecretAuthConfigKeys is the ALLOW-LIST of auth_config keys that stay in
// the clear in the JSONB column. Everything else in the document is treated as
// a credential and lives only inside the Fernet envelope.
//
// The direction is deliberately the opposite of catalog.AuthConfigSecretKeys,
// which strips a deny-list. That fits helm_repositories, whose document IS a
// credential with a couple of known non-secret extras. This document is the
// inverse: a fixed set of structural config keys written by this codebase,
// wrapped around an unbounded, operator-authored credential portion — the
// request DTO takes `authConfig json.RawMessage` and stores whatever arrives,
// so a deny-list would leak any key an operator names something we did not
// think of (`apiKey`, `x-scope-token`, `headers`). The API-response redaction
// in internal/handler/monitoring.go already made exactly this call for the
// same reason; using one list for both keeps them from drifting, because a key
// the API hides but the JSONB keeps is a key that leaks through pg_dump.
//
// Every entry here is written and read by this codebase, never by an operator:
//
//	operationPolicies    retry / auto-rollback policy for monitoring operations
//	sharedThanos         shared Thanos stack deployment metadata + status
//	sharedAlertmanager   shared Alertmanager deployment metadata + status
//	sharedGrafana        shared Grafana stack deployment metadata + status
//	sharedAlertingAssets rendered rule/routing/silence ConfigMap hashes
//	status               backend health, read by the monitoring summary
//
// Nothing SQL-queries into this column (the only three statements over it are
// `SELECT *`, `SELECT mb.auth_config` and `auth_config = EXCLUDED.auth_config`
// — no `->>`, no `@>`, no expression index), so moving a key out of the
// projection cannot break a query. It can only break a Go reader, and the
// readers of these five keys are enumerated in migration 146.
var NonSecretAuthConfigKeys = []string{
	"operationPolicies",
	"sharedThanos",
	"sharedAlertmanager",
	"sharedGrafana",
	"sharedAlertingAssets",
	"status",
}

// ResolveAuthConfig returns the complete plaintext auth_config document for a
// backend row, given its two columns.
//
// It takes the column pair rather than a row struct because there are two row
// types carrying this column — sqlc.MonitoringBackend and
// sqlc.GetClusterMonitoringContextRow — and a helper that only accepted one of
// them would leave the other path reading the JSONB directly.
func ResolveAuthConfig(sealed string, public json.RawMessage, dec Decryptor) (json.RawMessage, error) {
	return credential.Resolve(sealed, public, dec, "monitoring backend auth_config")
}

// SealAuthConfig splits a COMPLETE auth_config document into the pair of
// columns migration 146 defines. `full` must be a resolved document: passing
// the stored JSONB projection of an already-sealed row re-seals a document
// with the credential missing, which is the exact failure this package exists
// to prevent.
func SealAuthConfig(full json.RawMessage, enc Encryptor) (ciphertext string, public json.RawMessage, err error) {
	return credential.Seal(full, enc, HasAuthConfigSecret, StripAuthConfigSecrets, "monitoring backend auth_config")
}

// SealInto fills BOTH auth columns of an upsert from a RESOLVED document. It
// is the ONLY way anything in this codebase writes monitoring_backends.
//
// The two columns are halves of one value, and the sqlc params struct compiles
// perfectly well with AuthConfigEncrypted left at its zero value: a writer that
// set only the projection would store the stripped document with an empty
// envelope, which reads back as a backend that never had a credential. There is
// no compiler error and no runtime error; the only symptom is 401s hours later,
// after an edit that had nothing to do with the credential. Making the pair
// unsettable except together is the single structural defence against that, so
// there is exactly one copy of it — this one.
//
// It lives here rather than beside either writer because there are two of them
// in two packages (internal/handler and internal/worker/tasks) and a third
// would naturally grow a third copy. `doc` must be a document that came out of
// ResolveAuthConfig; passing the stored JSONB projection of an already-sealed
// row re-seals a document with the credential missing.
func SealInto(params *sqlc.UpsertDefaultMonitoringBackendParams, doc map[string]any, enc Encryptor) error {
	full, err := EncodeAuthConfig(doc)
	if err != nil {
		return err
	}
	ciphertext, public, err := SealAuthConfig(full, enc)
	if err != nil {
		return err
	}
	params.AuthConfig = public
	params.AuthConfigEncrypted = ciphertext
	return nil
}

// HasAuthConfigSecret reports whether a decoded document carries anything
// outside the non-secret allow-list, i.e. anything worth sealing.
//
// This must stay in exact step with the SQL predicate in
// ListMonitoringBackendsWithLegacyAuthConfig: a row the query returns but this
// function calls unsealable never leaves the sweep's result set.
func HasAuthConfigSecret(doc map[string]any) bool {
	for key := range doc {
		if !slices.Contains(NonSecretAuthConfigKeys, key) {
			return true
		}
	}
	return false
}

// StripAuthConfigSecrets returns the non-secret projection of a decoded
// document: only the allow-listed keys survive.
//
// Secret keys are REMOVED, not blanked. A blank `"password": ""` reads back as
// a real (empty) credential and gets sent — a request that fails with an
// upstream 401 that looks like a wrong password rather than a dropped one.
func StripAuthConfigSecrets(doc map[string]any) map[string]any {
	out := make(map[string]any, len(NonSecretAuthConfigKeys))
	for _, key := range NonSecretAuthConfigKeys {
		if value, ok := doc[key]; ok {
			out[key] = value
		}
	}
	return out
}

// AuthConfigSecretKeyNames lists the key names (never values) that a resolved
// document keeps in the envelope, so a read-only operator can tell that
// credentials are configured without receiving them.
func AuthConfigSecretKeyNames(doc map[string]any) []string {
	keys := make([]string, 0, len(doc))
	for key := range doc {
		if slices.Contains(NonSecretAuthConfigKeys, key) {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// DecodeAuthConfig decodes a document into a mutable map, tolerating an empty
// or unparseable column the same way every existing reader of this field does.
func DecodeAuthConfig(raw json.RawMessage) map[string]any {
	doc := map[string]any{}
	if len(raw) == 0 {
		return doc
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return map[string]any{}
	}
	return doc
}

// EncodeAuthConfig re-marshals a mutated document for SealAuthConfig.
func EncodeAuthConfig(doc map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode monitoring backend auth_config: %w", err)
	}
	return raw, nil
}
