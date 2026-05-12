// Migration 055 — catalog rating + recommendation CRUD, hand-authored
// sqlc shim. Mirrors what `sqlc generate` would emit for the queries
// against the migration 055 tables. Kept in its own file so a future
// sqlc regeneration run leaves these hand-written method bodies alone.

package sqlc

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ----------------------------------------------------------------------
// chart_ratings
// ----------------------------------------------------------------------

const chartRatingSelectColumns = `
    id, chart_id, installation_id, user_id, stars, note, created_at, updated_at`

func scanChartRatingRow(row interface {
	Scan(dest ...any) error
}) (ChartRating, error) {
	var i ChartRating
	err := row.Scan(
		&i.ID,
		&i.ChartID,
		&i.InstallationID,
		&i.UserID,
		&i.Stars,
		&i.Note,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	return i, err
}

const createChartRating = `-- name: CreateChartRating :one
INSERT INTO chart_ratings (chart_id, installation_id, user_id, stars, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING ` + chartRatingSelectColumns

// CreateChartRatingParams binds the INSERT for chart_ratings.
type CreateChartRatingParams struct {
	ChartID        uuid.UUID   `json:"chart_id"`
	InstallationID pgtype.UUID `json:"installation_id"`
	UserID         uuid.UUID   `json:"user_id"`
	Stars          int16       `json:"stars"`
	Note           string      `json:"note"`
}

func (q *Queries) CreateChartRating(ctx context.Context, arg CreateChartRatingParams) (ChartRating, error) {
	row := q.db.QueryRow(ctx, createChartRating,
		arg.ChartID, arg.InstallationID, arg.UserID, arg.Stars, arg.Note)
	return scanChartRatingRow(row)
}

const updateChartRating = `-- name: UpdateChartRating :one
UPDATE chart_ratings
SET stars = $2, note = $3, updated_at = now()
WHERE id = $1
RETURNING ` + chartRatingSelectColumns

type UpdateChartRatingParams struct {
	ID    uuid.UUID `json:"id"`
	Stars int16     `json:"stars"`
	Note  string    `json:"note"`
}

func (q *Queries) UpdateChartRating(ctx context.Context, arg UpdateChartRatingParams) (ChartRating, error) {
	row := q.db.QueryRow(ctx, updateChartRating, arg.ID, arg.Stars, arg.Note)
	return scanChartRatingRow(row)
}

const getChartRatingByID = `-- name: GetChartRatingByID :one
SELECT ` + chartRatingSelectColumns + `
FROM chart_ratings
WHERE id = $1`

func (q *Queries) GetChartRatingByID(ctx context.Context, id uuid.UUID) (ChartRating, error) {
	return scanChartRatingRow(q.db.QueryRow(ctx, getChartRatingByID, id))
}

const getChartRatingByUserAndInstallation = `-- name: GetChartRatingByUserAndInstallation :one
SELECT ` + chartRatingSelectColumns + `
FROM chart_ratings
WHERE user_id = $1 AND installation_id = $2
LIMIT 1`

// GetChartRatingByUserAndInstallation looks up a per-(user, installation)
// rating. Pass a valid pgtype.UUID with the installation ID for the
// installed-rating case; for the "rated without installing" case use
// GetChartRatingByUserAndChartNoInstall.
type GetChartRatingByUserAndInstallationParams struct {
	UserID         uuid.UUID   `json:"user_id"`
	InstallationID pgtype.UUID `json:"installation_id"`
}

func (q *Queries) GetChartRatingByUserAndInstallation(ctx context.Context, arg GetChartRatingByUserAndInstallationParams) (ChartRating, error) {
	row := q.db.QueryRow(ctx, getChartRatingByUserAndInstallation, arg.UserID, arg.InstallationID)
	return scanChartRatingRow(row)
}

const getChartRatingByUserAndChartNoInstall = `-- name: GetChartRatingByUserAndChartNoInstall :one
SELECT ` + chartRatingSelectColumns + `
FROM chart_ratings
WHERE user_id = $1 AND chart_id = $2 AND installation_id IS NULL
LIMIT 1`

type GetChartRatingByUserAndChartNoInstallParams struct {
	UserID  uuid.UUID `json:"user_id"`
	ChartID uuid.UUID `json:"chart_id"`
}

func (q *Queries) GetChartRatingByUserAndChartNoInstall(ctx context.Context, arg GetChartRatingByUserAndChartNoInstallParams) (ChartRating, error) {
	row := q.db.QueryRow(ctx, getChartRatingByUserAndChartNoInstall, arg.UserID, arg.ChartID)
	return scanChartRatingRow(row)
}

// GetChartRatingForUserChart returns the user's rating against a chart
// regardless of whether it was installation-bound. Prefers the
// installation-bound row when one exists (most recent).
const getChartRatingForUserChart = `-- name: GetChartRatingForUserChart :one
SELECT ` + chartRatingSelectColumns + `
FROM chart_ratings
WHERE user_id = $1 AND chart_id = $2
ORDER BY (installation_id IS NULL), updated_at DESC
LIMIT 1`

type GetChartRatingForUserChartParams struct {
	UserID  uuid.UUID `json:"user_id"`
	ChartID uuid.UUID `json:"chart_id"`
}

func (q *Queries) GetChartRatingForUserChart(ctx context.Context, arg GetChartRatingForUserChartParams) (ChartRating, error) {
	row := q.db.QueryRow(ctx, getChartRatingForUserChart, arg.UserID, arg.ChartID)
	return scanChartRatingRow(row)
}

const listChartRatingsByChart = `-- name: ListChartRatingsByChart :many
SELECT ` + chartRatingSelectColumns + `
FROM chart_ratings
WHERE chart_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3`

type ListChartRatingsByChartParams struct {
	ChartID uuid.UUID `json:"chart_id"`
	Limit   int32     `json:"limit"`
	Offset  int32     `json:"offset"`
}

func (q *Queries) ListChartRatingsByChart(ctx context.Context, arg ListChartRatingsByChartParams) ([]ChartRating, error) {
	rows, err := q.db.Query(ctx, listChartRatingsByChart, arg.ChartID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ChartRating{}
	for rows.Next() {
		i, err := scanChartRatingRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const countChartRatingsByChart = `-- name: CountChartRatingsByChart :one
SELECT COUNT(*) FROM chart_ratings WHERE chart_id = $1`

func (q *Queries) CountChartRatingsByChart(ctx context.Context, chartID uuid.UUID) (int64, error) {
	row := q.db.QueryRow(ctx, countChartRatingsByChart, chartID)
	var n int64
	err := row.Scan(&n)
	return n, err
}

const deleteChartRating = `-- name: DeleteChartRating :exec
DELETE FROM chart_ratings WHERE id = $1`

func (q *Queries) DeleteChartRating(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.Exec(ctx, deleteChartRating, id)
	return err
}

// chartRatingHistogramRow is the shape returned by ChartRatingHistogram.
type chartRatingHistogramRow struct {
	Stars int16
	Count int64
}

const chartRatingHistogram = `-- name: ChartRatingHistogram :many
SELECT stars, COUNT(*) FROM chart_ratings WHERE chart_id = $1 GROUP BY stars`

// ChartRatingHistogram returns the per-star counts as a length-5 array
// indexed by stars-1 (so [0] is 1-star, [4] is 5-star).
func (q *Queries) ChartRatingHistogram(ctx context.Context, chartID uuid.UUID) ([5]int64, error) {
	var out [5]int64
	rows, err := q.db.Query(ctx, chartRatingHistogram, chartID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r chartRatingHistogramRow
		if err := rows.Scan(&r.Stars, &r.Count); err != nil {
			return out, err
		}
		if r.Stars >= 1 && r.Stars <= 5 {
			out[r.Stars-1] = r.Count
		}
	}
	return out, rows.Err()
}

const listDistinctRatedChartIDs = `-- name: ListDistinctRatedChartIDs :many
SELECT DISTINCT chart_id FROM chart_ratings`

func (q *Queries) ListDistinctRatedChartIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := q.db.Query(ctx, listDistinctRatedChartIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		items = append(items, id)
	}
	return items, rows.Err()
}

// ----------------------------------------------------------------------
// chart_rating_aggregates
// ----------------------------------------------------------------------

const chartAggregateSelectColumns = `
    chart_id, rating_count, rating_sum, avg_stars, bayesian_score, updated_at`

func scanChartAggregateRow(row interface {
	Scan(dest ...any) error
}) (ChartRatingAggregate, error) {
	var i ChartRatingAggregate
	err := row.Scan(
		&i.ChartID, &i.RatingCount, &i.RatingSum, &i.AvgStars, &i.BayesianScore, &i.UpdatedAt,
	)
	return i, err
}

const getChartRatingAggregate = `-- name: GetChartRatingAggregate :one
SELECT ` + chartAggregateSelectColumns + `
FROM chart_rating_aggregates
WHERE chart_id = $1`

func (q *Queries) GetChartRatingAggregate(ctx context.Context, chartID uuid.UUID) (ChartRatingAggregate, error) {
	return scanChartAggregateRow(q.db.QueryRow(ctx, getChartRatingAggregate, chartID))
}

const upsertChartRatingAggregate = `-- name: UpsertChartRatingAggregate :one
INSERT INTO chart_rating_aggregates
    (chart_id, rating_count, rating_sum, avg_stars, bayesian_score, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (chart_id) DO UPDATE SET
    rating_count = EXCLUDED.rating_count,
    rating_sum = EXCLUDED.rating_sum,
    avg_stars = EXCLUDED.avg_stars,
    bayesian_score = EXCLUDED.bayesian_score,
    updated_at = now()
RETURNING ` + chartAggregateSelectColumns

// UpsertChartRatingAggregateParams binds the recompute write. The
// caller is responsible for computing the Bayesian score; this method
// only persists the result.
type UpsertChartRatingAggregateParams struct {
	ChartID       uuid.UUID      `json:"chart_id"`
	RatingCount   int32          `json:"rating_count"`
	RatingSum     int32          `json:"rating_sum"`
	AvgStars      pgtype.Numeric `json:"avg_stars"`
	BayesianScore pgtype.Numeric `json:"bayesian_score"`
}

func (q *Queries) UpsertChartRatingAggregate(ctx context.Context, arg UpsertChartRatingAggregateParams) (ChartRatingAggregate, error) {
	row := q.db.QueryRow(ctx, upsertChartRatingAggregate,
		arg.ChartID, arg.RatingCount, arg.RatingSum, arg.AvgStars, arg.BayesianScore)
	return scanChartAggregateRow(row)
}

const listTopChartsByBayesian = `-- name: ListTopChartsByBayesian :many
SELECT ` + chartAggregateSelectColumns + `
FROM chart_rating_aggregates
WHERE rating_count >= $1
ORDER BY bayesian_score DESC, rating_count DESC
LIMIT $2`

type ListTopChartsByBayesianParams struct {
	MinRatingCount int32 `json:"min_rating_count"`
	Limit          int32 `json:"limit"`
}

func (q *Queries) ListTopChartsByBayesian(ctx context.Context, arg ListTopChartsByBayesianParams) ([]ChartRatingAggregate, error) {
	rows, err := q.db.Query(ctx, listTopChartsByBayesian, arg.MinRatingCount, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ChartRatingAggregate{}
	for rows.Next() {
		i, err := scanChartAggregateRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ----------------------------------------------------------------------
// chart_co_installation
// ----------------------------------------------------------------------

// TruncateChartCoInstallation drops every edge so the recompute can
// re-INSERT the full matrix inside a transaction. Used by the nightly
// RecomputeCoInstallation task.
const truncateChartCoInstallation = `-- name: TruncateChartCoInstallation :exec
TRUNCATE TABLE chart_co_installation`

func (q *Queries) TruncateChartCoInstallation(ctx context.Context) error {
	_, err := q.db.Exec(ctx, truncateChartCoInstallation)
	return err
}

const upsertChartCoInstallation = `-- name: UpsertChartCoInstallation :exec
INSERT INTO chart_co_installation (chart_a_id, chart_b_id, weight, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (chart_a_id, chart_b_id) DO UPDATE SET
    weight = EXCLUDED.weight,
    updated_at = now()`

type UpsertChartCoInstallationParams struct {
	ChartAID uuid.UUID `json:"chart_a_id"`
	ChartBID uuid.UUID `json:"chart_b_id"`
	Weight   int32     `json:"weight"`
}

func (q *Queries) UpsertChartCoInstallation(ctx context.Context, arg UpsertChartCoInstallationParams) error {
	_, err := q.db.Exec(ctx, upsertChartCoInstallation, arg.ChartAID, arg.ChartBID, arg.Weight)
	return err
}

// ListChartCoInstallationsFor returns every edge incident to chartID.
// Because the table only stores (a < b), we query both sides and let
// the caller normalize to "the other chart" when surfacing results.
const listChartCoInstallationsFor = `-- name: ListChartCoInstallationsFor :many
SELECT chart_a_id, chart_b_id, weight, updated_at
FROM chart_co_installation
WHERE (chart_a_id = $1 OR chart_b_id = $1)
ORDER BY weight DESC
LIMIT $2`

type ListChartCoInstallationsForParams struct {
	ChartID uuid.UUID `json:"chart_id"`
	Limit   int32     `json:"limit"`
}

func (q *Queries) ListChartCoInstallationsFor(ctx context.Context, arg ListChartCoInstallationsForParams) ([]ChartCoInstallation, error) {
	rows, err := q.db.Query(ctx, listChartCoInstallationsFor, arg.ChartID, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ChartCoInstallation{}
	for rows.Next() {
		var c ChartCoInstallation
		if err := rows.Scan(&c.ChartAID, &c.ChartBID, &c.Weight, &c.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}

// ListInstalledChartChartPairs scans installed_charts (joined to
// helm_chart_versions to recover the chart_id) for rows whose
// installation timestamps fall within the supplied 30-day window
// per cluster. Returns canonicalized (a < b) chart-id pairs along
// with the cluster they co-occurred in, used by the nightly
// co-installation recompute. We do the windowing + de-dup in Go,
// so this query is just the raw join.
const listInstalledChartChartPairs = `-- name: ListInstalledChartChartPairs :many
SELECT ic.cluster_id, hcv.chart_id, ic.created_at
FROM installed_charts ic
JOIN helm_chart_versions hcv ON hcv.id = ic.chart_version_id
WHERE ic.chart_version_id IS NOT NULL
ORDER BY ic.cluster_id, ic.created_at`

// InstalledChartChartPair is one (cluster, chart, installed-at) tuple
// emitted by ListInstalledChartChartPairs. Defined here because the
// query is materialized as a Go slice rather than a one-shot scan.
type InstalledChartChartPair struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	ChartID   uuid.UUID `json:"chart_id"`
	CreatedAt pgtype.Timestamptz
}

func (q *Queries) ListInstalledChartChartPairs(ctx context.Context) ([]InstalledChartChartPair, error) {
	rows, err := q.db.Query(ctx, listInstalledChartChartPairs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []InstalledChartChartPair{}
	for rows.Next() {
		var p InstalledChartChartPair
		if err := rows.Scan(&p.ClusterID, &p.ChartID, &p.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}
