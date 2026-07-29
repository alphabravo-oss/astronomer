package credential

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// This package exists for one reason, stated in its own header: two tables
// (helm_repositories, migration 145; monitoring_backends, migration 146) share
// a (JSONB, ciphertext TEXT) column pair, and the three dangerous answers —
// which column is authoritative, what an empty ciphertext means, and what a
// failed decrypt is allowed to return — must not be answered twice.
//
// Those answers were previously pinned only TRANSITIVELY, through
// internal/catalog/authconfig_test.go and internal/monitoring/authconfig_test.go.
// That is not a pin: an edit to Resolve or Seal could be made to look correct
// by whichever of the two owners' suites happened to be run, and if a third
// table adopts the envelope and either suite is refactored the invariants lose
// their anchor entirely. A shared invariant with no test of its own is not an
// invariant. These tests pin the three answers directly.

const testCiphertext = "gAAAAA-fernet-token"
const testPlaintextDoc = `{"password":"hunter2","username":"ops"}`

type fakeCipher struct {
	decrypted  string
	decryptErr error
	encryptErr error
	// sealed records what Encrypt was handed, so a test can assert the
	// COMPLETE document was sealed rather than the projection.
	sealed string
}

func (f *fakeCipher) Decrypt(string) (string, error) {
	if f.decryptErr != nil {
		return "", f.decryptErr
	}
	return f.decrypted, nil
}

func (f *fakeCipher) Encrypt(plaintext string) (string, error) {
	if f.encryptErr != nil {
		return "", f.encryptErr
	}
	f.sealed = plaintext
	return testCiphertext, nil
}

// ANSWER 1: which column is authoritative.
//
// An EMPTY ciphertext means the row predates the envelope migration and the
// JSONB column is the complete document. A NON-EMPTY one means the envelope
// is authoritative and the JSONB is only the non-secret projection.
//
// The disambiguation is by COLUMN, never by sniffing the bytes. The
// whitespace cases are here because a TEXT NOT NULL column defaulting to the
// empty string, that
// has been through a backup/restore or a hand-run UPDATE can hold " " rather
// than "", and treating that as a sealed row would send every legacy install
// down the decrypt path.
func TestResolveEmptyCiphertextMeansThePlaintextColumnIsTheWholeDocument(t *testing.T) {
	for name, sealed := range map[string]string{
		"empty":           "",
		"whitespace":      "   ",
		"newline":         "\n",
		"tabs and spaces": "\t \t",
	} {
		t.Run(name, func(t *testing.T) {
			// A decryptor that would fail loudly if this branch used it.
			dec := &fakeCipher{decryptErr: errors.New("must not be called")}
			got, err := Resolve(sealed, json.RawMessage(testPlaintextDoc), dec, "subject")
			if err != nil {
				t.Fatalf("Resolve on an unsealed row: %v", err)
			}
			if string(got) != testPlaintextDoc {
				t.Fatalf("Resolve returned %s, want the plaintext column verbatim (%s)", got, testPlaintextDoc)
			}
		})
	}
}

// A non-empty ciphertext is authoritative even when the plaintext column still
// holds a full-looking document. This is the mid-rollout shape: an OLD binary
// wrote the complete document into the JSONB of a row a NEW binary had already
// sealed. The envelope wins; the stale JSONB is ignored.
func TestResolveNonEmptyCiphertextWinsOverTheJSONBColumn(t *testing.T) {
	dec := &fakeCipher{decrypted: `{"password":"current"}`}
	got, err := Resolve(testCiphertext, json.RawMessage(`{"password":"stale"}`), dec, "subject")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != `{"password":"current"}` {
		t.Fatalf("Resolve returned %s, want the decrypted envelope", got)
	}
}

