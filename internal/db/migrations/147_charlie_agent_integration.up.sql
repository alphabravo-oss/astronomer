-- Charlie is an opt-in, separately deployed SRE assistant. This migration
-- stores only Astronomer-owned integration metadata, authorization references,
-- idempotency receipts, trigger state, and bounded finding summaries. It never
-- stores prompts, model responses, raw evidence, tool arguments/results, or a
-- Charlie central runtime credential.

INSERT INTO platform_settings (key, value, description)
VALUES ('feature.charlie', 'false'::jsonb, 'Charlie SRE assistant integration')
ON CONFLICT (key) DO NOTHING;

-- Charlie permissions are integration permissions, never underlying target
-- permissions. No role below grants management writes, and no service identity
-- is bound automatically. The explicit approver role still needs the target
-- permission for every proposed action.
UPDATE global_roles
SET rules = rules || '[{"resource":"charlie","verbs":["create","read"]}]'::jsonb,
    updated_at = now()
WHERE name IN ('Read Only', 'Platform Operator')
  AND NOT rules @> '[{"resource":"charlie"}]'::jsonb;

INSERT INTO global_roles (name, description, permissions, rules, is_builtin)
VALUES
  ('Charlie Approver', 'May use Charlie and approve one exact action; target permissions are still required.', '{}', '[{"resource":"charlie","verbs":["create","read","approve"]}]', true),
  ('Charlie Automation', 'Unbound template for the hidden Charlie service identity; explicit target grants are required.', '{}', '[{"resource":"charlie","verbs":["create","read"]}]', true)
ON CONFLICT (name) DO NOTHING;

-- Astronomer-owned operational telemetry used to diagnose the cluster-agent
-- fleet without opening a downstream tunnel. These rows contain bounded status
-- codes and counters only: no downstream Kubernetes objects, logs, credentials,
-- command payloads, or raw errors.
CREATE TABLE agent_operational_statuses (
    cluster_id                    UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    agent_id                     VARCHAR(128) NOT NULL DEFAULT '',
    installed_agent_version      VARCHAR(64) NOT NULL DEFAULT '',
    desired_agent_version        VARCHAR(64) NOT NULL DEFAULT '',
    protocol_version             VARCHAR(64) NOT NULL DEFAULT '',
    protocol_compatible          BOOLEAN,
    authentication_state         VARCHAR(24) NOT NULL DEFAULT 'unknown',
    registration_state           VARCHAR(24) NOT NULL DEFAULT 'unknown',
    credential_state             VARCHAR(24) NOT NULL DEFAULT 'unknown',
    credential_expires_at        TIMESTAMPTZ,
    upgrade_state                VARCHAR(24) NOT NULL DEFAULT 'unknown',
    audit_ingestion_state        VARCHAR(24) NOT NULL DEFAULT 'unknown',
    metrics_ingestion_state      VARCHAR(24) NOT NULL DEFAULT 'unknown',
    state_ingestion_state        VARCHAR(24) NOT NULL DEFAULT 'unknown',
    pending_command_count        INTEGER NOT NULL DEFAULT 0,
    failed_command_count         INTEGER NOT NULL DEFAULT 0,
    expired_command_count        INTEGER NOT NULL DEFAULT 0,
    downstream_api_reachable     BOOLEAN,
    downstream_api_reported_at   TIMESTAMPTZ,
    owning_server_replica        VARCHAR(128) NOT NULL DEFAULT '',
    last_successful_connection_at TIMESTAMPTZ,
    last_status_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_ops_auth_state CHECK (authentication_state IN ('unknown', 'ok', 'failed', 'expired', 'revoked')),
    CONSTRAINT agent_ops_registration_state CHECK (registration_state IN ('unknown', 'registered', 'failed', 'rejected')),
    CONSTRAINT agent_ops_credential_state CHECK (credential_state IN ('unknown', 'valid', 'expiring', 'expired', 'revoked')),
    CONSTRAINT agent_ops_upgrade_state CHECK (upgrade_state IN ('unknown', 'current', 'available', 'pending', 'running', 'stalled', 'failed')),
    CONSTRAINT agent_ops_audit_ingestion CHECK (audit_ingestion_state IN ('unknown', 'healthy', 'degraded', 'failed')),
    CONSTRAINT agent_ops_metrics_ingestion CHECK (metrics_ingestion_state IN ('unknown', 'healthy', 'degraded', 'failed')),
    CONSTRAINT agent_ops_state_ingestion CHECK (state_ingestion_state IN ('unknown', 'healthy', 'degraded', 'failed')),
    CONSTRAINT agent_ops_command_counts CHECK (pending_command_count >= 0 AND failed_command_count >= 0 AND expired_command_count >= 0)
);
CREATE INDEX agent_operational_statuses_replica_idx ON agent_operational_statuses (owning_server_replica) WHERE owning_server_replica <> '';
CREATE INDEX agent_operational_statuses_health_idx ON agent_operational_statuses (audit_ingestion_state, metrics_ingestion_state, state_ingestion_state);

