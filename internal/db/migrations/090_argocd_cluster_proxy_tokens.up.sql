CREATE TABLE IF NOT EXISTS argocd_cluster_proxy_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    purpose         VARCHAR(64) NOT NULL DEFAULT 'argocd_cluster_proxy',
    token_hash      VARCHAR(128) NOT NULL UNIQUE,
    token_prefix    VARCHAR(32) NOT NULL DEFAULT '',
    token_encrypted TEXT NOT NULL,
    expires_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    is_revoked      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, purpose)
);

CREATE INDEX IF NOT EXISTS idx_argocd_cluster_proxy_tokens_cluster
    ON argocd_cluster_proxy_tokens (cluster_id);

CREATE INDEX IF NOT EXISTS idx_argocd_cluster_proxy_tokens_hash
    ON argocd_cluster_proxy_tokens (token_hash);

DROP TRIGGER IF EXISTS set_updated_at_argocd_cluster_proxy_tokens ON argocd_cluster_proxy_tokens;
CREATE TRIGGER set_updated_at_argocd_cluster_proxy_tokens
    BEFORE UPDATE ON argocd_cluster_proxy_tokens
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
