-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Platform Configuration (singleton, pk=1)
CREATE TABLE platform_configuration (
    id              INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    server_url      VARCHAR(500) NOT NULL DEFAULT '',
    platform_name   VARCHAR(255) NOT NULL DEFAULT 'Astronomer',
    telemetry_enabled BOOLEAN NOT NULL DEFAULT false,
    bootstrapped_at TIMESTAMPTZ
);

-- Users
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       VARCHAR(254) NOT NULL UNIQUE,
    username    VARCHAR(150) NOT NULL UNIQUE,
    first_name  VARCHAR(150) NOT NULL DEFAULT '',
    last_name   VARCHAR(150) NOT NULL DEFAULT '',
    password    VARCHAR(128) NOT NULL DEFAULT '',
    is_active   BOOLEAN NOT NULL DEFAULT true,
    is_staff    BOOLEAN NOT NULL DEFAULT false,
    is_superuser BOOLEAN NOT NULL DEFAULT false,
    last_login  TIMESTAMPTZ,
    date_joined TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_users_email ON users (email);
CREATE INDEX idx_users_username ON users (username);
CREATE INDEX idx_users_created_at ON users (created_at);

-- SSO Configuration
CREATE TABLE sso_configurations (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider                VARCHAR(16) NOT NULL UNIQUE,
    is_enabled              BOOLEAN NOT NULL DEFAULT false,
    display_name            VARCHAR(255) NOT NULL DEFAULT '',
    config                  JSONB NOT NULL DEFAULT '{}',
    client_id               VARCHAR(255) NOT NULL DEFAULT '',
    client_secret_encrypted TEXT NOT NULL DEFAULT '',
    allowed_organizations   JSONB NOT NULL DEFAULT '[]',
    allowed_domains         JSONB NOT NULL DEFAULT '[]',
    auto_create_users       BOOLEAN NOT NULL DEFAULT true,
    default_global_role_id  UUID,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- API Tokens
CREATE TABLE api_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        VARCHAR(128) NOT NULL,
    token_hash  VARCHAR(128) NOT NULL UNIQUE,
    prefix      VARCHAR(8) NOT NULL,
    expires_at  TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    is_revoked  BOOLEAN NOT NULL DEFAULT false,
    scopes      JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_api_tokens_user_revoked ON api_tokens (user_id, is_revoked);
CREATE INDEX idx_api_tokens_hash ON api_tokens (token_hash);

-- Clusters
CREATE TABLE clusters (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name              VARCHAR(128) NOT NULL UNIQUE,
    display_name      VARCHAR(255) NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    status            VARCHAR(16) NOT NULL DEFAULT 'pending',
    api_server_url    VARCHAR(512) NOT NULL DEFAULT '',
    ca_certificate    TEXT NOT NULL DEFAULT '',
    environment       VARCHAR(16) NOT NULL DEFAULT 'development',
    region            VARCHAR(64) NOT NULL DEFAULT '',
    provider          VARCHAR(16) NOT NULL DEFAULT 'other',
    labels            JSONB NOT NULL DEFAULT '{}',
    annotations       JSONB NOT NULL DEFAULT '{}',
    distribution      VARCHAR(32) NOT NULL DEFAULT '',
    agent_version     VARCHAR(32) NOT NULL DEFAULT '',
    last_heartbeat    TIMESTAMPTZ,
    kubernetes_version VARCHAR(32) NOT NULL DEFAULT '',
    node_count        INTEGER NOT NULL DEFAULT 0,
    created_by_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_clusters_name ON clusters (name);
CREATE INDEX idx_clusters_status ON clusters (status);
CREATE INDEX idx_clusters_status_env ON clusters (status, environment);
CREATE INDEX idx_clusters_provider_region ON clusters (provider, region);
CREATE INDEX idx_clusters_heartbeat ON clusters (last_heartbeat);
CREATE INDEX idx_clusters_created_at ON clusters (created_at);

-- Cluster Registration Tokens
CREATE TABLE cluster_registration_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    token       VARCHAR(128) NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    is_used     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_reg_tokens_token_used ON cluster_registration_tokens (token, is_used);
CREATE INDEX idx_reg_tokens_expires ON cluster_registration_tokens (expires_at);

-- Cluster Agent Tokens
CREATE TABLE cluster_agent_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id   UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    token        VARCHAR(128) NOT NULL UNIQUE,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_cluster_agent_tokens_token ON cluster_agent_tokens (token);

-- Cluster Health Status
CREATE TABLE cluster_health_statuses (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    cpu_usage_percent   DOUBLE PRECISION NOT NULL DEFAULT 0,
    memory_usage_percent DOUBLE PRECISION NOT NULL DEFAULT 0,
    pod_count           INTEGER NOT NULL DEFAULT 0,
    node_count          INTEGER NOT NULL DEFAULT 0,
    conditions          JSONB NOT NULL DEFAULT '[]',
    last_check          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cluster Registry Config
CREATE TABLE cluster_registry_configs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    private_registry_url VARCHAR(500) NOT NULL DEFAULT '',
    registry_username   VARCHAR(255) NOT NULL DEFAULT '',
    registry_password   VARCHAR(255) NOT NULL DEFAULT '',
    insecure            BOOLEAN NOT NULL DEFAULT false,
    ca_bundle           TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Projects
CREATE TABLE projects (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL,
    display_name    VARCHAR(255) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespaces      JSONB NOT NULL DEFAULT '[]',
    resource_quota  JSONB NOT NULL DEFAULT '{}',
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (name, cluster_id)
);
CREATE INDEX idx_projects_cluster_name ON projects (cluster_id, name);
CREATE INDEX idx_projects_created_at ON projects (created_at);

-- RBAC: Global Roles
CREATE TABLE global_roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(128) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    permissions JSONB NOT NULL DEFAULT '{}',
    rules       JSONB NOT NULL DEFAULT '[]',
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- RBAC: Global Role Bindings
CREATE TABLE global_role_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    "group"     VARCHAR(255) NOT NULL DEFAULT '',
    role_id     UUID NOT NULL REFERENCES global_roles(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, role_id),
    UNIQUE ("group", role_id)
);
CREATE INDEX idx_grb_group ON global_role_bindings ("group");

-- RBAC: Cluster Roles
CREATE TABLE cluster_roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(128) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    permissions JSONB NOT NULL DEFAULT '{}',
    rules       JSONB NOT NULL DEFAULT '[]',
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- RBAC: Cluster Role Bindings
CREATE TABLE cluster_role_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    "group"     VARCHAR(255) NOT NULL DEFAULT '',
    role_id     UUID NOT NULL REFERENCES cluster_roles(id) ON DELETE CASCADE,
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, role_id, cluster_id),
    UNIQUE ("group", role_id, cluster_id)
);
CREATE INDEX idx_crb_cluster_user ON cluster_role_bindings (cluster_id, user_id);
CREATE INDEX idx_crb_group ON cluster_role_bindings ("group");

