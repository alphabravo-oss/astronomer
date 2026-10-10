package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
)

func TestPreferencePostgresAttachmentFailureRollsBackReplacementAuditAndReservation(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	actor := preferencePostgresUser(t, ctx, tx)
	state := &preferencePostgresBoundary{}
	h := preferencePostgresHandler(t, ctx, tx, state)
	body := userpreferences.Defaults()
	if response := preferencePostgresPut(t, h, actor, body, "initial"); response.Code != http.StatusOK {
		t.Fatal("initial preference setup failed")
	}
	state.upsertCalls, state.auditCalls, state.reserveCalls, state.attachCalls = 0, 0, 0, 0
	state.attachmentFailure = true
	body.Theme = userpreferences.ThemeDark
	response := preferencePostgresPut(t, h, actor, body, "attachment-failure")
	row, err := sqlc.New(tx).GetUserPreferences(ctx, actor)
	request := preferenceRequest(http.MethodPut, actor, body)
	request.Header.Set("Idempotency-Key", "attachment-failure")
	scope, _ := operationIdempotencyFromContext(withOperationIdempotency(request, "user-preferences:v1"))
	var reserved int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM operation_idempotency_keys WHERE scope = $1 AND idempotency_key = $2`, scope.scope, scope.key).Scan(&reserved); err != nil {
		t.Fatal("cannot inspect actual rolled-back reservation")
	}
	if response.Code != http.StatusInternalServerError || err != nil || row.Theme != "system" || preferencePostgresAuditCount(t, ctx, tx, actor) != 1 || reserved != 0 || state.reserveCalls != 1 || state.upsertCalls != 1 || state.auditCalls != 1 || state.attachCalls != 1 {
		t.Fatal("attachment failure did not roll back actual reservation, replacement and mandatory audit")
	}
	state.attachmentFailure = false
	if response := preferencePostgresPut(t, h, actor, body, "attachment-failure"); response.Code != http.StatusOK {
		t.Fatal("attachment rollback prevented successful retry")
	}
	row, err = sqlc.New(tx).GetUserPreferences(ctx, actor)
	if err != nil || row.Theme != "dark" || preferencePostgresAuditCount(t, ctx, tx, actor) != 2 {
		t.Fatal("retry failed to commit one replacement and one intent")
	}
}

func TestPreferencePostgresReplayReturnsReceiptWithoutOverwritingLaterReplacement(t *testing.T) {
	ctx, tx := paginationPostgresTx(t)
	actor := preferencePostgresUser(t, ctx, tx)
	state := &preferencePostgresBoundary{}
	h := preferencePostgresHandler(t, ctx, tx, state)
	original := userpreferences.Defaults()
	original.Theme = userpreferences.ThemeDark
	first := preferencePostgresPut(t, h, actor, original, "stable-key")
	if first.Code != http.StatusOK {
		t.Fatal("first keyed preference failed")
	}
	changed := original
	changed.Theme = userpreferences.ThemeLight
	if response := preferencePostgresPut(t, h, actor, changed, ""); response.Code != http.StatusOK {
		t.Fatal("ordinary replacement failed")
	}
	state.upsertCalls, state.auditCalls, state.reserveCalls, state.attachCalls = 0, 0, 0, 0
	replay := preferencePostgresPut(t, h, actor, original, "stable-key")
	row, err := sqlc.New(tx).GetUserPreferences(ctx, actor)
	if replay.Code != http.StatusOK || first.Body.String() != replay.Body.String() || err != nil || row.Theme != "light" || state.reserveCalls != 1 || state.upsertCalls != 0 || state.auditCalls != 0 || state.attachCalls != 0 || preferencePostgresAuditCount(t, ctx, tx, actor) != 2 {
		t.Fatal("same key replay did not preserve exact committed receipt and current ordinary replacement")
	}
	var document struct {
		Data userpreferences.Preferences `json:"data"`
	}
	if json.Unmarshal(replay.Body.Bytes(), &document) != nil || document.Data.Theme != userpreferences.ThemeDark {
		t.Fatal("key replay fabricated latest state instead of exact committed response")
	}
}
