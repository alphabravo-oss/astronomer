package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// fernetish is a stand-in cipher. The real envelope is auth.Encryptor's Fernet
// token; this package must not import internal/auth (it is a leaf that both
// the handler and the worker depend on), and what is under test here is the
// column split and the failure behaviour, not the crypto.
type fernetish struct {
	prefix    string
	failOnAny bool
}

func (f fernetish) Encrypt(plaintext string) (string, error) {
	return f.prefix + plaintext, nil
}

func (f fernetish) Decrypt(token string) (string, error) {
	if f.failOnAny || !strings.HasPrefix(token, f.prefix) {
		return "", errWrongKey{}
	}
	return strings.TrimPrefix(token, f.prefix), nil
}

type errWrongKey struct{}

func (errWrongKey) Error() string { return "fernet: invalid token (wrong key?)" }

const credentialedDoc = `{
	"token": "s3cr3t-thanos-token",
	"username": "ops",
	"password": "hunter2",
	"operationPolicies": {"maxRetryAttempts": 3},
	"sharedThanos": {"namespace": "monitoring", "status": "healthy"}
}`

// TestSealAuthConfigRemovesCredentialFromTheClearColumn is the at-rest
// assertion: after sealing, the JSONB column that ends up in pg_dump carries
// the config bag and NOTHING that could be sent as a credential.
//
// Pre-fix, there was no envelope at all — auth_config held the token and the
// password in the clear, which is the whole finding.
func TestSealAuthConfigRemovesCredentialFromTheClearColumn(t *testing.T) {
	enc := fernetish{prefix: "sealed:"}
	ciphertext, public, err := SealAuthConfig(json.RawMessage(credentialedDoc), enc)
	if err != nil {
		t.Fatalf("SealAuthConfig: %v", err)
	}
	if ciphertext == "" {
		t.Fatal("SealAuthConfig produced no envelope for a document carrying a credential")
	}
	for _, secret := range []string{"s3cr3t-thanos-token", "hunter2", "ops"} {
		if strings.Contains(string(public), secret) {
			t.Fatalf("plaintext projection still carries %q: %s", secret, public)
		}
	}
	doc := DecodeAuthConfig(public)
	// Removed, not blanked. A blank "password" reads back as a real (empty)
	// credential and gets sent, producing an upstream 401 that looks like a
	// wrong password rather than a dropped one.
	for _, key := range []string{"token", "username", "password"} {
		if _, present := doc[key]; present {
			t.Fatalf("secret key %q survives in the projection (blanked instead of removed): %s", key, public)
		}
	}
	// The config bag stays queryable — the operation reconciler reads
	// operationPolicies and the status pages read sharedThanos.
	if _, ok := doc["operationPolicies"]; !ok {
		t.Fatalf("projection dropped operationPolicies: %s", public)
	}
	if _, ok := doc["sharedThanos"]; !ok {
		t.Fatalf("projection dropped sharedThanos: %s", public)
	}

	// And the envelope round-trips the COMPLETE document.
	full, err := ResolveAuthConfig(ciphertext, public, enc)
	if err != nil {
		t.Fatalf("ResolveAuthConfig: %v", err)
	}
	back := DecodeAuthConfig(full)
	if back["token"] != "s3cr3t-thanos-token" || back["password"] != "hunter2" {
		t.Fatalf("envelope did not round-trip the credential: %v", back)
	}
}

// TestSealAuthConfigLeavesAConfigOnlyDocumentInTheClear pins the "nothing to
// protect" branch. An unauthenticated in-cluster Prometheus is the common
// case, and sealing its operationPolicies would make the operation
// reconciler's retry policy depend on a successful decrypt for no gain.
func TestSealAuthConfigLeavesAConfigOnlyDocumentInTheClear(t *testing.T) {
	ciphertext, public, err := SealAuthConfig(json.RawMessage(`{"operationPolicies":{"maxRetryAttempts":2}}`), fernetish{prefix: "sealed:"})
	if err != nil {
		t.Fatalf("SealAuthConfig: %v", err)
	}
	if ciphertext != "" {
		t.Fatalf("sealed a document with nothing secret in it: %q", ciphertext)
	}
	if !strings.Contains(string(public), "maxRetryAttempts") {
		t.Fatalf("config-only document was not preserved: %s", public)
	}
}

