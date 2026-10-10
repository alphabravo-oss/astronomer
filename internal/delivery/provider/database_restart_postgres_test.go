//go:build unix && integration

package provider

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration-only, no skipped pseudo-qualification in ordinary package tests.
// The external owned runner must provide a genuine signed QA registry graph and
// create a uniquely owned DB through coherent migrations 078/079 before launch.
func TestDatabaseRestartPostgresAbsentAndActualInitialBase(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	if _, err := fixture.revoker.RevokeLostCurrent(t.Context()); !errors.Is(err, ErrDatabaseRestartBaseAbsent) {
		t.Fatal("locked absence was not distinguished from unknown failure")
	}
	base, err := fixture.baseOwner.Initialize(t.Context())
	if err != nil {
		t.Fatal("actual authenticated QA base initialization failed")
	}
	repeated, err := fixture.baseOwner.Initialize(t.Context())
	if err != nil || repeated != base {
		t.Fatal("initialization changed exact current base")
	}
	if _, err := fixture.revoker.RevokeLostCurrent(t.Context()); !errors.Is(err, distribution.ErrNoConfirmedDatabaseRestartTrustLoss) {
		t.Fatal("healthy restart mutated or failed retained base")
	}
	fixture.assertState(t, false, 0)
	fixture.assertBackendReleased(t)
}

func TestDatabaseRestartPostgresPermanentLossCannotResurrect(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	base, err := fixture.baseOwner.Initialize(t.Context())
	if err != nil {
		t.Fatal("actual authenticated QA base initialization failed")
	}
	original := fixture.vendorBytes
	if err := os.Remove(fixture.vendorPath); err != nil {
		t.Fatal(err)
	}
	lost, err := fixture.revoker.RevokeLostCurrent(t.Context())
	if err != nil || lost != base {
		t.Fatal("actual missing public key did not permanently revoke exact current base")
	}
	fixture.assertState(t, true, 0)
	repeated, err := fixture.revoker.RevokeLostCurrent(t.Context())
	if err != nil || repeated != base {
		t.Fatal("same exact loss was not idempotent")
	}
	if err := os.WriteFile(fixture.vendorPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.baseOwner.Initialize(t.Context()); err == nil {
		t.Fatal("restored physical key resurrected revoked base")
	}
	fixture.assertState(t, true, 0)
	fixture.assertBackendReleased(t)
}

func TestDatabaseRestartPostgresRestoredBeforeCommitRollsBack(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	if _, err := fixture.baseOwner.Initialize(t.Context()); err != nil {
		t.Fatal("actual authenticated QA base initialization failed")
	}
	barrier, err := fixture.pool.BeginTx(t.Context(), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = barrier.Rollback(ctx)
		}
	}()
	// The actual queue INSERT is blocked after the base revoke write, before the
	// production owner's final mounted-loss inspection and transaction commit.
	if _, err := barrier.Exec(t.Context(), "LOCK TABLE public.vulnerability_database_rescan_intents IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.vendorPath); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := fixture.revoker.RevokeLostCurrent(ctx); done <- err }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
				joined = true
			case <-time.After(7 * time.Second):
				t.Error("owned restart worker remains unjoined after deferred cancellation")
			}
		}
	}()
	if err := fixture.awaitBlocked(ctx, "vulnerability_database_rescan_intents"); err != nil {
		cancel()
		select {
		case <-done:
			joined = true
			t.Fatal("queue barrier failed; actual worker joined after cancellation")
		case <-time.After(7 * time.Second):
			t.Fatal("queue barrier failed; worker remains unjoined after cleanup deadline")
		}
	}
	if err := os.WriteFile(fixture.vendorPath, fixture.vendorBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	released = true
	select {
	case err := <-done:
		joined = true
		if err == nil {
			t.Fatal("restored physical trust still committed revocation")
		}
	case <-ctx.Done():
		cancel()
		select {
		case <-done:
			joined = true
			t.Fatal("revocation exceeded finite test deadline; joined after cancellation")
		case <-time.After(5 * time.Second):
			t.Fatal("revocation exceeded cleanup deadline; reader remains unjoined")
		}
	}
	fixture.assertState(t, false, 0)
	fixture.assertBackendReleased(t)
}

