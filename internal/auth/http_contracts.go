package auth

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type apiTokenContextKey struct{}

// TokenUserQuerier resolves API tokens to concrete users.
type TokenUserQuerier interface {
	GetTokenByHash(ctx context.Context, tokenHash string) (sqlc.ApiToken, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	UpdateAPITokenLastUsed(ctx context.Context, id uuid.UUID) error
}

func WithAuthenticatedAPIToken(ctx context.Context, token *sqlc.ApiToken) context.Context {
	return context.WithValue(ctx, apiTokenContextKey{}, token)
}

func AuthenticatedAPIToken(ctx context.Context) (*sqlc.ApiToken, bool) {
	if ctx == nil {
		return nil, false
	}
	token, ok := ctx.Value(apiTokenContextKey{}).(*sqlc.ApiToken)
	return token, ok && token != nil
}
