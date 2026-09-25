package handler

import (
	"context"
	"errors"
	"math"

	"github.com/google/uuid"
)

const catalogVisibilityPageSize int32 = 500

// Visibility must cover the complete authorized inventory. Each database read
// is bounded; errors or a non-advancing backend fail instead of hiding rows.
func collectCatalogVisibilityPages[T any](ctx context.Context, fetch func(int32, int32) ([]T, error), identity func(T) uuid.UUID) ([]T, error) {
	var result []T
	seen := map[uuid.UUID]struct{}{}
	for offset := int32(0); ; offset += catalogVisibilityPageSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := fetch(catalogVisibilityPageSize, offset)
		if err != nil {
			return nil, err
		}
		if len(rows) > int(catalogVisibilityPageSize) {
			return nil, errors.New("catalog visibility page exceeded its bound")
		}
		advanced := false
		for _, row := range rows {
			id := identity(row)
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, row)
			advanced = true
		}
		if len(rows) < int(catalogVisibilityPageSize) {
			return result, nil
		}
		if !advanced || offset > math.MaxInt32-catalogVisibilityPageSize {
			return nil, errors.New("catalog visibility pagination did not advance")
		}
	}
}