// ANSWER 2: what a failed decrypt may return — NOTHING, ever.
//
// The forbidden shape is a fallback to the stored blob (decryptGitAuth in
// internal/worker/tasks/gitops_sync.go still has it): sending a Fernet token
// as a password produces an upstream 401, which reads to the operator as "the
// backend rejected my credential" when what actually happened is that
// ASTRONOMER_ENCRYPTION_KEY is wrong. Every failure path must return a nil
// document alongside ErrUnavailable, so a caller that ignores the error still
// cannot send anything.
func TestResolveFailureReturnsNoDocumentAndNeverTheCiphertext(t *testing.T) {
	cases := map[string]Decryptor{
		// No key configured at all for a row that is sealed.
		"nil decryptor": nil,
		// Wrong key, or a rotation that retired the old key too early.
		"decrypt error": &fakeCipher{decryptErr: errors.New("fernet: invalid token")},
	}
	for name, dec := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Resolve(testCiphertext, json.RawMessage(testPlaintextDoc), dec, "the subject")
			if err == nil {
				t.Fatal("Resolve succeeded on an unreadable envelope")
			}
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("error %v does not wrap ErrUnavailable; callers keyed on the sentinel will miss it", err)
			}
			if got != nil {
				t.Fatalf("Resolve returned a document (%s) alongside an error; a caller that ignores err would send it", got)
			}
			if strings.Contains(string(got), testCiphertext) {
				t.Fatalf("Resolve leaked the ciphertext as a document: %s", got)
			}
			// The log line an operator has to act on must name the row and the
			// thing to check.
			if !strings.Contains(err.Error(), "the subject") {
				t.Fatalf("error %q does not name the subject", err)
			}
			if !strings.Contains(err.Error(), "ASTRONOMER_ENCRYPTION_KEY") {
				t.Fatalf("error %q does not tell the operator what to check", err)
			}
		})
	}
}

// ANSWER 3: what an empty DECRYPT result means.
//
// A successfully-decrypted empty payload is an empty document, not a missing
// one and not a nil that a caller has to guard. It normalises to `{}` so
// json.Unmarshal downstream cannot fail on it.
func TestResolveEmptyDecryptedPayloadNormalisesToEmptyObject(t *testing.T) {
	for name, decrypted := range map[string]string{
		"empty string": "",
		"whitespace":   "  \n ",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Resolve(testCiphertext, nil, &fakeCipher{decrypted: decrypted}, "subject")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if string(got) != `{}` {
				t.Fatalf("Resolve returned %q, want `{}`", got)
			}
			var doc map[string]any
			if err := json.Unmarshal(got, &doc); err != nil {
				t.Fatalf("Resolve returned something a caller cannot unmarshal: %v", err)
			}
		})
	}
}

func alwaysSealable(map[string]any) bool { return true }
func neverSealable(map[string]any) bool  { return false }

// projectDropPassword is the shape both owners' projections have: secret keys
// are REMOVED, not blanked. A blank "password" reads back as a real (empty)
// credential and gets sent.
func projectDropPassword(doc map[string]any) map[string]any {
	delete(doc, "password")
	return doc
}

// Seal must encrypt the COMPLETE document and only then project. Sealing the
// projection instead would put a credential-free document in the envelope —
// the exact silent-drop this whole package exists to prevent — and there is no
// error and no compiler complaint when it happens.
func TestSealEncryptsTheCompleteDocumentBeforeProjecting(t *testing.T) {
	enc := &fakeCipher{}
	ciphertext, public, err := Seal(json.RawMessage(testPlaintextDoc), enc, alwaysSealable, projectDropPassword, "subject")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if ciphertext != testCiphertext {
		t.Fatalf("ciphertext = %q, want the encryptor's output", ciphertext)
	}
	if !strings.Contains(enc.sealed, `"password":"hunter2"`) {
		t.Fatalf("Seal encrypted %q, which does not carry the credential: the envelope is credential-free", enc.sealed)
	}
	if strings.Contains(string(public), "hunter2") {
		t.Fatalf("the plaintext projection kept the credential: %s", public)
	}
	// REMOVED, not blanked.
	var doc map[string]any
	if err := json.Unmarshal(public, &doc); err != nil {
		t.Fatalf("unmarshal projection: %v", err)
	}
	if _, present := doc["password"]; present {
		t.Fatalf("projection blanked rather than removed the secret key: %s", public)
	}
	if doc["username"] != "ops" {
		t.Fatalf("projection dropped a non-secret key: %s", public)
	}
}

// With no encryptor (development only; config.ValidateProductionSecurity
// refuses to start a production server in this state) Seal must produce
// exactly the PRE-MIGRATION row shape: empty ciphertext, document unchanged.
// That is precisely what Resolve's empty-ciphertext branch expects, so the two
// halves compose — a dev-written row is readable, and a projection is never
// stored without an envelope to recover the rest from.
func TestSealWithNoEncryptorProducesThePreMigrationRowShape(t *testing.T) {
	ciphertext, public, err := Seal(json.RawMessage(testPlaintextDoc), nil, alwaysSealable, projectDropPassword, "subject")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if ciphertext != "" {
		t.Fatalf("ciphertext = %q, want empty with no encryptor", ciphertext)
	}
	if string(public) != testPlaintextDoc {
		t.Fatalf("public = %s, want the document unchanged", public)
	}
	// Compose: what Seal wrote is what Resolve reads back, whole.
	got, err := Resolve(ciphertext, public, nil, "subject")
	if err != nil {
		t.Fatalf("Resolve of a no-encryptor row: %v", err)
	}
	if string(got) != testPlaintextDoc {
		t.Fatalf("round-trip lost the document: %s", got)
	}
}