func TestDatabaseRestartPostgresCanceledAuthorityLockReleasesBackend(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	if _, err := fixture.baseOwner.Initialize(t.Context()); err != nil {
		t.Fatal("actual authenticated QA base initialization failed")
	}
	barrier, err := fixture.pool.BeginTx(t.Context(), pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = barrier.Rollback(ctx)
		}
	}()
	if err := sqlc.New(barrier).LockVulnerabilityDatabaseAuthority(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := fixture.revoker.RevokeLostCurrent(ctx); done <- err }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
				joined = true
			case <-time.After(7 * time.Second):
				t.Error("owned restart worker remains unjoined after deferred cancellation")
			}
		}
	}()
	if err := fixture.awaitBlocked(ctx, "pg_advisory_xact_lock"); err != nil {
		cancel()
		select {
		case <-done:
			joined = true
			t.Fatal("authority barrier failed; actual worker joined after cancellation")
		case <-time.After(7 * time.Second):
			t.Fatal("authority barrier failed; worker remains unjoined after cleanup deadline")
		}
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled actual authority wait lost canonical cancellation")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("canceled authority wait did not join; backend cleanup unqualified")
	}
	cleanup, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCleanup()
	if err := barrier.Rollback(cleanup); err != nil {
		t.Fatal(err)
	}
	released = true
	fixture.assertState(t, false, 0)
	fixture.assertBackendReleased(t)
}

func (fixture *databaseRestartPostgresFixture) assertState(t *testing.T, revoked bool, outbox int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var actualRevoked bool
	var bases, actualOutbox int64
	if err := fixture.pool.QueryRow(ctx, "SELECT count(*),bool_or(revoked_at IS NOT NULL) FROM public.vulnerability_database_bases").Scan(&bases, &actualRevoked); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(ctx, "SELECT count(*) FROM public.vulnerability_database_rescan_intents").Scan(&actualOutbox); err != nil {
		t.Fatal(err)
	}
	if bases != 1 || actualRevoked != revoked || actualOutbox != outbox {
		t.Fatal("unexpected actual permanent base/outbox transaction state")
	}
}

func (fixture *databaseRestartPostgresFixture) awaitBlocked(ctx context.Context, queryFragment string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := fixture.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1 AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $2", fixture.applicationName, "%"+queryFragment+"%").Scan(&count)
		if err != nil {
			return errors.New("actual blocked transaction observation failed")
		}
		if count == 1 {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("actual owner never reached bounded SQL barrier")
		case <-ticker.C:
		}
	}
}

func (fixture *databaseRestartPostgresFixture) assertBackendReleased(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var held int
	err := fixture.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1 AND pid<>pg_backend_pid() AND (state='idle in transaction' OR wait_event_type='Lock')", fixture.applicationName).Scan(&held)
	if err != nil || held != 0 {
		t.Fatal("owned backend still has transaction or lock wait")
	}
	conn, err := fixture.pool.Acquire(ctx)
	if err != nil {
		t.Fatal("returned pool capacity unavailable")
	}
	defer conn.Release()
	var locks int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND pid IN(SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1)", fixture.applicationName).Scan(&locks); err != nil || locks != 0 {
		t.Fatal("owned backend retained advisory lock")
	}
}

func verifyOwnedDatabaseRestartPool(t *testing.T, pool *pgxpool.Pool, major int) {
	t.Helper()
	expectedName := os.Getenv("W04_TEST_DATABASE_NAME")
	lease, leaseError := uuid.Parse(os.Getenv("W04_TEST_DATABASE_LEASE"))
	expectedOID, oidError := strconv.ParseUint(os.Getenv("W04_TEST_DATABASE_OID"), 10, 32)
	if leaseError != nil || lease.Version() != 4 || oidError != nil || expectedOID == 0 || expectedName != "plan032_w04_"+strings.ReplaceAll(lease.String(), "-", "") {
		t.Fatal("exact newly created runner-owned database lease identity missing")
	}
	var database, mountedLease string
	var version int
	var actualOID uint32
	if err := pool.QueryRow(t.Context(), "SELECT current_database(),current_setting('server_version_num')::integer,(SELECT oid FROM pg_database WHERE datname=current_database()),current_setting('astronomer.plan032_w04_lease',true)").Scan(&database, &version, &actualOID, &mountedLease); err != nil {
		t.Fatal("owned PostgreSQL identity unavailable")
	}
	if database != expectedName || uint64(actualOID) != expectedOID || mountedLease != lease.String() || version/10000 != major || major != 17 && major != 18 {
		t.Fatal("fixture is not exact uniquely owned selected PostgreSQL lane")
	}
	var schema int
	if err := pool.QueryRow(t.Context(), "SELECT version FROM public.schema_migrations WHERE dirty=false").Scan(&schema); err != nil || schema != 79 {
		t.Fatal("coherent selected schema079 not installed")
	}
}
