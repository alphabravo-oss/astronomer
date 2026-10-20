package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
)

type preferencePostgresBoundary struct {
	*sqlc.Queries
	auditFailure              bool
	attachmentFailure         bool
	reserveCalls, attachCalls int
	upsertCalls, auditCalls   int
	upsertSQLState            string
}

func (q *preferencePostgresBoundary) UpsertUserPreferences(ctx context.Context, params sqlc.UpsertUserPreferencesParams) (sqlc.UserPreference, error) {
	q.upsertCalls++
	row, err := q.Queries.UpsertUserPreferences(ctx, params)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		q.upsertSQLState = pgerr.Code
	}
	return row, err
}

func (q *preferencePostgresBoundary) UpsertAuditOutbox(ctx context.Context, params sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	q.auditCalls++
	if q.auditFailure {
		return sqlc.AuditOutbox{}, errors.New("fixture audit persistence unavailable")
	}
	return q.Queries.UpsertAuditOutbox(ctx, params)
}

func (q *preferencePostgresBoundary) ReserveOperationIdempotencyKey(ctx context.Context, params sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	q.reserveCalls++
	return q.Queries.ReserveOperationIdempotencyKey(ctx, params)
}

func (q *preferencePostgresBoundary) AttachOperationIdempotencyKey(ctx context.Context, params sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	q.attachCalls++
	if q.attachmentFailure {
		return sqlc.OperationIdempotencyKey{}, errors.New("fixture preference attachment unavailable")
	}
	return q.Queries.AttachOperationIdempotencyKey(ctx, params)
}

func preferencePostgresHandler(t *testing.T, ctx context.Context, outer pgx.Tx, state *preferencePostgresBoundary) *AuthHandler {
	t.Helper()
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(sqlc.New(outer), func(ctx context.Context, mutate func(UserPreferencesMutationTx) error) error {
		// Every request uses an actual savepoint on the fixture's one physical session.
		tx, err := outer.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error("preference fixture transaction cleanup failed")
			}
		}()
		state.Queries = sqlc.New(tx)
		if err := mutate(state); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	return h
}

func preferencePostgresUser(t *testing.T, ctx context.Context, tx pgx.Tx) uuid.UUID {
	t.Helper()
	id := uuid.New()
	paginationExec(t, ctx, tx, `INSERT INTO users (id, email, username) VALUES ($1, $2, $3)`, id, "preference-fixture-"+id.String()+"@example.invalid", "preference-fixture-"+id.String())
	return id
}

func preferencePostgresAuditCount(t *testing.T, ctx context.Context, tx pgx.Tx, actor uuid.UUID) int {
	t.Helper()
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_outbox WHERE user_id = $1 AND action = 'user.preferences.updated'`, actor).Scan(&count); err != nil {
		t.Fatal("preference audit count query failed")
	}
	return count
}

func preferencePostgresPut(t *testing.T, h *AuthHandler, actor uuid.UUID, body any, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := preferenceRequest(http.MethodPut, actor, body)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	h.PutUserPreferences(response, req)
	return response
}

func TestPreferencePostgresMissingReadIsActorScopedAndHasNoWrites(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	actor := preferencePostgresUser(t, ctx, tx)
	other := preferencePostgresUser(t, ctx, tx)
	state := &preferencePostgresBoundary{}
	h := preferencePostgresHandler(t, ctx, tx, state)
	request := preferenceRequest(http.MethodGet, actor, nil)
	request.URL.RawQuery = "user_id=" + other.String()
	response := httptest.NewRecorder()
	h.GetUserPreferences(response, request)
	var got struct {
		Data userpreferences.Preferences `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil || got.Data.Theme != userpreferences.Defaults().Theme || got.Data.Favorites == nil {
		t.Fatal("missing actor preferences did not return complete defaults")
	}
	for _, id := range []uuid.UUID{actor, other} {
		if _, err := sqlc.New(tx).GetUserPreferences(ctx, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("read created or selected a preference row")
		}
	}
	if state.upsertCalls != 0 || state.auditCalls != 0 || preferencePostgresAuditCount(t, ctx, tx, actor) != 0 {
		t.Fatal("read created a mutation or audit intent")
	}
}