-- RBAC: Project Roles
CREATE TABLE project_roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(128) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    permissions JSONB NOT NULL DEFAULT '{}',
    rules       JSONB NOT NULL DEFAULT '[]',
    is_builtin  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- RBAC: Project Role Bindings
CREATE TABLE project_role_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    "group"     VARCHAR(255) NOT NULL DEFAULT '',
    role_id     UUID NOT NULL REFERENCES project_roles(id) ON DELETE CASCADE,
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, role_id, project_id),
    UNIQUE ("group", role_id, project_id)
);
CREATE INDEX idx_prb_project_user ON project_role_bindings (project_id, user_id);
CREATE INDEX idx_prb_group ON project_role_bindings ("group");

-- Add FK from sso_configurations to global_roles (deferred)
ALTER TABLE sso_configurations
    ADD CONSTRAINT fk_sso_default_role
    FOREIGN KEY (default_global_role_id) REFERENCES global_roles(id) ON DELETE SET NULL;

-- Alerting: Notification Channels
CREATE TABLE notification_channels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    channel_type    VARCHAR(16) NOT NULL,
    configuration   JSONB NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notif_channels_type_enabled ON notification_channels (channel_type, enabled);

-- Alerting: Alert Rules
CREATE TABLE alert_rules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) NOT NULL,
    cluster_id          UUID REFERENCES clusters(id) ON DELETE CASCADE,
    rule_type           VARCHAR(16) NOT NULL,
    configuration       JSONB NOT NULL,
    severity            VARCHAR(16) NOT NULL DEFAULT 'warning',
    enabled             BOOLEAN NOT NULL DEFAULT true,
    cooldown_minutes    INTEGER NOT NULL DEFAULT 15,
    created_by_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_alert_rules_type_enabled ON alert_rules (rule_type, enabled);