CREATE TABLE agent_connection_events (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id         UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    connection_id      UUID REFERENCES agent_connections(id) ON DELETE SET NULL,
    event_type         VARCHAR(32) NOT NULL,
    reason_code        VARCHAR(64) NOT NULL DEFAULT '',
    agent_id           VARCHAR(128) NOT NULL DEFAULT '',
    agent_version      VARCHAR(64) NOT NULL DEFAULT '',
    protocol_version   VARCHAR(64) NOT NULL DEFAULT '',
    server_replica     VARCHAR(128) NOT NULL DEFAULT '',
    metadata           JSONB NOT NULL DEFAULT '{}',
    occurred_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agent_connection_event_type CHECK (event_type IN ('connected', 'disconnected', 'reconnecting', 'auth_failed', 'registration_failed', 'heartbeat_stale', 'api_unreachable', 'protocol_incompatible', 'credential_expired', 'credential_revoked', 'upgrade_failed', 'upgrade_stalled', 'audit_ingestion_failed', 'metrics_ingestion_failed', 'state_ingestion_failed', 'command_expired', 'command_failed')),
    CONSTRAINT agent_connection_event_metadata_bounded CHECK (octet_length(metadata::text) <= 8192)
);
CREATE INDEX agent_connection_events_cluster_time_idx ON agent_connection_events (cluster_id, occurred_at DESC);
CREATE INDEX agent_connection_events_type_time_idx ON agent_connection_events (event_type, occurred_at DESC);
CREATE INDEX agent_connection_events_replica_time_idx ON agent_connection_events (server_replica, occurred_at DESC) WHERE server_replica <> '';

CREATE TABLE tunnel_locator_events (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id      VARCHAR(128) NOT NULL DEFAULT '',
    cluster_id         UUID REFERENCES clusters(id) ON DELETE SET NULL,
    event_type         VARCHAR(32) NOT NULL,
    reason_code        VARCHAR(64) NOT NULL,
    server_replica     VARCHAR(128) NOT NULL DEFAULT '',
    occurred_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tunnel_locator_event_type CHECK (event_type IN ('lookup_failed', 'owner_missing', 'owner_unreachable', 'registration_failed', 'state_stale', 'recovered'))
);
CREATE INDEX tunnel_locator_events_time_idx ON tunnel_locator_events (occurred_at DESC);
CREATE INDEX tunnel_locator_events_cluster_time_idx ON tunnel_locator_events (cluster_id, occurred_at DESC) WHERE cluster_id IS NOT NULL;

