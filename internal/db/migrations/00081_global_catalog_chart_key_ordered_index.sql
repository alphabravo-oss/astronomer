-- +goose Up
-- +goose NO TRANSACTION
-- migration-phase: expand
-- One standalone statement: do not wrap in BEGIN or append SET/COMMENT/DO.
-- Canonical migrator statement timeout and session advisory lock bound/own it.
-- No IF NOT EXISTS: a name collision or interrupted INVALID index must fail.
CREATE INDEX CONCURRENTLY idx_catalog_global_chart_keys_ordered
ON public.catalog_global_chart_keys USING btree
(name ASC, repository_id ASC, chart_id ASC);
