// Migration 055 — catalog rating + recommendation models, hand-authored
// sqlc shim. Mirrors what `sqlc generate` would emit for
// internal/db/migrations/055_catalog_ratings.up.sql. Kept out of the
// canonical models.go target so future sqlc regenerations don't clobber
// these hand additions (the sqlc CLI is currently broken; tracked
// alongside the cluster_snapshots / cluster_registry shim pattern).

package sqlc

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ChartRating mirrors one row in chart_ratings: a single user's rating
// of a chart, optionally bound to a specific installation. When
// InstallationID is invalid (NULL in the DB), the partial unique index
// idx_chart_ratings_user_chart_unique constrains the user to one
// rating per chart.
type ChartRating struct {
	ID             uuid.UUID   `json:"id"`
	ChartID        uuid.UUID   `json:"chart_id"`
	InstallationID pgtype.UUID `json:"installation_id"`
	UserID         uuid.UUID   `json:"user_id"`
	Stars          int16       `json:"stars"`
	Note           string      `json:"note"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// ChartRatingAggregate mirrors one row in chart_rating_aggregates: the
// pre-computed score cache for a single chart. BayesianScore is the
// confidence-weighted average used to order "popular in your org".
// Numeric columns surface as pgtype.Numeric so the caller can decode
// to a float without losing precision.
type ChartRatingAggregate struct {
	ChartID       uuid.UUID      `json:"chart_id"`
	RatingCount   int32          `json:"rating_count"`
	RatingSum     int32          `json:"rating_sum"`
	AvgStars      pgtype.Numeric `json:"avg_stars"`
	BayesianScore pgtype.Numeric `json:"bayesian_score"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// ChartCoInstallation mirrors one edge in the co-installation matrix.
// chart_a_id < chart_b_id by CHECK constraint — readers must canonical-
// ize their pair lookups (smaller ID first).
type ChartCoInstallation struct {
	ChartAID  uuid.UUID `json:"chart_a_id"`
	ChartBID  uuid.UUID `json:"chart_b_id"`
	Weight    int32     `json:"weight"`
	UpdatedAt time.Time `json:"updated_at"`
}
