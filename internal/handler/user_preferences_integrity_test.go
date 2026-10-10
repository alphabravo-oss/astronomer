package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type preferenceReplayFake struct {
	*preferenceTxFake
	*fakeOperationIdempotencyStore
	upserts int
}

func (q *preferenceReplayFake) ReserveOperationIdempotencyKey(ctx context.Context, arg sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	row, err := q.fakeOperationIdempotencyStore.ReserveOperationIdempotencyKey(ctx, arg)
	if !row.OperationID.Valid && row.OperationTable == "" && len(row.Response) == 0 {
		row.Response = json.RawMessage(`{}`)
	}
	return row, err
}

func (q *preferenceReplayFake) UpsertUserPreferences(ctx context.Context, arg sqlc.UpsertUserPreferencesParams) (sqlc.UserPreference, error) {
	q.upserts++
	return q.preferenceTxFake.UpsertUserPreferences(ctx, arg)
}

func TestPreferenceKeyReturnsCommittedDocumentWithoutRepeatingWrites(t *testing.T) {
	actor := uuid.New()
	q := &preferenceReplayFake{preferenceTxFake: &preferenceTxFake{}, fakeOperationIdempotencyStore: &fakeOperationIdempotencyStore{}}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(&preferenceStoreFake{}, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error { return fn(q) })
	body := userpreferences.Defaults()
	body.Theme = userpreferences.ThemeDark
	send := func(prefs userpreferences.Preferences, key string) *httptest.ResponseRecorder {
		r := preferenceRequest(http.MethodPut, actor, prefs)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.PutUserPreferences(w, r)
		return w
	}
	first := send(body, " internal spaces remain valid ")
	second := send(body, "internal spaces remain valid")
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() || q.upserts != 1 || len(q.audits) != 1 {
		t.Fatal("same body did not return exact committed receipt without writes")
	}
	body.Theme = userpreferences.ThemeLight
	changed := send(body, "internal spaces remain valid")
	if changed.Code != http.StatusConflict || q.upserts != 1 || len(q.audits) != 1 || q.row.Theme != "dark" {
		t.Fatal("changed key body mutated preference or audit")
	}
}

func TestPreferenceReceiptReaderRejectsCorruptStateAndSeparatesBodyConflict(t *testing.T) {
	for _, mode := range []string{"valid", "changed-request", "wrong-version", "wrong-actor", "wrong-method", "wrong-path", "wrong-resource", "wrong-table", "body-digest-mismatch", "null-favorites", "null-optional", "unknown-field", "duplicate-field", "trailing-value", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			actor := uuid.New()
			r := preferenceRequest(http.MethodPut, actor, nil)
			r.Header.Set("Idempotency-Key", "receipt-reader")
			ctx := withOperationIdempotency(r, "user-preferences:v1")
			scope, _ := operationIdempotencyFromContext(ctx)
			prefs := userpreferences.Defaults()
			digest, err := canonicalOperationRequestDigest(prefs)
			if err != nil {
				t.Fatal(err)
			}
			receipt := preferenceReceipt{Version: 1, Actor: actor, Method: r.Method, Path: r.URL.EscapedPath(), RequestDigest: digest, Preferences: prefs}
			id := actor
			table := preferenceReceiptTable
			requestDigest := digest
			switch mode {
			case "changed-request":
				changed := userpreferences.Defaults()
				changed.Theme = userpreferences.ThemeDark
				requestDigest, err = canonicalOperationRequestDigest(changed)
				if err != nil {
					t.Fatal(err)
				}
			case "wrong-version":
				receipt.Version = 2
			case "wrong-actor":
				receipt.Actor = uuid.New()
			case "wrong-method":
				receipt.Method = http.MethodPost
			case "wrong-path":
				receipt.Path = "/api/v1/other/"
			case "wrong-resource":
				id = uuid.New()
			case "wrong-table":
				table = "other:v1"
			case "body-digest-mismatch":
				receipt.Preferences.Theme = userpreferences.ThemeDark
			case "null-favorites":
				receipt.Preferences.Favorites = nil
			case "null-optional":
				receipt.Preferences.PinnedClusters = nil
			}
			raw, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unknown-field":
				raw = append(raw[:len(raw)-1], []byte(`,"extra":true}`)...)
			case "duplicate-field":
				raw = append(raw[:len(raw)-1], []byte(`,"version":1}`)...)
			case "trailing-value":
				raw = append(raw, []byte(` {}`)...)
			case "oversized":
				raw = []byte(strings.Repeat(" ", maxPreferenceReceiptBytes+1))
			}
			q := &fakeOperationIdempotencyStore{rows: map[string]sqlc.OperationIdempotencyKey{
				scope.scope + "\x00" + scope.key: {Scope: scope.scope, IdempotencyKey: scope.key, OperationTable: table, OperationID: pgtype.UUID{Bytes: id, Valid: true}, Response: raw},
			}}
			got, replay, err := claimPreferenceReceipt(ctx, q, r, actor, requestDigest)
			if mode == "valid" {
				if err != nil || !replay || got.Theme != prefs.Theme {
					t.Fatal("valid committed receipt rejected")
				}
				return
			}
			expected := errPreferenceReceiptInvalid
			if mode == "changed-request" {
				expected = errOperationIdempotencyConflict
			}
			if replay || !errors.Is(err, expected) {
				t.Fatal("corrupt receipt became a replay or wrong public error category")
			}
		})
	}
}

