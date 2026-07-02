-- Backfill for migration 128 (group-sync connector provenance).
--
-- Migration 128 added group_sync_connector_id but left every pre-existing
-- source='group_sync' binding NULL. With the sweep-4 reconciler fix, NULL
-- provenance is now treated as "wildcard-owned / connector-agnostic" and is
-- enumerated + revoked on EVERY sync. That correctly stops over-retention,
-- but it would also wrongly revoke a legacy binding that was actually
-- granted by a *named* connector the moment the user logs in through a
-- DIFFERENT connector (the multi-connector case migration 128 set out to
-- protect).
--
-- So we attribute a legacy NULL binding to a named connector ONLY when the
-- current mapping set proves that connector still grants that exact
-- (scope, role, cluster/project) tuple to the user's last-observed
-- connector (user_idp_groups.connector_id). Bindings we cannot justify with
-- a named-connector mapping are deliberately LEFT NULL so they stay
-- connector-agnostic and keep getting reconciled on every login — which is
-- the correct, fail-safe behaviour for wildcard-granted and truly-unknown
-- legacy rows.
--
-- Idempotent: only touches rows that are still NULL, so re-running is a
-- no-op. Set-based UPDATE with an EXISTS guard; no table rewrite.

UPDATE global_role_bindings b
SET group_sync_connector_id = uig.connector_id
FROM user_idp_groups uig
WHERE b.user_id = uig.user_id
  AND b.source = 'group_sync'
  AND b.group_sync_connector_id IS NULL
  AND uig.connector_id IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM identity_group_mappings m
      WHERE m.connector_id = uig.connector_id
        AND m.scope = 'global'
        AND m.role_id = b.role_id
  );

UPDATE cluster_role_bindings b
SET group_sync_connector_id = uig.connector_id
FROM user_idp_groups uig
WHERE b.user_id = uig.user_id
  AND b.source = 'group_sync'
  AND b.group_sync_connector_id IS NULL
  AND uig.connector_id IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM identity_group_mappings m
      WHERE m.connector_id = uig.connector_id
        AND m.scope = 'cluster'
        AND m.role_id = b.role_id
        AND m.cluster_id = b.cluster_id
  );

UPDATE project_role_bindings b
SET group_sync_connector_id = uig.connector_id
FROM user_idp_groups uig
WHERE b.user_id = uig.user_id
  AND b.source = 'group_sync'
  AND b.group_sync_connector_id IS NULL
  AND uig.connector_id IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM identity_group_mappings m
      WHERE m.connector_id = uig.connector_id
        AND m.scope = 'project'
        AND m.role_id = b.role_id
        AND m.project_id = b.project_id
  );
