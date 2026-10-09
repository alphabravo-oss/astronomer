package builtin

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresBuiltinCanceledTransactionReleasesSessionLock(t *testing.T) {
	dsn := os.Getenv("BUILTIN_PROVISIONER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BUILTIN_PROVISIONER_TEST_DATABASE_URL is not set")
	}
	ctx, stop := context.WithTimeout(t.Context(), 15*time.Second)
	defer stop()
	pool := openBuiltinProvisionerTestPool(t, ctx, dsn)
	defer pool.Close()
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	stageKey := 1 + time.Now().UnixNano()%1_000_000_000
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_lock($1)`, stageKey); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := blocker.QueryRow(cleanup, `SELECT pg_advisory_unlock($1)`, stageKey).Scan(&unlocked); err != nil || !unlocked {
			t.Error("fixture stage lock cleanup failed")
		}
	}()
	statement := fmt.Sprintf(`CREATE FUNCTION pause_builtin_project() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
	CREATE TRIGGER pause_builtin_project BEFORE INSERT ON projects FOR EACH ROW EXECUTE FUNCTION pause_builtin_project()`, stageKey)
	if _, err := pool.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
	cluster := uuid.New()
	catalog := loadBuiltinTestCatalog(t)
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := (&Provisioner{pool: pool}).ensureAssetsTransaction(requestCtx, cluster, catalog)
		result <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory'
			AND classid=0 AND objid=$1 AND NOT granted)`, stageKey).Scan(&waiting); err != nil {
			t.Fatal("cannot observe fixture transaction lock")
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("transaction returned before fixture stage: %v", err)
		case <-ctx.Done():
			t.Fatal("transaction did not reach controlled stage")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled transaction succeeded")
		}
	case <-ctx.Done():
		t.Fatal("canceled transaction cleanup did not finish")
	}
	// pgx marks canceled connections closed before asynchronous server cleanup.
	// A replacement must acquire within the same bounded cleanup budget; an
	// instantaneous try-lock would incorrectly treat that transition as a leak.
	cleanup, finish := context.WithTimeout(ctx, 5*time.Second)
	defer finish()
	if _, err := blocker.Exec(cleanup, `SELECT pg_advisory_lock(hashtextextended('astronomer-builtins:' || $1::text,0))`, cluster); err != nil {
		t.Fatal("canceled built-in transaction leaked session lock")
	}
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('astronomer-builtins:' || $1::text,0))`, cluster); err != nil {
		t.Fatal("fixture cluster lock cleanup failed")
	}
	assertBuiltinProjectCounts(t, ctx, pool, stableID("project", cluster.String()), 0, 0, 0, 0)
}
