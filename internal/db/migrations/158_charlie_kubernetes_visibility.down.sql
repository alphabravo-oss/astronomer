ALTER TABLE charlie_connections
    DROP CONSTRAINT IF EXISTS charlie_connection_kubernetes_visibility_disabled_content,
    DROP CONSTRAINT IF EXISTS charlie_connection_kubernetes_visibility_rediscovery,
    DROP CONSTRAINT IF EXISTS charlie_connection_kubernetes_visibility_profile,
    DROP COLUMN IF EXISTS kubernetes_visibility_candidate_digest,
    DROP COLUMN IF EXISTS kubernetes_visibility_rediscovery_state,
    DROP COLUMN IF EXISTS kubernetes_visibility_pod_logs,
    DROP COLUMN IF EXISTS kubernetes_visibility_profile;
