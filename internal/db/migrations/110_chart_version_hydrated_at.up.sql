-- Migration 110 — track when a chart version's content was hydrated.
--
-- The lazy hydrate (GetChartValues) backfills default_values + README on first
-- view. It now also extracts values.schema.json (the typed install form). The
-- old "is default_values empty?" cache check can't tell "not hydrated yet" from
-- "hydrated, but this chart ships no schema" — so rows hydrated before the
-- schema code would never pick up a schema. This nullable timestamp is the
-- hydrate-once marker: NULL = never hydrated (pull), set = done (skip).
ALTER TABLE helm_chart_versions ADD COLUMN content_hydrated_at TIMESTAMPTZ;
