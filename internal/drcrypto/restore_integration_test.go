//go:build integration

package drcrypto

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/alphabravocompany/astronomer-go/internal/db/migrations"
	columncrypto "github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// This is a disposable PostgreSQL rehearsal of the runbook gates, not a
// claim that an operator-controlled writer restart is automatically fenced.
func TestDisposableDatabaseRestoreAndRequiredKeyGates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c, err := postgres.Run(ctx, "postgres:17.11-alpine", postgres.WithDatabase("source"), postgres.WithUsername("restore_test"), postgres.WithPassword("disposable-test-only"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Terminate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrations.Up(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	identity := createRestoreProof(t, ctx, db)
	run := restoreContainerCommand(t, c)
	if run("pg_dump", "-U", "restore_test", "-d", "source", "-Fc", "--no-owner", "--no-acl", "-f", "/tmp/restore-test.dump") != 0 {
		t.Fatal("dump failed")
	}
	reader, err := c.CopyFileFromContainer(ctx, "/tmp/restore-test.dump")
	if err != nil {
		t.Fatal(err)
	}
	dump, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	recovered := authenticateRestoreDump(t, f, dump)
	if err := c.CopyToContainer(ctx, recovered, "/tmp/authenticated.dump", 0600); err != nil {
		t.Fatal(err)
	}
	if run("createdb", "-U", "restore_test", "restored") != 0 {
		t.Fatal("create failed")
	}
	if run("pg_restore", "-U", "restore_test", "-d", "restored", "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", "/tmp/authenticated.dump") != 0 {
		t.Fatal("valid restore failed")
	}
	restoredDB, err := sql.Open("pgx", strings.Replace(dsn, "/source?", "/restored?", 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restoredDB.Close() })
	var version int64
	if err := restoredDB.QueryRowContext(ctx, `WITH latest AS (SELECT DISTINCT ON(version_id) version_id,is_applied FROM public.goose_db_version ORDER BY version_id,id DESC) SELECT COALESCE(MAX(version_id),0) FROM latest WHERE is_applied`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != migrations.Latest() {
		t.Fatalf("restored schema %d != %d", version, migrations.Latest())
	}
	restoreKeyProof(t, ctx, f, identity, restoredDB)
	// Compare the restored Goose observation with an intentionally wrong
	// manifest version using the same query; this must not authorize resume.
	var matches bool
	if err := restoredDB.QueryRowContext(ctx, `WITH latest AS (SELECT DISTINCT ON(version_id) version_id,is_applied FROM public.goose_db_version ORDER BY version_id,id DESC) SELECT COALESCE(MAX(version_id),0) = $1 FROM latest WHERE is_applied`, version+1).Scan(&matches); err != nil || matches {
		t.Fatalf("schema mismatch gate: matches=%v err=%v", matches, err)
	}
	// A second restore into the populated target must fail and roll back, not
	// be treated as success because some objects/data already exist.
	if run("pg_restore", "-U", "restore_test", "-d", "restored", "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl", "/tmp/authenticated.dump") == 0 {
		t.Fatal("restore SQL error ignored")
	}
	t.Logf("PASS: PostgreSQL 17.11, Goose %d, authenticated dump/keys, optional trust, encrypted column; missing-key/schema-mismatch/restore-error rejected. No management database or writer touched.", version)
}

func createRestoreProof(t *testing.T, ctx context.Context, db *sql.DB) *age.X25519Identity {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := columncrypto.NewEncryptor(identity.String())
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := enc.Encrypt([]byte("synthetic restored credential"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE public.restore_proof(key_id text, algorithm text, ciphertext bytea, nonce bytea)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO public.restore_proof VALUES($1,$2,$3,$4)`, sealed.KeyID, sealed.Algorithm, sealed.Ciphertext, sealed.Nonce); err != nil {
		t.Fatal(err)
	}
	return identity
}

func authenticateRestoreDump(t *testing.T, f fixture, dump []byte) []byte {
	t.Helper()
	plain := f.write(t, "dump", dump)
	sealedPath := filepath.Join(f.root, "dump.aead")
	restored := filepath.Join(f.root, "restored.dump")
	if err := EncryptFile(plain, sealedPath, f.passphrase, "database-dump", f.context); err != nil {
		t.Fatal(err)
	}
	if err := DecryptFile(sealedPath, restored, filepath.Join(f.root, "missing-key"), "database-dump", f.context); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err := os.Stat(restored); !os.IsNotExist(err) {
		t.Fatal("missing key exposed dump")
	}
	if err := DecryptFile(sealedPath, restored, f.passphrase, "database-dump", f.context); err != nil {
		t.Fatal(err)
	}
	recovered, err := os.ReadFile(restored)
	if err != nil || !bytes.Equal(recovered, dump) {
		t.Fatal("dump did not authenticate/roundtrip")
	}
	return recovered
}

func restoreKeyProof(t *testing.T, ctx context.Context, f fixture, identity *age.X25519Identity, restoredDB *sql.DB) {
	t.Helper()
	// Bundle recovery includes both optional trust directories and the actual
	// key used by the application's column-encryption implementation.
	source := filepath.Join(f.root, "keys")
	if err := os.MkdirAll(filepath.Join(source, "catalog-ca"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "catalog-signature-trust"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"SECRET_KEY": "synthetic-signing-key", "ASTRONOMER_ENCRYPTION_KEY": identity.String(), "catalog-ca/ca.pem": "synthetic-ca", "catalog-signature-trust/key.pub": "synthetic-trust"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundle := filepath.Join(f.root, "keys.tar")
	bundleCipher := filepath.Join(f.root, "keys.aead")
	bundlePlain := filepath.Join(f.root, "keys-restored.tar")
	keys := filepath.Join(f.root, "recovered-keys")
	for _, err := range []error{CreateKeyBundle(source, bundle), EncryptFile(bundle, bundleCipher, f.passphrase, "key-bundle", f.context), DecryptFile(bundleCipher, bundlePlain, f.passphrase, "key-bundle", f.context), ExtractKeyBundle(bundlePlain, keys)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	key, err := os.ReadFile(filepath.Join(keys, "ASTRONOMER_ENCRYPTION_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	recoveredEnc, err := columncrypto.NewEncryptor(string(key))
	if err != nil {
		t.Fatal(err)
	}
	var proof columncrypto.Sealed
	if err := restoredDB.QueryRowContext(ctx, `SELECT key_id,algorithm,ciphertext,nonce FROM public.restore_proof`).Scan(&proof.KeyID, &proof.Algorithm, &proof.Ciphertext, &proof.Nonce); err != nil {
		t.Fatal(err)
	}
	value, err := recoveredEnc.Decrypt(proof)
	if err != nil || string(value) != "synthetic restored credential" {
		t.Fatal("restored encrypted column not readable")
	}
	for _, name := range []string{"catalog-ca/ca.pem", "catalog-signature-trust/key.pub"} {
		if _, err := os.ReadFile(filepath.Join(keys, name)); err != nil {
			t.Fatal(err)
		}
	}

}

func restoreContainerCommand(t *testing.T, c *postgres.PostgresContainer) func(...string) int {
	t.Helper()
	ctx := t.Context()
	return func(args ...string) int {
		t.Helper()
		code, out, err := c.Exec(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		diagnostic, _ := io.ReadAll(out)
		if code != 0 {
			t.Logf("command %s failed as observed (exit %d): %s", args[0], code, diagnostic)
		}
		return code
	}
}