func TestPreferencePostgresRequiredFavoritesRejectBeforeMutation(t *testing.T) {
	for _, name := range []string{"missing", "null", "empty"} {
		t.Run(name, func(t *testing.T) {
			ctx, tx := paginationPostgresTx(t)
			actor := preferencePostgresUser(t, ctx, tx)
			state := &preferencePostgresBoundary{}
			h := preferencePostgresHandler(t, ctx, tx, state)
			body := map[string]any{"theme": "dark", "table_density": "compact", "landing_route": "/dashboard", "time_format": "locale"}
			if name == "null" {
				body["favorites"] = nil
			}
			if name == "empty" {
				body["favorites"] = []string{}
			}
			response := preferencePostgresPut(t, h, actor, body, "")
			if name == "empty" {
				row, err := sqlc.New(tx).GetUserPreferences(ctx, actor)
				if response.Code != http.StatusOK || err != nil || string(row.Favorites) != "[]" || string(row.PinnedClusters) != "[]" || string(row.StarredTypes) != "[]" || row.RowsPerPage != 25 || row.DateFormat != "locale" || preferencePostgresAuditCount(t, ctx, tx, actor) != 1 {
					t.Fatal("valid empty favorites or optional normalization changed")
				}
				return
			}
			_, readErr := sqlc.New(tx).GetUserPreferences(ctx, actor)
			if !errors.Is(readErr, pgx.ErrNoRows) || state.auditCalls != 0 || preferencePostgresAuditCount(t, ctx, tx, actor) != 0 {
				t.Fatal("invalid required favorites committed state")
			}
			// Preserve the actual baseline CHECK evidence before asserting the intended boundary.
			if response.Code != http.StatusBadRequest && (response.Code != http.StatusInternalServerError || state.upsertSQLState != "23514" || state.upsertCalls != 1) {
				t.Fatal("invalid-array baseline failed for an unrelated reason")
			}
			if response.Code != http.StatusBadRequest || state.upsertCalls != 0 {
				t.Fatal("invalid required favorites reached the SQL array constraint instead of safe request validation")
			}
		})
	}
}

func TestPreferencePostgresAuditFailureRollsBackActualReplacementAndRetry(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	actor := preferencePostgresUser(t, ctx, tx)
	state := &preferencePostgresBoundary{}
	h := preferencePostgresHandler(t, ctx, tx, state)
	body := userpreferences.Defaults()
	if response := preferencePostgresPut(t, h, actor, body, "initial-intent"); response.Code != http.StatusOK {
		t.Fatal("initial valid preference setup failed")
	}
	state.upsertCalls, state.auditCalls = 0, 0
	state.auditFailure = true
	body.Theme = userpreferences.Theme("dark")
	response := preferencePostgresPut(t, h, actor, body, "retry-intent")
	var errorBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &errorBody); err != nil {
		t.Fatal("audit failure error document malformed")
	}
	row, err := sqlc.New(tx).GetUserPreferences(ctx, actor)
	if response.Code != http.StatusServiceUnavailable || errorBody.Error.Code != "audit_unavailable" || err != nil || row.Theme != "system" || state.upsertCalls != 1 || state.auditCalls != 1 || preferencePostgresAuditCount(t, ctx, tx, actor) != 1 {
		t.Fatal("mandatory audit failure did not roll back the actual preference Upsert")
	}
	state.auditFailure = false
	if response := preferencePostgresPut(t, h, actor, body, "retry-intent"); response.Code != http.StatusOK {
		t.Fatal("successful retry after audit rollback failed")
	}
	row, err = sqlc.New(tx).GetUserPreferences(ctx, actor)
	if err != nil || row.Theme != "dark" || preferencePostgresAuditCount(t, ctx, tx, actor) != 2 {
		t.Fatal("retry did not commit one preference replacement and one audit intent")
	}
}

func TestPreferencePostgresChangedBodySameKeyCannotRetainStaleAudit(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	actor := preferencePostgresUser(t, ctx, tx)
	state := &preferencePostgresBoundary{}
	h := preferencePostgresHandler(t, ctx, tx, state)
	body := userpreferences.Defaults()
	body.Theme = userpreferences.Theme("dark")
	for attempt := 0; attempt < 2; attempt++ {
		if response := preferencePostgresPut(t, h, actor, body, "same-intent"); response.Code != http.StatusOK {
			t.Fatal("valid same-body replay failed")
		}
	}
	if preferencePostgresAuditCount(t, ctx, tx, actor) != 1 {
		t.Fatal("same-body replay produced duplicate audit intents")
	}
	body.Theme = userpreferences.Theme("light")
	response := preferencePostgresPut(t, h, actor, body, "same-intent")
	row, err := sqlc.New(tx).GetUserPreferences(ctx, actor)
	if err != nil {
		t.Fatal("cannot inspect actual stored preference after replay")
	}
	var recordedTheme string
	if err := tx.QueryRow(ctx, `SELECT detail->>'theme' FROM audit_outbox WHERE user_id = $1 AND action = 'user.preferences.updated'`, actor).Scan(&recordedTheme); err != nil {
		t.Fatal("cannot inspect actual retained audit detail")
	}
	if preferencePostgresAuditCount(t, ctx, tx, actor) != 1 {
		t.Fatal("changed-body retry duplicated audit identity")
	}
	if response.Code != http.StatusConflict && (response.Code != http.StatusOK || row.Theme != "light" || recordedTheme != "dark") {
		t.Fatal("changed-body baseline differed from the actual full-replace and retained-audit path")
	}
	if response.Code != http.StatusConflict || row.Theme != "dark" || recordedTheme != "dark" {
		t.Fatal("changed-body same-key replay replaced preferences while retaining the first audit detail")
	}
}