CREATE INDEX idx_alert_rules_severity_enabled ON alert_rules (severity, enabled);
CREATE INDEX idx_alert_rules_cluster_enabled ON alert_rules (cluster_id, enabled);

-- Alert Rules <-> Notification Channels (M2M)
CREATE TABLE alert_rule_channels (
    alert_rule_id           UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    notification_channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (alert_rule_id, notification_channel_id)
);

-- Alerting: Alert Events
CREATE TABLE alert_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id             UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    cluster_id          UUID REFERENCES clusters(id) ON DELETE SET NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'firing',
    message             TEXT NOT NULL,
    details             JSONB NOT NULL DEFAULT '{}',
    fired_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ,
    acknowledged_by_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    acknowledged_at     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_alert_events_rule_status ON alert_events (rule_id, status);
CREATE INDEX idx_alert_events_cluster_status ON alert_events (cluster_id, status);
CREATE INDEX idx_alert_events_status_fired ON alert_events (status, fired_at);
CREATE INDEX idx_alert_events_fired ON alert_events (fired_at);

-- Alerting: Alert Silences
CREATE TABLE alert_silences (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id         UUID REFERENCES alert_rules(id) ON DELETE CASCADE,
    cluster_id      UUID REFERENCES clusters(id) ON DELETE CASCADE,
    reason          TEXT NOT NULL,
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_alert_silences_window ON alert_silences (starts_at, ends_at);
CREATE INDEX idx_alert_silences_rule_cluster ON alert_silences (rule_id, cluster_id);

-- Catalog: Helm Repositories
CREATE TABLE helm_repositories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL UNIQUE,
    url             VARCHAR(500) NOT NULL,
    repo_type       VARCHAR(20) NOT NULL DEFAULT 'helm',
    description     TEXT NOT NULL DEFAULT '',
    is_default      BOOLEAN NOT NULL DEFAULT false,
    auth_type       VARCHAR(20) NOT NULL DEFAULT 'none',
    auth_config     JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    last_synced_at  TIMESTAMPTZ,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_helm_repos_type_enabled ON helm_repositories (repo_type, enabled);

-- Catalog: Helm Charts
CREATE TABLE helm_charts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id   UUID NOT NULL REFERENCES helm_repositories(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    display_name    VARCHAR(255) NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    icon_url        VARCHAR(500) NOT NULL DEFAULT '',
    home_url        VARCHAR(500) NOT NULL DEFAULT '',
    category        VARCHAR(100) NOT NULL DEFAULT '',
    keywords        JSONB NOT NULL DEFAULT '[]',
    maintainers     JSONB NOT NULL DEFAULT '[]',
    deprecated      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (repository_id, name)
);
CREATE INDEX idx_helm_charts_category ON helm_charts (category);
CREATE INDEX idx_helm_charts_deprecated ON helm_charts (deprecated);

-- Catalog: Helm Chart Versions
CREATE TABLE helm_chart_versions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chart_id            UUID NOT NULL REFERENCES helm_charts(id) ON DELETE CASCADE,
    version             VARCHAR(100) NOT NULL,
    app_version         VARCHAR(100) NOT NULL DEFAULT '',
    digest              VARCHAR(256) NOT NULL DEFAULT '',
    urls                JSONB NOT NULL DEFAULT '[]',
    values_schema       JSONB NOT NULL DEFAULT '{}',
    default_values      TEXT NOT NULL DEFAULT '',
    readme              TEXT NOT NULL DEFAULT '',
    created_at_upstream TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chart_id, version)
);

-- Catalog: Installed Charts
CREATE TABLE installed_charts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    chart_version_id    UUID REFERENCES helm_chart_versions(id) ON DELETE SET NULL,
    release_name        VARCHAR(255) NOT NULL,
    namespace           VARCHAR(255) NOT NULL DEFAULT 'default',
    values_override     TEXT NOT NULL DEFAULT '',
    status              VARCHAR(50) NOT NULL DEFAULT 'pending_install',
    revision            INTEGER NOT NULL DEFAULT 1,
    notes               TEXT NOT NULL DEFAULT '',
    installed_by_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    request_id          UUID,
    tool_slug           VARCHAR(50),
    preset_used         VARCHAR(20),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, release_name, namespace)
);
CREATE INDEX idx_installed_charts_cluster_status ON installed_charts (cluster_id, status);
CREATE INDEX idx_installed_charts_release ON installed_charts (release_name);
CREATE INDEX idx_installed_charts_tool_slug ON installed_charts (tool_slug);

