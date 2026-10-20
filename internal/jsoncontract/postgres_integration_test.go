//go:build integration

package jsoncontract_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"

	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func postgresJSONFixture(t *testing.T) *pgx.Conn {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("cannot connect to owned JSON test database")
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func rollbackJSONFixture(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Error("JSON fixture rollback failed")
	}
}

//nolint:tparallel // subtests run sequentially against one shared per-test database fixture
func TestPostgresDurableJSONNestedNewWritesAndLegacyArrays(t *testing.T) {
	t.Parallel()
	conn := postgresJSONFixture(t)
	for _, item := range []struct {
		name, source, target, equal string
		valid                       bool
	}{
		{"legacy_empty", "[]", "[]", "[]", true},
		{"optional_regex_and_additive_key", `[{"label":"kind","value":"","future":true}]`, `[{"label":"kind","value":""}]`, `[]`, true},
		{"Go_regex_not_Postgres_regex", `[{"label":"kind","value":"\\p{L}+","is_regex":true}]`, `[]`, `[]`, true},
		{"missing_value", `[{"label":"kind"}]`, `[]`, `[]`, false},
		{"null_value", `[{"label":"kind","value":null}]`, `[]`, `[]`, false},
		{"null_regex", `[{"label":"kind","value":"x","is_regex":null}]`, `[]`, `[]`, false},
		{"equal_nested_value", `[]`, `[]`, `[null]`, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			tx, err := conn.Begin(t.Context())
			if err != nil {
				t.Fatal("begin fixture failed")
			}
			defer rollbackJSONFixture(t, tx)
			_, err = tx.Exec(t.Context(), `INSERT INTO public.alert_inhibitions (id,name,source_matchers,target_matchers,equal_labels) VALUES ($1,'synthetic',$2::jsonb,$3::jsonb,$4::jsonb)`, uuid.New(), item.source, item.target, item.equal)
			if item.valid {
				if err != nil {
					t.Fatalf("valid v1 wire shape rejected: %v", err)
				}
			} else {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "alert_inhibitions_nested_v1" {
					t.Fatalf("nested constraint did not reject invalid write: %v", err)
				}
			}
		})
	}
}

func TestPostgresTaskPayloadSchemaMatchesV1Adapter(t *testing.T) {
	t.Parallel()
	conn := postgresJSONFixture(t)
	for _, raw := range [][]byte{nil, []byte(`{}`), []byte(`{"future":1e999999999}`), []byte(`{"_correlation_id":null,"_correlation_id":"valid"}`), []byte(`null`), []byte(`[]`), []byte(`{} {}`), []byte(`{"_traceparent":42}`), []byte(` `)} {
		_, goErr := jsoncontract.ReadTaskPayloadV1(raw)
		var accepted bool
		// nil has legacy zero-byte semantics, not SQL NULL.
		if raw == nil {
			raw = []byte{}
		}
		if err := conn.QueryRow(t.Context(), `SELECT public.durable_json_task_payload_v1($1::bytea)`, raw).Scan(&accepted); err != nil {
			t.Fatal("SQL task shape inspection failed")
		}
		if accepted != (goErr == nil) {
			t.Fatalf("Go/SQL v1 adapter disagreement: accepted=%t category=%v", accepted, goErr)
		}
		tx, err := conn.Begin(t.Context())
		if err != nil {
			t.Fatal("begin fixture failed")
		}
		_, err = tx.Exec(t.Context(), `INSERT INTO public.task_outbox (id,task_type,payload) VALUES ($1,'synthetic:work',$2)`, uuid.New(), raw)
		rollbackJSONFixture(t, tx)
		if accepted && err != nil {
			t.Fatal("valid task write failed")
		}
		if !accepted {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.ConstraintName != "task_outbox_payload_v1" {
				t.Fatal("invalid task write did not fail owned constraint")
			}
		}
	}
}

