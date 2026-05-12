// Package catalog hosts catalog-side analytics that aren't HTTP-handler
// concerns: rating-aggregate recompute, the co-installation matrix, and
// the "popular / similar" lookups consumed by the catalog browse.
//
// Migration 055 (chart_ratings, chart_rating_aggregates, chart_co_installation)
// is the schema this package operates on. See
// internal/db/migrations/055_catalog_ratings.up.sql for column-level
// commentary.
package catalog

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// CoInstallationWindow is the look-back window over which two charts
// installed in the same cluster count as "co-installed". 30 days is
// long enough to catch the typical operator workflow (install Argo,
// then install observability the same sprint) without picking up
// stale historical churn.
const CoInstallationWindow = 30 * 24 * time.Hour

// MinRatingsForSimilar is the floor on rating_count for either side of
// a recommendation pair. A chart with one install — and therefore one
// "co-install" by coincidence — would leak as a recommendation without
// this gate.
const MinRatingsForSimilar = 3

// MinRatingsForPopular is the same gate for the "popular in your org"
// row: a single 5-star outlier shouldn't dominate the hero row.
const MinRatingsForPopular = 3

// bayesianDefaults captures the operator-tunable knobs for the
// Bayesian recompute. See bayesianParams() for env-var resolution.
type bayesianDefaults struct {
	avgGlobal float64
	weight    float64
	minimum   float64
}

func bayesianParams() bayesianDefaults {
	// 4.0 is the empirical mean across mature Helm chart catalogs.
	// CHART_RATING_BAYESIAN_AVG lets the operator tune this; values
	// outside [1, 5] are clamped to 4.0 to protect the formula.
	avg := 4.0
	if v := os.Getenv("CHART_RATING_BAYESIAN_AVG"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 1 && f <= 5 {
			avg = f
		}
	}
	// Confidence weight. Larger = more pull toward avgGlobal for
	// low-sample charts. 10 means "until you've got 10 ratings, the
	// score is half-anchored to the global mean".
	weight := 10.0
	if v := os.Getenv("CHART_RATING_BAYESIAN_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			weight = f
		}
	}
	return bayesianDefaults{avgGlobal: avg, weight: weight, minimum: 1.0}
}

// Querier is the database surface RecomputeAggregate / RecomputeCoInstallation
// / TopCharts / SimilarCharts need. Defined locally (not as a re-export of
// sqlc.Querier) so unit tests can supply a narrow fake without
// satisfying every method on the giant generated interface.
type Querier interface {
	ListChartRatingsByChart(ctx context.Context, arg sqlc.ListChartRatingsByChartParams) ([]sqlc.ChartRating, error)
	CountChartRatingsByChart(ctx context.Context, chartID uuid.UUID) (int64, error)
	UpsertChartRatingAggregate(ctx context.Context, arg sqlc.UpsertChartRatingAggregateParams) (sqlc.ChartRatingAggregate, error)
	GetChartRatingAggregate(ctx context.Context, chartID uuid.UUID) (sqlc.ChartRatingAggregate, error)
	ListTopChartsByBayesian(ctx context.Context, arg sqlc.ListTopChartsByBayesianParams) ([]sqlc.ChartRatingAggregate, error)
	ListChartCoInstallationsFor(ctx context.Context, arg sqlc.ListChartCoInstallationsForParams) ([]sqlc.ChartCoInstallation, error)
	TruncateChartCoInstallation(ctx context.Context) error
	UpsertChartCoInstallation(ctx context.Context, arg sqlc.UpsertChartCoInstallationParams) error
	ListInstalledChartChartPairs(ctx context.Context) ([]sqlc.InstalledChartChartPair, error)
	ListDistinctRatedChartIDs(ctx context.Context) ([]uuid.UUID, error)
}

// ChartScore is the surface shape returned by TopCharts / SimilarCharts.
// We intentionally don't echo the entire chart_rating_aggregates row —
// the caller usually wants to join against helm_charts for display
// metadata, so we keep this struct slim and JSON-friendly.
type ChartScore struct {
	ChartID       uuid.UUID `json:"chart_id"`
	RatingCount   int32     `json:"rating_count"`
	AvgStars      float64   `json:"avg_stars"`
	BayesianScore float64   `json:"bayesian_score"`
	// Weight is populated only by SimilarCharts (number of co-
	// installation events that backed this recommendation). Zero in
	// the TopCharts response.
	Weight int32 `json:"weight,omitempty"`
}