CREATE TABLE charlie_connections (
    id                              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id                 UUID NOT NULL,
    product_id                      VARCHAR(128) NOT NULL,
    product_slug                    VARCHAR(63) NOT NULL,
    deployment_id                   VARCHAR(128) NOT NULL,
    route_id                        VARCHAR(128) NOT NULL,
    central_url                     VARCHAR(512) NOT NULL,
    central_ca_fingerprint          VARCHAR(128) NOT NULL,
    signing_key_id                  VARCHAR(128) NOT NULL,
    signing_key_fingerprint         VARCHAR(128) NOT NULL,
    onboarding_schema_version       VARCHAR(32) NOT NULL,
    central_api_version             VARCHAR(32) NOT NULL,
    agent_protocol_version          VARCHAR(32) NOT NULL,
    chart_version                   VARCHAR(64) NOT NULL,
    chart_digest                    VARCHAR(128) NOT NULL,
    image_digest                    VARCHAR(128) NOT NULL,
    logical_agent_id                VARCHAR(128) NOT NULL,
    replica_count                   INTEGER NOT NULL DEFAULT 2,
    bridge_service_name             VARCHAR(253) NOT NULL,
    mcp_service_name                VARCHAR(253) NOT NULL,
    local_trust_material_encrypted  TEXT NOT NULL DEFAULT '',
    agent_secret_name               VARCHAR(253) NOT NULL,
    onboarding_package_id           VARCHAR(128) NOT NULL UNIQUE,
    onboarding_package_digest       VARCHAR(128) NOT NULL,
    onboarding_package_expires_at   TIMESTAMPTZ NOT NULL,
    enrollment_credentials_expires_at TIMESTAMPTZ NOT NULL,
    artifact_credential_expires_at  TIMESTAMPTZ NOT NULL,
    certificate_expires_at          TIMESTAMPTZ NOT NULL,
    onboarding_state                VARCHAR(32) NOT NULL DEFAULT 'validated',
    agent_secret_hmac               VARCHAR(128) NOT NULL DEFAULT '',
    requested_mode                  VARCHAR(16) NOT NULL DEFAULT 'disabled',
    verified_mode                   VARCHAR(16) NOT NULL DEFAULT 'disabled',
    verified_mode_revision          BIGINT NOT NULL DEFAULT 0,
    emergency_disabled              BOOLEAN NOT NULL DEFAULT false,
    emergency_disabled_by_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    emergency_disabled_at           TIMESTAMPTZ,
    disclosure_digest               VARCHAR(128) NOT NULL DEFAULT '',
    acknowledged_disclosure_digest  VARCHAR(128) NOT NULL DEFAULT '',
    leader_instance_id              VARCHAR(128) NOT NULL DEFAULT '',
    fencing_epoch                   BIGINT NOT NULL DEFAULT 0,
    health_state                    VARCHAR(32) NOT NULL DEFAULT 'inactive',
    active                          BOOLEAN NOT NULL DEFAULT false,
    last_error_code                 VARCHAR(64) NOT NULL DEFAULT '',
    last_verified_at                TIMESTAMPTZ,
    last_connected_at               TIMESTAMPTZ,
    last_rotated_at                 TIMESTAMPTZ,
    reconciliation_due_at           TIMESTAMPTZ,
    created_by_id                   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_connection_product_nonempty CHECK (length(trim(product_id)) > 0),
    CONSTRAINT charlie_connection_deployment_nonempty CHECK (length(trim(deployment_id)) > 0),
    CONSTRAINT charlie_connection_route_nonempty CHECK (length(trim(route_id)) > 0),
    CONSTRAINT charlie_connection_central_https CHECK (central_url ~ '^https://[^/]+'),
    CONSTRAINT charlie_connection_product_slug_astronomer CHECK (product_slug = 'astronomer'),
    CONSTRAINT charlie_connection_replica_count CHECK (replica_count BETWEEN 2 AND 20),
    CONSTRAINT charlie_connection_onboarding_state CHECK (onboarding_state IN ('validated', 'secrets_pending', 'secrets_written', 'consumed', 'active', 'failed')),
    CONSTRAINT charlie_connection_requested_mode CHECK (requested_mode IN ('disabled', 'read_only', 'approval', 'auto')),
    CONSTRAINT charlie_connection_verified_mode CHECK (verified_mode IN ('disabled', 'read_only', 'approval', 'auto')),
    CONSTRAINT charlie_connection_health CHECK (health_state IN ('inactive', 'installing', 'ready', 'degraded', 'unavailable', 'disconnected', 'failed')),
    CONSTRAINT charlie_connection_epoch_nonnegative CHECK (fencing_epoch >= 0),
    CONSTRAINT charlie_connection_mode_revision_nonnegative CHECK (verified_mode_revision >= 0),
    CONSTRAINT charlie_connection_package_expiry CHECK (onboarding_package_expires_at > created_at),
    CONSTRAINT charlie_connection_enrollment_expiry CHECK (enrollment_credentials_expires_at > created_at),
    CONSTRAINT charlie_connection_artifact_expiry CHECK (artifact_credential_expires_at > created_at),
    CONSTRAINT charlie_connection_certificate_expiry CHECK (certificate_expires_at > created_at),
    CONSTRAINT charlie_connection_emergency_actor CHECK (NOT emergency_disabled OR (emergency_disabled_at IS NOT NULL AND emergency_disabled_by_id IS NOT NULL))
);
CREATE UNIQUE INDEX charlie_one_active_connection_idx ON charlie_connections ((true)) WHERE active;
CREATE INDEX charlie_connections_reconcile_idx ON charlie_connections (onboarding_state, reconciliation_due_at) WHERE onboarding_state NOT IN ('active', 'failed');

