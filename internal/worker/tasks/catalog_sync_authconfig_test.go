package tasks

// The scheduled catalog:sync sweep is the SECOND reader of a chart-repository
// credential (internal/handler/catalog.go is the first). The credential is
// sealed (pkg/crypto), and the classic way an
// encryption-at-rest change ships half-done is to wire the interactive path
// and forget the unattended one: the operator clicks Sync, it works, and then
// every 6-hourly sweep quietly 401s against the same repository.

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

func sweepTestEncryptor(t *testing.T) *crypto.Encryptor {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return enc
}

func sealedSweepRepo(t *testing.T, enc *crypto.Encryptor, name, url, authType, doc string) sqlc.HelmRepository {
	t.Helper()
	sealed, public, err := catalog.SealAuthConfig(json.RawMessage(doc), enc)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed.IsZero() {
		t.Fatalf("fixture %q sealed to nothing", doc)
	}
	repo := sqlc.HelmRepository{
		ID: uuid.New(), Name: name, Url: url, RepoType: "helm",
		AuthType: authType, AuthConfig: public,
	}
	repo.AuthConfigKeyID, repo.AuthConfigAlgorithm, repo.AuthConfigCiphertext, repo.AuthConfigNonce = sealed.Parts()
	return repo
}

// TestHandleCatalogSyncDecryptsSealedRepoAuth — the sweep recovers the same
// credential the handler does from a post-145 row.
//
// Pre-fix (encryption wired on the write path only) the worker read
// repo.AuthConfig directly. After sealing, that column no longer contains the
// password, so the index fetch would have gone out with no credential (or, on
// the naive "just unmarshal it" variant, with an empty one) and every private
// repository would have started failing on the schedule while its Sync button
// still worked.
func TestHandleCatalogSyncDecryptsSealedRepoAuth(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	enc := sweepTestEncryptor(t)
	basicSrv, basicSeen := indexServer(t, 200)
	bearerSrv, bearerSeen := indexServer(t, 200)

	repos := []sqlc.HelmRepository{
		sealedSweepRepo(t, enc, "private-basic", basicSrv.URL, "basic", `{"username":"u","password":"p"}`),
		sealedSweepRepo(t, enc, "private-bearer", bearerSrv.URL, "bearer", `{"token":"tok"}`),
	}
	q := &catalogSweepQuerier{repos: repos}
	ctx := testRuntimeContext(RuntimeDependencies{
		Queries: q, Log: slog.Default(),
		CatalogDecryptor: CatalogDecryptorFor(enc),
	})

	if err := HandleCatalogSync(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	basicReqs := basicSeen()
	if len(basicReqs) != 1 {
		t.Fatalf("basic repo: expected 1 index request, got %d", len(basicReqs))
	}
	user, pass, ok := basicReqs[0].BasicAuth()
	if !ok || user != "u" || pass != "p" {
		t.Fatalf("sealed basic credential not recovered by the scheduled sweep: ok=%v user=%q", ok, user)
	}
	bearerReqs := bearerSeen()
	if len(bearerReqs) != 1 || bearerReqs[0].Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("sealed bearer credential not recovered by the scheduled sweep: %q",
			bearerReqs[0].Header.Get("Authorization"))
	}
}

// TestHandleCatalogSyncSendsNoCredentialWhenDecryptFails is the anti-regression
// for the decryptGitAuth shape in gitops_sync.go, which returns the CIPHERTEXT
// on decrypt failure. Sending ciphertext as a password gets a 401 from
// upstream and is recorded against the repository as an authentication
// problem — pointing the operator at registry ACLs when the actual fault is
// ASTRONOMER_ENCRYPTION_KEY.
func TestHandleCatalogSyncSendsNoCredentialWhenDecryptFails(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	srv, seen := indexServer(t, 200)
	// Sealed under one key, read with another: a rotation that dropped the
	// old key one step too early.
	repoRecord := sealedSweepRepo(t, sweepTestEncryptor(t), "private", srv.URL, "basic",
		`{"username":"u","password":"p"}`)
	q := &catalogSweepQuerier{repos: []sqlc.HelmRepository{repoRecord}}
	ctx := testRuntimeContext(RuntimeDependencies{
		Queries: q, Log: slog.Default(),
		CatalogDecryptor: CatalogDecryptorFor(sweepTestEncryptor(t)),
	})

	if err := HandleCatalogSync(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	reqs := seen()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 index request, got %d", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "" {
		t.Fatalf("undecryptable credential must produce NO Authorization header, got %q", got)
	}
	if _, _, ok := reqs[0].BasicAuth(); ok {
		t.Fatal("undecryptable credential was sent as basic auth")
	}
}

// TestHandleCatalogSyncStillReadsPreMigrationPlaintextRow — a row with no
// sealed value (written by a development server without a key) holds the
// whole document in the JSONB, and the sweep must keep authenticating it.
func TestHandleCatalogSyncStillReadsPreMigrationPlaintextRow(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	srv, seen := indexServer(t, 200)
	legacy := sqlc.HelmRepository{
		ID: uuid.New(), Name: "pre-145", Url: srv.URL, RepoType: "helm",
		AuthType:   "basic",
		AuthConfig: json.RawMessage(`{"username":"u","password":"p"}`),
		// No sealed value: the JSONB is the whole document.
	}
	q := &catalogSweepQuerier{repos: []sqlc.HelmRepository{legacy}}
	ctx := testRuntimeContext(RuntimeDependencies{
		Queries: q, Log: slog.Default(),
		CatalogDecryptor: CatalogDecryptorFor(sweepTestEncryptor(t)),
	})

	if err := HandleCatalogSync(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	reqs := seen()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 index request, got %d", len(reqs))
	}
	user, pass, ok := reqs[0].BasicAuth()
	if !ok || user != "u" || pass != "p" {
		t.Fatalf("pre-145 plaintext row stopped authenticating on the sweep: ok=%v user=%q", ok, user)
	}
}