// A document with nothing secret in it is left in the clear. Sealing it anyway
// would put its non-secret contents out of reach of every reader for no gain
// and make them depend on a successful decrypt.
func TestSealLeavesANonSecretDocumentInTheClear(t *testing.T) {
	enc := &fakeCipher{}
	ciphertext, public, err := Seal(json.RawMessage(`{"charts":["a","b"]}`), enc, neverSealable, projectDropPassword, "subject")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if ciphertext != "" {
		t.Fatalf("ciphertext = %q, want empty for a document with nothing to protect", ciphertext)
	}
	if string(public) != `{"charts":["a","b"]}` {
		t.Fatalf("public = %s, want the document unchanged", public)
	}
	if enc.sealed != "" {
		t.Fatalf("Encrypt was called for an unsealable document (%q)", enc.sealed)
	}
}

// Unparseable input, JSON null and an empty column all normalise to `{}` with
// no envelope: there is nothing to protect and nothing worth storing, and `{}`
// keeps the column's NOT NULL JSONB contract.
func TestSealNormalisesUnusableInputToEmptyObject(t *testing.T) {
	for name, in := range map[string]string{
		"json null":   `null`,
		"unparseable": `not json at all`,
		"array":       `["not","an","object"]`,
	} {
		t.Run(name, func(t *testing.T) {
			enc := &fakeCipher{}
			ciphertext, public, err := Seal(json.RawMessage(in), enc, alwaysSealable, projectDropPassword, "subject")
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if ciphertext != "" {
				t.Fatalf("ciphertext = %q, want empty", ciphertext)
			}
			if string(public) != `{}` {
				t.Fatalf("public = %s, want `{}`", public)
			}
		})
	}

	// An empty column is a parseable empty document, so it takes the normal
	// path rather than the bail-out one: it is normalised to `{}` BEFORE the
	// sealable check, which is what keeps a NOT NULL JSONB column from ever
	// being handed a zero-length value.
	t.Run("empty column", func(t *testing.T) {
		enc := &fakeCipher{}
		ciphertext, public, err := Seal(nil, enc, alwaysSealable, projectDropPassword, "subject")
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		if string(public) != `{}` {
			t.Fatalf("public = %s, want `{}`", public)
		}
		if ciphertext != testCiphertext || enc.sealed != `{}` {
			t.Fatalf("Seal(nil) sealed %q as %q, want the normalised `{}`", enc.sealed, ciphertext)
		}
	})
}

// An encrypt failure must abort with NO columns, not with a projection and an
// empty envelope — that pair is indistinguishable from "this row never had a
// credential" and is exactly how a credential disappears silently.
func TestSealEncryptFailureReturnsNoColumns(t *testing.T) {
	enc := &fakeCipher{encryptErr: errors.New("cipher unavailable")}
	ciphertext, public, err := Seal(json.RawMessage(testPlaintextDoc), enc, alwaysSealable, projectDropPassword, "the subject")
	if err == nil {
		t.Fatal("Seal succeeded despite the encryptor failing")
	}
	if ciphertext != "" || public != nil {
		t.Fatalf("Seal returned columns (%q, %s) alongside an error; a caller that ignores err would write a credential-free row", ciphertext, public)
	}
	if !strings.Contains(err.Error(), "the subject") {
		t.Fatalf("error %q does not name the subject", err)
	}
}

// Seal → Resolve is the round-trip every owner depends on: the envelope
// recovers the COMPLETE document, including the keys the projection dropped.
func TestSealResolveRoundTripRecoversTheCompleteDocument(t *testing.T) {
	enc := &fakeCipher{}
	ciphertext, public, err := Seal(json.RawMessage(testPlaintextDoc), enc, alwaysSealable, projectDropPassword, "subject")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// The fake returns whatever it was handed, which is what a real Fernet
	// round-trip does.
	got, err := Resolve(ciphertext, public, &fakeCipher{decrypted: enc.sealed}, "subject")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["password"] != "hunter2" || doc["username"] != "ops" {
		t.Fatalf("round-trip did not recover the complete document: %v", doc)
	}
}