CREATE TABLE charlie_sessions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id         UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    charlie_session_id    VARCHAR(128) NOT NULL DEFAULT '',
    client_session_id     UUID NOT NULL,
    owner_user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    source                VARCHAR(16) NOT NULL,
    visibility            VARCHAR(16) NOT NULL,
    intent                VARCHAR(128) NOT NULL DEFAULT '',
    resource_scope_summary VARCHAR(512) NOT NULL DEFAULT '',
    state                 VARCHAR(24) NOT NULL DEFAULT 'active',
    last_event_id         VARCHAR(128) NOT NULL DEFAULT '',
    central_revision      BIGINT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at          TIMESTAMPTZ,
    UNIQUE (connection_id, client_session_id),
    CONSTRAINT charlie_session_source CHECK (source IN ('user', 'event')),
    CONSTRAINT charlie_session_visibility CHECK (visibility IN ('private', 'incident')),
    CONSTRAINT charlie_session_state CHECK (state IN ('creating', 'active', 'waiting_approval', 'completed', 'aborted', 'failed')),
    CONSTRAINT charlie_session_owner CHECK ((source = 'user' AND owner_user_id IS NOT NULL AND visibility = 'private') OR (source = 'event' AND visibility = 'incident')),
    CONSTRAINT charlie_session_revision_nonnegative CHECK (central_revision >= 0)
);
CREATE UNIQUE INDEX charlie_sessions_central_id_idx ON charlie_sessions (connection_id, charlie_session_id) WHERE charlie_session_id <> '';
CREATE INDEX charlie_sessions_owner_idx ON charlie_sessions (owner_user_id, updated_at DESC) WHERE owner_user_id IS NOT NULL;
CREATE INDEX charlie_sessions_state_idx ON charlie_sessions (state, updated_at DESC);

CREATE TABLE charlie_session_resources (
    session_id       UUID NOT NULL REFERENCES charlie_sessions(id) ON DELETE CASCADE,
    resource_type    VARCHAR(64) NOT NULL,
    resource_id      VARCHAR(255) NOT NULL,
    required_verb    VARCHAR(32) NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, resource_type, resource_id, required_verb),
	UNIQUE (session_id, resource_id),
    CONSTRAINT charlie_session_resource_nonempty CHECK (length(trim(resource_type)) > 0 AND length(trim(resource_id)) > 0 AND length(trim(required_verb)) > 0)
);
CREATE INDEX charlie_session_resources_target_idx ON charlie_session_resources (resource_type, resource_id);

CREATE TABLE charlie_delegations (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id            UUID NOT NULL REFERENCES charlie_sessions(id) ON DELETE CASCADE,
    authorization_hash    VARCHAR(128) NOT NULL UNIQUE,
    authorization_prefix  VARCHAR(16) NOT NULL,
    principal_type        VARCHAR(16) NOT NULL,
    principal_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at            TIMESTAMPTZ NOT NULL,
    revoked_at            TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_delegation_principal CHECK (principal_type IN ('user', 'service')),
    CONSTRAINT charlie_delegation_expiry CHECK (expires_at > issued_at)
);
CREATE INDEX charlie_delegations_active_idx ON charlie_delegations (session_id, expires_at) WHERE revoked_at IS NULL;

-- Delegated browser authority is deliberately short-lived, but it must also
-- fail closed immediately when its originating principal or the product-local
-- RBAC graph changes. A broad RBAC invalidation is conservative and avoids
-- trying to reconstruct group membership inside PostgreSQL: role changes are
-- rare, and issuing a fresh delegation is safer than retaining stale access.
CREATE FUNCTION revoke_charlie_delegations_for_deactivated_user() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE charlie_delegations
    SET revoked_at = now()
    WHERE principal_id = NEW.id
      AND revoked_at IS NULL;
    RETURN NEW;
END;
$$;

CREATE TRIGGER revoke_charlie_delegations_user_deactivated
AFTER UPDATE OF is_active ON users
FOR EACH ROW
WHEN (OLD.is_active IS DISTINCT FROM NEW.is_active AND NEW.is_active = false)
EXECUTE FUNCTION revoke_charlie_delegations_for_deactivated_user();

