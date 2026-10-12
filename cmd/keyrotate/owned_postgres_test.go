package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	appdb "github.com/alphabravocompany/astronomer-go/internal/db"
)

const rotationFixtureLock int64 = 328016

func ownedRotationDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	path := os.Getenv("ASTRONOMER_ROTATION_DATABASE_REF")
	if path == "" {
		t.Skip("owned rotation database reference not configured")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatal("private regular rotation reference required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read private rotation reference")
	}
	identity := sha256.Sum256(raw)
	if expected := os.Getenv("ASTRONOMER_ROTATION_REFERENCE_SHA256"); expected == "" || expected != hex.EncodeToString(identity[:]) {
		t.Fatal("reference must match immediately preceding runtime ownership fence")
	}
	var ref struct {
		URL        string `json:"database_url"`
		Database   string `json:"database"`
		ClusterUID string `json:"cluster_uid"`
		Primary    string `json:"primary_at_allocation"`
	}
	if json.Unmarshal(raw, &ref) != nil || ref.Database != "plan032_w16_rotation" || ref.ClusterUID != "ea0f410a-e1ea-4cb4-9fe5-40a35a020d3e" {
		t.Fatal("exclusive owned rotation reference required")
	}
	u, err := url.Parse(ref.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" || u.Path != "/"+ref.Database || u.Fragment != "" {
		t.Fatal("exclusive rotation database URL required")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		t.Fatal("invalid owned database query")
	}
	for key, values := range query {
		if len(values) != 1 {
			t.Fatal("duplicate owned database query option")
		}
		switch key {
		case "sslmode", "sslrootcert", "connect_timeout", "application_name":
		default:
			t.Fatal("owned database connection override rejected")
		}
	}
	cfg, err := pgx.ParseConfig(ref.URL)
	if err != nil {
		t.Fatal("parse rotation database configuration")
	}
	if cfg.Database != ref.Database {
		t.Fatal("owned database parser identity mismatch")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || cfg.Host != u.Hostname() || cfg.Port != uint16(port) || cfg.RuntimeParams["options"] != "" {
		t.Fatal("parsed connection differs from the runtime-fenced endpoint")
	}
	cfg.RuntimeParams["application_name"] = "plan032-w16-rotation"
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	var name string
	var version, schema int64
	var dirty bool
	if db.QueryRowContext(ctx, `SELECT current_database(), current_setting('server_version_num')::bigint`).Scan(&name, &version) != nil || name != ref.Database || version/10000 != 18 {
		t.Fatal("owned PostgreSQL18 identity required before fixture writes")
	}
	if db.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&schema, &dirty) != nil || dirty || schema != appdb.ExpectedSchemaVersion {
		t.Fatal("exact current migrated schema required before fixture writes")
	}
	t.Logf("owned PostgreSQL major=%d schema=%d", version/10000, schema)
	return db, ctx
}

type rotationKeys struct{ old, primary, mixed *auth.Encryptor }

func syntheticRotationKeys(t *testing.T) rotationKeys {
	t.Helper()
	old, err := auth.GenerateKey()
	if err != nil {
		t.Fatal("generate old synthetic key")
	}
	primary, err := auth.GenerateKey()
	if err != nil {
		t.Fatal("generate primary synthetic key")
	}
	makeEncryptor := func(value string) *auth.Encryptor {
		e, err := auth.NewEncryptor(value)
		if err != nil {
			t.Fatal("initialize synthetic encryptor")
		}
		return e
	}
	return rotationKeys{makeEncryptor("old:" + old), makeEncryptor("current:" + primary), makeEncryptor("current:" + primary + ",old:" + old)}
}

func fixtureEncrypt(t *testing.T, e *auth.Encryptor, value string) string {
	t.Helper()
	ciphertext, err := e.Encrypt(value)
	if err != nil {
		t.Fatal("encrypt synthetic fixture")
	}
	return ciphertext
}

