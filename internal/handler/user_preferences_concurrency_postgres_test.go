package handler

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db"
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
type preferenceConcurrencyDatabaseIdentity struct {
	name, lease, comment string
	major, schema        int
	oid                  uint64
	explicit             bool
}

func preferenceConcurrencyExpectedDatabase(getenv func(string) string) (preferenceConcurrencyDatabaseIdentity, bool) {
	name := getenv("PREFERENCE_ROUTE_TEST_DATABASE_NAME")
	majorText := getenv("PREFERENCE_ROUTE_TEST_DATABASE_MAJOR")
	oidText := getenv("PREFERENCE_TEST_DATABASE_OID")
	leaseText := getenv("PREFERENCE_TEST_DATABASE_LEASE_UUID")
	comment := getenv("PREFERENCE_TEST_DATABASE_COMMENT")
	schemaText := getenv("PREFERENCE_TEST_DATABASE_SCHEMA")
	sourceText := getenv("PREFERENCE_TEST_DATABASE_SOURCE")
	if name == "" && majorText == "" && oidText == "" && leaseText == "" && comment == "" && schemaText == "" && sourceText == "" {
		return preferenceConcurrencyDatabaseIdentity{}, true
	}
	major, majorErr := strconv.Atoi(majorText)
	oid, oidErr := strconv.ParseUint(oidText, 10, 32)
	lease, leaseErr := uuid.Parse(leaseText)
	schema, schemaErr := strconv.Atoi(schemaText)
	sourceDigest, sourceErr := hex.DecodeString(sourceText)
	if majorErr != nil || (major != 17 && major != 18) || strconv.Itoa(major) != majorText || oidErr != nil || oid == 0 || strconv.FormatUint(oid, 10) != oidText || leaseErr != nil || lease == uuid.Nil || lease.String() != leaseText || schemaErr != nil || schemaText != strconv.FormatInt(db.ExpectedSchemaVersion, 10) || schema != int(db.ExpectedSchemaVersion) || len(sourceText) != 40 || sourceErr != nil || hex.EncodeToString(sourceDigest) != sourceText || sourceText == "0000000000000000000000000000000000000000" {
		return preferenceConcurrencyDatabaseIdentity{}, false
	}
	expectedName := "plan032_w13w07_" + majorText + "_" + lease.String()[:8] + lease.String()[9:13]
	expectedComment := "plan032-20261009 w13-w07 qualification lease " + leaseText + " schema" + schemaText + " source" + sourceText[:8]
	if name != expectedName || comment != expectedComment {
		return preferenceConcurrencyDatabaseIdentity{}, false
	}
	return preferenceConcurrencyDatabaseIdentity{name: name, major: major, schema: schema, oid: oid, lease: leaseText, comment: comment, explicit: true}, true
}

func (expected preferenceConcurrencyDatabaseIdentity) matches(database string, major, version int, dirty bool, oid uint64, owner, comment string) bool {
	if dirty || oid == 0 || owner != "astronomer" {
		return false
	}
	if !expected.explicit {
		return version == 77 && ((database == "plan032_w07_cursor17" && major == 17) || (database == "plan032_w07_cursor18" && major == 18))
	}
	return database == expected.name && major == expected.major && version == expected.schema && expected.schema == int(db.ExpectedSchemaVersion) && oid == expected.oid && comment == expected.comment
}

