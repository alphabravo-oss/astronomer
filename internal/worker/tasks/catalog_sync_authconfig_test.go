package tasks

// The scheduled catalog:sync sweep is the SECOND reader of a chart-repository
// credential (internal/handler/catalog.go is the first). Migration 145 sealed
// that credential behind a Fernet envelope, and the classic way an
// encryption-at-rest change ships half-done is to wire the interactive path
// and forget the unattended one: the operator clicks Sync, it works, and then
// every 6-hourly sweep quietly 401s against the same repository.

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func sweepTestEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return enc
}

func sealedSweepRepo(t *testing.T, enc *auth.Encryptor, name, url, authType, doc string) sqlc.HelmRepository {
	t.Helper()
	ciphertext, public, err := catalog.SealAuthConfig(json.RawMessage(doc), enc)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if ciphertext == "" {
		t.Fatalf("fixture %q sealed to nothing", doc)
	}
	return sqlc.HelmRepository{
		ID: uuid.New(), Name: name, Url: url, RepoType: "helm",
		AuthType: authType, AuthConfig: public, AuthConfigEncrypted: ciphertext,
	}
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
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	enc := sweepTestEncryptor(t)
	basicSrv, basicSeen := indexServer(t, 200)
	bearerSrv, bearerSeen := indexServer(t, 200)

	repos := []sqlc.HelmRepository{
		sealedSweepRepo(t, enc, "private-basic", basicSrv.URL, "basic", `{"username":"u","password":"p"}`),
		sealedSweepRepo(t, enc, "private-bearer", bearerSrv.URL, "bearer", `{"token":"tok"}`),
	}
	q := &catalogSweepQuerier{repos: repos}
	runtimeDeps = RuntimeDependencies{
		Queries: q, Log: slog.Default(),
		CatalogDecryptor: CatalogDecryptorFor(enc),
	}

	if err := HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
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
// on decrypt failure. Sending a Fernet token as a password gets a 401 from
// upstream and is recorded against the repository as an authentication
// problem — pointing the operator at registry ACLs when the actual fault is
// ASTRONOMER_ENCRYPTION_KEY.
func TestHandleCatalogSyncSendsNoCredentialWhenDecryptFails(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	srv, seen := indexServer(t, 200)
	// Sealed under one key, read with another: a rotation that dropped the
	// old key one step too early.
	repoRecord := sealedSweepRepo(t, sweepTestEncryptor(t), "private", srv.URL, "basic",
		`{"username":"u","password":"p"}`)
	q := &catalogSweepQuerier{repos: []sqlc.HelmRepository{repoRecord}}
	runtimeDeps = RuntimeDependencies{
		Queries: q, Log: slog.Default(),
		CatalogDecryptor: CatalogDecryptorFor(sweepTestEncryptor(t)),
	}

	if err := HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
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

// TestHandleCatalogSyncStillReadsPreMigrationPlaintextRow — the upgrade
// window. Until the sealing sweep converts a row it still holds the whole
// document in the JSONB with an empty envelope, and the sweep must keep
// authenticating it.
func TestHandleCatalogSyncStillReadsPreMigrationPlaintextRow(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	srv, seen := indexServer(t, 200)
	legacy := sqlc.HelmRepository{
		ID: uuid.New(), Name: "pre-145", Url: srv.URL, RepoType: "helm",
		AuthType:   "basic",
		AuthConfig: json.RawMessage(`{"username":"u","password":"p"}`),
		// AuthConfigEncrypted empty — the pre-upgrade shape.
	}
	q := &catalogSweepQuerier{repos: []sqlc.HelmRepository{legacy}}
	runtimeDeps = RuntimeDependencies{
		Queries: q, Log: slog.Default(),
		CatalogDecryptor: CatalogDecryptorFor(sweepTestEncryptor(t)),
	}

	if err := HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
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

// TestPlaintextCredentialMigrationSealsChartRepositoryAuthConfig — this is
// what ends the upgrade window opened by migration 145.
//
// Pre-fix there was no converter at all: a repository created before the
// upgrade and never re-saved would have kept its password in the clear
// indefinitely, so the security fix would only ever have applied to rows
// somebody happened to edit.
func TestPlaintextCredentialMigrationSealsChartRepositoryAuthConfig(t *testing.T) {
	ResetPlaintextCredentialMigration()
	enc := sweepTestEncryptor(t)

	legacy := sqlc.HelmRepository{
		ID: uuid.New(), Name: "pre-145", AuthType: "basic",
		AuthConfig: json.RawMessage(`{"username":"u","password":"s3cret","charts":["app"]}`),
	}
	// No secret to protect — must be left alone so the chart list stays
	// readable to the catalog API.
	noSecret := sqlc.HelmRepository{
		ID: uuid.New(), Name: "public-oci", AuthType: "",
		AuthConfig: json.RawMessage(`{"charts":["app"]}`),
	}
	q := &fakePlaintextCredentialMigrationQuerier{repos: []sqlc.HelmRepository{legacy, noSecret}}
	ConfigurePlaintextCredentialMigration(PlaintextCredentialMigrationDeps{Queries: q, Encryptor: enc})
	t.Cleanup(ResetPlaintextCredentialMigration)

	if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
		t.Fatalf("migration: %v", err)
	}

	if len(q.repoSeals) != 1 {
		t.Fatalf("expected exactly 1 sealed repository, got %d", len(q.repoSeals))
	}
	sealed := q.repoSeals[0]
	if sealed.ID != legacy.ID {
		t.Fatalf("sealed the wrong row: %s", sealed.ID)
	}
	if s := string(sealed.AuthConfig); s == "" || strings.Contains(s, "s3cret") {
		t.Fatalf("plaintext column still carries the credential after sealing: %s", s)
	}
	plain, err := enc.Decrypt(sealed.AuthConfigEncrypted)
	if err != nil {
		t.Fatalf("sealed ciphertext does not decrypt: %v", err)
	}
	var doc map[string]any
	_ = json.Unmarshal([]byte(plain), &doc)
	if doc["password"] != "s3cret" {
		t.Fatalf("sealing lost the credential: %v", doc)
	}
	// The chart list must survive in the plaintext projection.
	var public map[string]any
	_ = json.Unmarshal(sealed.AuthConfig, &public)
	if _, ok := public["charts"]; !ok {
		t.Fatalf("non-secret chart list was stripped: %s", sealed.AuthConfig)
	}

	// Idempotent: a second run finds nothing left to do (the fake mirrors the
	// SQL predicate, so a sealed row stops matching).
	before := len(q.repoSeals)
	if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	if len(q.repoSeals) != before {
		t.Fatalf("re-sealed an already-sealed row: %d -> %d", before, len(q.repoSeals))
	}
}

// TestPlaintextCredentialMigrationSealsPastAFullPageOfUnsealableRows pins the
// starvation bug: the sweep used to stop as soon as a page produced no seals.
//
// The query re-reads from a fixed position because sealing removes a row from
// the predicate, and the old loop's `!sealedAny` guard was there to stop that
// re-read from spinning forever. But the predicate also matched rows with
// nothing to seal (an OCI repo whose auth_config is only a `charts` list), and
// those never leave the result set. A full page of them sorting ahead of a
// credentialed row meant `sealedAny` stayed false, the sweep returned, and the
// password stayed in the clear on every subsequent run as well — silent, and
// in the direction of not encrypting.
//
// pageSize is 500, so the fixture needs more than that to reach the bug at
// all; the 2-row fixture above cannot.
func TestPlaintextCredentialMigrationSealsPastAFullPageOfUnsealableRows(t *testing.T) {
	ResetPlaintextCredentialMigration()
	enc := sweepTestEncryptor(t)

	const unsealableRows = 600 // > the sweep's 500-row page
	repos := make([]sqlc.HelmRepository, 0, unsealableRows+1)
	for i := 0; i < unsealableRows; i++ {
		repos = append(repos, sqlc.HelmRepository{
			ID: uuid.New(), Name: "public-oci", AuthType: "",
			AuthConfig: json.RawMessage(`{"charts":["app"]}`),
		})
	}
	// Sorts last, so every page of unsealable rows is walked before it.
	credentialed := sqlc.HelmRepository{
		ID: uuid.New(), Name: "private", AuthType: "basic",
		AuthConfig: json.RawMessage(`{"username":"u","password":"s3cret"}`),
	}
	repos = append(repos, credentialed)

	// Both halves of the fix are load-bearing on their own, so both are
	// exercised: "narrow" is the shipped SQL predicate, which never offers an
	// unsealable row in the first place; "loose" is the pre-fix predicate,
	// proving the loop still reaches the credential when the SQL and
	// catalog.SealAuthConfig disagree about what is sealable.
	for _, tc := range []struct {
		name  string
		loose bool
	}{
		{"narrow sql predicate", false},
		{"loose sql predicate (pre-fix)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ResetPlaintextCredentialMigration()
			rows := make([]sqlc.HelmRepository, len(repos))
			copy(rows, repos)
			q := &fakePlaintextCredentialMigrationQuerier{repos: rows, looseRepoListPredicate: tc.loose}
			ConfigurePlaintextCredentialMigration(PlaintextCredentialMigrationDeps{Queries: q, Encryptor: enc})
			t.Cleanup(ResetPlaintextCredentialMigration)

			if err := HandlePlaintextCredentialMigration(context.Background(), nil); err != nil {
				t.Fatalf("migration: %v", err)
			}

			if len(q.repoSeals) != 1 {
				t.Fatalf("expected the credentialed row to be sealed exactly once, got %d seals", len(q.repoSeals))
			}
			if q.repoSeals[0].ID != credentialed.ID {
				t.Fatalf("sealed the wrong row: %s", q.repoSeals[0].ID)
			}
			plain, err := enc.Decrypt(q.repoSeals[0].AuthConfigEncrypted)
			if err != nil {
				t.Fatalf("sealed ciphertext does not decrypt: %v", err)
			}
			if !strings.Contains(plain, "s3cret") {
				t.Fatalf("sealing lost the credential: %s", plain)
			}
			if s := string(q.repoSeals[0].AuthConfig); strings.Contains(s, "s3cret") {
				t.Fatalf("plaintext column still carries the credential: %s", s)
			}
		})
	}
}
