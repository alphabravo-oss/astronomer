package userpreferences

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type receiptStoreFake struct {
	rows map[string]sqlc.OperationIdempotencyKey
}

func (q *receiptStoreFake) ReserveOperationIdempotencyKey(_ context.Context, p sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	row, ok := q.rows[p.Scope+"\x00"+p.IdempotencyKey]
	if !ok {
		row.Response = json.RawMessage(`{}`)
	}
	return row, nil
}
func (q *receiptStoreFake) AttachOperationIdempotencyKey(_ context.Context, p sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	if q.rows == nil {
		q.rows = map[string]sqlc.OperationIdempotencyKey{}
	}
	row := sqlc.OperationIdempotencyKey{Scope: p.Scope, IdempotencyKey: p.IdempotencyKey, OperationTable: p.OperationTable, OperationID: pgtype.UUID{Bytes: p.OperationID, Valid: true}, Response: p.Response}
	q.rows[p.Scope+"\x00"+p.IdempotencyKey] = row
	return row, nil
}
func TestPreferenceReceiptReaderRejectsCorruptStateAndSeparatesBodyConflict(t *testing.T) {
	for _, mode := range []string{"valid", "changed-request", "wrong-version", "wrong-actor", "wrong-method", "wrong-path", "wrong-resource", "wrong-table", "body-digest-mismatch", "null-favorites", "null-optional", "unknown-field", "duplicate-field", "trailing-value", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			actor := uuid.New()
			replay := Replay{Scope: "user-preferences:v1:" + actor.String(), Key: "receipt-reader", Method: "PUT", Path: "/api/v1/user/preferences/"}
			ctx := context.Background()
			scope := replay
			prefs := Defaults()
			digest, err := DocumentDigest(prefs)
			if err != nil {
				t.Fatal(err)
			}
			receipt := preferenceReceipt{Version: 1, Actor: actor, Method: replay.Method, Path: replay.Path, RequestDigest: digest, Preferences: prefs}
			id := actor
			table := preferenceReceiptTable
			requestDigest := digest
			switch mode {
			case "changed-request":
				changed := Defaults()
				changed.Theme = ThemeDark
				requestDigest, err = DocumentDigest(changed)
				if err != nil {
					t.Fatal(err)
				}
			case "wrong-version":
				receipt.Version = 2
			case "wrong-actor":
				receipt.Actor = uuid.New()
			case "wrong-method":
				receipt.Method = "POST"
			case "wrong-path":
				receipt.Path = "/api/v1/other/"
			case "wrong-resource":
				id = uuid.New()
			case "wrong-table":
				table = "other:v1"
			case "body-digest-mismatch":
				receipt.Preferences.Theme = ThemeDark
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
			q := &receiptStoreFake{rows: map[string]sqlc.OperationIdempotencyKey{
				scope.Scope + "\x00" + scope.Key: {Scope: scope.Scope, IdempotencyKey: scope.Key, OperationTable: table, OperationID: pgtype.UUID{Bytes: id, Valid: true}, Response: raw},
			}}
			got, replayed, err := claimPreferenceReceipt(ctx, q, replay, actor, requestDigest)
			if mode == "valid" {
				if err != nil || !replayed || got.Theme != prefs.Theme {
					t.Fatal("valid committed receipt rejected")
				}
				return
			}
			expected := ErrReceiptInvalid
			if mode == "changed-request" {
				expected = ErrConflict
			}
			if replayed || !errors.Is(err, expected) {
				t.Fatal("corrupt receipt became a replay or wrong public error category")
			}
		})
	}
}

func TestPreferenceReceiptReservationRejectsPartialState(t *testing.T) {
	for _, mode := range []string{"fresh", "nonempty-table", "nonempty-response", "null-response", "empty-response", "array-response"} {
		t.Run(mode, func(t *testing.T) {
			actor := uuid.New()
			replay := Replay{Scope: "user-preferences:v1:" + actor.String(), Key: "partial-reader", Method: "PUT", Path: "/api/v1/user/preferences/"}
			ctx := context.Background()
			scope := replay
			row := sqlc.OperationIdempotencyKey{Scope: scope.Scope, IdempotencyKey: scope.Key, Response: json.RawMessage(`{}`)}
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
			q := &receiptStoreFake{rows: map[string]sqlc.OperationIdempotencyKey{scope.Scope + "\x00" + scope.Key: row}}
			_, replayed, err := claimPreferenceReceipt(ctx, q, replay, actor, "unused-for-unattached")
			if mode == "fresh" {
				if err != nil || replayed {
					t.Fatal("canonical empty reservation rejected")
				}
				return
			}
			if replayed || !errors.Is(err, ErrReceiptInvalid) {
				t.Fatal("partial reservation was treated as a fresh mutation")
			}
		})
	}
}

func TestPreferenceReceiptWriterBoundsBeforeAttachment(t *testing.T) {
	actor := uuid.New()
	replay := Replay{Scope: "user-preferences:v1:" + actor.String(), Key: "writer-bound", Method: "PUT", Path: "/api/v1/user/preferences/"}
	ctx := context.Background()
	q := &receiptStoreFake{}
	prefs := Defaults()
	prefs.LandingRoute = strings.Repeat("x", maxPreferenceReceiptBytes)
	if err := attachPreferenceReceipt(ctx, q, replay, actor, "digest", prefs); !errors.Is(err, ErrReceiptInvalid) || len(q.rows) != 0 {
		t.Fatal("oversized durable receipt reached attachment")
	}
}