func TestPreferenceReceiptReservationRejectsPartialState(t *testing.T) {
	for _, mode := range []string{"fresh", "nonempty-table", "nonempty-response", "null-response", "empty-response", "array-response"} {
		t.Run(mode, func(t *testing.T) {
			actor := uuid.New()
			r := preferenceRequest(http.MethodPut, actor, nil)
			r.Header.Set("Idempotency-Key", "partial-reader")
			ctx := withOperationIdempotency(r, "user-preferences:v1")
			scope, _ := operationIdempotencyFromContext(ctx)
			row := sqlc.OperationIdempotencyKey{Scope: scope.scope, IdempotencyKey: scope.key, Response: json.RawMessage(`{}`)}
			switch mode {
			case "nonempty-table":
				row.OperationTable = preferenceReceiptTable
			case "nonempty-response":
				row.Response = json.RawMessage(`{"version":1}`)
			case "null-response":
				row.Response = json.RawMessage(`null`)
			case "empty-response":
				row.Response = nil
			case "array-response":
				row.Response = json.RawMessage(`[]`)
			}
			q := &fakeOperationIdempotencyStore{rows: map[string]sqlc.OperationIdempotencyKey{scope.scope + "\x00" + scope.key: row}}
			_, replay, err := claimPreferenceReceipt(ctx, q, r, actor, "unused-for-unattached")
			if mode == "fresh" {
				if err != nil || replay {
					t.Fatal("canonical empty reservation rejected")
				}
				return
			}
			if replay || !errors.Is(err, errPreferenceReceiptInvalid) {
				t.Fatal("partial reservation was treated as a fresh mutation")
			}
		})
	}
}

func TestPreferenceReceiptWriterBoundsBeforeAttachment(t *testing.T) {
	actor := uuid.New()
	r := preferenceRequest(http.MethodPut, actor, nil)
	r.Header.Set("Idempotency-Key", "writer-bound")
	ctx := withOperationIdempotency(r, "user-preferences:v1")
	q := &fakeOperationIdempotencyStore{}
	prefs := userpreferences.Defaults()
	prefs.LandingRoute = strings.Repeat("x", maxPreferenceReceiptBytes)
	if err := attachPreferenceReceipt(ctx, q, r, actor, "digest", prefs); !errors.Is(err, errPreferenceReceiptInvalid) || len(q.rows) != 0 {
		t.Fatal("oversized durable receipt reached attachment")
	}
}
