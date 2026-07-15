-- DEX-01 follow-up: bind the bundled chart's immutable Kubernetes identity and
-- give every runtime candidate an opaque, monotonic database generation.
-- Generations are sequence numbers only; they never derive from credentials.
ALTER TABLE dex_settings
    ADD COLUMN IF NOT EXISTS chart_release_name VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_name VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS service_name VARCHAR(253) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS runtime_generation BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS runtime_applied_generation BIGINT NOT NULL DEFAULT 0;

UPDATE dex_settings
SET deployment_name = release_name
WHERE deployment_name = '';

UPDATE dex_settings
SET service_name = release_name
WHERE service_name = '';

ALTER TABLE dex_settings
    ADD CONSTRAINT dex_settings_runtime_generation_positive
        CHECK (runtime_generation > 0),
    ADD CONSTRAINT dex_settings_runtime_applied_generation_valid
        CHECK (runtime_applied_generation >= 0 AND runtime_applied_generation <= runtime_generation);

COMMENT ON COLUMN dex_settings.chart_release_name IS
    'Immutable Helm/Argo release identity for bundled Dex; empty for operator-managed Dex.';
COMMENT ON COLUMN dex_settings.deployment_name IS
    'Exact Kubernetes Deployment name reconciled by DexHandler.';
COMMENT ON COLUMN dex_settings.service_name IS
    'Exact Kubernetes Service name used for the verified health proxy.';
COMMENT ON COLUMN dex_settings.runtime_generation IS
    'Opaque monotonic generation for Dex runtime candidates; never content-derived.';
COMMENT ON COLUMN dex_settings.runtime_applied_generation IS
    'Last generation conditionally verified in Secret, Deployment, and health checks.';

CREATE OR REPLACE FUNCTION bump_dex_runtime_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE dex_settings
    SET runtime_generation = runtime_generation + 1, updated_at = now()
    WHERE id = '00000000-0000-0000-0000-000000000001'::uuid;
    RETURN COALESCE(NEW, OLD);
END;
$$;

DROP TRIGGER IF EXISTS dex_connectors_runtime_generation ON dex_connectors;
CREATE TRIGGER dex_connectors_runtime_generation
AFTER INSERT OR UPDATE OR DELETE ON dex_connectors
FOR EACH ROW EXECUTE FUNCTION bump_dex_runtime_generation();
