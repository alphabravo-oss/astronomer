// Sprint 075 — slug-resolution shim for the platform-baseline coverage
// endpoint (internal/handler/platform_baseline_coverage.go).
//
// The baseline cluster_template references five chart slugs by string.
// To answer "is this slug resolvable today?" the coverage handler needs
// (chart_id, repository_name) for a given chart name, across all enabled
// repositories. sqlc has `GetHelmChartByRepoAndName` (scoped to one repo)
// but no global-by-name lookup; the catalog allows the same chart name
// to live in multiple repos (UNIQUE is on (repository_id, name)) so we
// pick the first match deterministically by repository name.
//
// File suffix `_ext` is intentional — same reason as
// cluster_registry_configs_ext.sql.go: keep hand-written queries out of
// the catalog.sql.go regeneration target so a future `sqlc generate` run
// doesn't drop them.

package sqlc

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrCoverageSlugNotFound is returned by ResolveChartByName when no
// helm_charts row matches the given name. Distinct from pgx.ErrNoRows so
// callers can switch on it without leaking pgx into the handler.
var ErrCoverageSlugNotFound = errors.New("coverage: slug not found in catalog")

// ChartResolution is the slim shape ResolveChartByName returns —
// just enough for the coverage endpoint to render a row.
type ChartResolution struct {
	ChartID    uuid.UUID
	Repository string
}

const resolveChartByName = `
SELECT c.id, r.name
FROM   helm_charts c
JOIN   helm_repositories r ON r.id = c.repository_id
WHERE  c.name = $1
ORDER  BY r.name ASC
LIMIT  1
`

// ResolveChartByName looks up the first helm_charts row matching the
// given chart name (the "slug"), returning the chart's UUID and the
// owning repository's display name. Ordered by repo name for stable
// resolution when the same slug exists in multiple repos. Returns
// ErrCoverageSlugNotFound when the slug isn't present in the catalog.
func (q *Queries) ResolveChartByName(ctx context.Context, name string) (ChartResolution, error) {
	row := q.db.QueryRow(ctx, resolveChartByName, name)
	var res ChartResolution
	if err := row.Scan(&res.ChartID, &res.Repository); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ChartResolution{}, ErrCoverageSlugNotFound
		}
		return ChartResolution{}, err
	}
	return res, nil
}
