-- +goose Up
-- +goose StatementBegin
-- migration-phase: contract
-- Clusters tombstoned before TombstoneCluster cascaded to cluster_deployments
-- left their deployments in an active phase forever (permanent delivery
-- backlog/stale-status alerts). Mark them terminal, as the tombstone now does.
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='120s';
UPDATE public.cluster_deployments AS d
SET phase = 'removed', last_error_code = 'cluster_decommissioned', last_message = ''
FROM public.clusters AS c
WHERE c.id = d.cluster_id
  AND c.decommissioned_at IS NOT NULL
  AND d.phase <> 'removed';
-- +goose StatementEnd
