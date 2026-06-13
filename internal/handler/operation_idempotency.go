package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

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
	scope := strings.Join([]string{
		strings.TrimSpace(domain),
		"user:" + userScope,
		strings.ToUpper(r.Method),
		r.URL.EscapedPath(),
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