-- Backups: Storage Config
CREATE TABLE backup_storage_configs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    storage_type    VARCHAR(20) NOT NULL DEFAULT 's3',
    bucket          VARCHAR(255) NOT NULL,
    prefix          VARCHAR(255) NOT NULL DEFAULT 'astronomer-backups/',
    region          VARCHAR(50) NOT NULL DEFAULT '',
    endpoint_url    VARCHAR(500) NOT NULL DEFAULT '',
    access_key      VARCHAR(255) NOT NULL DEFAULT '',
    secret_key      VARCHAR(255) NOT NULL DEFAULT '',
    is_default      BOOLEAN NOT NULL DEFAULT false,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backups
CREATE TABLE backups (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) NOT NULL,
    storage_id          UUID NOT NULL REFERENCES backup_storage_configs(id) ON DELETE RESTRICT,
    backup_type         VARCHAR(20) NOT NULL DEFAULT 'full',
    status              VARCHAR(20) NOT NULL DEFAULT 'pending',
    file_path           VARCHAR(500) NOT NULL DEFAULT '',
    file_size_bytes     BIGINT NOT NULL DEFAULT 0,
    database_tables     JSONB NOT NULL DEFAULT '[]',
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    error_message       TEXT NOT NULL DEFAULT '',
    created_by_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backup Schedules
CREATE TABLE backup_schedules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) NOT NULL,
    storage_id          UUID NOT NULL REFERENCES backup_storage_configs(id) ON DELETE RESTRICT,
    backup_type         VARCHAR(20) NOT NULL DEFAULT 'full',
    cron_expression     VARCHAR(100) NOT NULL DEFAULT '0 2 * * *',
    retention_count     INTEGER NOT NULL DEFAULT 30,
    enabled             BOOLEAN NOT NULL DEFAULT true,
    last_backup_id      UUID REFERENCES backups(id) ON DELETE SET NULL,
    created_by_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Restore Operations
CREATE TABLE restore_operations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    backup_id       UUID NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    error_message   TEXT NOT NULL DEFAULT '',
    initiated_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Security: Pod Security Templates
