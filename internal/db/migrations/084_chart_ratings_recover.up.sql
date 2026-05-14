-- Sprint 082+ — defensive recover for the chart_ratings family of
-- tables (originally created in migration 073).
--
-- We discovered a DB on the live test environment where
-- schema_migrations.version showed 73 as applied but the three tables
-- the migration creates (chart_ratings, chart_rating_aggregates,
-- chart_co_installation) were missing. The most likely cause is that
-- 073.down.sql was run manually at some point (during a backfill or
-- branch switch) and never re-up'd; the recommendations endpoint then
-- 500s with "relation does not exist" on every read.
--
-- This migration is IF NOT EXISTS-safe so:
--   * Healthy DBs (where 073 ran cleanly) see no-ops.
--   * Recovering DBs (where 073 was rolled back) get the tables back.
--   * Fresh installs are unaffected (073 runs first, this is a no-op).
--
-- The table definitions here mirror 073's. Keep them in sync if 073
-- is ever re-edited — but historical migrations should be append-only
-- so the only way they'll diverge is via another forward migration
-- modifying columns, in which case the modification will land on the
-- already-created table.

CREATE TABLE IF NOT EXISTS chart_ratings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chart_id        UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    installation_id UUID REFERENCES installed_charts(id) ON DELETE SET NULL,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rating          SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment         TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chart_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_chart_ratings_chart_id ON chart_ratings (chart_id);
CREATE INDEX IF NOT EXISTS idx_chart_ratings_user_id ON chart_ratings (user_id);

CREATE TABLE IF NOT EXISTS chart_rating_aggregates (
    chart_id        UUID PRIMARY KEY REFERENCES helm_charts(id) ON DELETE CASCADE,
    rating_count    INTEGER NOT NULL DEFAULT 0,
    rating_sum      INTEGER NOT NULL DEFAULT 0,
    rating_avg      NUMERIC(4,3) NOT NULL DEFAULT 0,
    bayesian_score  NUMERIC(5,3) NOT NULL DEFAULT 0,
    install_count   INTEGER NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_chart_rating_aggregates_bayesian
    ON chart_rating_aggregates (bayesian_score DESC, rating_count DESC);

CREATE TABLE IF NOT EXISTS chart_co_installation (
    chart_a_id      UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    chart_b_id      UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    co_count        INTEGER NOT NULL DEFAULT 0,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chart_a_id, chart_b_id),
    CHECK (chart_a_id <> chart_b_id)
);
CREATE INDEX IF NOT EXISTS idx_chart_co_installation_a
    ON chart_co_installation (chart_a_id, co_count DESC);
CREATE INDEX IF NOT EXISTS idx_chart_co_installation_b
    ON chart_co_installation (chart_b_id, co_count DESC);