// TestNewClientRecoversTheSealedCredential is the live-reader round-trip: the
// column pair goes in, and the outbound request to Thanos carries the right
// Authorization header.
func TestNewClientRecoversTheSealedCredential(t *testing.T) {
	enc := fernetish{prefix: "sealed:"}
	ciphertext, public, err := SealAuthConfig(json.RawMessage(`{"token":"s3cr3t-thanos-token"}`), enc)
	if err != nil {
		t.Fatalf("SealAuthConfig: %v", err)
	}

	got := captureAuthorization(t, BackendConfig{
		AuthType:            "bearer",
		AuthConfig:          public,
		AuthConfigEncrypted: ciphertext,
		Decryptor:           enc,
	})
	if got != "Bearer s3cr3t-thanos-token" {
		t.Fatalf("Authorization = %q, want the decrypted bearer token", got)
	}
}

// TestNewClientReadsAPreMigrationRow is the upgrade path: a row written before
// migration 146 has an empty ciphertext and its credential still inline, and
// must keep authenticating until the sealing sweep converts it.
//
// Disambiguation is on the COLUMN. There is deliberately no decryptor here —
// an empty envelope means "auth_config is the whole document" regardless of
// what the bytes look like.
func TestNewClientReadsAPreMigrationRow(t *testing.T) {
	got := captureAuthorization(t, BackendConfig{
		AuthType:   "bearer",
		AuthConfig: json.RawMessage(`{"token":"legacy-plaintext-token"}`),
	})
	if got != "Bearer legacy-plaintext-token" {
		t.Fatalf("Authorization = %q, want the pre-146 plaintext token", got)
	}
}

// TestNewClientSendsNoCredentialWhenDecryptFails is the failure contract.
//
// The two wrong answers are both live bugs elsewhere in this codebase or its
// history: returning the ciphertext as the credential (decryptGitAuth in
// internal/worker/tasks/gitops_sync.go) turns a key-management problem into
// what looks like a rejected password, and failing the client outright would
// take every dashboard and alert evaluation down over the same problem. The
// request goes out unauthenticated, the upstream 401s, and the log line says
// what actually happened.
func TestNewClientSendsNoCredentialWhenDecryptFails(t *testing.T) {
	enc := fernetish{prefix: "sealed:"}
	ciphertext, public, err := SealAuthConfig(json.RawMessage(`{"token":"s3cr3t-thanos-token"}`), enc)
	if err != nil {
		t.Fatalf("SealAuthConfig: %v", err)
	}

	got := captureAuthorization(t, BackendConfig{
		AuthType:            "bearer",
		AuthConfig:          public,
		AuthConfigEncrypted: ciphertext,
		// Rotated key / wrong ASTRONOMER_ENCRYPTION_KEY.
		Decryptor: fernetish{prefix: "sealed:", failOnAny: true},
	})
	if got != "" {
		t.Fatalf("Authorization = %q, want no header at all", got)
	}
	if strings.Contains(got, ciphertext) {
		t.Fatalf("the ciphertext itself was sent as a credential: %q", got)
	}
}

// TestNewClientSendsNoCredentialWithoutAKey covers the deployment that stored
// an envelope and then lost its encryptor entirely.
func TestNewClientSendsNoCredentialWithoutAKey(t *testing.T) {
	got := captureAuthorization(t, BackendConfig{
		AuthType:            "bearer",
		AuthConfig:          json.RawMessage(`{}`),
		AuthConfigEncrypted: "sealed:{\"token\":\"s3cr3t\"}",
		Decryptor:           nil,
	})
	if got != "" {
		t.Fatalf("Authorization = %q, want no header when no Fernet key is configured", got)
	}
}

