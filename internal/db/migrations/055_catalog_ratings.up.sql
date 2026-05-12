-- Catalog ratings + recommendations.
--
-- Operators rate installed Helm charts on a 1-5 scale and we surface
-- two recommendation surfaces on the catalog browse:
--   1. "Popular in your org" — top charts by a confidence-weighted
--      (Bayesian) score so two 5-stars don't outrank fifty 4-stars.
--   2. "Frequently installed together" — chart pairs that get co-
--      installed in the same cluster within a 30-day window.
--
-- The chart_rating_aggregates and chart_co_installation tables are
-- pre-computed cache tables (recomputed inline on rating writes and
-- nightly by chart_recommendations:recompute). The hot path on the
-- catalog browse is a single indexed read per row, not a per-render
-- aggregation across chart_ratings.
--
-- Note: the spec referenced a `helm_installations` table; the real
-- catalog uses `installed_charts` (migration 001). We FK the optional
-- installation_id at installed_charts.id.

CREATE TABLE chart_ratings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- helm_charts.id; the rating is per CHART, not per chart-version.
    -- Operators rate the chart as a whole; per-version ratings are a v2.
    chart_id        UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    -- The installation this rating attaches to. NULL means "rated without
    -- installing" (operator browsed and felt strongly). Most rows will
    -- have an installation_id. ON DELETE SET NULL preserves history when
    -- the installation row is removed.
    installation_id UUID REFERENCES installed_charts(id) ON DELETE SET NULL,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stars           SMALLINT NOT NULL CHECK (stars BETWEEN 1 AND 5),
    note            VARCHAR(280) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One rating per (user, installation). When installation_id is NULL,
    -- only one rating per (user, chart) — enforced by the partial index
    -- below (NULLs are distinct in standard UNIQUE constraints).
    UNIQUE (user_id, installation_id)
);
CREATE UNIQUE INDEX idx_chart_ratings_user_chart_unique
    ON chart_ratings (user_id, chart_id)
    WHERE installation_id IS NULL;
CREATE INDEX idx_chart_ratings_chart ON chart_ratings (chart_id);

-- Pre-aggregated per-chart score. Recomputed on rating write + via a
-- nightly retention task. The recompute is cheap enough that we could
-- compute on read, but the catalog browse is hot and we want one
-- indexed read per chart.
CREATE TABLE chart_rating_aggregates (
    chart_id        UUID PRIMARY KEY REFERENCES helm_charts(id) ON DELETE CASCADE,
    rating_count    INTEGER NOT NULL DEFAULT 0,
    rating_sum      INTEGER NOT NULL DEFAULT 0,
    avg_stars       NUMERIC(3,2) NOT NULL DEFAULT 0.00,
    -- "Confidence-weighted score" for the top-list: a Wilson-style
    -- lower-bound on the avg, so a chart with 2 5-stars doesn't outrank
    -- one with 50 4-stars. Stored to avoid recomputing on every browse.
    bayesian_score  NUMERIC(4,2) NOT NULL DEFAULT 0.00,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_chart_rating_aggregates_bayesian
    ON chart_rating_aggregates (bayesian_score DESC, rating_count DESC);

-- Co-installation matrix. Edge weights are # of times the pair was
-- co-installed in the same cluster within a 30-day window. We refresh
-- this nightly. Used for "frequently installed together".
CREATE TABLE chart_co_installation (
    chart_a_id      UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    chart_b_id      UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    weight          INTEGER NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chart_a_id, chart_b_id),
    CHECK (chart_a_id < chart_b_id)  -- canonical ordering, both directions covered by SELECT
);
CREATE INDEX idx_chart_co_a ON chart_co_installation (chart_a_id, weight DESC);
CREATE INDEX idx_chart_co_b ON chart_co_installation (chart_b_id, weight DESC);