CREATE FUNCTION revoke_charlie_delegations_for_inactive_connection() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE charlie_delegations AS delegation
    SET revoked_at = now()
    FROM charlie_sessions AS session
    WHERE session.connection_id = NEW.id
      AND delegation.session_id = session.id
      AND delegation.revoked_at IS NULL;
    RETURN NEW;
END;
$$;

CREATE TRIGGER revoke_charlie_delegations_connection_inactive
AFTER UPDATE OF active ON charlie_connections
FOR EACH ROW
WHEN (OLD.active IS DISTINCT FROM NEW.active AND NEW.active = false)
EXECUTE FUNCTION revoke_charlie_delegations_for_inactive_connection();

CREATE FUNCTION revoke_charlie_delegations_on_rbac_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE charlie_delegations
    SET revoked_at = now()
    WHERE revoked_at IS NULL;
    RETURN NULL;
END;
$$;

CREATE TRIGGER revoke_charlie_delegations_global_roles
AFTER INSERT OR UPDATE OR DELETE ON global_roles
FOR EACH STATEMENT EXECUTE FUNCTION revoke_charlie_delegations_on_rbac_change();
CREATE TRIGGER revoke_charlie_delegations_global_role_bindings
AFTER INSERT OR UPDATE OR DELETE ON global_role_bindings
FOR EACH STATEMENT EXECUTE FUNCTION revoke_charlie_delegations_on_rbac_change();
CREATE TRIGGER revoke_charlie_delegations_cluster_roles
AFTER INSERT OR UPDATE OR DELETE ON cluster_roles
FOR EACH STATEMENT EXECUTE FUNCTION revoke_charlie_delegations_on_rbac_change();
CREATE TRIGGER revoke_charlie_delegations_cluster_role_bindings
AFTER INSERT OR UPDATE OR DELETE ON cluster_role_bindings
FOR EACH STATEMENT EXECUTE FUNCTION revoke_charlie_delegations_on_rbac_change();
CREATE TRIGGER revoke_charlie_delegations_project_roles
AFTER INSERT OR UPDATE OR DELETE ON project_roles
FOR EACH STATEMENT EXECUTE FUNCTION revoke_charlie_delegations_on_rbac_change();
CREATE TRIGGER revoke_charlie_delegations_project_role_bindings
AFTER INSERT OR UPDATE OR DELETE ON project_role_bindings
FOR EACH STATEMENT EXECUTE FUNCTION revoke_charlie_delegations_on_rbac_change();

-- Product-local approval facts are hash-only and exact-action bound. Charlie's
-- central approval is not sufficient authority by itself: the Astronomer
-- approver and target permission intersection is validated live, then this row
-- records the one dispatch that may consume the approval.
CREATE TABLE charlie_action_approvals (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id         UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    session_id            UUID NOT NULL REFERENCES charlie_sessions(id) ON DELETE CASCADE,
    approval_id           VARCHAR(128) NOT NULL UNIQUE,
    charlie_action_id     VARCHAR(128) NOT NULL UNIQUE,
    turn_id               VARCHAR(128) NOT NULL,
    capability            VARCHAR(128) NOT NULL,
    argument_digest       VARCHAR(128) NOT NULL,
    disclosure_digest     VARCHAR(128) NOT NULL,
    mode_revision         BIGINT NOT NULL,
    policy_revision       BIGINT NOT NULL,
    fencing_epoch         BIGINT NOT NULL,
    manifest_digest       VARCHAR(64) NOT NULL,
	resource_type         VARCHAR(64) NOT NULL,
	resource_id           VARCHAR(128) NOT NULL,
    approver_id           UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    rationale_digest      VARCHAR(128) NOT NULL,
    state                 VARCHAR(16) NOT NULL DEFAULT 'pending',
    expires_at            TIMESTAMPTZ NOT NULL,
    dispatched_at         TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_approval_state CHECK (state IN ('pending', 'approved', 'rejected', 'expired', 'dispatched')),
    CONSTRAINT charlie_approval_revision_positive CHECK (mode_revision > 0 AND policy_revision > 0 AND fencing_epoch > 0),
    CONSTRAINT charlie_approval_manifest_digest CHECK (manifest_digest ~ '^[a-f0-9]{64}$'),
	CONSTRAINT charlie_approval_resource_nonempty CHECK (length(trim(resource_type)) > 0 AND length(trim(resource_id)) > 0),
    CONSTRAINT charlie_approval_expiry CHECK (expires_at > created_at),
    CONSTRAINT charlie_approval_dispatch CHECK ((state = 'dispatched') = (dispatched_at IS NOT NULL))
);
CREATE INDEX charlie_action_approvals_active_idx ON charlie_action_approvals (session_id, expires_at) WHERE state IN ('pending', 'approved');