// TestNewClientPrefersTheEnvelopeOverAStaleJSONBColumn is the MID-ROLLOUT
// shape, and it is the one nothing else covers: an OLD binary wrote this row
// AFTER migration 146 ran.
//
// The pre-146 UpsertDefaultMonitoringBackend statement has no
// auth_config_encrypted in its column list, so an old binary writing an
// already-sealed row replaces auth_config with a complete plaintext document
// and leaves the existing ciphertext untouched. The row ends up carrying BOTH
// a full credential in the clear and a (now stale) envelope.
//
// The reader must still disambiguate on the COLUMN: the envelope wins. That is
// the whole point of not sniffing the bytes — the alternative rules ("prefer
// whichever looks like a credential", "prefer the newer-looking one") are
// unimplementable, and a plaintext document that happens to start with 'g'
// would be misread as a Fernet token.
//
// The consequence is worth stating plainly, because it is the cost of this
// design: during a mixed-version window a write by an old binary is IGNORED by
// new binaries until a new binary re-seals the row. The credential is not lost
// and nothing leaks; the reader authenticates with the previous credential,
// which 401s if it was being rotated. The next write by a new binary — the
// operator re-saving the backend, or the monitoring:reconcile status stamp —
// resolves and re-seals, and the row is consistent again.
func TestNewClientPrefersTheEnvelopeOverAStaleJSONBColumn(t *testing.T) {
	enc := fernetish{prefix: "sealed:"}
	ciphertext, _, err := SealAuthConfig(json.RawMessage(`{"token":"sealed-token"}`), enc)
	if err != nil {
		t.Fatalf("SealAuthConfig: %v", err)
	}

	got := captureAuthorization(t, BackendConfig{
		AuthType: "bearer",
		// What an old binary put back in the clear.
		AuthConfig:          json.RawMessage(`{"token":"written-by-an-old-binary"}`),
		AuthConfigEncrypted: ciphertext,
		Decryptor:           enc,
	})
	if got != "Bearer sealed-token" {
		t.Fatalf("Authorization = %q, want the envelope to win over the stale plaintext column", got)
	}
}

// TestHasAuthConfigSecretMatchesTheSweepPredicate pins the Go half of the
// agreement with ListMonitoringBackendsWithLegacyAuthConfig. A row the SQL
// returns but this function calls unsealable never leaves the sweep's result
// set, and a full page of them ahead of a credentialed row starves it.
func TestHasAuthConfigSecretMatchesTheSweepPredicate(t *testing.T) {
	cases := map[string]bool{
		`{}`: false,
		`{"operationPolicies":{"maxRetryAttempts":1}}`:                                             false,
		`{"sharedThanos":{},"sharedAlertmanager":{},"sharedAlertingAssets":{},"status":"healthy"}`: false,
		`{"token":"t"}`:                          true,
		`{"operationPolicies":{},"username":""}`: true,
		`{"x-scope-token":"t"}`:                  true,
	}
	for doc, want := range cases {
		if got := HasAuthConfigSecret(DecodeAuthConfig(json.RawMessage(doc))); got != want {
			t.Errorf("HasAuthConfigSecret(%s) = %v, want %v", doc, got, want)
		}
	}
}

// captureAuthorization builds a client against a recording server and returns
// the Authorization header the query carried.
func captureAuthorization(t *testing.T, cfg BackendConfig) string {
	t.Helper()
	// The monitoring client dials through httpclient.SafeClientAllowPrivate,
	// whose guard rejects loopback — so httptest is unreachable without this.
	defer httpclient.DisableGuardForTest()()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(srv.Close)

	cfg.QueryURL = srv.URL
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.QueryScalar(context.Background(), "vector(1)"); err != nil &&
		!strings.Contains(err.Error(), "empty result") && !strings.Contains(err.Error(), "no data") {
		// A vector with no samples is a normal, successful response for this
		// probe; the header is what the test is after.
		t.Logf("QueryScalar: %v", err)
	}
	return seen
}
