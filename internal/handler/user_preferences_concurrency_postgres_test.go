package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// These two-connection fixtures need a committed synthetic actor visible to
// both transactions. Run only through the live UID-fenced owned cursor runner.
// The internal database/major/schema guard is an additional fail-closed check,
// not a replacement for the harness's Kubernetes ownership authorization.
func preferenceConcurrencyConnection(t *testing.T, ctx context.Context) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv("PAGINATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PAGINATION_TEST_DATABASE_URL is not set")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("preference fixture connection failed")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if conn.Close(cleanup) != nil {
			t.Error("preference fixture connection cleanup failed")
		}
	})
	var database string
	var major, version int
	var dirty bool
	if err := conn.QueryRow(ctx, `SELECT current_database(), current_setting('server_version_num')::int / 10000, version, dirty FROM schema_migrations`).Scan(&database, &major, &version, &dirty); err != nil || version != 77 || dirty || !(database == "plan032_w07_cursor17" && major == 17 || database == "plan032_w07_cursor18" && major == 18) {
		t.Fatal("committed preference fixture requires exact owned cursor database and schema")
	}
	return conn
}

func preferenceConcurrencyActor(t *testing.T, ctx context.Context, conn *pgx.Conn, key string) uuid.UUID {
	t.Helper()
	actor := uuid.New()
	email := "preference-concurrency-" + actor.String() + "@example.invalid"
	username := "preference-concurrency-" + actor.String()
	request := preferenceRequest(http.MethodPut, actor, nil)
	request.Header.Set("Idempotency-Key", key)
	scope, _ := operationIdempotencyFromContext(withOperationIdempotency(request, "user-preferences:v1"))
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		tx, err := conn.Begin(cleanup)
		if err != nil {
			t.Error("owned preference cleanup transaction failed")
			return
		}
		defer func() {
			if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error("owned preference cleanup rollback failed")
			}
		}()
		commands := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM audit_outbox WHERE user_id = $1 AND action = 'user.preferences.updated' AND resource_type = 'user_preferences' AND resource_id = $2`, []any{actor, actor.String()}},
			{`DELETE FROM operation_idempotency_keys WHERE scope = $1 AND idempotency_key = $2`, []any{scope.scope, scope.key}},
			{`DELETE FROM user_preferences WHERE user_id = $1`, []any{actor}},
			{`DELETE FROM users WHERE id = $1 AND email = $2 AND username = $3`, []any{actor, email, username}},
		}
		for _, command := range commands {
			if _, err := tx.Exec(cleanup, command.sql, command.args...); err != nil {
				t.Error("owned synthetic preference cleanup failed")
				return
			}
		}
		if tx.Commit(cleanup) != nil {
			t.Error("owned synthetic preference cleanup commit failed")
		}
		var remaining int
		if err := conn.QueryRow(cleanup, `SELECT
         (SELECT count(*) FROM users WHERE id = $1) +
         (SELECT count(*) FROM user_preferences WHERE user_id = $1) +
         (SELECT count(*) FROM audit_outbox WHERE user_id = $1 AND action = 'user.preferences.updated') +
         (SELECT count(*) FROM operation_idempotency_keys WHERE scope = $2 AND idempotency_key = $3)`, actor, scope.scope, scope.key).Scan(&remaining); err != nil || remaining != 0 {
			t.Error("owned synthetic preference rows remained after cleanup")
		}
	})
	if _, err := conn.Exec(ctx, `INSERT INTO users (id,email,username) VALUES ($1,$2,$3)`, actor, email, username); err != nil {
		t.Fatal("owned preference actor setup failed")
	}
	return actor
}

// This prefix is the canonical generated reservation statement; inspect only a
// boolean match, never expose the backend query or its parameter values.
const preferenceReservationQueryPrefix = `-- name: ReserveOperationIdempotencyKey :one
INSERT INTO operation_idempotency_keys (scope, idempotency_key)
VALUES ($1, $2)
ON CONFLICT (scope, idempotency_key) DO UPDATE
SET updated_at = operation_idempotency_keys.updated_at
RETURNING `

type preferenceHeldAudit struct {
	*sqlc.Queries
	entered, release chan struct{}
	once             sync.Once
}

func (q *preferenceHeldAudit) UpsertAuditOutbox(ctx context.Context, params sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	row, err := q.Queries.UpsertAuditOutbox(ctx, params)
	if err != nil {
		return row, err
	}
	q.once.Do(func() { close(q.entered) })
	select {
	case <-q.release:
		return row, nil
	case <-ctx.Done():
		return sqlc.AuditOutbox{}, ctx.Err()
	}
}

func preferenceConcurrentHandler(t *testing.T, conn *pgx.Conn, wrap func(*sqlc.Queries) UserPreferencesMutationTx) *AuthHandler {
	t.Helper()
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(sqlc.New(conn), func(ctx context.Context, mutate func(UserPreferencesMutationTx) error) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error("concurrent preference rollback failed")
			}
		}()
		if err := mutate(wrap(sqlc.New(tx))); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	return h
}

func TestPreferencePostgresConcurrentKeysSerializeActualReplacementAndAudit(t *testing.T) {
	for _, mode := range []string{"equal-body", "changed-body"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			t.Cleanup(cancel)
			observer := preferenceConcurrencyConnection(t, ctx)
			key := "concurrent internal spaces"
			actor := preferenceConcurrencyActor(t, ctx, observer, key)
			firstConn := preferenceConcurrencyConnection(t, ctx)
			secondConn := preferenceConcurrencyConnection(t, ctx)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			first := preferenceConcurrentHandler(t, firstConn, func(q *sqlc.Queries) UserPreferencesMutationTx {
				return &preferenceHeldAudit{Queries: q, entered: entered, release: release}
			})
			second := preferenceConcurrentHandler(t, secondConn, func(q *sqlc.Queries) UserPreferencesMutationTx { return q })
			firstDone, secondDone := make(chan struct{}), make(chan struct{})
			firstResult, secondResult := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
			firstStarted, secondStarted := false, false
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				cancel()
				for _, item := range []struct {
					started bool
					done    chan struct{}
				}{{firstStarted, firstDone}, {secondStarted, secondDone}} {
					if item.started {
						select {
						case <-item.done:
						case <-time.After(5 * time.Second):
							t.Error("concurrent preference request cleanup did not join")
						}
					}
				}
			})
			send := func(h *AuthHandler, prefs userpreferences.Preferences) *httptest.ResponseRecorder {
				request := preferenceRequest(http.MethodPut, actor, prefs)
				request = request.WithContext(reqctx.WithUser(ctx, &reqctx.User{ID: actor.String(), AuthMethod: "jwt"}))
				request.Header.Set("Idempotency-Key", key)
				response := httptest.NewRecorder()
				h.PutUserPreferences(response, request)
				return response
			}
			original := userpreferences.Defaults()
			original.Theme = userpreferences.ThemeDark
			changed := original
			if mode == "changed-body" {
				changed.Theme = userpreferences.ThemeLight
			}
			firstStarted = true
			go func() { defer close(firstDone); firstResult <- send(first, original) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first preference transaction did not reach actual audited replacement")
			}
			secondStarted = true
			go func() { defer close(secondDone); secondResult <- send(second, changed) }()
			deadline := time.Now().Add(5 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				var reservationLock bool
				if err := observer.QueryRow(ctx, `SELECT COALESCE(wait_event_type,'') = 'Lock'
                    AND $2 = ANY(pg_blocking_pids(pid)) AND strpos(query, $3) = 1
                    FROM pg_stat_activity WHERE pid = $1`, int64(secondConn.PgConn().PID()), int64(firstConn.PgConn().PID()), preferenceReservationQueryPrefix).Scan(&reservationLock); err != nil {
					t.Fatal("cannot inspect owned reservation blocker")
				}
				if reservationLock {
					blocked = true
					break
				}
				select {
				case <-secondDone:
					t.Fatal("second preference request bypassed the uncommitted receipt lock")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if !blocked {
				t.Fatal("second preference transaction did not block on actual reservation lock")
			}
			releaseOnce.Do(func() { close(release) })
			var a, b *httptest.ResponseRecorder
			select {
			case a = <-firstResult:
			case <-ctx.Done():
				t.Fatal("first preference request did not complete")
			}
			select {
			case b = <-secondResult:
			case <-ctx.Done():
				t.Fatal("second preference request did not complete")
			}
			expected := http.StatusOK
			if mode == "changed-body" {
				expected = http.StatusConflict
			}
			row, err := sqlc.New(observer).GetUserPreferences(ctx, actor)
			var intents int
			if err := observer.QueryRow(ctx, `SELECT count(*) FROM audit_outbox WHERE user_id = $1 AND action = 'user.preferences.updated'`, actor).Scan(&intents); err != nil {
				t.Fatal("cannot inspect actual concurrent audit intents")
			}
			if a.Code != http.StatusOK || b.Code != expected || err != nil || row.Theme != "dark" || intents != 1 {
				t.Fatal("concurrent key changed replacement or mandatory intent cardinality")
			}
			if mode == "equal-body" && a.Body.String() != b.Body.String() {
				t.Fatal("concurrent equal request did not return exact committed receipt")
			}
		})
	}
}
