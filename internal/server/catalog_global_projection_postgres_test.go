package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Source dependency: the reviewed compliance snapshot ownership helper and its
// permanent cleanup guard. Qualification injects lease identity only after
// native allocation fences; current canonical schema is required, never 78/79
// guessed. No customer tables or roles are discovered or changed.
func TestCatalogGlobalProjectionPostgresOwnerInterleavings(t *testing.T) {
	for _, direction := range []string{"global-to-private", "private-to-global"} {
		for _, action := range []string{"insert", "rename", "move-out", "move-in", "delete"} {
			t.Run(direction+"/"+action, func(t *testing.T) {
				ctx, database, guard := complianceSnapshotOwnedDatabase(t)
				prefix := "w07-projection-" + uuid.NewString()
				cluster, project, watched, other, chart := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
				repos := []uuid.UUID{watched, other}
				t.Cleanup(func() {
					if !guard.mayCleanup() {
						t.Error("unjoined projection worker: exact owned rows retained")
						return
					}
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					commands := []struct {
						sql  string
						args []any
					}{
						{`DELETE FROM helm_charts WHERE id=$1 AND repository_id=ANY($2::uuid[])`, []any{chart, repos}},
						{`DELETE FROM helm_repositories WHERE id=ANY($1::uuid[]) AND name LIKE $2`, []any{repos, prefix + "%"}},
						{`DELETE FROM projects WHERE id=$1 AND cluster_id=$2 AND name=$3`, []any{project, cluster, prefix}},
						{`DELETE FROM clusters WHERE id=$1 AND name=$2`, []any{cluster, prefix}},
					}
					for _, command := range commands {
						if _, err := database.Pool().Exec(cleanup, command.sql, command.args...); err != nil {
							t.Error("exact projection fixture cleanup failed")
						}
					}
				})
				setup := []struct {
					sql  string
					args []any
				}{
					{`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, []any{cluster, prefix}},
					{`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, []any{project, prefix, cluster}},
					{`INSERT INTO helm_repositories(id,name,url) VALUES($1,$2,'https://example.invalid/owned')`, []any{other, prefix + "-other"}},
				}
				for _, command := range setup {
					if _, err := database.Pool().Exec(ctx, command.sql, command.args...); err != nil {
						t.Fatal("owned projection setup failed")
					}
				}
				var initialOwner, finalOwner any
				if direction == "global-to-private" {
					finalOwner = project
				} else {
					initialOwner = project
				}
				if _, err := database.Pool().Exec(ctx, `INSERT INTO helm_repositories(id,name,url,owner_project_id) VALUES($1,$2,'https://example.invalid/owned',$3)`, watched, prefix+"-watched", initialOwner); err != nil {
					t.Fatal(err)
				}
				initialRepo := watched
				if action == "move-in" {
					initialRepo = other
				}
				if action != "insert" {
					if _, err := database.Pool().Exec(ctx, `INSERT INTO helm_charts(id,repository_id,name) VALUES($1,$2,$3)`, chart, initialRepo, prefix); err != nil {
						t.Fatal(err)
					}
				}
				ownerConnection, err := database.Pool().Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if guard.mayCleanup() {
						ownerConnection.Release()
					} else {
						t.Error("unjoined projection worker: owner connection retained")
					}
				})
				ownerTx, err := ownerConnection.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if !guard.mayCleanup() {
						t.Error("unjoined projection worker: owner transaction retained")
						return
					}
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					_ = ownerTx.Rollback(cleanup)
				})
				var ownerPID int
				if err := ownerTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&ownerPID); err != nil {
					t.Fatal(err)
				}
				if _, err := ownerTx.Exec(ctx, `UPDATE helm_repositories SET owner_project_id=$2 WHERE id=$1`, watched, finalOwner); err != nil {
					t.Fatal(err)
				}
				workerPID := make(chan int, 1)
				mutate, cancelMutation := complianceSnapshotMutation(t, ctx, database, guard, func(mutationCtx context.Context, tx pgx.Tx) error {
					var pid int
					if err := tx.QueryRow(mutationCtx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
						return err
					}
					workerPID <- pid
					var sql string
					var args []any
					switch action {
					case "insert":
						sql = `INSERT INTO helm_charts(id,repository_id,name) VALUES($1,$2,$3)`
						args = []any{chart, watched, prefix}
					case "rename":
						sql = `UPDATE helm_charts SET name=$2 WHERE id=$1`
						args = []any{chart, prefix + "-renamed"}
					case "move-out":
						sql = `UPDATE helm_charts SET repository_id=$2 WHERE id=$1`
						args = []any{chart, other}
					case "move-in":
						sql = `UPDATE helm_charts SET repository_id=$2 WHERE id=$1`
						args = []any{chart, watched}
					case "delete":
						sql = `DELETE FROM helm_charts WHERE id=$1`
						args = []any{chart}
					}
					result, err := tx.Exec(mutationCtx, sql, args...)
					if err != nil {
						return err
					}
					if result.RowsAffected() != 1 {
						return errors.New("owned chart mutation cardinality changed")
					}
					return nil
				})
				defer cancelMutation()
				returned := make(chan struct{})
				var mutationError error
				t.Cleanup(func() {
					cancelMutation()
					select {
					case <-returned:
					case <-time.After(5 * time.Second):
						guard.joinFailed = true
						t.Error("projection trigger caller did not join; cleanup retained")
					}
				})
				go func() { defer close(returned); mutationError = mutate() }()
				var pid int
				select {
				case pid = <-workerPID:
				case <-ctx.Done():
					t.Fatal("owned chart mutation did not enter")
				}
				observationCtx, stop := context.WithTimeout(ctx, 2*time.Second)
				blocked := false
				for !blocked {
					err := database.Pool().QueryRow(observationCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND $2=ANY(pg_blocking_pids(pid)) AND (query LIKE 'INSERT INTO helm_charts%' OR query LIKE 'UPDATE helm_charts%' OR query LIKE 'DELETE FROM helm_charts%'))`, pid, ownerPID).Scan(&blocked)
					if err != nil {
						stop()
						t.Fatal("owner flip did not causally block the actual chart mutation")
					}
					if !blocked {
						select {
						case <-time.After(5 * time.Millisecond):
						case <-observationCtx.Done():
							stop()
							t.Fatal("owner/chart interleaving was not reached")
						}
					}
				}
				stop()
				if err := ownerTx.Commit(ctx); err != nil {
					t.Fatal("owned owner flip failed")
				}
				select {
				case <-returned:
				case <-ctx.Done():
					t.Fatal("chart mutation did not finish after owner commit")
				}
				if mutationError != nil {
					t.Fatal("owned chart mutation failed after visibility commit", mutationError)
				}
				var private, missing, stale, keys int
				err = database.Pool().QueryRow(ctx, `SELECT
(SELECT count(*) FROM catalog_global_chart_keys k JOIN helm_repositories r ON r.id=k.repository_id WHERE k.repository_id=ANY($1::uuid[]) AND r.owner_project_id IS NOT NULL),
(SELECT count(*) FROM helm_charts c JOIN helm_repositories r ON r.id=c.repository_id WHERE c.repository_id=ANY($1::uuid[]) AND r.owner_project_id IS NULL AND NOT EXISTS(SELECT 1 FROM catalog_global_chart_keys k WHERE k.chart_id=c.id AND k.repository_id=c.repository_id AND k.name=c.name)),
(SELECT count(*) FROM catalog_global_chart_keys k LEFT JOIN helm_charts c ON c.id=k.chart_id WHERE k.repository_id=ANY($1::uuid[]) AND (c.id IS NULL OR c.repository_id<>k.repository_id OR c.name<>k.name)),
(SELECT count(*) FROM catalog_global_chart_keys WHERE chart_id=$2)`, repos, chart).Scan(&private, &missing, &stale, &keys)
				expected := 1
				if action == "delete" || (direction == "global-to-private" && action != "move-out") {
					expected = 0
				}
				if err != nil || private != 0 || missing != 0 || stale != 0 || keys != expected {
					t.Fatal(fmt.Sprintf("committed projection invariant private=%d missing=%d stale=%d keys=%d expected=%d", private, missing, stale, keys, expected))
				}
			})
		}
	}
}

func TestCatalogGlobalProjectionPostgresChartFirstInterleavings(t *testing.T) {
	for _, direction := range []string{"global-to-private", "private-to-global"} {
		for _, action := range []string{"insert", "rename", "move-out", "move-in", "delete"} {
			t.Run(direction+"/"+action, func(t *testing.T) {
				ctx, database, guard := complianceSnapshotOwnedDatabase(t)
				prefix := "w07-projection-reverse-" + uuid.NewString()
				cluster, project, watched, other, chart := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
				repos := []uuid.UUID{watched, other}
				t.Cleanup(func() {
					if !guard.mayCleanup() {
						t.Error("unjoined projection worker: exact owned rows retained")
						return
					}
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					commands := []struct {
						sql  string
						args []any
					}{
						{`DELETE FROM helm_charts WHERE id=$1 AND repository_id=ANY($2::uuid[])`, []any{chart, repos}},
						{`DELETE FROM helm_repositories WHERE id=ANY($1::uuid[]) AND name LIKE $2`, []any{repos, prefix + "%"}},
						{`DELETE FROM projects WHERE id=$1 AND cluster_id=$2 AND name=$3`, []any{project, cluster, prefix}},
						{`DELETE FROM clusters WHERE id=$1 AND name=$2`, []any{cluster, prefix}},
					}
					for _, command := range commands {
						if _, err := database.Pool().Exec(cleanup, command.sql, command.args...); err != nil {
							t.Error("exact projection fixture cleanup failed")
						}
					}
				})
				setup := []struct {
					sql  string
					args []any
				}{
					{`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, []any{cluster, prefix}},
					{`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, []any{project, prefix, cluster}},
					{`INSERT INTO helm_repositories(id,name,url) VALUES($1,$2,'https://example.invalid/owned')`, []any{other, prefix + "-other"}},
				}
				for _, command := range setup {
					if _, err := database.Pool().Exec(ctx, command.sql, command.args...); err != nil {
						t.Fatal("owned projection setup failed")
					}
				}
				var initialOwner, finalOwner any
				if direction == "global-to-private" {
					finalOwner = project
				} else {
					initialOwner = project
				}
				if _, err := database.Pool().Exec(ctx, `INSERT INTO helm_repositories(id,name,url,owner_project_id) VALUES($1,$2,'https://example.invalid/owned',$3)`, watched, prefix+"-watched", initialOwner); err != nil {
					t.Fatal(err)
				}
				initialRepo := watched
				if action == "move-in" {
					initialRepo = other
				}
				if action != "insert" {
					if _, err := database.Pool().Exec(ctx, `INSERT INTO helm_charts(id,repository_id,name) VALUES($1,$2,$3)`, chart, initialRepo, prefix); err != nil {
						t.Fatal(err)
					}
				}
				ownerConnection, err := database.Pool().Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if guard.mayCleanup() {
						ownerConnection.Release()
					} else {
						t.Error("unjoined projection worker: owner connection retained")
					}
				})
				ownerTx, err := ownerConnection.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if !guard.mayCleanup() {
						t.Error("unjoined projection worker: owner transaction retained")
						return
					}
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					_ = ownerTx.Rollback(cleanup)
				})
				var ownerPID int
				if err := ownerTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&ownerPID); err != nil {
					t.Fatal(err)
				}
				if _, err := ownerTx.Exec(ctx, `SET LOCAL statement_timeout='5s'; SET LOCAL lock_timeout='3s'`); err != nil {
					t.Fatal(err)
				}
				chartReady, releaseChart := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				releaseMutation := func() { releaseOnce.Do(func() { close(releaseChart) }) }
				t.Cleanup(releaseMutation)
				workerPID := make(chan int, 1)
				mutate, cancelMutation := complianceSnapshotMutation(t, ctx, database, guard, func(mutationCtx context.Context, tx pgx.Tx) error {
					var pid int
					if err := tx.QueryRow(mutationCtx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
						return err
					}
					workerPID <- pid
					var sql string
					var args []any
					switch action {
					case "insert":
						sql = `INSERT INTO helm_charts(id,repository_id,name) VALUES($1,$2,$3)`
						args = []any{chart, watched, prefix}
					case "rename":
						sql = `UPDATE helm_charts SET name=$2 WHERE id=$1`
						args = []any{chart, prefix + "-renamed"}
					case "move-out":
						sql = `UPDATE helm_charts SET repository_id=$2 WHERE id=$1`
						args = []any{chart, other}
					case "move-in":
						sql = `UPDATE helm_charts SET repository_id=$2 WHERE id=$1`
						args = []any{chart, watched}
					case "delete":
						sql = `DELETE FROM helm_charts WHERE id=$1`
						args = []any{chart}
					}
					result, err := tx.Exec(mutationCtx, sql, args...)
					if err != nil {
						return err
					}
					if result.RowsAffected() != 1 {
						return errors.New("owned chart mutation cardinality changed")
					}
					close(chartReady)
					select {
					case <-releaseChart:
						return nil
					case <-mutationCtx.Done():
						return mutationCtx.Err()
					}
				})
				defer cancelMutation()
				returned := make(chan struct{})
				var mutationError error
				t.Cleanup(func() {
					cancelMutation()
					select {
					case <-returned:
					case <-time.After(5 * time.Second):
						guard.joinFailed = true
						t.Error("projection trigger caller did not join; cleanup retained")
					}
				})
				go func() { defer close(returned); mutationError = mutate() }()
				var pid int
				select {
				case pid = <-workerPID:
				case <-ctx.Done():
					t.Fatal("owned chart mutation did not enter")
				}
				select {
				case <-chartReady:
				case <-ctx.Done():
					t.Fatal("chart trigger did not acquire parent visibility locks")
				}
				ownerCtx, cancelOwner := context.WithCancel(ctx)
				defer cancelOwner()
				ownerReturned := make(chan struct{})
				var ownerError error
				t.Cleanup(func() {
					cancelOwner()
					select {
					case <-ownerReturned:
					case <-time.After(5 * time.Second):
						guard.joinFailed = true
						t.Error("blocked owner caller did not join; cleanup retained")
					}
				})
				go func() {
					defer close(ownerReturned)
					_, ownerError = ownerTx.Exec(ownerCtx, `UPDATE helm_repositories SET owner_project_id=$2 WHERE id=$1`, watched, finalOwner)
					if ownerError == nil {
						ownerError = ownerTx.Commit(ownerCtx)
					}
				}()
				observationCtx, stop := context.WithTimeout(ctx, 2*time.Second)
				blocked := false
				for !blocked {
					err := database.Pool().QueryRow(observationCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND $2=ANY(pg_blocking_pids(pid)) AND query LIKE 'UPDATE helm_repositories SET owner_project_id=%')`, ownerPID, pid).Scan(&blocked)
					if err != nil {
						stop()
						t.Fatal("chart parent locks did not causally block actual owner update")
					}
					if !blocked {
						select {
						case <-time.After(5 * time.Millisecond):
						case <-observationCtx.Done():
							stop()
							t.Fatal("chart/owner reverse interleaving was not reached")
						}
					}
				}
				stop()
				releaseMutation()
				select {
				case <-returned:
				case <-ctx.Done():
					t.Fatal("chart mutation did not commit before owner backfill")
				}
				if mutationError != nil {
					t.Fatal("owned chart-first mutation failed", mutationError)
				}
				select {
				case <-ownerReturned:
				case <-ctx.Done():
					t.Fatal("owner flip did not finish after chart commit")
				}
				if ownerError != nil {
					t.Fatal("owned owner backfill failed after chart commit", ownerError)
				}
				var private, missing, stale, keys int
				err = database.Pool().QueryRow(ctx, `SELECT
(SELECT count(*) FROM catalog_global_chart_keys k JOIN helm_repositories r ON r.id=k.repository_id WHERE k.repository_id=ANY($1::uuid[]) AND r.owner_project_id IS NOT NULL),
(SELECT count(*) FROM helm_charts c JOIN helm_repositories r ON r.id=c.repository_id WHERE c.repository_id=ANY($1::uuid[]) AND r.owner_project_id IS NULL AND NOT EXISTS(SELECT 1 FROM catalog_global_chart_keys k WHERE k.chart_id=c.id AND k.repository_id=c.repository_id AND k.name=c.name)),
(SELECT count(*) FROM catalog_global_chart_keys k LEFT JOIN helm_charts c ON c.id=k.chart_id WHERE k.repository_id=ANY($1::uuid[]) AND (c.id IS NULL OR c.repository_id<>k.repository_id OR c.name<>k.name)),
(SELECT count(*) FROM catalog_global_chart_keys WHERE chart_id=$2)`, repos, chart).Scan(&private, &missing, &stale, &keys)
				expected := 1
				if action == "delete" || (direction == "global-to-private" && action != "move-out") {
					expected = 0
				}
				if err != nil || private != 0 || missing != 0 || stale != 0 || keys != expected {
					t.Fatal(fmt.Sprintf("committed projection invariant private=%d missing=%d stale=%d keys=%d expected=%d", private, missing, stale, keys, expected))
				}
			})
		}
	}
}