func seedRotationRows(t *testing.T, ctx context.Context, db *sql.DB, keys rotationKeys) ([2]uuid.UUID, string) {
	t.Helper()
	var count int
	if db.QueryRowContext(ctx, `SELECT count(*) FROM smtp_settings`).Scan(&count) != nil || count != 0 {
		t.Fatal("rotation fixture requires an empty owned SMTP table")
	}
	ids := [2]uuid.UUID{uuid.MustParse("00000000-0000-4000-8000-000000000001"), uuid.MustParse("00000000-0000-4000-8000-000000000002")}
	backup := fixtureEncrypt(t, keys.old, "synthetic-old-backup")
	for i, id := range ids {
		if _, err := db.ExecContext(ctx, `INSERT INTO smtp_settings (id,password_encrypted) VALUES ($1,$2)`, id, fixtureEncrypt(t, keys.old, "synthetic-row-"+string(rune('a'+i)))); err != nil {
			t.Fatal("seed owned rotation rows")
		}
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, `DELETE FROM smtp_settings WHERE id IN ($1,$2)`, ids[0], ids[1]); err != nil {
			t.Error("clean owned rotation rows")
		}
	})
	return ids, backup
}

func rotateFixture(ctx context.Context, db *sql.DB, keys rotationKeys, dryRun bool) stats {
	return rewriteColumn(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), db, keys.mixed, target{"smtp_settings", "id", "password_encrypted"}, 2, dryRun)
}

func fixtureCiphertext(t *testing.T, ctx context.Context, db *sql.DB, id uuid.UUID) string {
	t.Helper()
	var ciphertext string
	if db.QueryRowContext(ctx, `SELECT password_encrypted FROM smtp_settings WHERE id=$1`, id).Scan(&ciphertext) != nil {
		t.Fatal("read owned rotation row")
	}
	return ciphertext
}

func TestPostgresRotationCatalogCoversActualEncryptedColumns(t *testing.T) {
	db, ctx := ownedRotationDatabase(t)
	rows, err := db.QueryContext(ctx, `SELECT table_name,column_name FROM information_schema.columns WHERE table_schema='public' AND column_name LIKE '%encrypted%' AND data_type IN ('text','character varying','bytea') ORDER BY table_name,column_name`)
	if err != nil {
		t.Fatal("read actual encrypted column inventory")
	}
	defer func(close func() error) { _ = close() }(rows.Close)
	covered := map[string]bool{}
	for _, item := range rewriteTargets {
		covered[item.table+"."+item.column] = true
	}
	count := 0
	for rows.Next() {
		var table, column string
		if rows.Scan(&table, &column) != nil {
			t.Fatal("read encrypted column identity")
		}
		count++
		if !covered[table+"."+column] {
			t.Errorf("actual encrypted column missing rotation target: %s.%s", table, column)
		}
	}
	if rows.Err() != nil || count == 0 {
		t.Fatal("actual encrypted column inventory incomplete")
	}
	t.Logf("actual encrypted plain columns=%d", count)
}

func TestPostgresRotationDryRunPreservesRowsAndHistoricalKey(t *testing.T) {
	db, ctx := ownedRotationDatabase(t)
	keys := syntheticRotationKeys(t)
	ids, backup := seedRotationRows(t, ctx, db, keys)
	original := fixtureCiphertext(t, ctx, db, ids[0])
	if s := rotateFixture(ctx, db, keys, true); s.failed != 0 || s.rewrote != 2 {
		t.Fatalf("dry-run progress=%+v", s)
	}
	if fixtureCiphertext(t, ctx, db, ids[0]) != original {
		t.Fatal("dry-run changed ciphertext")
	}
	if s := rotateFixture(ctx, db, keys, false); s.failed != 0 || s.rewrote != 2 {
		t.Fatalf("rotation progress=%+v", s)
	}
	for _, id := range ids {
		ct := fixtureCiphertext(t, ctx, db, id)
		if !keys.primary.IsPrimaryCiphertext(ct) {
			t.Fatal("rotation did not promote envelope")
		}
		if _, err := keys.primary.Decrypt(ct); err != nil {
			t.Fatal("current key cannot read rotated value")
		}
		if _, err := keys.old.Decrypt(ct); err == nil {
			t.Fatal("old-only rollback unexpectedly reads new ciphertext")
		}
	}
	if _, err := keys.primary.Decrypt(backup); err == nil {
		t.Fatal("old backup unexpectedly readable without retained old key")
	}
	if value, err := keys.mixed.Decrypt(backup); err != nil || value != "synthetic-old-backup" {
		t.Fatal("independently retained historical ciphertext lost")
	}
	if s := rotateFixture(ctx, db, keys, false); s.failed != 0 || s.skipped != 2 || s.rewrote != 0 {
		t.Fatalf("idempotent rotation progress=%+v", s)
	}
}

