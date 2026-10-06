package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Opt-in real PostgreSQL contract for saved table views. The unit tests use an
// in-memory fake that cannot prove the advisory-lock cap, the unique indexes or
// the single-default invariant under parallel requests. The runner script
// scripts/test-postgres-integration.sh provides TABLE_VIEWS_TEST_DATABASE_URL
// for a migrated disposable database.

func tableViewsPostgresHandler(t *testing.T) (*TableViewsHandler, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TABLE_VIEWS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TABLE_VIEWS_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	runTx := func(ctx context.Context, fn func(TableViewsMutationTx) error) error {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := fn(sqlc.New(tx)); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	return NewTableViewsHandler(sqlc.New(pool), runTx), pool
}

func tableViewsTestUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, username) VALUES ($1, $2, $3)`,
		id, id.String()+"@table-views.test", "tv-"+id.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_outbox WHERE user_id = $1`, id)
	})
	return id
}

func tableViewsCount(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, key string) (total, defaults int) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(*) FILTER (WHERE is_default) FROM user_table_views WHERE user_id = $1 AND table_key = $2`,
		user, key).Scan(&total, &defaults)
	if err != nil {
		t.Fatal(err)
	}
	return total, defaults
}

// runParallel starts every fn at once and returns their HTTP statuses.
func runParallel(n int, fn func(i int) *httptest.ResponseRecorder) []int {
	statuses := make([]int, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			statuses[i] = fn(i).Code
		}(i)
	}
	close(start)
	wg.Wait()
	return statuses
}

func tally(statuses []int) map[int]int {
	out := map[int]int{}
	for _, s := range statuses {
		out[s]++
	}
	return out
}

func TestTableViewsPostgresConcurrentCreatesRespectTheCap(t *testing.T) {
	h, pool := tableViewsPostgresHandler(t)
	user := tableViewsTestUser(t, pool)
	statuses := runParallel(40, func(i int) *httptest.ResponseRecorder {
		return createView(t, h, user, "pods", fmt.Sprintf("view-%02d", i))
	})
	got := tally(statuses)
	if got[http.StatusCreated] != 20 || got[http.StatusConflict] != 20 || len(got) != 2 {
		t.Fatalf("40 parallel creates produced %v, want exactly 20x201 and 20x409", got)
	}
	if total, _ := tableViewsCount(t, pool, user, "pods"); total != 20 {
		t.Fatalf("stored views = %d, want 20", total)
	}
	var audits int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_outbox WHERE user_id = $1 AND action = 'user.table_view.created'`, user).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 20 {
		t.Fatalf("audit outbox rows = %d, want one per committed create (20)", audits)
	}
	// The cap is per table and per user.
	if w := createView(t, h, user, "events", "A"); w.Code != http.StatusCreated {
		t.Fatalf("other table status = %d", w.Code)
	}
	other := tableViewsTestUser(t, pool)
	if w := createView(t, h, other, "pods", "A"); w.Code != http.StatusCreated {
		t.Fatalf("other user status = %d", w.Code)
	}
}

func TestTableViewsPostgresConcurrentDuplicateNameYieldsOneWinner(t *testing.T) {
	h, pool := tableViewsPostgresHandler(t)
	user := tableViewsTestUser(t, pool)
	statuses := runParallel(10, func(int) *httptest.ResponseRecorder {
		return createView(t, h, user, "pods", "same-name")
	})
	got := tally(statuses)
	if got[http.StatusCreated] != 1 || got[http.StatusConflict] != 9 || len(got) != 2 {
		t.Fatalf("10 parallel duplicate creates produced %v, want 1x201 and 9x409", got)
	}
	if total, _ := tableViewsCount(t, pool, user, "pods"); total != 1 {
		t.Fatalf("stored views = %d, want 1", total)
	}
}

func TestTableViewsPostgresConcurrentDefaultSwitchKeepsOneDefault(t *testing.T) {
	h, pool := tableViewsPostgresHandler(t)
	user := tableViewsTestUser(t, pool)
	ids := make([]uuid.UUID, 4)
	for i := range ids {
		w := createView(t, h, user, "pods", fmt.Sprintf("v%d", i))
		if w.Code != http.StatusCreated {
			t.Fatalf("seed %d status = %d", i, w.Code)
		}
		if err := pool.QueryRow(context.Background(),
			`SELECT id FROM user_table_views WHERE user_id = $1 AND name = $2`, user, fmt.Sprintf("v%d", i)).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	for round := 0; round < 5; round++ {
		statuses := runParallel(len(ids), func(i int) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			h.Update(w, tableViewRequest(http.MethodPatch, "/x", user, ids[i].String(), map[string]any{"is_default": true}))
			return w
		})
		for _, s := range statuses {
			if s != http.StatusOK && s != http.StatusConflict {
				t.Fatalf("round %d: statuses = %v; only 200 or 409 are acceptable", round, tally(statuses))
			}
		}
		if _, defaults := tableViewsCount(t, pool, user, "pods"); defaults != 1 {
			t.Fatalf("round %d: %d default views after racing switches, want exactly 1 (statuses %v)", round, defaults, tally(statuses))
		}
	}
}