func TestPostgresDurableJSONExpandPreservesCorruptHistoricalRows(t *testing.T) {
	t.Parallel()
	conn := postgresJSONFixture(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal("begin fixture failed")
	}
	defer rollbackJSONFixture(t, tx)
	var definition string
	if err := tx.QueryRow(t.Context(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='public.alert_inhibitions'::regclass AND conname='alert_inhibitions_nested_v1' AND NOT convalidated`).Scan(&definition); err != nil {
		t.Fatal("expand constraint must exist without validating historical rows")
	}
	// Reconstruct only the owned expand constraint inside a rollback-only fixture.
	// Baseline71 -> migration072 no-rewrite qualification is separately receipted.
	if _, err := tx.Exec(t.Context(), `ALTER TABLE public.alert_inhibitions DROP CONSTRAINT alert_inhibitions_nested_v1`); err != nil {
		t.Fatal("fixture constraint reset failed")
	}
	id := uuid.New()
	if _, err := tx.Exec(t.Context(), `INSERT INTO public.alert_inhibitions (id,name,source_matchers,target_matchers,equal_labels) VALUES ($1,'synthetic','[]','[]','[42]')`, id); err != nil {
		t.Fatal("historical fixture seed failed")
	}
	if _, err := tx.Exec(t.Context(), `ALTER TABLE public.alert_inhibitions ADD CONSTRAINT alert_inhibitions_nested_v1 `+definition); err != nil {
		t.Fatal("expand stage rejected old row")
	}
	var equal string
	if err := tx.QueryRow(t.Context(), `SELECT equal_labels::text FROM public.alert_inhibitions WHERE id=$1`, id).Scan(&equal); err != nil || equal != "[42]" {
		t.Fatal("historical corrupt document was rewritten")
	}
	_, err = tx.Exec(t.Context(), `ALTER TABLE public.alert_inhibitions VALIDATE CONSTRAINT alert_inhibitions_nested_v1`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "alert_inhibitions_nested_v1" {
		t.Fatal("contract validation must refuse unreviewed corrupt rows")
	}
}

func TestPostgresPlacementAndRolloutNestedWireShapes(t *testing.T) {
	t.Parallel()
	conn := postgresJSONFixture(t)
	for _, item := range []struct {
		kind, raw string
		valid     bool
	}{
		{"placement", `{}`, true},
		{"placement", `{"match_labels":null,"match_expressions":null,"future_v1":true}`, true},
		{"placement", `{"match_expressions":[{"key":"namespace","operator":"In","values":[""]}]}`, true},
		{"placement", `{"match_labels":{"namespace":null}}`, false},
		{"placement", `{"match_expressions":[{"key":"namespace","operator":"In","values":[null]}]}`, false},
		{"placement", `{"all_clusters":null}`, false},
		{"rollout_strategy", `{"type":"rolling","max_concurrent":1,"max_unavailable":{"type":"count","value":0},"failure_threshold":{"type":"count","value":1},"progress_deadline":"1m","on_failure":"pause","future_v1":true}`, true},
		{"rollout_strategy", `{"type":"rolling","max_concurrent":0,"max_unavailable":{"type":"count","value":0},"failure_threshold":{"type":"count","value":1},"progress_deadline":"1m","on_failure":"pause"}`, false},
		{"rollout_strategy", `{"type":"rolling","max_concurrent":1,"max_unavailable":{"type":"count","value":null},"failure_threshold":{"type":"count","value":1},"progress_deadline":"1m","on_failure":"pause"}`, false},
	} {
		var accepted bool
		statement := `SELECT public.durable_json_` + item.kind + `_v1($1::jsonb)`
		if err := conn.QueryRow(t.Context(), statement, item.raw).Scan(&accepted); err != nil {
			t.Fatal("nested wire shape query failed")
		}
		if accepted != item.valid {
			t.Fatalf("%s primitive shape: accepted=%t want=%t", item.kind, accepted, item.valid)
		}
	}
}

func TestPostgresIntegrityAuditDedupeUsesExistingOutbox(t *testing.T) {
	t.Parallel()
	conn := postgresJSONFixture(t)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal("begin fixture failed")
	}
	defer rollbackJSONFixture(t, tx)
	q := sqlc.New(tx)
	key := "synthetic-integrity:" + uuid.NewString()
	event := audit.Event{Action: "alert.inhibition.integrity_failure", ResourceType: "alert_inhibition", ResourceID: uuid.NewString(), Detail: map[string]any{"category": "equal_labels:invalid_item", "schema_version": 1}}
	first, err := audit.RecordOutbox(t.Context(), q, event, key, audit.OutboxOptions{})
	if err != nil {
		t.Fatal("first integrity intent failed")
	}
	again, err := audit.RecordOutbox(t.Context(), q, event, key, audit.OutboxOptions{})
	if err != nil || first.ID != again.ID {
		t.Fatal("same corruption revision did not dedupe")
	}
	changed, err := audit.RecordOutbox(t.Context(), q, event, key+":new-revision", audit.OutboxOptions{})
	if err != nil || changed.ID == first.ID {
		t.Fatal("new corruption revision lost new intent")
	}
	var count int
	if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM public.audit_outbox WHERE dedupe_key LIKE $1`, key+"%").Scan(&count); err != nil || count != 2 {
		t.Fatal("actual outbox did not retain exactly two revision intents")
	}
}