CREATE TABLE charlie_action_receipts (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id            UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    session_id               UUID NOT NULL REFERENCES charlie_sessions(id) ON DELETE CASCADE,
    charlie_action_id        VARCHAR(128) NOT NULL UNIQUE,
    turn_id                  VARCHAR(128) NOT NULL,
    capability               VARCHAR(128) NOT NULL,
    effect                   VARCHAR(16) NOT NULL,
    argument_digest          VARCHAR(128) NOT NULL,
    arguments_encrypted      TEXT NOT NULL,
    authorization_hash       VARCHAR(128) NOT NULL,
    resource_digest          VARCHAR(128) NOT NULL,
    fencing_epoch            BIGINT NOT NULL,
    product_idempotency_key  VARCHAR(128) NOT NULL UNIQUE,
    state                    VARCHAR(24) NOT NULL DEFAULT 'claimed',
    attempt                  INTEGER NOT NULL DEFAULT 1,
    lease_owner              VARCHAR(128) NOT NULL,
    lease_expires_at         TIMESTAMPTZ NOT NULL,
    result_digest            VARCHAR(128) NOT NULL DEFAULT '',
    result_status            VARCHAR(32) NOT NULL DEFAULT '',
    result_encrypted         TEXT NOT NULL DEFAULT '',
    audit_correlation_id     UUID NOT NULL,
    dispatched_at            TIMESTAMPTZ,
    verified_at              TIMESTAMPTZ,
    auto_budget_reserved     BOOLEAN NOT NULL DEFAULT false,
    safety_policy_revision   BIGINT NOT NULL DEFAULT 0,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_receipt_effect CHECK (effect IN ('read', 'write')),
    CONSTRAINT charlie_receipt_state CHECK (state IN ('claimed', 'blocked', 'waiting_approval', 'deferred', 'dispatched', 'ambiguous', 'verifying', 'succeeded', 'failed', 'fenced')),
    CONSTRAINT charlie_receipt_attempt_positive CHECK (attempt > 0),
    CONSTRAINT charlie_receipt_epoch_nonnegative CHECK (fencing_epoch >= 0),
    CONSTRAINT charlie_receipt_safety_revision CHECK (safety_policy_revision >= 0),
	CONSTRAINT charlie_receipt_arguments_present CHECK (length(arguments_encrypted) > 0),
	CONSTRAINT charlie_receipt_terminal_result CHECK (
		state NOT IN ('blocked', 'deferred', 'succeeded', 'failed', 'fenced')
		OR (length(result_digest) > 0 AND length(result_status) > 0 AND length(result_encrypted) > 0)
	),
    CONSTRAINT charlie_receipt_lease_valid CHECK (lease_expires_at > created_at)
);
CREATE INDEX charlie_receipts_session_idx ON charlie_action_receipts (session_id, created_at DESC);
CREATE INDEX charlie_receipts_reconcile_idx ON charlie_action_receipts (state, lease_expires_at) WHERE state IN ('claimed', 'dispatched', 'ambiguous', 'verifying');

-- Auto is fail-closed until an operator creates an enabled policy for one
-- exact catalog capability. Budgets are deliberately local and durable: a
-- Charlie service restart cannot reset them. Resource targets remain hash-only.
CREATE TABLE charlie_automation_policies (
    id                         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id              UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    capability                 VARCHAR(128) NOT NULL,
    enabled                    BOOLEAN NOT NULL DEFAULT false,
    max_actions_per_incident   INTEGER NOT NULL DEFAULT 1,
    max_actions_per_window     INTEGER NOT NULL DEFAULT 1,
    budget_window_seconds      INTEGER NOT NULL DEFAULT 1800,
    cooldown_seconds           INTEGER NOT NULL DEFAULT 1800,
    revision                   BIGINT NOT NULL DEFAULT 1,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, capability),
    CONSTRAINT charlie_auto_policy_incident_budget CHECK (max_actions_per_incident BETWEEN 1 AND 100),
    CONSTRAINT charlie_auto_policy_window_budget CHECK (max_actions_per_window BETWEEN 1 AND 100),
    CONSTRAINT charlie_auto_policy_window CHECK (budget_window_seconds BETWEEN 60 AND 86400),
    CONSTRAINT charlie_auto_policy_cooldown CHECK (cooldown_seconds BETWEEN 30 AND 604800),
    CONSTRAINT charlie_auto_policy_revision CHECK (revision > 0)
);

