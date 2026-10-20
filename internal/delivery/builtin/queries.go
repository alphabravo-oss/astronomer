package builtin

import (
	"context"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Both interfaces receive sqlc.New(tx) from the existing asset transaction.
// Session advisory lock ownership remains on its checked-out connection.
type builtinSourceQueries interface {
	EnsureBuiltinCatalogSource(context.Context, sqlc.EnsureBuiltinCatalogSourceParams) error
	BuiltinCatalogSourceMatches(context.Context, sqlc.BuiltinCatalogSourceMatchesParams) (bool, error)
}

type builtinComponentQueries interface {
	EnsureBuiltinComponentBundle(context.Context, sqlc.EnsureBuiltinComponentBundleParams) error
	GetBuiltinComponentBundleProject(context.Context, uuid.UUID) (uuid.UUID, error)
	EnsureBuiltinComponentVersion(context.Context, sqlc.EnsureBuiltinComponentVersionParams) error
	GetBuiltinComponentVersionIdentity(context.Context, uuid.UUID) (sqlc.GetBuiltinComponentVersionIdentityRow, error)
	EnsureBuiltinComponentTarget(context.Context, sqlc.EnsureBuiltinComponentTargetParams) (int64, error)
}