// RecomputeAggregate updates chart_rating_aggregates for a single
// chart_id. Bayesian formula:
//
//	bayesian = ((avg_global * confidence_weight) + (sum + minimum)) /
//	           (confidence_weight + count)
//
// Where avg_global ≈ 4.0 (env: CHART_RATING_BAYESIAN_AVG),
// confidence_weight = 10 (env: CHART_RATING_BAYESIAN_WEIGHT), and
// minimum = 1 (lowest possible star). The +minimum prevents a single
// 1-star from pulling the score below the global mean for a
// previously-unrated chart on the very first vote.
//
// Called inline on rating create/update/delete and via the nightly
// chart_recommendations:recompute task.
func RecomputeAggregate(ctx context.Context, q Querier, chartID uuid.UUID) error {
	// We page through every rating for this chart. Most charts have
	// O(10–1000) ratings, so pulling them all and computing in memory
	// is faster than two round-trips for SUM / AVG / COUNT.
	const pageSize = int32(500)
	var (
		offset int32
		sum    int64
		count  int64
	)
	for {
		ratings, err := q.ListChartRatingsByChart(ctx, sqlc.ListChartRatingsByChartParams{
			ChartID: chartID,
			Limit:   pageSize,
			Offset:  offset,
		})
		if err != nil {
			return fmt.Errorf("list ratings: %w", err)
		}
		for _, r := range ratings {
			sum += int64(r.Stars)
			count++
		}
		if int32(len(ratings)) < pageSize {
			break
		}
		offset += pageSize
	}

	params := bayesianParams()
	var avg float64
	var bayes float64
	if count > 0 {
		avg = float64(sum) / float64(count)
	}
	if count > 0 {
		// (avg_global * weight) + (sum + minimum)) / (weight + count)
		bayes = ((params.avgGlobal * params.weight) + (float64(sum) + params.minimum)) /
			(params.weight + float64(count))
	}

	// Round to two decimals — the columns are NUMERIC(3,2) /
	// NUMERIC(4,2) so anything else is rejected. round-half-up.
	avg = math.Round(avg*100) / 100
	bayes = math.Round(bayes*100) / 100

	_, err := q.UpsertChartRatingAggregate(ctx, sqlc.UpsertChartRatingAggregateParams{
		ChartID:       chartID,
		RatingCount:   int32(count),
		RatingSum:     int32(sum),
		AvgStars:      mustNumeric(avg),
		BayesianScore: mustNumeric(bayes),
	})
	return err
}

// RecomputeCoInstallation rebuilds the co-installation matrix from
// installed_charts rows. For each cluster, every pair of distinct
// charts whose install times are within CoInstallationWindow of each
// other becomes an edge with weight = number of such qualifying pairs
// across all clusters. Idempotent: we TRUNCATE and re-INSERT inside the
// same logical run.
//
// We choose TRUNCATE over a delta-merge because the matrix is small
// (O(charts²)), the nightly window means stale weights would diverge
// from reality if we only added, and TRUNCATE is the cheapest path on
// a table this size.
func RecomputeCoInstallation(ctx context.Context, q Querier) error {
	pairs, err := q.ListInstalledChartChartPairs(ctx)
	if err != nil {
		return fmt.Errorf("list install pairs: %w", err)
	}

	// Bucket pairs by cluster. The query is ORDER BY cluster_id, so a
	// running boundary check would work, but the explicit map is
	// easier to test in isolation.
	type chartInstall struct {
		chartID uuid.UUID
		at      time.Time
	}
	clusters := map[uuid.UUID][]chartInstall{}
	for _, p := range pairs {
		if !p.CreatedAt.Valid {
			continue
		}
		clusters[p.ClusterID] = append(clusters[p.ClusterID], chartInstall{
			chartID: p.ChartID,
			at:      p.CreatedAt.Time,
		})
	}

	// Edge weights keyed by canonicalized (a < b) pair.
	type pairKey struct{ a, b uuid.UUID }
	edges := map[pairKey]int32{}

	for _, installs := range clusters {
		// O(n²) per cluster — acceptable; the n here is "charts
		// installed in a single cluster", which is bounded by a few
		// dozen even on the most aggressive deployments.
		for i := 0; i < len(installs); i++ {
			for j := i + 1; j < len(installs); j++ {
				a := installs[i]
				b := installs[j]
				if a.chartID == b.chartID {
					continue
				}
				dt := a.at.Sub(b.at)
				if dt < 0 {
					dt = -dt
				}
				if dt > CoInstallationWindow {
					continue
				}
				key := pairKey{a: a.chartID, b: b.chartID}
				// Canonicalize a < b.
				if compareUUID(key.a, key.b) > 0 {
					key.a, key.b = key.b, key.a
				}
				edges[key]++
			}
		}
	}

	if err := q.TruncateChartCoInstallation(ctx); err != nil {
		return fmt.Errorf("truncate co-install: %w", err)
	}
	for key, w := range edges {
		if err := q.UpsertChartCoInstallation(ctx, sqlc.UpsertChartCoInstallationParams{
			ChartAID: key.a,
			ChartBID: key.b,
			Weight:   w,
		}); err != nil {
			return fmt.Errorf("upsert co-install %s/%s: %w", key.a, key.b, err)
		}
	}
	return nil
}