CREATE INDEX charlie_receipts_safety_idx ON charlie_action_receipts
    (session_id, capability, resource_digest, updated_at DESC);

-- Deferred Charlie actions intentionally store no envelope, arguments, prompt,
-- or result. Charlie retains its own work and retries the same signed action at
-- deferred_until; Astronomer only persists the bounded scheduling decision.
CREATE TABLE charlie_action_deferrals (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    charlie_action_id  VARCHAR(128) NOT NULL UNIQUE REFERENCES charlie_action_receipts(charlie_action_id) ON DELETE CASCADE,
    window_id          UUID NOT NULL REFERENCES maintenance_windows(id) ON DELETE RESTRICT,
    deferred_until     TIMESTAMPTZ NOT NULL,
    expires_at         TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_deferral_expiry CHECK (expires_at > deferred_until)
);
CREATE INDEX charlie_action_deferrals_due_idx ON charlie_action_deferrals (deferred_until, expires_at);

CREATE TABLE charlie_trigger_rules (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id            UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    name                     VARCHAR(128) NOT NULL,
    rule_type                VARCHAR(64) NOT NULL,
    category                 VARCHAR(64) NOT NULL,
    enabled                  BOOLEAN NOT NULL DEFAULT true,
    minimum_severity         VARCHAR(16) NOT NULL DEFAULT 'warning',
    selectors                JSONB NOT NULL DEFAULT '{}',
    thresholds               JSONB NOT NULL DEFAULT '{}',
    window_seconds           INTEGER NOT NULL,
    cooldown_seconds         INTEGER NOT NULL,
    service_identity_id      UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    mode_ceiling             VARCHAR(16) NOT NULL DEFAULT 'read_only',
    created_by_id            UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, name),
    CONSTRAINT charlie_trigger_severity CHECK (minimum_severity IN ('info', 'warning', 'critical')),
    CONSTRAINT charlie_trigger_mode CHECK (mode_ceiling IN ('read_only', 'approval', 'auto')),
    CONSTRAINT charlie_trigger_window CHECK (window_seconds BETWEEN 1 AND 86400),
    CONSTRAINT charlie_trigger_cooldown CHECK (cooldown_seconds BETWEEN 0 AND 604800)
);

CREATE TABLE charlie_trigger_events (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id               UUID NOT NULL REFERENCES charlie_trigger_rules(id) ON DELETE CASCADE,
    retry_of_event_id     UUID REFERENCES charlie_trigger_events(id) ON DELETE SET NULL,
    source                VARCHAR(64) NOT NULL,
    event_type            VARCHAR(64) NOT NULL,
    resource_type         VARCHAR(64) NOT NULL,
    resource_id           VARCHAR(255) NOT NULL,
    fingerprint           VARCHAR(128) NOT NULL,
    summary_metadata      JSONB NOT NULL DEFAULT '{}',
    state                 VARCHAR(24) NOT NULL DEFAULT 'pending',
    session_id            UUID REFERENCES charlie_sessions(id) ON DELETE SET NULL,
    repeat_count          INTEGER NOT NULL DEFAULT 1,
    first_occurred_at     TIMESTAMPTZ NOT NULL,
    last_occurred_at      TIMESTAMPTZ NOT NULL,
    origin_resource_ref   VARCHAR(255) NOT NULL DEFAULT '',
    origin_event_ref      VARCHAR(255) NOT NULL DEFAULT '',
    attempt_count         INTEGER NOT NULL DEFAULT 0,
    next_attempt_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error_code       VARCHAR(64) NOT NULL DEFAULT '',
    dead_lettered_at      TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT charlie_trigger_event_state CHECK (state IN ('pending', 'dispatching', 'dispatched', 'retry', 'dead', 'completed', 'suppressed')),
    CONSTRAINT charlie_trigger_event_attempt CHECK (attempt_count >= 0),
    CONSTRAINT charlie_trigger_event_repeat CHECK (repeat_count > 0),
    CONSTRAINT charlie_trigger_event_time_order CHECK (last_occurred_at >= first_occurred_at)
);
CREATE UNIQUE INDEX charlie_trigger_event_active_dedupe_idx ON charlie_trigger_events (rule_id, fingerprint) WHERE state IN ('pending', 'dispatching', 'dispatched', 'retry');
CREATE UNIQUE INDEX charlie_trigger_event_active_operator_retry_idx ON charlie_trigger_events (retry_of_event_id) WHERE retry_of_event_id IS NOT NULL AND state IN ('pending', 'dispatching', 'dispatched', 'retry');
CREATE INDEX charlie_trigger_event_due_idx ON charlie_trigger_events (state, next_attempt_at) WHERE state IN ('pending', 'retry');