// Restrict this fixture trigger to one owned row. It gates the actual CAS UPDATE
// on a separately held advisory lock without replacing the production rotator.
func pauseRotationRow(t *testing.T, ctx context.Context, db *sql.DB, id uuid.UUID) func() {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal("acquire fixture control session")
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, rotationFixtureLock); err != nil {
		_ = conn.Close()
		t.Fatal("hold fixture lock")
	}
	function := `CREATE FUNCTION plan032_rotation_pause() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.id='` + id.String() + `'::uuid THEN PERFORM pg_advisory_xact_lock(328016); END IF; RETURN NEW; END $$`
	if _, err = db.ExecContext(ctx, function); err != nil {
		t.Fatal("create owned fixture gate")
	}
	if _, err = db.ExecContext(ctx, `CREATE TRIGGER plan032_rotation_pause BEFORE UPDATE ON smtp_settings FOR EACH ROW EXECUTE FUNCTION plan032_rotation_pause()`); err != nil {
		t.Fatal("attach owned fixture gate")
	}
	unlocked := false
	unlock := func() {
		if unlocked {
			return
		}
		unlocked = true
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock($1)`, rotationFixtureLock); err != nil {
			t.Error("unlock owned fixture gate")
		}
		_ = conn.Close()
	}
	t.Cleanup(func() {
		unlock()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, `DROP TRIGGER IF EXISTS plan032_rotation_pause ON smtp_settings; DROP FUNCTION IF EXISTS plan032_rotation_pause()`); err != nil {
			t.Error("remove owned fixture gate")
		}
	})
	return unlock
}

func waitRotationBlocked(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var blocked bool
		if db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid=$1 AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`, rotationFixtureLock).Scan(&blocked) != nil {
			t.Fatal("read fixture blocking state")
		}
		if blocked {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("rotation did not reach actual blocked UPDATE")
		case <-ctx.Done():
			t.Fatal("fixture context expired")
		case <-tick.C:
		}
	}
}

func rotationResult(t *testing.T, result <-chan stats) stats {
	t.Helper()
	select {
	case s := <-result:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("rotation did not terminate within fixture bound")
		return stats{}
	}
}

func TestPostgresRotationInterruptionResumesCommittedPages(t *testing.T) {
	db, ctx := ownedRotationDatabase(t)
	for _, serverCancel := range []bool{false, true} {
		characterizeRotationCancellation(t, ctx, db, serverCancel)
	}
}

func TestPostgresRotationCASPreservesConcurrentWriter(t *testing.T) {
	db, ctx := ownedRotationDatabase(t)
	keys := syntheticRotationKeys(t)
	ids, _ := seedRotationRows(t, ctx, db, keys)
	unlock := pauseRotationRow(t, ctx, db, ids[0])
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan stats, 1)
	go func() { result <- rotateFixture(run, db, keys, false) }()
	waitRotationBlocked(t, ctx, db)
	concurrent := fixtureEncrypt(t, keys.mixed, "synthetic-concurrent-writer")
	if _, err := db.ExecContext(ctx, `UPDATE smtp_settings SET password_encrypted=$1 WHERE id=$2`, concurrent, ids[1]); err != nil {
		t.Fatal("execute concurrent production-format writer")
	}
	unlock()
	s := rotationResult(t, result)
	if s.rewrote != 1 || s.failed != 1 {
		t.Fatalf("CAS-conflict progress=%+v", s)
	}
	if fixtureCiphertext(t, ctx, db, ids[1]) != concurrent {
		t.Fatal("rotation clobbered concurrent writer")
	}
	if s := rotateFixture(ctx, db, keys, false); s.failed != 0 || s.skipped != 2 {
		t.Fatalf("CAS-conflict resume progress=%+v", s)
	}
}