CREATE TABLE pod_security_templates (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(255) NOT NULL UNIQUE,
    description             TEXT NOT NULL DEFAULT '',
    is_default              BOOLEAN NOT NULL DEFAULT false,
    enforce_level           VARCHAR(20) NOT NULL DEFAULT 'baseline',
    enforce_version         VARCHAR(20) NOT NULL DEFAULT 'latest',
    audit_level             VARCHAR(20) NOT NULL DEFAULT 'restricted',
    audit_version           VARCHAR(20) NOT NULL DEFAULT 'latest',
    warn_level              VARCHAR(20) NOT NULL DEFAULT 'restricted',
    warn_version            VARCHAR(20) NOT NULL DEFAULT 'latest',
    exempt_usernames        JSONB NOT NULL DEFAULT '[]',
    exempt_runtime_classes  JSONB NOT NULL DEFAULT '[]',
    exempt_namespaces       JSONB NOT NULL DEFAULT '[]',
    created_by_id           UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Security: Cluster Security Policies
CREATE TABLE cluster_security_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    template_id     UUID NOT NULL REFERENCES pod_security_templates(id) ON DELETE RESTRICT,
    applied_at      TIMESTAMPTZ,
    sync_status     VARCHAR(20) NOT NULL DEFAULT 'pending',
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Security: Scan Results
CREATE TABLE security_scan_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    scan_type       VARCHAR(50) NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'running',
    summary         JSONB NOT NULL DEFAULT '{}',
    results         JSONB NOT NULL DEFAULT '[]',
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    initiated_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tools: Cluster Tools
CREATE TABLE cluster_tools (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                VARCHAR(50) NOT NULL UNIQUE,
    name                VARCHAR(128) NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    icon                VARCHAR(64) NOT NULL DEFAULT '',
    category            VARCHAR(20) NOT NULL,
    charts              JSONB NOT NULL DEFAULT '[]',
    version_constraint  VARCHAR(64) NOT NULL DEFAULT '',
    default_namespace   VARCHAR(128) NOT NULL,
    is_builtin          BOOLEAN NOT NULL DEFAULT true,
    is_enabled          BOOLEAN NOT NULL DEFAULT true,
    helm_chart_id       UUID REFERENCES helm_charts(id) ON DELETE SET NULL,
    presets             JSONB NOT NULL DEFAULT '{}',
    service_name        VARCHAR(128) NOT NULL DEFAULT '',
    service_port        INTEGER,
    service_path        VARCHAR(128) NOT NULL DEFAULT '/',
    sub_services        JSONB NOT NULL DEFAULT '[]',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ArgoCD: Instances
CREATE TABLE argocd_instances (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    VARCHAR(128) NOT NULL UNIQUE,
    cluster_id              UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    api_url                 VARCHAR(512) NOT NULL,
    auth_token_encrypted    TEXT NOT NULL DEFAULT '',
    verify_ssl              BOOLEAN NOT NULL DEFAULT true,
    is_healthy              BOOLEAN NOT NULL DEFAULT false,
    last_sync               TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_argocd_instances_cluster ON argocd_instances (cluster_id);

-- ArgoCD: Applications
CREATE TABLE argocd_applications (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    argocd_instance_id      UUID NOT NULL REFERENCES argocd_instances(id) ON DELETE CASCADE,
    name                    VARCHAR(255) NOT NULL,
    project                 VARCHAR(255) NOT NULL DEFAULT 'default',
    repo_url                VARCHAR(512) NOT NULL DEFAULT '',
    path                    VARCHAR(512) NOT NULL DEFAULT '',
    target_revision         VARCHAR(128) NOT NULL DEFAULT 'HEAD',
    destination_cluster     VARCHAR(512) NOT NULL DEFAULT '',
    destination_namespace   VARCHAR(255) NOT NULL DEFAULT '',
    sync_status             VARCHAR(16) NOT NULL DEFAULT 'Unknown',
    health_status           VARCHAR(16) NOT NULL DEFAULT 'Unknown',
    last_synced             TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (argocd_instance_id, name)
);
CREATE INDEX idx_argocd_apps_sync_health ON argocd_applications (sync_status, health_status);
CREATE INDEX idx_argocd_apps_instance_project ON argocd_applications (argocd_instance_id, project);

-- Logging: Outputs
CREATE TABLE logging_outputs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    output_type     VARCHAR(16) NOT NULL,
    configuration   JSONB NOT NULL,
    cluster_id      UUID REFERENCES clusters(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_logging_outputs_type_enabled ON logging_outputs (output_type, enabled);
CREATE INDEX idx_logging_outputs_cluster_enabled ON logging_outputs (cluster_id, enabled);

-- Logging: Pipelines
CREATE TABLE logging_pipelines (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespaces      JSONB NOT NULL DEFAULT '[]',
    labels          JSONB NOT NULL DEFAULT '{}',
    filters         JSONB NOT NULL DEFAULT '[]',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_logging_pipelines_cluster_enabled ON logging_pipelines (cluster_id, enabled);

-- Pipelines <-> Outputs (M2M)
CREATE TABLE logging_pipeline_outputs (
    logging_pipeline_id UUID NOT NULL REFERENCES logging_pipelines(id) ON DELETE CASCADE,
    logging_output_id   UUID NOT NULL REFERENCES logging_outputs(id) ON DELETE CASCADE,
    PRIMARY KEY (logging_pipeline_id, logging_output_id)
);

-- Agent Connections
CREATE TABLE agent_connections (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    agent_id        VARCHAR(128) NOT NULL,
    session_id      VARCHAR(255) NOT NULL DEFAULT '',
    connected_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    disconnected_at TIMESTAMPTZ,
    last_ping       TIMESTAMPTZ,
    status          VARCHAR(16) NOT NULL DEFAULT 'connected',
    channel_name    VARCHAR(255) NOT NULL DEFAULT '',
    pod_name        VARCHAR(255) NOT NULL DEFAULT '',
    node_name       VARCHAR(255) NOT NULL DEFAULT '',
    agent_version   VARCHAR(32) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_conns_cluster_status ON agent_connections (cluster_id, status);
CREATE INDEX idx_agent_conns_agent_id ON agent_connections (agent_id);
CREATE INDEX idx_agent_conns_session_id ON agent_connections (session_id);

-- Audit Log
CREATE TABLE audit_log (
    id                  UUID NOT NULL DEFAULT gen_random_uuid(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'audit-v1',
    user_id             UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_auth_method   VARCHAR(32) NOT NULL DEFAULT '',
    action              VARCHAR(64) NOT NULL,
    resource_type       VARCHAR(64) NOT NULL,
    resource_id         VARCHAR(255) NOT NULL DEFAULT '',
    resource_name       VARCHAR(255) NOT NULL DEFAULT '',
    http_method         VARCHAR(16) NOT NULL DEFAULT '',
    path                TEXT NOT NULL DEFAULT '',
    status_code         INTEGER NOT NULL DEFAULT 0,
    duration_ms         BIGINT NOT NULL DEFAULT 0,
    request_id          VARCHAR(64) NOT NULL DEFAULT '',
    ip_address          INET,
    user_agent          TEXT NOT NULL DEFAULT '',
    detail              JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
CREATE INDEX idx_audit_log_action_created ON audit_log (action, created_at DESC);
CREATE INDEX idx_audit_log_resource ON audit_log (resource_type, resource_id);
CREATE INDEX idx_audit_log_user_created ON audit_log (user_id, created_at DESC);
CREATE INDEX idx_audit_log_request_id ON audit_log (request_id);
CREATE INDEX idx_audit_log_schema_created ON audit_log (schema_version, created_at DESC);
CREATE TABLE audit_log_default PARTITION OF audit_log DEFAULT;

CREATE OR REPLACE FUNCTION create_audit_log_partition(target_month TIMESTAMPTZ)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    month_start TIMESTAMPTZ := date_trunc('month', target_month);
    month_end   TIMESTAMPTZ := month_start + INTERVAL '1 month';
    partition_name TEXT := 'audit_log_' || to_char(month_start, 'YYYY_MM');
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_log FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        month_start,
        month_end
    );
END;
$$;

SELECT create_audit_log_partition(now());
SELECT create_audit_log_partition(now() + INTERVAL '1 month');

-- Seed built-in RBAC roles
INSERT INTO global_roles (name, description, rules, is_builtin) VALUES
('Administrator', 'Full platform access', '[{"resource":"*","verbs":["*"]}]', true),
('Standard User', 'Can view clusters and manage assigned resources', '[{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"monitoring","verbs":["read","list"]}]', true),
('Read Only', 'View-only access across the platform', '[{"resource":"*","verbs":["read","list"]}]', true);

INSERT INTO cluster_roles (name, description, rules, is_builtin) VALUES
('Cluster Owner', 'Full access to a specific cluster', '[{"resource":"*","verbs":["*"]}]', true),
('Cluster Member', 'Can view cluster resources and manage workloads', '[{"resource":"clusters","verbs":["read"]},{"resource":"workloads","verbs":["read","list","create","update","delete","scale","restart"]},{"resource":"pods","verbs":["read","list","watch"]},{"resource":"monitoring","verbs":["read","list"]}]', true),
('Cluster Viewer', 'Read-only access to a cluster', '[{"resource":"*","verbs":["read","list","watch"]}]', true);

INSERT INTO project_roles (name, description, rules, is_builtin) VALUES
('Project Owner', 'Full access within a project scope', '[{"resource":"*","verbs":["*"]}]', true),
('Project Member', 'Can manage workloads within a project', '[{"resource":"workloads","verbs":["read","list","create","update","delete","scale","restart"]},{"resource":"pods","verbs":["read","list","watch"]}]', true),
('Project Viewer', 'Read-only access within a project', '[{"resource":"*","verbs":["read","list","watch"]}]', true);

-- Updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply trigger to all tables with updated_at
DO $$
DECLARE
    tbl TEXT;
BEGIN
    FOR tbl IN
        SELECT table_name FROM information_schema.columns
        WHERE column_name = 'updated_at'
        AND table_schema = 'public'
        AND table_name != 'platform_configuration'
    LOOP
        EXECUTE format(
            'CREATE TRIGGER set_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION update_updated_at()',
            tbl
        );
    END LOOP;
END;
$$;
