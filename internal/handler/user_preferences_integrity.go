package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const preferenceReceiptTable = "user_preferences:v1"
const maxPreferenceReceiptBytes = 32 * 1024

var (
	errPreferenceReceiptUnavailable = errors.New("preference receipt persistence unavailable")
	errPreferenceReceiptInvalid     = errors.New("stored preference receipt is invalid")
)

// preferenceReceipt is local durable replay state, not a public API type.
// OperationID points at the actor's existing preference resource; no synthetic
// operation or task is created. Reservation, replacement, audit and attachment
// use the same transaction-bound sqlc querier and physical session.
type preferenceReceipt struct {
	Version       int                         `json:"version"`
	Actor         uuid.UUID                   `json:"actor"`
	Method        string                      `json:"method"`
	Path          string                      `json:"path"`
	RequestDigest string                      `json:"request_digest"`
	Preferences   userpreferences.Preferences `json:"preferences"`
}

func claimPreferenceReceipt(ctx context.Context, q resourceOperationIdempotencyQuerier, r *http.Request, actor uuid.UUID, digest string) (userpreferences.Preferences, bool, error) {
	var zero userpreferences.Preferences
	item, ok := operationIdempotencyFromContext(ctx)
	if !ok {
		return zero, false, errPreferenceReceiptUnavailable
	}
	// ON CONFLICT's self-update takes the row lock. A concurrent request cannot
	// inspect a receipt until the first transaction commits or rolls back.
	claim, err := q.ReserveOperationIdempotencyKey(ctx, sqlc.ReserveOperationIdempotencyKeyParams{Scope: item.scope, IdempotencyKey: item.key})
	if err != nil {
		return zero, false, err
	}
	if len(claim.Response) > maxPreferenceReceiptBytes {
		return zero, false, errPreferenceReceiptInvalid
	}
	if !claim.OperationID.Valid {
		// Canonical SQL reserves exactly an empty table and empty JSON object.
		// A partially attached record is corruption, not a fresh request.
		var pending map[string]json.RawMessage
		if claim.OperationTable != "" || jsoncontract.Decode(claim.Response, &pending) != nil || pending == nil || len(pending) != 0 {
			return zero, false, errPreferenceReceiptInvalid
		}
		return zero, false, nil
	}
	if claim.OperationTable != preferenceReceiptTable || uuid.UUID(claim.OperationID.Bytes) != actor {
		return zero, false, errPreferenceReceiptInvalid
	}
	var stored preferenceReceipt
	if jsoncontract.Decode(claim.Response, &stored) != nil || stored.Version != 1 || stored.Actor != actor || stored.Method != r.Method || stored.Path != r.URL.EscapedPath() || stored.RequestDigest == "" {
		return zero, false, errPreferenceReceiptInvalid
	}
	if stored.Preferences.PinnedClusters == nil || stored.Preferences.StarredTypes == nil || stored.Preferences.Validate() != nil {
		return zero, false, errPreferenceReceiptInvalid
	}
	actualDigest, err := canonicalOperationRequestDigest(stored.Preferences)
	if err != nil || actualDigest != stored.RequestDigest {
		return zero, false, errPreferenceReceiptInvalid
	}
	if actualDigest != digest {
		return zero, false, errOperationIdempotencyConflict
	}
	return stored.Preferences, true, nil
}

func attachPreferenceReceipt(ctx context.Context, q resourceOperationIdempotencyQuerier, r *http.Request, actor uuid.UUID, digest string, stored userpreferences.Preferences) error {
	receipt := preferenceReceipt{Version: 1, Actor: actor, Method: r.Method, Path: r.URL.EscapedPath(), RequestDigest: digest, Preferences: stored}
	item, ok := operationIdempotencyFromContext(ctx)
	if !ok {
		return errPreferenceReceiptUnavailable
	}
	raw, err := json.Marshal(receipt)
	if err != nil || len(raw) > maxPreferenceReceiptBytes {
		return errPreferenceReceiptInvalid
	}
	_, err = q.AttachOperationIdempotencyKey(ctx, sqlc.AttachOperationIdempotencyKeyParams{Scope: item.scope, IdempotencyKey: item.key, OperationTable: preferenceReceiptTable, OperationID: actor, Response: raw})
	return err
}

// preferenceAuditIntent keeps the existing request-shaped event and nine
// bounded detail fields. Only this domain's dedupe identity includes the
// canonical body: ordinary replacements remain allowed without a valid key,
// while a reused request ID cannot conceal a changed preference document.
func preferenceAuditIntent(r *http.Request, actor uuid.UUID, prefs userpreferences.Preferences, digest string) audit.Intent {
	requestID := reqctx.RequestID(r.Context())
	if requestID == "" {
		requestID = uuid.NewString()
	}
	seed := audit.MutationDedupeKey(requestID, actor.String(), r.Method, r.URL.EscapedPath())
	if values := r.Header.Values("Idempotency-Key"); len(values) == 1 && validOperationIdempotencyKey(values[0]) {
		seed = audit.MutationDedupeKey(strings.TrimSpace(values[0]), actor.String(), r.Method, r.URL.EscapedPath())
	}
	seed = audit.MutationDedupeKey(seed, digest, "user_preferences", actor.String())
	return audit.Intent{
		Event: audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
			Request: r, Source: "service", CorrelationID: reqctx.CorrelationID(r.Context()),
			UserID: pgtype.UUID{Bytes: actor, Valid: true}, ActorAuthMethod: authMethodFromRequest(r),
			Action: "user.preferences.updated", ResourceType: "user_preferences", ResourceID: actor.String(), ResourceName: "Console preferences",
			StatusCode: http.StatusOK, RequestID: requestID, IPAddress: reqctx.ClientIP(r),
			Detail: map[string]any{
				"theme": prefs.Theme, "table_density": prefs.TableDensity, "landing_route": prefs.LandingRoute, "time_format": prefs.TimeFormat,
				"favorite_count": len(prefs.Favorites), "pinned_cluster_count": len(prefs.PinnedClusters), "starred_type_count": len(prefs.StarredTypes), "rows_per_page": prefs.RowsPerPage, "date_format": prefs.DateFormat,
			},
		}),
		DedupeKey: audit.MutationDedupeKey(seed, "user.preferences.updated", "user_preferences", actor.String()),
	}
}
