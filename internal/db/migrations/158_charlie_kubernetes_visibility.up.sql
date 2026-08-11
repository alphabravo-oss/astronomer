ALTER TABLE charlie_connections
    ADD COLUMN kubernetes_visibility_profile VARCHAR(32) NOT NULL DEFAULT 'disabled',
    ADD COLUMN kubernetes_visibility_pod_logs BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN kubernetes_visibility_rediscovery_state VARCHAR(32) NOT NULL DEFAULT 'ready',
    ADD COLUMN kubernetes_visibility_candidate_digest VARCHAR(64) NOT NULL DEFAULT '',
    ADD CONSTRAINT charlie_connection_kubernetes_visibility_profile
        CHECK (kubernetes_visibility_profile IN ('disabled', 'product_namespace', 'cluster_diagnostics')),
    ADD CONSTRAINT charlie_connection_kubernetes_visibility_disabled_content
        CHECK (kubernetes_visibility_profile <> 'disabled' OR NOT kubernetes_visibility_pod_logs),
    ADD CONSTRAINT charlie_connection_kubernetes_visibility_rediscovery
        CHECK (
            (kubernetes_visibility_rediscovery_state IN ('ready', 'required') AND kubernetes_visibility_candidate_digest = '') OR
            (kubernetes_visibility_rediscovery_state = 'review_required' AND kubernetes_visibility_candidate_digest ~ '^[a-f0-9]{64}$')
        );

-- Existing integrations already disclosed management-namespace and bounded
-- cluster diagnostics, including pod-log tails. Preserve that exact effective
-- access during upgrade; newly created connections remain disabled by default.
UPDATE charlie_connections
SET kubernetes_visibility_profile = 'cluster_diagnostics',
    kubernetes_visibility_pod_logs = true,
    kubernetes_visibility_rediscovery_state = 'required'
WHERE onboarding_state IN ('consumed', 'active');
