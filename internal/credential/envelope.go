// Fernet-envelope mechanics for a (JSONB document, ciphertext TEXT) column
// pair.
//
// Two tables keep an operator-supplied credential document in a JSONB column
// with a sibling `<col>_encrypted TEXT NOT NULL DEFAULT empty-string` holding a
// Fernet token over the COMPLETE document: helm_repositories.auth_config
// (migration 145) and monitoring_backends.auth_config (migration 146).
//
// What counts as secret differs between them and deliberately stays with each
// owner. The chart-repository document is a credential with a few known
// non-secret extras (charts, allow_catalog, username), so it strips a
// deny-list of secret keys. The monitoring document is the inverse — a config
// bag (operationPolicies, sharedThanos, sharedAlertmanager,
// sharedAlertingAssets) around an unbounded operator-authored credential
// portion — so it keeps an allow-list of non-secret keys and seals the rest.
//
// What does NOT differ is the envelope itself, and that is the part where two
// subtly different implementations would be dangerous: which column is
// authoritative, what an empty ciphertext means, and what a failed decrypt is
// allowed to return. Those three answers live here, once.
package credential

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Decryptor unwraps the Fernet envelope. *auth.Encryptor satisfies it; it is
// an interface here so this package depends on nothing but the standard
// library and cannot participate in an import cycle.
type Decryptor interface {
	Decrypt(token string) (string, error)
}

// Encryptor seals the envelope on the write path.
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
}

// ErrUnavailable reports that a stored credential could not be recovered.
// Callers must respond by sending NO credential — never by falling back to the
// ciphertext, and never by proceeding to a write that would persist a document
// they could not read.
//
// The fallback-to-ciphertext shape is a real bug in this codebase
// (decryptGitAuth in internal/worker/tasks/gitops_sync.go returns the stored
// blob when Decrypt fails), and it is worth naming why it is wrong: sending a
// Fernet token as a password produces an upstream 401, which reads to the
// operator as "the backend rejected my credential" when what actually happened
// is that ASTRONOMER_ENCRYPTION_KEY is wrong or a rotation dropped a key too
// early.
var ErrUnavailable = errors.New("stored credential unavailable")

// Resolve returns the plaintext document for a sealed/plaintext column pair.
//
// Disambiguation is by COLUMN, not by inspecting the bytes: a non-empty
// `sealed` means the envelope is authoritative; an empty one means the row
// predates the envelope migration (or was written by a deployment with no
// Fernet key) and `plaintext` is still the whole document. There is no "does
// this look like a Fernet token" heuristic to get wrong, so a plaintext
// document that happens to start with 'g' cannot be misread.
//
// The error is terminal for credential purposes. It is never accompanied by a
// usable document. `subject` names the row for the log line an operator will
// have to act on.
func Resolve(sealed string, plaintext json.RawMessage, dec Decryptor, subject string) (json.RawMessage, error) {
	sealed = strings.TrimSpace(sealed)
	if sealed == "" {
		// Legacy / unsealed row: the JSONB column is the complete document.
		// This branch is what keeps an existing install working across the
		// upgrade until security:migrate_plaintext_credentials seals the row.
		return plaintext, nil
	}
	if dec == nil {
		return nil, fmt.Errorf("%w: %s is encrypted but no Fernet key is configured (check ASTRONOMER_ENCRYPTION_KEY)",
			ErrUnavailable, subject)
	}
	decrypted, err := dec.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt %s (check ASTRONOMER_ENCRYPTION_KEY / key rotation): %w",
			ErrUnavailable, subject, err)
	}
	if strings.TrimSpace(decrypted) == "" {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(decrypted), nil
}

// Seal splits a complete document into the pair of columns the envelope
// migrations define: the Fernet token over the whole document, and the
// non-secret projection that stays queryable as JSONB.
//
// `sealable` decides whether the document carries anything worth protecting;
// `project` produces the plaintext remainder. Secret-valued keys must be
// REMOVED by `project`, not blanked — a blank "password" reads back as a real
// (empty) credential and gets sent.
//
// With no encryptor (development; config.ValidateProductionSecurity refuses to
// start a production server in this state) it returns an empty envelope and
// the document unchanged, which is exactly the pre-migration row shape and is
// what Resolve's empty-ciphertext branch expects.
func Seal(full json.RawMessage, enc Encryptor, sealable func(map[string]any) bool, project func(map[string]any) map[string]any, subject string) (ciphertext string, public json.RawMessage, err error) {
	if len(full) == 0 {
		full = json.RawMessage(`{}`)
	}
	if enc == nil {
		return "", full, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(full, &doc); err != nil || doc == nil {
		// Unparseable or JSON null: there is nothing to protect and nothing
		// worth storing. Normalising to {} keeps the column's NOT NULL JSONB
		// contract.
		return "", json.RawMessage(`{}`), nil
	}
	if !sealable(doc) {
		// Nothing secret in the document. Sealing it anyway would put its
		// non-secret contents out of reach of every reader for no gain and
		// make them depend on a successful decrypt.
		return "", full, nil
	}
	ciphertext, err = enc.Encrypt(string(full))
	if err != nil {
		return "", nil, fmt.Errorf("encrypt %s: %w", subject, err)
	}
	out, err := json.Marshal(project(doc))
	if err != nil {
		return "", nil, fmt.Errorf("project %s: %w", subject, err)
	}
	return ciphertext, out, nil
}
