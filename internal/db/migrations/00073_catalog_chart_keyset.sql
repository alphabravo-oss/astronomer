-- +goose Up
-- +goose NO TRANSACTION
-- migration-phase: expand
-- Complete chart name/repository/ID ordering for bounded keyset continuation.
-- One concurrent statement keeps PostgreSQL outside an implicit transaction.
-- Deliberately no IF NOT EXISTS: a collision or interrupted INVALID index
-- must fail visibly and be inspected before an operator retries this migration.
CREATE INDEX CONCURRENTLY idx_helm_charts_keyset
    ON public.helm_charts (name ASC, repository_id ASC, id ASC);
