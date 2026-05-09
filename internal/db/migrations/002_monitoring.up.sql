CREATE TABLE monitoring_backends (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  VARCHAR(128) NOT NULL UNIQUE,
    backend_type          VARCHAR(32) NOT NULL DEFAULT 'thanos',
    query_url             VARCHAR(500) NOT NULL,
    alertmanager_url      VARCHAR(500) NOT NULL DEFAULT '',
    tenant_id             VARCHAR(255) NOT NULL DEFAULT '',
    auth_type             VARCHAR(32) NOT NULL DEFAULT 'none',
    auth_config           JSONB NOT NULL DEFAULT '{}',
    default_step_seconds  INTEGER NOT NULL DEFAULT 300,
    timeout_seconds       INTEGER NOT NULL DEFAULT 30,
    is_default            BOOLEAN NOT NULL DEFAULT false,
    created_by_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_monitoring_backends_default
    ON monitoring_backends (is_default)
    WHERE is_default = true;

CREATE TABLE cluster_monitoring_configs (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id              UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    backend_id              UUID NOT NULL REFERENCES monitoring_backends(id) ON DELETE CASCADE,
    cluster_label           VARCHAR(128) NOT NULL DEFAULT 'cluster_id',
    cluster_label_value     VARCHAR(255) NOT NULL DEFAULT '',
    scrape_interval_seconds INTEGER NOT NULL DEFAULT 30,
    retention               VARCHAR(32) NOT NULL DEFAULT '15d',
    stack_namespace         VARCHAR(128) NOT NULL DEFAULT 'monitoring',
    prometheus_release_name VARCHAR(128) NOT NULL DEFAULT 'prometheus',
    thanos_sidecar_enabled  BOOLEAN NOT NULL DEFAULT true,
    status                  VARCHAR(32) NOT NULL DEFAULT 'pending',
    last_healthy_at         TIMESTAMPTZ,
    created_by_id           UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cluster_monitoring_configs_backend ON cluster_monitoring_configs (backend_id);