func TestCatalogGlobalProjectionPostgresRepositoryCascade(t *testing.T) {
	ctx, database, guard := complianceSnapshotOwnedDatabase(t)
	repo, chart := uuid.New(), uuid.New()
	prefix := "w07-projection-cascade-" + uuid.NewString()
	t.Cleanup(func() {
		if !guard.mayCleanup() {
			t.Error("unsafe projection cascade cleanup retained")
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := database.Pool().Exec(cleanup, `DELETE FROM helm_repositories WHERE id=$1 AND name=$2`, repo, prefix); err != nil {
			t.Error("owned cascade repository cleanup failed")
		}
	})
	if _, err := database.Pool().Exec(ctx, `INSERT INTO helm_repositories(id,name,url) VALUES($1,$2,'https://example.invalid/owned')`, repo, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool().Exec(ctx, `INSERT INTO helm_charts(id,repository_id,name) VALUES($1,$2,$3)`, chart, repo, prefix); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := database.Pool().QueryRow(ctx, `SELECT count(*) FROM catalog_global_chart_keys WHERE chart_id=$1 AND repository_id=$2`, chart, repo).Scan(&before); err != nil || before != 1 {
		t.Fatal("global fixture key did not exist before repository deletion")
	}
	result, err := database.Pool().Exec(ctx, `DELETE FROM helm_repositories WHERE id=$1 AND name=$2`, repo, prefix)
	if err != nil || result.RowsAffected() != 1 {
		t.Fatal("exact repository deletion failed")
	}
	var charts, keys int
	if err := database.Pool().QueryRow(ctx, `SELECT (SELECT count(*) FROM helm_charts WHERE id=$1),(SELECT count(*) FROM catalog_global_chart_keys WHERE chart_id=$1 OR repository_id=$2)`, chart, repo).Scan(&charts, &keys); err != nil || charts != 0 || keys != 0 {
		t.Fatal("repository cascade retained chart/projection key")
	}
}
