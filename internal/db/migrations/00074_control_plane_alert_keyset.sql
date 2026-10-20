-- +goose Up
-- +goose NO TRANSACTION
-- migration-phase: expand
-- Complete timestamp/ID ordering measured on more than10,000 rows.
-- A duplicate name or interrupted INVALID build must fail visibly.
-- One concurrent statement; no row/reader change or transaction wrapper.
CREATE INDEX CONCURRENTLY idx_control_plane_alerts_keyset
    ON public.control_plane_alerts (fired_at DESC, id DESC);
