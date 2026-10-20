// Sealing mechanics for a (JSONB document, sealed ciphertext) column pair.
//
// Two tables keep an operator-supplied credential document in a JSONB column
// with a sibling sealed column family (<col>_key_id, <col>_algorithm,
// <col>_ciphertext, <col>_nonce; see pkg/crypto) holding the COMPLETE
// document: helm_repositories.auth_config and monitoring_backends.auth_config.
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
// authoritative, what an absent sealed value means, and what a failed decrypt
// is allowed to return. Those three answers live here, once.
package credential

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// Decryptor opens a sealed value. *crypto.Encryptor satisfies it.
type Decryptor interface {
	Decrypt(sealed crypto.Sealed) ([]byte, error)
}

// Encryptor seals on the write path. *crypto.Encryptor satisfies it.
type Encryptor interface {
	Encrypt(plaintext []byte) (crypto.Sealed, error)
}

// ErrUnavailable reports that a stored credential could not be recovered.
// Callers must respond by sending NO credential — never by falling back to the
// ciphertext, and never by proceeding to a write that would persist a document
// they could not read.
//
// Sending ciphertext as a password produces an upstream 401, which reads to
// the operator as "the backend rejected my credential" when what actually
// happened is that ASTRONOMER_ENCRYPTION_KEY is wrong or a rotation dropped a
// key too early.
var ErrUnavailable = errors.New("stored credential unavailable")

// Resolve returns the plaintext document for a sealed/plaintext column pair.
//
// Disambiguation is by COLUMN, not by inspecting the bytes: a non-zero
// `sealed` value is authoritative; a zero one means the document carried
// nothing secret when it was written (or was written by a development server
// with no key) and `plaintext` is the whole document.
//
// The error is terminal for credential purposes. It is never accompanied by a
// usable document. `subject` names the row for the log line an operator will
// have to act on.
func Resolve(sealed crypto.Sealed, plaintext json.RawMessage, dec Decryptor, subject string) (json.RawMessage, error) {
	if sealed.IsZero() {
		return plaintext, nil
	}
	if dec == nil {
		return nil, fmt.Errorf("%w: %s is encrypted but no encryption key is configured (check ASTRONOMER_ENCRYPTION_KEY)",
			ErrUnavailable, subject)
	}
	decrypted, err := dec.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt %s (check ASTRONOMER_ENCRYPTION_KEY / key rotation): %w",
			ErrUnavailable, subject, err)
	}
	if len(bytes.TrimSpace(decrypted)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(decrypted), nil
}

// Seal splits a complete document into the pair of columns: the sealed value
// over the whole document, and the non-secret projection that stays queryable
// as JSONB.
//
// `sealable` decides whether the document carries anything worth protecting;
// `project` produces the plaintext remainder. Secret-valued keys must be
// REMOVED by `project`, not blanked — a blank "password" reads back as a real
// (empty) credential and gets sent.
//
// With no encryptor (development; config.ValidateProductionSecurity refuses to
// start a production server in this state) it returns a zero sealed value and
// the document unchanged, which is exactly what Resolve's zero branch expects.
func Seal(full json.RawMessage, enc Encryptor, sealable func(map[string]any) bool, project func(map[string]any) map[string]any, subject string) (crypto.Sealed, json.RawMessage, error) {
	if len(full) == 0 {
		full = json.RawMessage(`{}`)
	}
	if enc == nil {
		return crypto.Sealed{}, full, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(full, &doc); err != nil || doc == nil {
		// Unparseable or JSON null: there is nothing to protect and nothing
		// worth storing. Normalising to {} keeps the column's NOT NULL JSONB
		// contract.
		return crypto.Sealed{}, json.RawMessage(`{}`), nil
	}
	if !sealable(doc) {
		// Nothing secret in the document. Sealing it anyway would put its
		// non-secret contents out of reach of every reader for no gain and
		// make them depend on a successful decrypt.
		return crypto.Sealed{}, full, nil
	}
	sealed, err := enc.Encrypt(full)
	if err != nil {
		return crypto.Sealed{}, nil, fmt.Errorf("encrypt %s: %w", subject, err)
	}
	out, err := json.Marshal(project(doc))
	if err != nil {
		return crypto.Sealed{}, nil, fmt.Errorf("project %s: %w", subject, err)
	}
	return sealed, out, nil
}
