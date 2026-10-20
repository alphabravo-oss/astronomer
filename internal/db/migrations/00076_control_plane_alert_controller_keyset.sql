-- +goose Up
-- +goose NO TRANSACTION
-- migration-phase: expand
-- Measured sparse/absent controller traversal scans 20k/100k rows without this index.
CREATE INDEX CONCURRENTLY idx_control_plane_alerts_controller_keyset
    ON public.control_plane_alerts (controller, fired_at DESC, id DESC);
