package userpreferences

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

const preferenceReceiptTable = "user_preferences:v1"
const maxPreferenceReceiptBytes = 32 * 1024

var (
	ErrReceiptUnavailable = errors.New("preference receipt persistence unavailable")
	ErrReceiptInvalid     = errors.New("stored preference receipt is invalid")
)

// preferenceReceipt is local durable replay state, not a public API type.
// OperationID points at the actor's existing preference resource; no synthetic
// operation or task is created. Reservation, replacement, audit and attachment
// use the same transaction-bound sqlc querier and physical session.
type preferenceReceipt struct {
	Version       int         `json:"version"`
	Actor         uuid.UUID   `json:"actor"`
	Method        string      `json:"method"`
	Path          string      `json:"path"`
	RequestDigest string      `json:"request_digest"`
	Preferences   Preferences `json:"preferences"`
}

func claimPreferenceReceipt(ctx context.Context, q ReceiptQueries, replay Replay, actor uuid.UUID, digest string) (Preferences, bool, error) {
	var zero Preferences
	item, ok := replay, replay.Scope != "" && replay.Key != ""
	if !ok {
		return zero, false, ErrReceiptUnavailable
	}
	// ON CONFLICT's self-update takes the row lock. A concurrent request cannot
	// inspect a receipt until the first transaction commits or rolls back.
	claim, err := q.ReserveOperationIdempotencyKey(ctx, sqlc.ReserveOperationIdempotencyKeyParams{Scope: item.Scope, IdempotencyKey: item.Key})
	if err != nil {
		return zero, false, err
	}
	if len(claim.Response) > maxPreferenceReceiptBytes {
		return zero, false, ErrReceiptInvalid
	}
	if !claim.OperationID.Valid {
		// Canonical SQL reserves exactly an empty table and empty JSON object.
		// A partially attached record is corruption, not a fresh request.
		var pending map[string]json.RawMessage
		if claim.OperationTable != "" || jsoncontract.Decode(claim.Response, &pending) != nil || pending == nil || len(pending) != 0 {
			return zero, false, ErrReceiptInvalid
		}
		return zero, false, nil
	}
	if claim.OperationTable != preferenceReceiptTable || uuid.UUID(claim.OperationID.Bytes) != actor {
		return zero, false, ErrReceiptInvalid
	}
	var stored preferenceReceipt
	if jsoncontract.Decode(claim.Response, &stored) != nil || stored.Version != 1 || stored.Actor != actor || stored.Method != replay.Method || stored.Path != replay.Path || stored.RequestDigest == "" {
		return zero, false, ErrReceiptInvalid
	}
	if stored.Preferences.PinnedClusters == nil || stored.Preferences.StarredTypes == nil || stored.Preferences.Validate() != nil {
		return zero, false, ErrReceiptInvalid
	}
	actualDigest, err := DocumentDigest(stored.Preferences)
	if err != nil || actualDigest != stored.RequestDigest {
		return zero, false, ErrReceiptInvalid
	}
	if actualDigest != digest {
		return zero, false, ErrConflict
	}
	return stored.Preferences, true, nil
}

func attachPreferenceReceipt(ctx context.Context, q ReceiptQueries, replay Replay, actor uuid.UUID, digest string, stored Preferences) error {
	receipt := preferenceReceipt{Version: 1, Actor: actor, Method: replay.Method, Path: replay.Path, RequestDigest: digest, Preferences: stored}
	item, ok := replay, replay.Scope != "" && replay.Key != ""
	if !ok {
		return ErrReceiptUnavailable
	}
	raw, err := json.Marshal(receipt)
	if err != nil || len(raw) > maxPreferenceReceiptBytes {
		return ErrReceiptInvalid
	}
	_, err = q.AttachOperationIdempotencyKey(ctx, sqlc.AttachOperationIdempotencyKeyParams{Scope: item.Scope, IdempotencyKey: item.Key, OperationTable: preferenceReceiptTable, OperationID: actor, Response: raw})
	return err
}