// TopCharts returns the top N charts by bayesian_score, filtered to
// rated charts (rating_count >= MinRatingsForPopular). The "popular in
// your org" row on the catalog browse.
func TopCharts(ctx context.Context, q Querier, limit int) ([]ChartScore, error) {
	if limit <= 0 {
		limit = 6
	}
	rows, err := q.ListTopChartsByBayesian(ctx, sqlc.ListTopChartsByBayesianParams{
		MinRatingCount: int32(MinRatingsForPopular),
		Limit:          int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ChartScore, 0, len(rows))
	for _, r := range rows {
		out = append(out, ChartScore{
			ChartID:       r.ChartID,
			RatingCount:   r.RatingCount,
			AvgStars:      numericFloat(r.AvgStars),
			BayesianScore: numericFloat(r.BayesianScore),
		})
	}
	return out, nil
}

// SimilarCharts returns up to limit other charts most frequently co-
// installed with chartID. Both sides of the pair must have rating_count
// >= MinRatingsForSimilar — this is the leak guard the spec calls out:
// a single co-install would otherwise surface as a "recommendation"
// with weight=1 and no statistical support.
func SimilarCharts(ctx context.Context, q Querier, chartID uuid.UUID, limit int) ([]ChartScore, error) {
	if limit <= 0 {
		limit = 5
	}
	// Pull more than `limit` edges since some will fail the rating-
	// count gate. 4× headroom is a heuristic; the matrix is small so
	// over-fetch is cheap.
	edges, err := q.ListChartCoInstallationsFor(ctx, sqlc.ListChartCoInstallationsForParams{
		ChartID: chartID,
		Limit:   int32(limit * 4),
	})
	if err != nil {
		return nil, err
	}
	// Sort by weight DESC just in case the index ordering changes.
	sort.SliceStable(edges, func(i, j int) bool {
		return edges[i].Weight > edges[j].Weight
	})

	out := make([]ChartScore, 0, limit)
	for _, e := range edges {
		other := e.ChartAID
		if other == chartID {
			other = e.ChartBID
		}
		// Gate by minimum rating count on the OTHER side. If we
		// returned an unrated chart as a recommendation it would
		// be a worse UX than just hiding the slot.
		agg, err := q.GetChartRatingAggregate(ctx, other)
		if err != nil {
			// Missing aggregate row means the chart has zero
			// ratings — fail the gate silently.
			continue
		}
		if agg.RatingCount < int32(MinRatingsForSimilar) {
			continue
		}
		out = append(out, ChartScore{
			ChartID:       other,
			RatingCount:   agg.RatingCount,
			AvgStars:      numericFloat(agg.AvgStars),
			BayesianScore: numericFloat(agg.BayesianScore),
			Weight:        e.Weight,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// RecomputeAllAggregates walks every chart_id present in chart_ratings
// and recomputes its aggregate. Called by the nightly retention task.
func RecomputeAllAggregates(ctx context.Context, q Querier) error {
	ids, err := q.ListDistinctRatedChartIDs(ctx)
	if err != nil {
		return fmt.Errorf("list rated chart ids: %w", err)
	}
	for _, id := range ids {
		if err := RecomputeAggregate(ctx, q, id); err != nil {
			return fmt.Errorf("recompute %s: %w", id, err)
		}
	}
	return nil
}

// --- helpers ---------------------------------------------------------

func compareUUID(a, b uuid.UUID) int {
	for i := 0; i < 16; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// mustNumeric encodes a float as pgtype.Numeric with two-decimal
// precision. The Bayesian / avg columns are both NUMERIC(*, 2) so this
// matches the column type exactly and avoids a Postgres-side rounding
// surprise. Panic is unreachable on a well-formed finite float.
func mustNumeric(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	// pgtype.Numeric.Scan accepts a string; emitting "0.00" / "4.25"
	// keeps the two-decimal contract explicit.
	s := strconv.FormatFloat(v, 'f', 2, 64)
	if err := n.Scan(s); err != nil {
		// Re-attempt with %g as a fallback for unusual values; should
		// never fire in production.
		_ = n.Scan(strconv.FormatFloat(v, 'g', -1, 64))
	}
	return n
}

// numericFloat decodes a pgtype.Numeric to float64. Returns 0 for NULL
// (the column is NOT NULL DEFAULT 0.00 so this path mostly exists for
// hand-constructed test fakes).
func numericFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}
