package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type persistedOperationReceipt[T any] struct {
	RequestDigest string `json:"request_digest"`
	Receipt       T      `json:"receipt"`
}

func canonicalOperationRequestDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}

// claimOperationReceipt reserves the actor+route scoped key and, on replay,
// returns the exact committed receipt after verifying the canonical request.
func claimOperationReceipt[T any](ctx context.Context, q resourceOperationIdempotencyQuerier, operationTable, requestDigest string) (uuid.UUID, T, bool, error) {
	var zero T
	item, ok := operationIdempotencyFromContext(ctx)
	if !ok {
		return uuid.Nil, zero, false, errors.New("operation idempotency context is missing")
	}
	claim, err := q.ReserveOperationIdempotencyKey(ctx, sqlc.ReserveOperationIdempotencyKeyParams{Scope: item.scope, IdempotencyKey: item.key})
	if err != nil {
		return uuid.Nil, zero, false, err
	}
	if !claim.OperationID.Valid {
		return uuid.Nil, zero, false, nil
	}
	if claim.OperationTable != operationTable {
		return uuid.Nil, zero, false, errOperationIdempotencyConflict
	}
	var stored persistedOperationReceipt[T]
	if len(claim.Response) == 0 || json.Unmarshal(claim.Response, &stored) != nil || stored.RequestDigest == "" {
		return uuid.Nil, zero, false, errOperationIdempotencyConflict
	}
	if stored.RequestDigest != requestDigest {
		return uuid.Nil, zero, false, errOperationIdempotencyConflict
	}
	return uuid.UUID(claim.OperationID.Bytes), stored.Receipt, true, nil
}

func attachOperationReceipt[T any](ctx context.Context, q resourceOperationIdempotencyQuerier, operationTable string, operationID uuid.UUID, requestDigest string, receipt T) error {
	return attachResourceOperation(ctx, q, operationTable, operationID, persistedOperationReceipt[T]{RequestDigest: requestDigest, Receipt: receipt})
}

func validOperationIdempotencyKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

// RequireOperationIdempotencyKey rejects durable mutations that cannot be
// safely replayed across server replicas. Authorization must run first.
func RequireOperationIdempotencyKey(w http.ResponseWriter, r *http.Request) bool {
	if r != nil && len(r.Header.Values("Idempotency-Key")) == 1 && validOperationIdempotencyKey(r.Header.Get("Idempotency-Key")) {
		return true
	}
	RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "A valid Idempotency-Key header is required")
	return false
}

var errOperationIdempotencyConflict = errors.New("idempotency key already identifies a different operation")

type resourceOperationIdempotencyQuerier interface {
	ReserveOperationIdempotencyKey(context.Context, sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error)
	AttachOperationIdempotencyKey(context.Context, sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error)
}

// claimResourceOperation reserves one caller-scoped key inside the mutation
// transaction. A committed operation ID means this is a replay and the caller
// must return that exact durable resource without repeating side effects.
func claimResourceOperation(ctx context.Context, q resourceOperationIdempotencyQuerier, operationTable string) (uuid.UUID, bool, error) {
	item, ok := operationIdempotencyFromContext(ctx)
	if !ok {
		return uuid.Nil, false, errors.New("operation idempotency context is missing")
	}
	claim, err := q.ReserveOperationIdempotencyKey(ctx, sqlc.ReserveOperationIdempotencyKeyParams{
		Scope: item.scope, IdempotencyKey: item.key,
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	if !claim.OperationID.Valid {
		return uuid.Nil, false, nil
	}
	if claim.OperationTable != operationTable {
		return uuid.Nil, false, errOperationIdempotencyConflict
	}
	return uuid.UUID(claim.OperationID.Bytes), true, nil
}

func attachResourceOperation(ctx context.Context, q resourceOperationIdempotencyQuerier, operationTable string, operationID uuid.UUID, response any) error {
	item, ok := operationIdempotencyFromContext(ctx)
	if !ok {
		return errors.New("operation idempotency context is missing")
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = q.AttachOperationIdempotencyKey(ctx, sqlc.AttachOperationIdempotencyKeyParams{
		Scope: item.scope, IdempotencyKey: item.key, OperationTable: operationTable,
		OperationID: operationID, Response: raw,
	})
	return err
}

type operationIdempotencyContextKey struct{}

type operationIdempotencyContext struct {
	scope string
	key   string
}

func withOperationIdempotency(r *http.Request, domain string) context.Context {
	if r == nil {
		return context.Background()
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		return r.Context()
	}
	userScope := "anonymous"
	if userID := currentUserUUID(r); userID.Valid {
		userScope = uuid.UUID(userID.Bytes).String()
	}
	route := r.URL.EscapedPath()
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := strings.TrimSpace(routeContext.RoutePattern()); pattern != "" {
			route = pattern
		}
	}
	scope := strings.Join([]string{
		strings.TrimSpace(domain),
		"user:" + userScope,
		strings.ToUpper(r.Method),
		route,
	}, ":")
	return context.WithValue(r.Context(), operationIdempotencyContextKey{}, operationIdempotencyContext{
		scope: scope,
		key:   key,
	})
}

func operationIdempotencyFromContext(ctx context.Context) (operationIdempotencyContext, bool) {
	item, ok := ctx.Value(operationIdempotencyContextKey{}).(operationIdempotencyContext)
	if !ok {
		return operationIdempotencyContext{}, false
	}
	if strings.TrimSpace(item.scope) == "" || strings.TrimSpace(item.key) == "" {
		return operationIdempotencyContext{}, false
	}
	return item, true
}
