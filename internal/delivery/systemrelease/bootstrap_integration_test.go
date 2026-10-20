//go:build integration

package systemrelease

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func releasePostgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testdb.DSN(t)
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal("cannot connect to owned release test database")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := "w08_release_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid owned release database configuration")
	}
	config.MaxConns = 6
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("cannot create release test pool")
	}
	t.Cleanup(pool.Close)
	// Clone the migrated release table's real column, check, unique and identity
	// definitions instead of maintaining a second hand-written product schema.
	if _, err := pool.Exec(t.Context(), "CREATE TABLE delivery_system_releases (LIKE public.delivery_system_releases INCLUDING ALL)"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPostgresSystemReleaseBootstrapIdempotencyAndDraft(t *testing.T) {
	t.Parallel()
	pool := releasePostgresPool(t)
	config := validConfig()
	created, err := Ensure(t.Context(), pool, config)
	if err != nil || !created {
		t.Fatalf("first bootstrap created=%t error=%v", created, err)
	}
	created, err = Ensure(t.Context(), pool, config)
	if err != nil || created {
		t.Fatalf("retry created=%t error=%v", created, err)
	}
	config.Version = "1.0.1"
	created, err = Ensure(t.Context(), pool, config)
	if err != nil || !created {
		t.Fatalf("later draft created=%t error=%v", created, err)
	}
	var released, draft int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE state='released'),count(*) FILTER (WHERE state='draft') FROM delivery_system_releases`).Scan(&released, &draft); err != nil || released != 1 || draft != 1 {
		t.Fatalf("released=%d draft=%d error=%v", released, draft, err)
	}
}

func TestPostgresSystemReleaseBootstrapImmutableAndRevoked(t *testing.T) {
	t.Parallel()
	pool := releasePostgresPool(t)
	config := validConfig()
	if _, err := Ensure(t.Context(), pool, config); err != nil {
		t.Fatal(err)
	}
	spec, _, err := build(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE delivery_system_releases SET spec_digest=$2 WHERE version=$1`, spec.Version, "sha256:"+strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if created, err := Ensure(t.Context(), pool, config); created || err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable check created=%t error=%v", created, err)
	}
	_, digest, _ := build(config)
	if _, err := pool.Exec(t.Context(), `UPDATE delivery_system_releases SET spec_digest=$2,state='revoked' WHERE version=$1`, spec.Version, digest); err != nil {
		t.Fatal(err)
	}
	if created, err := Ensure(t.Context(), pool, config); created || err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revocation check created=%t error=%v", created, err)
	}
}

func TestPostgresSystemReleaseBootstrapRollbackAfterInsert(t *testing.T) {
	t.Parallel()
	pool := releasePostgresPool(t)
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION fail_promotion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic promotion failure' USING ERRCODE='P0001'; END; $$; CREATE TRIGGER fail_promotion BEFORE UPDATE ON delivery_system_releases FOR EACH ROW EXECUTE FUNCTION fail_promotion()`); err != nil {
		t.Fatal(err)
	}
	created, err := Ensure(t.Context(), pool, validConfig())
	var pgErr *pgconn.PgError
	if created || !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
		t.Fatalf("intermediate failure created=%t error=%v", created, err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM delivery_system_releases`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled back insertion count=%d error=%v", count, err)
	}
}

func TestPostgresSystemReleaseBootstrapSameTransactionLock(t *testing.T) {
	t.Parallel()
	pool := releasePostgresPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(82003208)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION hold_promotion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(82003208); RETURN NEW; END; $$; CREATE TRIGGER hold_promotion BEFORE UPDATE ON delivery_system_releases FOR EACH ROW EXECUTE FUNCTION hold_promotion()`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Ensure(ctx, pool, validConfig()); done <- err }()
	waitForReleaseLock(t, ctx, pool)
	var available bool
	if err := pool.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('astronomer-delivery-system-release',0))`).Scan(&available); err != nil || available {
		t.Fatalf("bootstrap lock lost before promotion commit: available=%t error=%v", available, err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('astronomer-delivery-system-release',0))`).Scan(&available); err != nil || !available {
		t.Fatalf("transaction advisory lock retained after commit: available=%t error=%v", available, err)
	}
}

func waitForReleaseLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND objid=82003208)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("bootstrap did not reach promotion within deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestPostgresSystemReleaseBootstrapConcurrentFirstCreate(t *testing.T) {
	t.Parallel()
	pool := releasePostgresPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('astronomer-delivery-system-release',0))`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		created bool
		err     error
	}
	done := make(chan result, 2)
	for range 2 {
		go func() { created, err := Ensure(ctx, pool, validConfig()); done <- result{created, err} }()
	}
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("two bootstrap transactions did not reach the lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	created, serializationFailures := 0, 0
	for range 2 {
		outcome := <-done
		if outcome.err == nil && outcome.created {
			created++
			continue
		}
		var pgErr *pgconn.PgError
		if !outcome.created && errors.As(outcome.err, &pgErr) && pgErr.Code == "40001" {
			serializationFailures++
			continue
		}
		t.Fatalf("unexpected concurrent bootstrap created=%t error=%v", outcome.created, outcome.err)
	}
	if created != 1 || serializationFailures != 1 {
		t.Fatalf("created=%d serialization failures=%d", created, serializationFailures)
	}
	if created, err := Ensure(ctx, pool, validConfig()); created || err != nil {
		t.Fatalf("retry did not converge: created=%t error=%v", created, err)
	}
	t.Log("baseline concurrent first insert: one committed creator, one exact SQLSTATE40001; explicit retry reads the same release")
}