CREATE TABLE charlie_findings (
    id                         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id              UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    charlie_finding_id         VARCHAR(128) NOT NULL,
    approval_id                VARCHAR(128) UNIQUE,
    session_id                 UUID REFERENCES charlie_sessions(id) ON DELETE SET NULL,
    source                     VARCHAR(32) NOT NULL,
    severity                   VARCHAR(16) NOT NULL,
    status                     VARCHAR(16) NOT NULL DEFAULT 'open',
    effective_mode             VARCHAR(16) NOT NULL,
    execution_block_code       VARCHAR(64) NOT NULL DEFAULT '',
    dedupe_fingerprint         VARCHAR(128) NOT NULL,
    title                      VARCHAR(256) NOT NULL,
    summary                    VARCHAR(2048) NOT NULL,
    recommended_action_label   VARCHAR(256) NOT NULL DEFAULT '',
    risk_impact                VARCHAR(1024) NOT NULL DEFAULT '',
    verification_summary       VARCHAR(1024) NOT NULL DEFAULT '',
    repeat_count               INTEGER NOT NULL DEFAULT 1,
    expires_at                 TIMESTAMPTZ,
    acknowledged_by_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    acknowledged_at            TIMESTAMPTZ,
    dismissed_by_id            UUID REFERENCES users(id) ON DELETE SET NULL,
    dismissed_at               TIMESTAMPTZ,
    resolved_by_id             UUID REFERENCES users(id) ON DELETE SET NULL,
    resolved_at                TIMESTAMPTZ,
    alert_event_id             UUID REFERENCES alert_events(id) ON DELETE SET NULL,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, charlie_finding_id),
    CONSTRAINT charlie_finding_source CHECK (source IN ('user', 'trigger', 'system')),
    CONSTRAINT charlie_finding_severity CHECK (severity IN ('info', 'low', 'medium', 'warning', 'high', 'critical')),
    CONSTRAINT charlie_finding_status CHECK (status IN ('open', 'acknowledged', 'dismissed', 'resolved', 'expired')),
    CONSTRAINT charlie_finding_mode CHECK (effective_mode IN ('read_only', 'approval', 'auto')),
    CONSTRAINT charlie_finding_approval_mode CHECK (approval_id IS NULL OR effective_mode = 'approval'),
    CONSTRAINT charlie_finding_repeat CHECK (repeat_count > 0)
);
CREATE UNIQUE INDEX charlie_finding_active_dedupe_idx ON charlie_findings (connection_id, dedupe_fingerprint) WHERE status IN ('open', 'acknowledged');
CREATE INDEX charlie_findings_status_idx ON charlie_findings (status, severity, updated_at DESC);

CREATE TABLE charlie_finding_resources (
    finding_id       UUID NOT NULL REFERENCES charlie_findings(id) ON DELETE CASCADE,
    resource_type    VARCHAR(64) NOT NULL,
    resource_id      VARCHAR(255) NOT NULL,
    required_verb    VARCHAR(32) NOT NULL DEFAULT 'read',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (finding_id, resource_type, resource_id, required_verb),
    CONSTRAINT charlie_finding_resource_nonempty CHECK (length(trim(resource_type)) > 0 AND length(trim(resource_id)) > 0 AND length(trim(required_verb)) > 0)
);
CREATE INDEX charlie_finding_resources_target_idx ON charlie_finding_resources (resource_type, resource_id);