func preferenceConcurrencyConnection(t *testing.T, ctx context.Context) *pgx.Conn {
	t.Helper()
	expected, valid := preferenceConcurrencyExpectedDatabase(os.Getenv)
	if !valid {
		t.Fatal("committed preference fixture owned database identity is incomplete")
	}
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
	var database, owner, comment string
	var oid uint64
	var major, version int
	var dirty bool
	if err := conn.QueryRow(ctx, `SELECT current_database(), current_setting('server_version_num')::int / 10000, m.version, m.dirty, d.oid::bigint, pg_get_userbyid(d.datdba), COALESCE(shobj_description(d.oid,'pg_database'),'') FROM schema_migrations m JOIN pg_database d ON d.datname=current_database()`).Scan(&database, &major, &version, &dirty, &oid, &owner, &comment); err != nil || !expected.matches(database, major, version, dirty, oid, owner, comment) {
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

func TestPreferenceConcurrencyOwnedDatabaseIdentity(t *testing.T) {
	good := map[string]string{
		"PREFERENCE_ROUTE_TEST_DATABASE_NAME":  "plan032_w13w07_17_53a2b55b9273",
		"PREFERENCE_ROUTE_TEST_DATABASE_MAJOR": "17",
		"PREFERENCE_TEST_DATABASE_OID":         "60512",
		"PREFERENCE_TEST_DATABASE_LEASE_UUID":  "53a2b55b-9273-405a-b067-32f70a7d0a79",
		"PREFERENCE_TEST_DATABASE_COMMENT":     "plan032-20261009 w13-w07 qualification lease 53a2b55b-9273-405a-b067-32f70a7d0a79 schema" + strconv.FormatInt(db.ExpectedSchemaVersion, 10) + " source5c4d273f",
		"PREFERENCE_TEST_DATABASE_SCHEMA":      strconv.FormatInt(db.ExpectedSchemaVersion, 10),
		"PREFERENCE_TEST_DATABASE_SOURCE":      "5c4d273ff564aab0ede8bb9265c789380f6f640d",
	}
	t.Run("owned-current-schema", func(t *testing.T) {
		expected, ok := preferenceConcurrencyExpectedDatabase(func(key string) string { return good[key] })
		if !ok || !expected.matches(good["PREFERENCE_ROUTE_TEST_DATABASE_NAME"], 17, int(db.ExpectedSchemaVersion), false, 60512, "astronomer", good["PREFERENCE_TEST_DATABASE_COMMENT"]) {
			t.Fatal("exact owned canonical schema identity rejected")
		}
	})
	for _, tc := range []struct{ name, key, value string }{
		{"partial", "PREFERENCE_TEST_DATABASE_OID", ""},
		{"wrong-name", "PREFERENCE_ROUTE_TEST_DATABASE_NAME", "customer"},
		{"wrong-major", "PREFERENCE_ROUTE_TEST_DATABASE_MAJOR", "18"},
		{"malformed-major", "PREFERENCE_ROUTE_TEST_DATABASE_MAJOR", "+17"},
		{"zero-oid", "PREFERENCE_TEST_DATABASE_OID", "0"},
		{"overflow-oid", "PREFERENCE_TEST_DATABASE_OID", "4294967296"},
		{"wrong-lease", "PREFERENCE_TEST_DATABASE_LEASE_UUID", "53a2b55b-9273-405a-b067-32f70a7d0a78"},
		{"noncanonical-lease", "PREFERENCE_TEST_DATABASE_LEASE_UUID", "53A2B55B-9273-405A-B067-32F70A7D0A79"},
		{"missing-schema", "PREFERENCE_TEST_DATABASE_SCHEMA", ""},
		{"stale-schema", "PREFERENCE_TEST_DATABASE_SCHEMA", strconv.FormatInt(db.ExpectedSchemaVersion-1, 10)},
		{"missing-source", "PREFERENCE_TEST_DATABASE_SOURCE", ""},
		{"malformed-source", "PREFERENCE_TEST_DATABASE_SOURCE", "not-a-commit"},
		{"zero-source", "PREFERENCE_TEST_DATABASE_SOURCE", "0000000000000000000000000000000000000000"},
		{"uppercase-source", "PREFERENCE_TEST_DATABASE_SOURCE", "5C4D273FF564AAB0EDE8BB9265C789380F6F640D"},
		{"source-comment-mismatch", "PREFERENCE_TEST_DATABASE_SOURCE", "6c4d273ff564aab0ede8bb9265c789380f6f640d"},
		{"wrong-comment", "PREFERENCE_TEST_DATABASE_COMMENT", "wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := preferenceConcurrencyExpectedDatabase(func(key string) string {
				if key == tc.key {
					return tc.value
				}
				return good[key]
			})
			if ok {
				t.Fatal("inconsistent owned expectation accepted")
			}
		})
	}
	expected, _ := preferenceConcurrencyExpectedDatabase(func(key string) string { return good[key] })
	for _, tc := range []struct {
		name, database, owner, comment string
		major, version                 int
		oid                            uint64
		dirty                          bool
	}{
		{"actual-name", "customer", "astronomer", expected.comment, 17, int(db.ExpectedSchemaVersion), 60512, false},
		{"actual-major", expected.name, "astronomer", expected.comment, 18, int(db.ExpectedSchemaVersion), 60512, false},
		{"actual-schema", expected.name, "astronomer", expected.comment, 17, int(db.ExpectedSchemaVersion) - 1, 60512, false},
		{"actual-oid", expected.name, "astronomer", expected.comment, 17, int(db.ExpectedSchemaVersion), 60513, false},
		{"actual-owner", expected.name, "customer", expected.comment, 17, int(db.ExpectedSchemaVersion), 60512, false},
		{"actual-comment", expected.name, "astronomer", "wrong", 17, int(db.ExpectedSchemaVersion), 60512, false},
		{"actual-dirty", expected.name, "astronomer", expected.comment, 17, int(db.ExpectedSchemaVersion), 60512, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if expected.matches(tc.database, tc.major, tc.version, tc.dirty, tc.oid, tc.owner, tc.comment) {
				t.Fatal("actual database identity drift accepted")
			}
		})
	}
	t.Run("nil-lease", func(t *testing.T) {
		_, ok := preferenceConcurrencyExpectedDatabase(func(key string) string {
			switch key {
			case "PREFERENCE_TEST_DATABASE_LEASE_UUID":
				return uuid.Nil.String()
			case "PREFERENCE_ROUTE_TEST_DATABASE_NAME":
				return "plan032_w13w07_17_000000000000"
			case "PREFERENCE_TEST_DATABASE_COMMENT":
				return "plan032-20261009 w13-w07 qualification lease " + uuid.Nil.String() + " schema" + strconv.FormatInt(db.ExpectedSchemaVersion, 10) + " source5c4d273f"
			default:
				return good[key]
			}
		})
		if ok {
			t.Fatal("nil lease identity accepted")
		}
	})
	t.Run("legacy-zero-oid", func(t *testing.T) {
		legacy, _ := preferenceConcurrencyExpectedDatabase(func(string) string { return "" })
		if legacy.matches("plan032_w07_cursor17", 17, 77, false, 0, "astronomer", "") {
			t.Fatal("impossible legacy database OID accepted")
		}
	})
	t.Run("legacy77", func(t *testing.T) {
		legacy, ok := preferenceConcurrencyExpectedDatabase(func(string) string { return "" })
		if !ok || !legacy.matches("plan032_w07_cursor17", 17, 77, false, 1, "astronomer", "") || !legacy.matches("plan032_w07_cursor18", 18, 77, false, 1, "astronomer", "") || legacy.matches(expected.name, 17, 77, false, 60512, "astronomer", expected.comment) || legacy.matches("plan032_w07_cursor17", 17, int(db.ExpectedSchemaVersion), false, 1, "astronomer", "") {
			t.Fatal("legacy exact schema77 boundary changed")
		}
	})
}
