-- Astronomer v1 canonical greenfield schema.
-- This is intentionally the only migration; pre-v1 databases are rejected by Helm preflight.
--
-- PostgreSQL database dump
--


-- Dumped from database version 17.10 (Debian 17.10-1.pgdg12+1)
-- Dumped by pg_dump version 17.10 (Debian 17.10-1.pgdg12+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: pgcrypto; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;


--
-- Name: EXTENSION pgcrypto; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION pgcrypto IS 'cryptographic functions';


--
-- Name: uuid-ossp; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS "uuid-ossp" WITH SCHEMA public;


--
-- Name: EXTENSION "uuid-ossp"; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION "uuid-ossp" IS 'generate universally unique identifiers (UUIDs)';


--
-- Name: bump_dex_runtime_generation(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.bump_dex_runtime_generation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE was_enabled boolean;
BEGIN
    IF current_setting('astronomer.dex_connector_stage_bypass', true) = '1' THEN
        PERFORM set_config('astronomer.dex_connector_stage_bypass', '', true);
        RETURN COALESCE(NEW, OLD);
    END IF;
    PERFORM pg_advisory_xact_lock(742193440558879931);
    SELECT COALESCE((SELECT is_enabled FROM sso_configurations WHERE provider='dex'),false)
        OR COALESCE((SELECT saga_previous_sso_enabled FROM dex_settings WHERE id='00000000-0000-0000-0000-000000000001'::uuid),false)
    INTO was_enabled;
    WITH staged AS (
        UPDATE dex_settings SET runtime_generation=runtime_generation+1,
            saga_previous_sso_enabled=was_enabled,updated_at=now()
        WHERE id='00000000-0000-0000-0000-000000000001'::uuid RETURNING 1
    )
    UPDATE sso_configurations SET is_enabled=false,updated_at=now()
    WHERE provider='dex' AND is_enabled=true AND EXISTS(SELECT 1 FROM staged);
    RETURN COALESCE(NEW, OLD);
END;
$$;


--
-- Name: create_audit_log_partition(timestamptz); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.create_audit_log_partition(target_month timestamptz) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    month_start TIMESTAMPTZ := date_trunc('month', target_month);
    month_end   TIMESTAMPTZ := month_start + INTERVAL '1 month';
    partition_name TEXT := 'audit_log_' || to_char(month_start, 'YYYY_MM');
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS public.%I PARTITION OF public.audit_log FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        month_start,
        month_end
    );
END;
$$;


--
-- Name: revoke_charlie_delegations_for_deactivated_user(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.revoke_charlie_delegations_for_deactivated_user() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE charlie_delegations
    SET revoked_at = now()
    WHERE principal_id = NEW.id
      AND revoked_at IS NULL;
    RETURN NEW;
END;
$$;


--
-- Name: revoke_charlie_delegations_for_inactive_connection(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.revoke_charlie_delegations_for_inactive_connection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


--
-- Name: revoke_charlie_delegations_on_rbac_change(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.revoke_charlie_delegations_on_rbac_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE charlie_delegations
    SET revoked_at = now()
    WHERE revoked_at IS NULL;
    RETURN NULL;
END;
$$;


--
-- Name: update_updated_at(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.update_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: agent_connection_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_connection_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    connection_id uuid,
    event_type character varying(32) NOT NULL,
    reason_code character varying(64) DEFAULT ''::character varying NOT NULL,
    agent_id character varying(128) DEFAULT ''::character varying NOT NULL,
    agent_version character varying(64) DEFAULT ''::character varying NOT NULL,
    protocol_version character varying(64) DEFAULT ''::character varying NOT NULL,
    server_replica character varying(128) DEFAULT ''::character varying NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    occurred_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT agent_connection_event_metadata_bounded CHECK ((octet_length((metadata)::text) <= 8192)),
    CONSTRAINT agent_connection_event_type CHECK (((event_type)::text = ANY ((ARRAY['connected'::character varying, 'disconnected'::character varying, 'reconnecting'::character varying, 'auth_failed'::character varying, 'registration_failed'::character varying, 'heartbeat_stale'::character varying, 'api_unreachable'::character varying, 'protocol_incompatible'::character varying, 'credential_expired'::character varying, 'credential_revoked'::character varying, 'upgrade_failed'::character varying, 'upgrade_stalled'::character varying, 'audit_ingestion_failed'::character varying, 'metrics_ingestion_failed'::character varying, 'state_ingestion_failed'::character varying, 'command_expired'::character varying, 'command_failed'::character varying])::text[])))
);


--
-- Name: agent_connections; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_connections (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    agent_id character varying(128) NOT NULL,
    session_id character varying(255) DEFAULT ''::character varying NOT NULL,
    connected_at timestamptz DEFAULT now() NOT NULL,
    disconnected_at timestamptz,
    last_ping timestamptz,
    status character varying(16) DEFAULT 'connected'::character varying NOT NULL,
    channel_name character varying(255) DEFAULT ''::character varying NOT NULL,
    pod_name character varying(255) DEFAULT ''::character varying NOT NULL,
    node_name character varying(255) DEFAULT ''::character varying NOT NULL,
    agent_version character varying(32) DEFAULT ''::character varying NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: agent_lifecycle_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_lifecycle_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    operation_type text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    target_version text NOT NULL,
    target_image text NOT NULL,
    current_version text DEFAULT ''::text NOT NULL,
    strategy text DEFAULT 'manifest_rollout'::text NOT NULL,
    operation_spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    requested_by uuid,
    started_at timestamptz,
    completed_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT agent_lifecycle_operations_operation_type_check CHECK ((operation_type = 'agent_upgrade'::text)),
    CONSTRAINT agent_lifecycle_operations_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text])))
);


--
-- Name: agent_operational_statuses; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_operational_statuses (
    cluster_id uuid NOT NULL,
    agent_id character varying(128) DEFAULT ''::character varying NOT NULL,
    installed_agent_version character varying(64) DEFAULT ''::character varying NOT NULL,
    desired_agent_version character varying(64) DEFAULT ''::character varying NOT NULL,
    protocol_version character varying(64) DEFAULT ''::character varying NOT NULL,
    protocol_compatible boolean,
    authentication_state character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    registration_state character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    credential_state character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    credential_expires_at timestamptz,
    upgrade_state character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    audit_ingestion_state character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    metrics_ingestion_state character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    state_ingestion_state character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    pending_command_count integer DEFAULT 0 NOT NULL,
    failed_command_count integer DEFAULT 0 NOT NULL,
    expired_command_count integer DEFAULT 0 NOT NULL,
    downstream_api_reachable boolean,
    downstream_api_reported_at timestamptz,
    owning_server_replica character varying(128) DEFAULT ''::character varying NOT NULL,
    last_successful_connection_at timestamptz,
    last_status_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT agent_ops_audit_ingestion CHECK (((audit_ingestion_state)::text = ANY ((ARRAY['unknown'::character varying, 'healthy'::character varying, 'degraded'::character varying, 'failed'::character varying])::text[]))),
    CONSTRAINT agent_ops_auth_state CHECK (((authentication_state)::text = ANY ((ARRAY['unknown'::character varying, 'ok'::character varying, 'failed'::character varying, 'expired'::character varying, 'revoked'::character varying])::text[]))),
    CONSTRAINT agent_ops_command_counts CHECK (((pending_command_count >= 0) AND (failed_command_count >= 0) AND (expired_command_count >= 0))),
    CONSTRAINT agent_ops_credential_state CHECK (((credential_state)::text = ANY ((ARRAY['unknown'::character varying, 'valid'::character varying, 'expiring'::character varying, 'expired'::character varying, 'revoked'::character varying])::text[]))),
    CONSTRAINT agent_ops_metrics_ingestion CHECK (((metrics_ingestion_state)::text = ANY ((ARRAY['unknown'::character varying, 'healthy'::character varying, 'degraded'::character varying, 'failed'::character varying])::text[]))),
    CONSTRAINT agent_ops_registration_state CHECK (((registration_state)::text = ANY ((ARRAY['unknown'::character varying, 'registered'::character varying, 'failed'::character varying, 'rejected'::character varying])::text[]))),
    CONSTRAINT agent_ops_state_ingestion CHECK (((state_ingestion_state)::text = ANY ((ARRAY['unknown'::character varying, 'healthy'::character varying, 'degraded'::character varying, 'failed'::character varying])::text[]))),
    CONSTRAINT agent_ops_upgrade_state CHECK (((upgrade_state)::text = ANY ((ARRAY['unknown'::character varying, 'current'::character varying, 'available'::character varying, 'pending'::character varying, 'running'::character varying, 'stalled'::character varying, 'failed'::character varying])::text[])))
);


--
-- Name: alert_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rule_id uuid NOT NULL,
    cluster_id uuid,
    status character varying(16) DEFAULT 'firing'::character varying NOT NULL,
    message text NOT NULL,
    details jsonb DEFAULT '{}'::jsonb NOT NULL,
    fired_at timestamptz DEFAULT now() NOT NULL,
    resolved_at timestamptz,
    acknowledged_by_id uuid,
    acknowledged_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: alert_inhibitions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_inhibitions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    source_matchers jsonb DEFAULT '[]'::jsonb NOT NULL,
    target_matchers jsonb DEFAULT '[]'::jsonb NOT NULL,
    equal_labels jsonb DEFAULT '[]'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: alert_rule_channels; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_rule_channels (
    alert_rule_id uuid NOT NULL,
    notification_channel_id uuid NOT NULL
);


--
-- Name: alert_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_rules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    cluster_id uuid,
    rule_type character varying(16) NOT NULL,
    configuration jsonb NOT NULL,
    severity character varying(16) DEFAULT 'warning'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    cooldown_minutes integer DEFAULT 15 NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    rule_kind character varying(16) DEFAULT 'threshold'::character varying NOT NULL,
    anomaly_stddev double precision,
    anomaly_window_seconds integer,
    anomaly_min_samples integer DEFAULT 50 NOT NULL,
    anomaly_direction character varying(8) DEFAULT 'above'::character varying NOT NULL,
    CONSTRAINT alert_anomaly_dir_valid CHECK (((anomaly_direction)::text = ANY ((ARRAY['above'::character varying, 'below'::character varying, 'either'::character varying])::text[]))),
    CONSTRAINT alert_rule_kind_valid CHECK (((rule_kind)::text = ANY ((ARRAY['threshold'::character varying, 'anomaly'::character varying])::text[])))
);


--
-- Name: alert_silences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.alert_silences (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rule_id uuid,
    cluster_id uuid,
    reason text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: anomaly_baselines; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.anomaly_baselines (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    metric_name character varying(128) NOT NULL,
    window_seconds integer DEFAULT 86400 NOT NULL,
    sample_count integer DEFAULT 0 NOT NULL,
    mean double precision DEFAULT 0 NOT NULL,
    stddev double precision DEFAULT 0 NOT NULL,
    min_value double precision DEFAULT 0 NOT NULL,
    max_value double precision DEFAULT 0 NOT NULL,
    p50 double precision DEFAULT 0 NOT NULL,
    p95 double precision DEFAULT 0 NOT NULL,
    p99 double precision DEFAULT 0 NOT NULL,
    last_value double precision DEFAULT 0 NOT NULL,
    last_value_at timestamptz,
    recent_samples jsonb DEFAULT '[]'::jsonb NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: api_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    name character varying(128) NOT NULL,
    token_hash character varying(128) NOT NULL,
    prefix character varying(16) NOT NULL,
    expires_at timestamptz,
    last_used_at timestamptz,
    is_revoked boolean DEFAULT false NOT NULL,
    scopes jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    allowed_cidrs text DEFAULT ''::text NOT NULL,
    last_seen_remote_ip text DEFAULT ''::text NOT NULL
);


--
-- Name: apiserver_allowlist_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.apiserver_allowlist_snapshots (
    id bigint NOT NULL,
    cluster_id uuid NOT NULL,
    captured_at timestamptz DEFAULT now() NOT NULL,
    effective_cidrs jsonb NOT NULL,
    desired_cidrs jsonb NOT NULL,
    drift boolean DEFAULT false NOT NULL
);


--
-- Name: apiserver_allowlist_snapshots_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.apiserver_allowlist_snapshots_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: apiserver_allowlist_snapshots_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.apiserver_allowlist_snapshots_id_seq OWNED BY public.apiserver_allowlist_snapshots.id;


--
-- Name: apiserver_allowlists; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.apiserver_allowlists (
    cluster_id uuid NOT NULL,
    cidrs jsonb DEFAULT '[]'::jsonb NOT NULL,
    mode character varying(16) DEFAULT 'monitor'::character varying NOT NULL,
    detected_provider character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    last_reconciled_at timestamptz,
    sync_status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    effective_cidrs jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT allowlist_mode_valid CHECK (((mode)::text = ANY ((ARRAY['enforce'::character varying, 'monitor'::character varying, 'disabled'::character varying])::text[]))),
    CONSTRAINT allowlist_status_valid CHECK (((sync_status)::text = ANY ((ARRAY['synced'::character varying, 'drifting'::character varying, 'pending'::character varying, 'failed'::character varying])::text[])))
);


--
-- Name: apiserver_audit_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.apiserver_audit_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    audit_id text NOT NULL,
    stage text DEFAULT ''::text NOT NULL,
    verb text DEFAULT ''::text NOT NULL,
    username text DEFAULT ''::text NOT NULL,
    resource text DEFAULT ''::text NOT NULL,
    namespace text DEFAULT ''::text NOT NULL,
    status_code integer DEFAULT 0 NOT NULL,
    event_time timestamptz DEFAULT now() NOT NULL,
    raw jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: audit_archive; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_archive (
    id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    schema_version character varying(32) DEFAULT 'audit-v1'::character varying NOT NULL,
    user_id uuid,
    actor_auth_method character varying(32) DEFAULT ''::character varying NOT NULL,
    action character varying(64) NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(255) DEFAULT ''::character varying NOT NULL,
    resource_name character varying(255) DEFAULT ''::character varying NOT NULL,
    http_method character varying(16) DEFAULT ''::character varying NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    status_code integer DEFAULT 0 NOT NULL,
    duration_ms bigint DEFAULT 0 NOT NULL,
    request_id character varying(64) DEFAULT ''::character varying NOT NULL,
    ip_address inet,
    user_agent text DEFAULT ''::text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    source character varying(16) DEFAULT 'service'::character varying NOT NULL,
    correlation_id character varying(64) DEFAULT ''::character varying NOT NULL,
    archived_cluster_id uuid,
    archived_at timestamptz DEFAULT now() NOT NULL,
    archived_cluster_name character varying(255) DEFAULT ''::character varying NOT NULL
);


--
-- Name: COLUMN audit_archive.archived_cluster_name; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.audit_archive.archived_cluster_name IS 'Denormalized name of the decommissioned cluster this row was archived for; makes the row self-describing so the cluster tombstone can eventually be purged.';


--
-- Name: audit_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_log (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    schema_version character varying(32) DEFAULT 'audit-v1'::character varying NOT NULL,
    user_id uuid,
    actor_auth_method character varying(32) DEFAULT ''::character varying NOT NULL,
    action character varying(64) NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(255) DEFAULT ''::character varying NOT NULL,
    resource_name character varying(255) DEFAULT ''::character varying NOT NULL,
    http_method character varying(16) DEFAULT ''::character varying NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    status_code integer DEFAULT 0 NOT NULL,
    duration_ms bigint DEFAULT 0 NOT NULL,
    request_id character varying(64) DEFAULT ''::character varying NOT NULL,
    ip_address inet,
    user_agent text DEFAULT ''::text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    source character varying(16) DEFAULT 'service'::character varying NOT NULL,
    correlation_id character varying(64) DEFAULT ''::character varying NOT NULL,
    action_class character varying(16) DEFAULT 'mutation'::character varying NOT NULL,
    CONSTRAINT audit_action_class_valid CHECK (((action_class)::text = ANY ((ARRAY['mutation'::character varying, 'read'::character varying, 'auth'::character varying, 'system'::character varying])::text[])))
)
PARTITION BY RANGE (created_at);


--
-- Name: audit_log_default; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_log_default (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    schema_version character varying(32) DEFAULT 'audit-v1'::character varying NOT NULL,
    user_id uuid,
    actor_auth_method character varying(32) DEFAULT ''::character varying NOT NULL,
    action character varying(64) NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(255) DEFAULT ''::character varying NOT NULL,
    resource_name character varying(255) DEFAULT ''::character varying NOT NULL,
    http_method character varying(16) DEFAULT ''::character varying NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    status_code integer DEFAULT 0 NOT NULL,
    duration_ms bigint DEFAULT 0 NOT NULL,
    request_id character varying(64) DEFAULT ''::character varying NOT NULL,
    ip_address inet,
    user_agent text DEFAULT ''::text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    source character varying(16) DEFAULT 'service'::character varying NOT NULL,
    correlation_id character varying(64) DEFAULT ''::character varying NOT NULL,
    action_class character varying(16) DEFAULT 'mutation'::character varying NOT NULL,
    CONSTRAINT audit_action_class_valid CHECK (((action_class)::text = ANY ((ARRAY['mutation'::character varying, 'read'::character varying, 'auth'::character varying, 'system'::character varying])::text[])))
);


--
-- Name: authored_constraints; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.authored_constraints (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    api_version text NOT NULL,
    yaml text NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: backup_drill_results; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.backup_drill_results (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    status character varying(32) NOT NULL,
    backup_key character varying(512) DEFAULT ''::character varying NOT NULL,
    schema_version integer,
    error_message text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: backup_schedules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.backup_schedules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    storage_id uuid NOT NULL,
    backup_type character varying(20) DEFAULT 'full'::character varying NOT NULL,
    cron_expression character varying(100) DEFAULT '0 2 * * *'::character varying NOT NULL,
    retention_count integer DEFAULT 30 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_backup_id uuid,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    cluster_id uuid,
    velero_namespace character varying(63) DEFAULT 'velero'::character varying NOT NULL,
    velero_schedule_name character varying(253) DEFAULT ''::character varying NOT NULL,
    included_namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    excluded_namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    ttl character varying(32) DEFAULT ''::character varying NOT NULL
);


--
-- Name: backup_storage_configs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.backup_storage_configs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    storage_type character varying(20) DEFAULT 's3'::character varying NOT NULL,
    bucket character varying(255) NOT NULL,
    prefix character varying(255) DEFAULT 'astronomer-backups/'::character varying NOT NULL,
    region character varying(50) DEFAULT ''::character varying NOT NULL,
    endpoint_url character varying(500) DEFAULT ''::character varying NOT NULL,
    access_key character varying(255) DEFAULT ''::character varying NOT NULL,
    secret_key character varying(255) DEFAULT ''::character varying NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    cluster_id uuid,
    velero_namespace character varying(63) DEFAULT 'velero'::character varying NOT NULL,
    bsl_name character varying(253) DEFAULT ''::character varying NOT NULL,
    encrypted_credentials text DEFAULT ''::text NOT NULL
);


--
-- Name: backups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.backups (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    storage_id uuid NOT NULL,
    backup_type character varying(20) DEFAULT 'full'::character varying NOT NULL,
    status character varying(20) DEFAULT 'pending'::character varying NOT NULL,
    file_path character varying(500) DEFAULT ''::character varying NOT NULL,
    file_size_bytes bigint DEFAULT 0 NOT NULL,
    database_tables jsonb DEFAULT '[]'::jsonb NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    cluster_id uuid,
    velero_backup_name character varying(253) DEFAULT ''::character varying NOT NULL,
    velero_namespace character varying(63) DEFAULT 'velero'::character varying NOT NULL,
    included_namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    excluded_namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    poll_attempts integer DEFAULT 0 NOT NULL,
    last_polled_at timestamptz
);


--
-- Name: catalog_blessed_charts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.catalog_blessed_charts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    repo_url character varying(500) NOT NULL,
    chart_name character varying(255) NOT NULL,
    display_name character varying(255) DEFAULT ''::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    category character varying(50) DEFAULT 'other'::character varying NOT NULL,
    icon_url character varying(500) DEFAULT ''::character varying NOT NULL,
    mgmt_safe boolean DEFAULT true NOT NULL,
    version_policy character varying(50) DEFAULT ''::character varying NOT NULL,
    source character varying(50) DEFAULT 'catalog.yaml'::character varying NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: catalog_operation_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.catalog_operation_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    operation_id uuid NOT NULL,
    level character varying(16) NOT NULL,
    stage character varying(64) NOT NULL,
    message text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: catalog_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.catalog_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_type character varying(64) NOT NULL,
    target_key character varying(255) NOT NULL,
    operation_type character varying(32) NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    status character varying(32) DEFAULT 'pending'::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: charlie_action_approvals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_action_approvals (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    session_id uuid NOT NULL,
    approval_id character varying(128) NOT NULL,
    charlie_action_id character varying(128) NOT NULL,
    turn_id character varying(128) NOT NULL,
    capability character varying(128) NOT NULL,
    argument_digest character varying(128) NOT NULL,
    disclosure_digest character varying(128) NOT NULL,
    mode_revision bigint NOT NULL,
    policy_revision bigint NOT NULL,
    fencing_epoch bigint NOT NULL,
    manifest_digest character varying(64) NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(128) NOT NULL,
    approver_id uuid NOT NULL,
    rationale_digest character varying(128) NOT NULL,
    state character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    expires_at timestamptz NOT NULL,
    dispatched_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    decision_request_id uuid NOT NULL,
    decision character varying(8) NOT NULL,
    CONSTRAINT charlie_approval_decision_valid CHECK (((decision)::text = ANY ((ARRAY['approve'::character varying, 'reject'::character varying])::text[]))),
    CONSTRAINT charlie_approval_dispatch CHECK ((((state)::text = 'dispatched'::text) = (dispatched_at IS NOT NULL))),
    CONSTRAINT charlie_approval_expiry CHECK ((expires_at > created_at)),
    CONSTRAINT charlie_approval_manifest_digest CHECK (((manifest_digest)::text ~ '^[a-f0-9]{64}$'::text)),
    CONSTRAINT charlie_approval_resource_nonempty CHECK (((length(TRIM(BOTH FROM resource_type)) > 0) AND (length(TRIM(BOTH FROM resource_id)) > 0))),
    CONSTRAINT charlie_approval_revision_positive CHECK (((mode_revision > 0) AND (policy_revision > 0) AND (fencing_epoch > 0))),
    CONSTRAINT charlie_approval_state CHECK (((state)::text = ANY ((ARRAY['pending'::character varying, 'approved'::character varying, 'rejected'::character varying, 'expired'::character varying, 'dispatched'::character varying])::text[])))
);


--
-- Name: charlie_action_deferrals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_action_deferrals (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    charlie_action_id character varying(128) NOT NULL,
    window_id uuid NOT NULL,
    deferred_until timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_deferral_expiry CHECK ((expires_at > deferred_until))
);


--
-- Name: charlie_action_receipts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_action_receipts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    session_id uuid NOT NULL,
    charlie_action_id character varying(128) NOT NULL,
    turn_id character varying(128) NOT NULL,
    capability character varying(128) NOT NULL,
    effect character varying(16) NOT NULL,
    argument_digest character varying(128) NOT NULL,
    arguments_encrypted text NOT NULL,
    authorization_hash character varying(128) NOT NULL,
    resource_digest character varying(128) NOT NULL,
    fencing_epoch bigint NOT NULL,
    product_idempotency_key character varying(128) NOT NULL,
    state character varying(24) DEFAULT 'claimed'::character varying NOT NULL,
    attempt integer DEFAULT 1 NOT NULL,
    lease_owner character varying(128) NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    result_digest character varying(128) DEFAULT ''::character varying NOT NULL,
    result_status character varying(32) DEFAULT ''::character varying NOT NULL,
    result_encrypted text DEFAULT ''::text NOT NULL,
    audit_correlation_id uuid NOT NULL,
    dispatched_at timestamptz,
    verified_at timestamptz,
    auto_budget_reserved boolean DEFAULT false NOT NULL,
    safety_policy_revision bigint DEFAULT 0 NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_receipt_arguments_present CHECK ((length(arguments_encrypted) > 0)),
    CONSTRAINT charlie_receipt_attempt_positive CHECK ((attempt > 0)),
    CONSTRAINT charlie_receipt_effect CHECK (((effect)::text = ANY ((ARRAY['read'::character varying, 'write'::character varying])::text[]))),
    CONSTRAINT charlie_receipt_epoch_nonnegative CHECK ((fencing_epoch >= 0)),
    CONSTRAINT charlie_receipt_lease_valid CHECK ((lease_expires_at > created_at)),
    CONSTRAINT charlie_receipt_safety_revision CHECK ((safety_policy_revision >= 0)),
    CONSTRAINT charlie_receipt_state CHECK (((state)::text = ANY ((ARRAY['claimed'::character varying, 'blocked'::character varying, 'waiting_approval'::character varying, 'deferred'::character varying, 'dispatched'::character varying, 'ambiguous'::character varying, 'verifying'::character varying, 'succeeded'::character varying, 'failed'::character varying, 'fenced'::character varying])::text[]))),
    CONSTRAINT charlie_receipt_terminal_result CHECK ((((state)::text <> ALL ((ARRAY['blocked'::character varying, 'deferred'::character varying, 'succeeded'::character varying, 'failed'::character varying, 'fenced'::character varying])::text[])) OR ((length((result_digest)::text) > 0) AND (length((result_status)::text) > 0) AND (length(result_encrypted) > 0))))
);


--
-- Name: charlie_alert_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_alert_deliveries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    finding_id uuid NOT NULL,
    notification_channel_id uuid,
    policy_revision bigint NOT NULL,
    delivery_kind character varying(16) NOT NULL,
    dedupe_bucket bigint NOT NULL,
    severity character varying(16) NOT NULL,
    status character varying(16) DEFAULT 'queued'::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    maximum_attempts integer DEFAULT 8 NOT NULL,
    next_attempt_at timestamptz DEFAULT now() NOT NULL,
    delivered_at timestamptz,
    last_error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    deep_link character varying(256) NOT NULL,
    subject character varying(256) NOT NULL,
    body character varying(1024) NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_alert_delivery_attempts CHECK (((attempt_count >= 0) AND ((maximum_attempts >= 1) AND (maximum_attempts <= 20)))),
    CONSTRAINT charlie_alert_delivery_deep_link CHECK (((deep_link)::text ~ '^/dashboard/charlie\?tab=findings&finding=[0-9a-f-]{36}$'::text)),
    CONSTRAINT charlie_alert_delivery_kind CHECK (((delivery_kind)::text = ANY ((ARRAY['initial'::character varying, 'escalation'::character varying])::text[]))),
    CONSTRAINT charlie_alert_delivery_revision CHECK ((policy_revision > 0)),
    CONSTRAINT charlie_alert_delivery_severity CHECK (((severity)::text = ANY ((ARRAY['info'::character varying, 'low'::character varying, 'medium'::character varying, 'warning'::character varying, 'high'::character varying, 'critical'::character varying])::text[]))),
    CONSTRAINT charlie_alert_delivery_status CHECK (((status)::text = ANY ((ARRAY['queued'::character varying, 'delivering'::character varying, 'retry'::character varying, 'delivered'::character varying, 'suppressed'::character varying, 'dead'::character varying])::text[])))
);


--
-- Name: charlie_alert_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_alert_policies (
    connection_id uuid NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    minimum_severity character varying(16) DEFAULT 'medium'::character varying NOT NULL,
    dedupe_window_seconds integer DEFAULT 1800 NOT NULL,
    escalation_after_seconds integer DEFAULT 3600 NOT NULL,
    quiet_hours_enabled boolean DEFAULT false NOT NULL,
    quiet_hours_start character(5) DEFAULT '22:00'::bpchar NOT NULL,
    quiet_hours_end character(5) DEFAULT '07:00'::bpchar NOT NULL,
    quiet_hours_timezone character varying(64) DEFAULT 'UTC'::character varying NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    updated_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_alert_policy_dedupe CHECK (((dedupe_window_seconds >= 60) AND (dedupe_window_seconds <= 604800))),
    CONSTRAINT charlie_alert_policy_escalation CHECK (((escalation_after_seconds = 0) OR ((escalation_after_seconds >= 300) AND (escalation_after_seconds <= 604800)))),
    CONSTRAINT charlie_alert_policy_quiet_end CHECK ((quiet_hours_end ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'::text)),
    CONSTRAINT charlie_alert_policy_quiet_start CHECK ((quiet_hours_start ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'::text)),
    CONSTRAINT charlie_alert_policy_revision CHECK ((revision > 0)),
    CONSTRAINT charlie_alert_policy_severity CHECK (((minimum_severity)::text = ANY ((ARRAY['info'::character varying, 'low'::character varying, 'medium'::character varying, 'warning'::character varying, 'high'::character varying, 'critical'::character varying])::text[]))),
    CONSTRAINT charlie_alert_policy_timezone_nonempty CHECK ((length(TRIM(BOTH FROM quiet_hours_timezone)) > 0))
);


--
-- Name: charlie_alert_policy_channels; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_alert_policy_channels (
    connection_id uuid NOT NULL,
    notification_channel_id uuid NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: charlie_artifact_credential_state; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_artifact_credential_state (
    connection_id uuid NOT NULL,
    current_lease_id character varying(128) DEFAULT ''::character varying NOT NULL,
    current_generation bigint DEFAULT 0 NOT NULL,
    renew_after timestamptz,
    expires_at timestamptz,
    pending_request_id uuid,
    pending_lease_id character varying(128) DEFAULT ''::character varying NOT NULL,
    pending_generation bigint DEFAULT 0 NOT NULL,
    pending_state character varying(24) DEFAULT 'idle'::character varying NOT NULL,
    materialization_digest character varying(71) DEFAULT ''::character varying NOT NULL,
    last_error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    acknowledged_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_artifact_expiry_order CHECK (((renew_after IS NULL) OR (expires_at IS NULL) OR (renew_after < expires_at))),
    CONSTRAINT charlie_artifact_generation_nonnegative CHECK (((current_generation >= 0) AND (pending_generation >= 0))),
    CONSTRAINT charlie_artifact_pending_binding CHECK (((((pending_state)::text = 'idle'::text) AND (pending_request_id IS NULL) AND ((pending_lease_id)::text = ''::text) AND (pending_generation = 0) AND ((materialization_digest)::text = ''::text)) OR (((pending_state)::text = 'claiming'::text) AND (pending_request_id IS NOT NULL) AND ((pending_lease_id)::text = ''::text) AND (pending_generation = 0) AND ((materialization_digest)::text = ''::text)) OR (((pending_state)::text = 'claimed'::text) AND (pending_request_id IS NOT NULL) AND ((pending_lease_id)::text <> ''::text) AND (pending_generation > current_generation) AND ((materialization_digest)::text = ''::text)) OR (((pending_state)::text = 'materialized'::text) AND (pending_request_id IS NOT NULL) AND ((pending_lease_id)::text <> ''::text) AND (pending_generation > current_generation) AND ((materialization_digest)::text ~ '^sha256:[a-f0-9]{64}$'::text)))),
    CONSTRAINT charlie_artifact_pending_state CHECK (((pending_state)::text = ANY ((ARRAY['idle'::character varying, 'claiming'::character varying, 'claimed'::character varying, 'materialized'::character varying])::text[])))
);


--
-- Name: charlie_automation_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_automation_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    capability character varying(128) NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    max_actions_per_incident integer DEFAULT 1 NOT NULL,
    max_actions_per_window integer DEFAULT 1 NOT NULL,
    budget_window_seconds integer DEFAULT 1800 NOT NULL,
    cooldown_seconds integer DEFAULT 1800 NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_auto_policy_cooldown CHECK (((cooldown_seconds >= 30) AND (cooldown_seconds <= 604800))),
    CONSTRAINT charlie_auto_policy_incident_budget CHECK (((max_actions_per_incident >= 1) AND (max_actions_per_incident <= 100))),
    CONSTRAINT charlie_auto_policy_revision CHECK ((revision > 0)),
    CONSTRAINT charlie_auto_policy_window CHECK (((budget_window_seconds >= 60) AND (budget_window_seconds <= 86400))),
    CONSTRAINT charlie_auto_policy_window_budget CHECK (((max_actions_per_window >= 1) AND (max_actions_per_window <= 100)))
);


--
-- Name: charlie_connections; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_connections (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    installation_id uuid NOT NULL,
    product_id character varying(128) NOT NULL,
    product_slug character varying(63) NOT NULL,
    deployment_id character varying(128) NOT NULL,
    route_id character varying(128) NOT NULL,
    central_url character varying(512) NOT NULL,
    central_ca_fingerprint character varying(128) NOT NULL,
    signing_key_id character varying(128) NOT NULL,
    signing_key_fingerprint character varying(128) NOT NULL,
    onboarding_schema_version character varying(32) NOT NULL,
    central_api_version character varying(32) NOT NULL,
    agent_protocol_version character varying(32) NOT NULL,
    chart_version character varying(64) NOT NULL,
    chart_digest character varying(128) NOT NULL,
    image_digest character varying(128) NOT NULL,
    logical_agent_id character varying(128) NOT NULL,
    replica_count integer DEFAULT 2 NOT NULL,
    bridge_service_name character varying(253) NOT NULL,
    mcp_service_name character varying(253) NOT NULL,
    local_trust_material_encrypted text DEFAULT ''::text NOT NULL,
    agent_secret_name character varying(253) NOT NULL,
    onboarding_package_id character varying(128) NOT NULL,
    onboarding_package_digest character varying(128) NOT NULL,
    onboarding_package_expires_at timestamptz NOT NULL,
    enrollment_credentials_expires_at timestamptz NOT NULL,
    artifact_credential_expires_at timestamptz NOT NULL,
    certificate_expires_at timestamptz NOT NULL,
    onboarding_state character varying(32) DEFAULT 'validated'::character varying NOT NULL,
    agent_secret_hmac character varying(128) DEFAULT ''::character varying NOT NULL,
    requested_mode character varying(16) DEFAULT 'disabled'::character varying NOT NULL,
    verified_mode character varying(16) DEFAULT 'disabled'::character varying NOT NULL,
    verified_mode_revision bigint DEFAULT 0 NOT NULL,
    emergency_disabled boolean DEFAULT false NOT NULL,
    emergency_disabled_by_id uuid,
    emergency_disabled_at timestamptz,
    disclosure_digest character varying(128) DEFAULT ''::character varying NOT NULL,
    acknowledged_disclosure_digest character varying(128) DEFAULT ''::character varying NOT NULL,
    leader_instance_id character varying(128) DEFAULT ''::character varying NOT NULL,
    fencing_epoch bigint DEFAULT 0 NOT NULL,
    health_state character varying(32) DEFAULT 'inactive'::character varying NOT NULL,
    active boolean DEFAULT false NOT NULL,
    last_error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    last_verified_at timestamptz,
    last_connected_at timestamptz,
    last_rotated_at timestamptz,
    reconciliation_due_at timestamptz,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    chart_reference character varying(512) NOT NULL,
    image_reference character varying(512) NOT NULL,
    kubernetes_visibility_profile character varying(32) DEFAULT 'disabled'::character varying NOT NULL,
    kubernetes_visibility_pod_logs boolean DEFAULT false NOT NULL,
    kubernetes_visibility_rediscovery_state character varying(32) DEFAULT 'ready'::character varying NOT NULL,
    kubernetes_visibility_candidate_digest character varying(64) DEFAULT ''::character varying NOT NULL,
    CONSTRAINT charlie_connection_artifact_expiry CHECK ((artifact_credential_expires_at > created_at)),
    CONSTRAINT charlie_connection_central_https CHECK (((central_url)::text ~ '^https://[^/]+'::text)),
    CONSTRAINT charlie_connection_certificate_expiry CHECK ((certificate_expires_at > created_at)),
    CONSTRAINT charlie_connection_chart_reference_oci CHECK (((chart_reference)::text ~ '^oci://[^/]+/.+'::text)),
    CONSTRAINT charlie_connection_deployment_nonempty CHECK ((length(TRIM(BOTH FROM deployment_id)) > 0)),
    CONSTRAINT charlie_connection_emergency_actor CHECK (((NOT emergency_disabled) OR ((emergency_disabled_at IS NOT NULL) AND (emergency_disabled_by_id IS NOT NULL)))),
    CONSTRAINT charlie_connection_enrollment_expiry CHECK ((enrollment_credentials_expires_at > created_at)),
    CONSTRAINT charlie_connection_epoch_nonnegative CHECK ((fencing_epoch >= 0)),
    CONSTRAINT charlie_connection_health CHECK (((health_state)::text = ANY ((ARRAY['inactive'::character varying, 'installing'::character varying, 'ready'::character varying, 'degraded'::character varying, 'unavailable'::character varying, 'disconnected'::character varying, 'failed'::character varying])::text[]))),
    CONSTRAINT charlie_connection_image_reference_pinned CHECK (((image_reference)::text ~ '@sha256:[0-9a-f]{64}$'::text)),
    CONSTRAINT charlie_connection_kubernetes_visibility_disabled_content CHECK ((((kubernetes_visibility_profile)::text <> 'disabled'::text) OR (NOT kubernetes_visibility_pod_logs))),
    CONSTRAINT charlie_connection_kubernetes_visibility_profile CHECK (((kubernetes_visibility_profile)::text = ANY ((ARRAY['disabled'::character varying, 'product_namespace'::character varying, 'cluster_diagnostics'::character varying])::text[]))),
    CONSTRAINT charlie_connection_kubernetes_visibility_rediscovery CHECK (((((kubernetes_visibility_rediscovery_state)::text = ANY ((ARRAY['ready'::character varying, 'required'::character varying])::text[])) AND ((kubernetes_visibility_candidate_digest)::text = ''::text)) OR (((kubernetes_visibility_rediscovery_state)::text = 'review_required'::text) AND ((kubernetes_visibility_candidate_digest)::text ~ '^[a-f0-9]{64}$'::text)))),
    CONSTRAINT charlie_connection_mode_revision_nonnegative CHECK ((verified_mode_revision >= 0)),
    CONSTRAINT charlie_connection_onboarding_state CHECK (((onboarding_state)::text = ANY ((ARRAY['validated'::character varying, 'secrets_pending'::character varying, 'secrets_written'::character varying, 'consumed'::character varying, 'active'::character varying, 'failed'::character varying])::text[]))),
    CONSTRAINT charlie_connection_package_expiry CHECK ((onboarding_package_expires_at > created_at)),
    CONSTRAINT charlie_connection_product_nonempty CHECK ((length(TRIM(BOTH FROM product_id)) > 0)),
    CONSTRAINT charlie_connection_product_slug_astronomer CHECK (((product_slug)::text = 'astronomer'::text)),
    CONSTRAINT charlie_connection_replica_count CHECK (((replica_count >= 2) AND (replica_count <= 20))),
    CONSTRAINT charlie_connection_requested_mode CHECK (((requested_mode)::text = ANY ((ARRAY['disabled'::character varying, 'read_only'::character varying, 'approval'::character varying, 'auto'::character varying])::text[]))),
    CONSTRAINT charlie_connection_route_nonempty CHECK ((length(TRIM(BOTH FROM route_id)) > 0)),
    CONSTRAINT charlie_connection_verified_mode CHECK (((verified_mode)::text = ANY ((ARRAY['disabled'::character varying, 'read_only'::character varying, 'approval'::character varying, 'auto'::character varying])::text[])))
);


--
-- Name: charlie_delegations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_delegations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    session_id uuid NOT NULL,
    authorization_hash character varying(128) NOT NULL,
    authorization_prefix character varying(16) NOT NULL,
    principal_type character varying(16) NOT NULL,
    principal_id uuid NOT NULL,
    issued_at timestamptz DEFAULT now() NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_delegation_expiry CHECK ((expires_at > issued_at)),
    CONSTRAINT charlie_delegation_principal CHECK (((principal_type)::text = ANY ((ARRAY['user'::character varying, 'service'::character varying])::text[])))
);


--
-- Name: charlie_finding_decisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_finding_decisions (
    request_id uuid NOT NULL,
    finding_id uuid NOT NULL,
    actor_ref character varying(44) NOT NULL,
    decision character varying(32) NOT NULL,
    result_status character varying(16) NOT NULL,
    result_workflow_state character varying(32) NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_finding_decision_actor_ref CHECK (((actor_ref)::text ~ '^productuser_[a-f0-9]{32}$'::text)),
    CONSTRAINT charlie_finding_decision_status CHECK (((result_status)::text = ANY ((ARRAY['open'::character varying, 'acknowledged'::character varying, 'dismissed'::character varying, 'resolved'::character varying, 'expired'::character varying])::text[]))),
    CONSTRAINT charlie_finding_decision_value CHECK (((decision)::text = ANY ((ARRAY['acknowledge'::character varying, 'start_remediation'::character varying, 'request_verification'::character varying, 'dismiss'::character varying, 'resolve'::character varying])::text[]))),
    CONSTRAINT charlie_finding_decision_workflow CHECK (((result_workflow_state)::text = ANY ((ARRAY['approval_pending'::character varying, 'manual_remediation_required'::character varying, 'remediation_in_progress'::character varying, 'verification_pending'::character varying, 'resolved'::character varying, 'rejected'::character varying, 'dismissed'::character varying, 'expired'::character varying])::text[])))
);


--
-- Name: charlie_finding_projection_cursors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_finding_projection_cursors (
    connection_id uuid NOT NULL,
    sequence bigint DEFAULT 0 NOT NULL,
    last_error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_finding_projection_cursors_sequence_check CHECK ((sequence >= 0))
);


--
-- Name: charlie_finding_resources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_finding_resources (
    finding_id uuid NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(255) NOT NULL,
    required_verb character varying(32) DEFAULT 'read'::character varying NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_finding_resource_nonempty CHECK (((length(TRIM(BOTH FROM resource_type)) > 0) AND (length(TRIM(BOTH FROM resource_id)) > 0) AND (length(TRIM(BOTH FROM required_verb)) > 0)))
);


--
-- Name: charlie_findings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_findings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    charlie_finding_id character varying(128) NOT NULL,
    approval_id character varying(128),
    session_id uuid,
    source character varying(32) NOT NULL,
    severity character varying(16) NOT NULL,
    status character varying(16) DEFAULT 'open'::character varying NOT NULL,
    effective_mode character varying(16) NOT NULL,
    execution_block_code character varying(64) DEFAULT ''::character varying NOT NULL,
    dedupe_fingerprint character varying(128) NOT NULL,
    title character varying(256) NOT NULL,
    summary character varying(2048) NOT NULL,
    recommended_action_label character varying(256) DEFAULT ''::character varying NOT NULL,
    risk_impact character varying(1024) DEFAULT ''::character varying NOT NULL,
    verification_summary character varying(1024) DEFAULT ''::character varying NOT NULL,
    repeat_count integer DEFAULT 1 NOT NULL,
    expires_at timestamptz,
    acknowledged_by_id uuid,
    acknowledged_at timestamptz,
    dismissed_by_id uuid,
    dismissed_at timestamptz,
    resolved_by_id uuid,
    resolved_at timestamptz,
    alert_event_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    workflow_state character varying(32) DEFAULT 'manual_remediation_required'::character varying NOT NULL,
    CONSTRAINT charlie_finding_approval_mode CHECK (((approval_id IS NULL) OR ((effective_mode)::text = 'approval'::text))),
    CONSTRAINT charlie_finding_mode CHECK (((effective_mode)::text = ANY ((ARRAY['read_only'::character varying, 'approval'::character varying, 'auto'::character varying])::text[]))),
    CONSTRAINT charlie_finding_repeat CHECK ((repeat_count > 0)),
    CONSTRAINT charlie_finding_severity CHECK (((severity)::text = ANY ((ARRAY['info'::character varying, 'low'::character varying, 'medium'::character varying, 'warning'::character varying, 'high'::character varying, 'critical'::character varying])::text[]))),
    CONSTRAINT charlie_finding_source CHECK (((source)::text = ANY ((ARRAY['user'::character varying, 'trigger'::character varying, 'system'::character varying])::text[]))),
    CONSTRAINT charlie_finding_status CHECK (((status)::text = ANY ((ARRAY['open'::character varying, 'acknowledged'::character varying, 'dismissed'::character varying, 'resolved'::character varying, 'expired'::character varying])::text[]))),
    CONSTRAINT charlie_finding_workflow_consistency CHECK (((((workflow_state)::text = 'approval_pending'::text) AND ((status)::text = ANY ((ARRAY['open'::character varying, 'acknowledged'::character varying])::text[])) AND ((execution_block_code)::text = 'approval_required'::text) AND (approval_id IS NOT NULL)) OR (((workflow_state)::text = 'manual_remediation_required'::text) AND ((status)::text = ANY ((ARRAY['open'::character varying, 'acknowledged'::character varying])::text[]))) OR (((workflow_state)::text = ANY ((ARRAY['remediation_in_progress'::character varying, 'verification_pending'::character varying])::text[])) AND ((status)::text = 'acknowledged'::text)) OR (((workflow_state)::text = 'resolved'::text) AND ((status)::text = 'resolved'::text)) OR (((workflow_state)::text = 'rejected'::text) AND ((status)::text = 'resolved'::text) AND ((execution_block_code)::text = 'approval_rejected'::text)) OR (((workflow_state)::text = 'dismissed'::text) AND ((status)::text = 'dismissed'::text)) OR (((workflow_state)::text = 'expired'::text) AND ((status)::text = 'expired'::text)))),
    CONSTRAINT charlie_finding_workflow_state CHECK (((workflow_state)::text = ANY ((ARRAY['approval_pending'::character varying, 'manual_remediation_required'::character varying, 'remediation_in_progress'::character varying, 'verification_pending'::character varying, 'resolved'::character varying, 'rejected'::character varying, 'dismissed'::character varying, 'expired'::character varying])::text[])))
);


--
-- Name: charlie_interactive_threads; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_interactive_threads (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    owner_user_id uuid NOT NULL,
    title character varying(256) DEFAULT ''::character varying NOT NULL,
    state character varying(32) NOT NULL,
    current_session_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    archived_at timestamptz,
    CONSTRAINT charlie_interactive_threads_state_chk CHECK (((state)::text = ANY ((ARRAY['active'::character varying, 'archived'::character varying])::text[])))
);


--
-- Name: charlie_session_resources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_session_resources (
    session_id uuid NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(255) NOT NULL,
    required_verb character varying(32) NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_session_resource_nonempty CHECK (((length(TRIM(BOTH FROM resource_type)) > 0) AND (length(TRIM(BOTH FROM resource_id)) > 0) AND (length(TRIM(BOTH FROM required_verb)) > 0)))
);


--
-- Name: charlie_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    charlie_session_id character varying(128) DEFAULT ''::character varying NOT NULL,
    client_session_id uuid NOT NULL,
    owner_user_id uuid,
    source character varying(16) NOT NULL,
    visibility character varying(16) NOT NULL,
    intent character varying(128) DEFAULT ''::character varying NOT NULL,
    resource_scope_summary character varying(512) DEFAULT ''::character varying NOT NULL,
    state character varying(24) DEFAULT 'active'::character varying NOT NULL,
    last_event_id character varying(128) DEFAULT ''::character varying NOT NULL,
    central_revision bigint DEFAULT 0 NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    completed_at timestamptz,
    thread_id uuid,
    CONSTRAINT charlie_session_owner CHECK (((((source)::text = 'user'::text) AND (owner_user_id IS NOT NULL) AND ((visibility)::text = 'private'::text)) OR (((source)::text = 'event'::text) AND ((visibility)::text = 'incident'::text)))),
    CONSTRAINT charlie_session_revision_nonnegative CHECK ((central_revision >= 0)),
    CONSTRAINT charlie_session_source CHECK (((source)::text = ANY ((ARRAY['user'::character varying, 'event'::character varying])::text[]))),
    CONSTRAINT charlie_session_state CHECK (((state)::text = ANY ((ARRAY['creating'::character varying, 'active'::character varying, 'waiting_approval'::character varying, 'completed'::character varying, 'aborted'::character varying, 'failed'::character varying])::text[]))),
    CONSTRAINT charlie_session_visibility CHECK (((visibility)::text = ANY ((ARRAY['private'::character varying, 'incident'::character varying])::text[])))
);


--
-- Name: charlie_thread_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_thread_sessions (
    thread_id uuid NOT NULL,
    session_id uuid NOT NULL,
    sequence integer NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_thread_sessions_sequence_positive CHECK ((sequence > 0))
);


--
-- Name: charlie_trigger_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_trigger_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rule_id uuid NOT NULL,
    retry_of_event_id uuid,
    source character varying(64) NOT NULL,
    event_type character varying(64) NOT NULL,
    resource_type character varying(64) NOT NULL,
    resource_id character varying(255) NOT NULL,
    fingerprint character varying(128) NOT NULL,
    summary_metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    state character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    session_id uuid,
    repeat_count integer DEFAULT 1 NOT NULL,
    first_occurred_at timestamptz NOT NULL,
    last_occurred_at timestamptz NOT NULL,
    origin_resource_ref character varying(255) DEFAULT ''::character varying NOT NULL,
    origin_event_ref character varying(255) DEFAULT ''::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamptz DEFAULT now() NOT NULL,
    last_error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    dead_lettered_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_trigger_event_attempt CHECK ((attempt_count >= 0)),
    CONSTRAINT charlie_trigger_event_repeat CHECK ((repeat_count > 0)),
    CONSTRAINT charlie_trigger_event_state CHECK (((state)::text = ANY ((ARRAY['pending'::character varying, 'dispatching'::character varying, 'dispatched'::character varying, 'retry'::character varying, 'dead'::character varying, 'completed'::character varying, 'suppressed'::character varying])::text[]))),
    CONSTRAINT charlie_trigger_event_time_order CHECK ((last_occurred_at >= first_occurred_at))
);


--
-- Name: charlie_trigger_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.charlie_trigger_rules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id uuid NOT NULL,
    name character varying(128) NOT NULL,
    rule_type character varying(64) NOT NULL,
    category character varying(64) NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    minimum_severity character varying(16) DEFAULT 'warning'::character varying NOT NULL,
    selectors jsonb DEFAULT '{}'::jsonb NOT NULL,
    thresholds jsonb DEFAULT '{}'::jsonb NOT NULL,
    window_seconds integer NOT NULL,
    cooldown_seconds integer NOT NULL,
    service_identity_id uuid NOT NULL,
    mode_ceiling character varying(16) DEFAULT 'read_only'::character varying NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT charlie_trigger_cooldown CHECK (((cooldown_seconds >= 0) AND (cooldown_seconds <= 604800))),
    CONSTRAINT charlie_trigger_mode CHECK (((mode_ceiling)::text = ANY ((ARRAY['read_only'::character varying, 'approval'::character varying, 'auto'::character varying])::text[]))),
    CONSTRAINT charlie_trigger_severity CHECK (((minimum_severity)::text = ANY ((ARRAY['info'::character varying, 'warning'::character varying, 'critical'::character varying])::text[]))),
    CONSTRAINT charlie_trigger_window CHECK (((window_seconds >= 1) AND (window_seconds <= 86400)))
);


--
-- Name: chart_co_installation; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chart_co_installation (
    chart_a_id uuid NOT NULL,
    chart_b_id uuid NOT NULL,
    weight integer DEFAULT 0 NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT chart_co_installation_check CHECK ((chart_a_id < chart_b_id))
);


--
-- Name: chart_rating_aggregates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chart_rating_aggregates (
    chart_id uuid NOT NULL,
    rating_count integer DEFAULT 0 NOT NULL,
    rating_sum integer DEFAULT 0 NOT NULL,
    avg_stars numeric(3,2) DEFAULT 0.00 NOT NULL,
    bayesian_score numeric(4,2) DEFAULT 0.00 NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: chart_ratings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chart_ratings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    chart_id uuid NOT NULL,
    installation_id uuid,
    user_id uuid NOT NULL,
    stars smallint NOT NULL,
    note character varying(280) DEFAULT ''::character varying NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT chart_ratings_stars_check CHECK (((stars >= 1) AND (stars <= 5)))
);


--
-- Name: cloud_credential_materializations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cloud_credential_materializations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    credential_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    namespace character varying(63) NOT NULL,
    secret_name character varying(253) NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    last_applied_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cloud_credentials; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cloud_credentials (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    name character varying(128) NOT NULL,
    provider character varying(32) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    data_encrypted text NOT NULL,
    target_refs jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_agent_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_agent_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    token character varying(128) NOT NULL,
    last_used_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    token_hash character varying(128) DEFAULT ''::character varying NOT NULL,
    revoked_at timestamptz,
    previous_token_hash text,
    rotation_pending_at timestamptz,
    last_rotated_at timestamptz,
    adopted_at timestamptz
);


--
-- Name: cluster_condition_remediation_attempts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_condition_remediation_attempts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    condition_type character varying(64) NOT NULL,
    action character varying(64) NOT NULL,
    outcome character varying(16) NOT NULL,
    error text DEFAULT ''::text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    attempted_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT cluster_condition_remediation_attempts_outcome_check CHECK (((outcome)::text = ANY ((ARRAY['success'::character varying, 'failed'::character varying, 'skipped'::character varying])::text[])))
);


--
-- Name: cluster_conditions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_conditions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    type character varying(64) NOT NULL,
    status character varying(8) NOT NULL,
    reason character varying(64) DEFAULT ''::character varying NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    last_transition_time timestamptz DEFAULT now() NOT NULL,
    last_probe_time timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_decommissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_decommissions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    phases jsonb DEFAULT '{}'::jsonb NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    requested_by_id uuid,
    cluster_name text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    force boolean DEFAULT false NOT NULL
);


--
-- Name: cluster_deployment_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_deployment_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    deployment_id uuid NOT NULL,
    rollout_id uuid,
    event_type character varying(48) NOT NULL,
    from_phase character varying(24) DEFAULT ''::character varying NOT NULL,
    to_phase character varying(24) DEFAULT ''::character varying NOT NULL,
    generation bigint DEFAULT 0 NOT NULL,
    spec_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    reason_code character varying(96) DEFAULT ''::character varying NOT NULL,
    message character varying(4096) DEFAULT ''::character varying NOT NULL,
    observed_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT cluster_deployment_event_generation_valid CHECK ((generation >= 0))
);


--
-- Name: TABLE cluster_deployment_events; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.cluster_deployment_events IS 'Coalesced state transitions and warnings only; never raw Flux objects, values, manifests, or credentials.';


--
-- Name: cluster_deployments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_deployments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    current_rollout_id uuid,
    desired_bundle_version_id uuid,
    previous_bundle_version_id uuid,
    desired_generation bigint DEFAULT 0 NOT NULL,
    observed_generation bigint DEFAULT 0 NOT NULL,
    desired_spec_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    observed_spec_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    desired_revision character varying(256) DEFAULT ''::character varying NOT NULL,
    observed_revision character varying(256) DEFAULT ''::character varying NOT NULL,
    action character varying(16) DEFAULT 'apply'::character varying NOT NULL,
    phase character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    conditions jsonb DEFAULT '[]'::jsonb NOT NULL,
    source_kind character varying(32) DEFAULT ''::character varying NOT NULL,
    source_name character varying(128) DEFAULT ''::character varying NOT NULL,
    reconciler_kind character varying(32) DEFAULT ''::character varying NOT NULL,
    reconciler_name character varying(128) DEFAULT ''::character varying NOT NULL,
    inventory jsonb DEFAULT '{}'::jsonb NOT NULL,
    agent_session_id character varying(128) DEFAULT ''::character varying NOT NULL,
    agent_sequence bigint DEFAULT 0 NOT NULL,
    last_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    last_message character varying(4096) DEFAULT ''::character varying NOT NULL,
    last_observed_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT cluster_deployment_action_valid CHECK (((action)::text = ANY ((ARRAY['apply'::character varying, 'suspend'::character varying, 'delete'::character varying])::text[]))),
    CONSTRAINT cluster_deployment_desired_digest_valid CHECK ((((desired_spec_digest)::text = ''::text) OR ((desired_spec_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text))),
    CONSTRAINT cluster_deployment_generation_valid CHECK (((desired_generation >= 0) AND (observed_generation >= 0) AND (agent_sequence >= 0))),
    CONSTRAINT cluster_deployment_observed_digest_valid CHECK ((((observed_spec_digest)::text = ''::text) OR ((observed_spec_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text))),
    CONSTRAINT cluster_deployment_phase_valid CHECK (((phase)::text = ANY ((ARRAY['pending'::character varying, 'blocked'::character varying, 'applying'::character varying, 'ready'::character varying, 'degraded'::character varying, 'failed'::character varying, 'suspended'::character varying, 'deleting'::character varying, 'removed'::character varying, 'unknown'::character varying])::text[])))
);


--
-- Name: cluster_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_groups (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    slug character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    parent_id uuid,
    color character varying(16) DEFAULT '#6b7280'::character varying NOT NULL,
    icon character varying(64) DEFAULT 'folder'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_health_statuses; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_health_statuses (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    cpu_usage_percent double precision DEFAULT 0 NOT NULL,
    memory_usage_percent double precision DEFAULT 0 NOT NULL,
    pod_count integer DEFAULT 0 NOT NULL,
    node_count integer DEFAULT 0 NOT NULL,
    conditions jsonb DEFAULT '[]'::jsonb NOT NULL,
    last_check timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    last_metrics_at timestamptz
);


--
-- Name: cluster_monitoring_configs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_monitoring_configs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    backend_id uuid NOT NULL,
    cluster_label character varying(128) DEFAULT 'cluster_id'::character varying NOT NULL,
    cluster_label_value character varying(255) DEFAULT ''::character varying NOT NULL,
    scrape_interval_seconds integer DEFAULT 30 NOT NULL,
    retention character varying(32) DEFAULT '15d'::character varying NOT NULL,
    stack_namespace character varying(128) DEFAULT 'monitoring'::character varying NOT NULL,
    prometheus_release_name character varying(128) DEFAULT 'prometheus'::character varying NOT NULL,
    thanos_sidecar_enabled boolean DEFAULT true NOT NULL,
    status character varying(32) DEFAULT 'pending'::character varying NOT NULL,
    last_healthy_at timestamptz,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    storage_config_id uuid,
    object_storage_secret_name character varying(128) DEFAULT ''::character varying NOT NULL,
    storage_class character varying(128) DEFAULT ''::character varying NOT NULL,
    storage_size character varying(64) DEFAULT ''::character varying NOT NULL,
    last_applied_spec_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    last_observed_status character varying(64) DEFAULT ''::character varying NOT NULL,
    last_observed_revision integer DEFAULT 0 NOT NULL,
    last_observed_at timestamptz,
    last_drift_detected_at timestamptz
);


--
-- Name: cluster_registration_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_registration_policies (
    cluster_id uuid NOT NULL,
    token_rotation_days integer DEFAULT 0 NOT NULL,
    source_template_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_registration_steps; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_registration_steps (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    step_name character varying(64) NOT NULL,
    label character varying(255) NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    progress_pct integer DEFAULT 0 NOT NULL,
    detail_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    step_order integer DEFAULT 0 NOT NULL,
    CONSTRAINT step_status_valid CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, 'running'::character varying, 'success'::character varying, 'failed'::character varying, 'skipped'::character varying])::text[])))
);


--
-- Name: cluster_registration_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_registration_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    token character varying(128) NOT NULL,
    expires_at timestamptz NOT NULL,
    is_used boolean DEFAULT false NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    token_hash character varying(64) DEFAULT ''::character varying NOT NULL
);


--
-- Name: cluster_registry_configs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_registry_configs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    private_registry_url character varying(500) DEFAULT ''::character varying NOT NULL,
    registry_username character varying(255) DEFAULT ''::character varying NOT NULL,
    registry_password character varying(255) DEFAULT ''::character varying NOT NULL,
    insecure boolean DEFAULT false NOT NULL,
    ca_bundle text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    inject_default_sa boolean DEFAULT true NOT NULL,
    secret_name character varying(128) DEFAULT ''::character varying NOT NULL,
    last_applied_at timestamptz,
    last_apply_error text DEFAULT ''::text NOT NULL,
    registry_password_encrypted text DEFAULT ''::text NOT NULL
);


--
-- Name: cluster_restores; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_restores (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    snapshot_id uuid NOT NULL,
    target_cluster_id uuid NOT NULL,
    velero_name character varying(253) NOT NULL,
    velero_namespace character varying(63) DEFAULT 'velero'::character varying NOT NULL,
    spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    phase character varying(32) DEFAULT 'New'::character varying NOT NULL,
    start_time timestamptz,
    completion_time timestamptz,
    warnings_count integer DEFAULT 0 NOT NULL,
    errors_count integer DEFAULT 0 NOT NULL,
    last_poll_at timestamptz,
    last_poll_error text DEFAULT ''::text NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_role_bindings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_role_bindings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid,
    "group" character varying(255) DEFAULT ''::character varying NOT NULL,
    role_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    source character varying(32) DEFAULT 'manual'::character varying NOT NULL,
    namespace character varying(253) DEFAULT ''::character varying NOT NULL,
    group_sync_connector_id uuid
);


--
-- Name: cluster_roles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_roles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    permissions jsonb DEFAULT '{}'::jsonb NOT NULL,
    rules jsonb DEFAULT '[]'::jsonb NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    display_name character varying(255) DEFAULT ''::character varying NOT NULL
);


--
-- Name: cluster_security_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_security_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    template_id uuid NOT NULL,
    applied_at timestamptz,
    sync_status character varying(20) DEFAULT 'pending'::character varying NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_service_mesh; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_service_mesh (
    cluster_id uuid NOT NULL,
    detected_mesh character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    detected_version character varying(64) DEFAULT ''::character varying NOT NULL,
    control_plane_namespace character varying(253) DEFAULT ''::character varying NOT NULL,
    gateway_count integer DEFAULT 0 NOT NULL,
    virtual_service_count integer DEFAULT 0 NOT NULL,
    destination_rule_count integer DEFAULT 0 NOT NULL,
    peer_authentication_count integer DEFAULT 0 NOT NULL,
    service_profile_count integer DEFAULT 0 NOT NULL,
    server_auth_count integer DEFAULT 0 NOT NULL,
    mtls_coverage_pct integer DEFAULT 0 NOT NULL,
    last_detected_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT detected_mesh_valid CHECK (((detected_mesh)::text = ANY ((ARRAY['istio'::character varying, 'linkerd'::character varying, 'kuma'::character varying, 'cilium'::character varying, 'none'::character varying, 'unknown'::character varying])::text[])))
);


--
-- Name: cluster_snapshot_schedules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_snapshot_schedules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    name character varying(128) NOT NULL,
    cron_schedule character varying(64) NOT NULL,
    spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_run_at timestamptz,
    last_run_status character varying(32) DEFAULT ''::character varying NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_snapshots (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    velero_name character varying(253) NOT NULL,
    velero_namespace character varying(63) DEFAULT 'velero'::character varying NOT NULL,
    source character varying(32) DEFAULT 'manual'::character varying NOT NULL,
    spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    phase character varying(32) DEFAULT 'New'::character varying NOT NULL,
    start_time timestamptz,
    completion_time timestamptz,
    expires_at timestamptz,
    warnings_count integer DEFAULT 0 NOT NULL,
    errors_count integer DEFAULT 0 NOT NULL,
    last_poll_at timestamptz,
    last_poll_error text DEFAULT ''::text NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_template_applications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_template_applications (
    cluster_id uuid NOT NULL,
    template_id uuid NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    spec_snapshot jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    applied_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_templates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_templates (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: cluster_tools; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cluster_tools (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug character varying(50) NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    icon character varying(64) DEFAULT ''::character varying NOT NULL,
    category character varying(20) NOT NULL,
    charts jsonb DEFAULT '[]'::jsonb NOT NULL,
    version_constraint character varying(64) DEFAULT ''::character varying NOT NULL,
    default_namespace character varying(128) NOT NULL,
    is_builtin boolean DEFAULT true NOT NULL,
    is_enabled boolean DEFAULT true NOT NULL,
    helm_chart_id uuid,
    presets jsonb DEFAULT '{}'::jsonb NOT NULL,
    service_name character varying(128) DEFAULT ''::character varying NOT NULL,
    service_port integer,
    service_path character varying(128) DEFAULT '/'::character varying NOT NULL,
    sub_services jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: clusters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.clusters (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    display_name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    api_server_url character varying(512) DEFAULT ''::character varying NOT NULL,
    ca_certificate text DEFAULT ''::text NOT NULL,
    environment character varying(16) DEFAULT 'development'::character varying NOT NULL,
    region character varying(64) DEFAULT ''::character varying NOT NULL,
    provider character varying(16) DEFAULT 'other'::character varying NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    annotations jsonb DEFAULT '{}'::jsonb NOT NULL,
    distribution character varying(32) DEFAULT ''::character varying NOT NULL,
    agent_version character varying(32) DEFAULT ''::character varying NOT NULL,
    last_heartbeat timestamptz,
    kubernetes_version character varying(32) DEFAULT ''::character varying NOT NULL,
    node_count integer DEFAULT 0 NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    is_local boolean DEFAULT false NOT NULL,
    decommissioned_at timestamptz,
    cluster_uid character varying(64) DEFAULT ''::character varying NOT NULL,
    group_id uuid,
    registration_phase character varying(32) DEFAULT 'created'::character varying NOT NULL,
    registration_started_at timestamptz,
    registration_completed_at timestamptz,
    install_baseline boolean,
    managed_by character varying(16) DEFAULT 'api'::character varying NOT NULL,
    external_ref_api_version character varying(128) DEFAULT ''::character varying NOT NULL,
    external_ref_kind character varying(64) DEFAULT ''::character varying NOT NULL,
    external_ref_namespace character varying(253) DEFAULT ''::character varying NOT NULL,
    external_ref_name character varying(253) DEFAULT ''::character varying NOT NULL,
    observed_generation bigint DEFAULT 0 NOT NULL,
    CONSTRAINT clusters_external_ref_all_or_none CHECK (((((external_ref_api_version)::text = ''::text) AND ((external_ref_kind)::text = ''::text) AND ((external_ref_namespace)::text = ''::text) AND ((external_ref_name)::text = ''::text)) OR (((external_ref_api_version)::text <> ''::text) AND ((external_ref_kind)::text <> ''::text) AND ((external_ref_namespace)::text <> ''::text) AND ((external_ref_name)::text <> ''::text)))),
    CONSTRAINT clusters_managed_by_valid CHECK (((managed_by)::text = ANY ((ARRAY['ui'::character varying, 'api'::character varying, 'crd'::character varying, 'system'::character varying, 'flux'::character varying])::text[]))),
    CONSTRAINT registration_phase_valid CHECK (((registration_phase)::text = ANY ((ARRAY['created'::character varying, 'awaiting_agent'::character varying, 'connected'::character varying, 'provisioning'::character varying, 'ready'::character varying, 'failed'::character varying])::text[])))
);


--
-- Name: compliance_baseline_applications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.compliance_baseline_applications (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    baseline_id uuid NOT NULL,
    previous_state jsonb NOT NULL,
    applied_by uuid,
    applied_at timestamptz DEFAULT now() NOT NULL,
    status character varying(16) DEFAULT 'applied'::character varying NOT NULL,
    reverted_at timestamptz,
    reverted_by uuid,
    notes text DEFAULT ''::text NOT NULL,
    CONSTRAINT app_status_valid CHECK (((status)::text = ANY ((ARRAY['applied'::character varying, 'reverted'::character varying])::text[])))
);


--
-- Name: compliance_baselines; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.compliance_baselines (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug character varying(64) NOT NULL,
    name character varying(128) NOT NULL,
    description text NOT NULL,
    version character varying(32) DEFAULT '1.0'::character varying NOT NULL,
    spec jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: component_bundle_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.component_bundle_versions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    bundle_id uuid NOT NULL,
    source_id uuid NOT NULL,
    version character varying(128) NOT NULL,
    renderer character varying(16) NOT NULL,
    scope character varying(16) DEFAULT 'namespace'::character varying NOT NULL,
    requested_revision character varying(256) NOT NULL,
    resolved_revision character varying(256) DEFAULT ''::character varying NOT NULL,
    artifact_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    source_spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    renderer_spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    reconciliation_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    health_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    requirements jsonb DEFAULT '{}'::jsonb NOT NULL,
    dependency_bundle_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    spec_digest character varying(80) NOT NULL,
    verification_status character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    verification_identity character varying(512) DEFAULT ''::character varying NOT NULL,
    state character varying(24) DEFAULT 'resolving'::character varying NOT NULL,
    last_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT component_bundle_renderer_valid CHECK (((renderer)::text = ANY ((ARRAY['kustomize'::character varying, 'helm'::character varying])::text[]))),
    CONSTRAINT component_bundle_resolved_pair_valid CHECK (((((state)::text = 'ready'::text) AND ((resolved_revision)::text <> ''::text) AND ((artifact_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)) OR ((state)::text <> 'ready'::text))),
    CONSTRAINT component_bundle_scope_valid CHECK (((scope)::text = ANY ((ARRAY['namespace'::character varying, 'platform'::character varying])::text[]))),
    CONSTRAINT component_bundle_spec_digest_valid CHECK (((spec_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)),
    CONSTRAINT component_bundle_state_valid CHECK (((state)::text = ANY ((ARRAY['resolving'::character varying, 'ready'::character varying, 'failed'::character varying, 'revoked'::character varying])::text[]))),
    CONSTRAINT component_bundle_verification_valid CHECK (((verification_status)::text = ANY ((ARRAY['pending'::character varying, 'verified'::character varying, 'failed'::character varying, 'unsigned'::character varying])::text[])))
);


--
-- Name: COLUMN component_bundle_versions.spec_digest; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.component_bundle_versions.spec_digest IS 'Canonical digest over credential-free immutable delivery intent.';


--
-- Name: component_bundles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.component_bundles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    created_by uuid,
    updated_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: control_plane_alerts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.control_plane_alerts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    controller character varying(32) NOT NULL,
    condition_type character varying(32) NOT NULL,
    status character varying(16) DEFAULT 'active'::character varying NOT NULL,
    message text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    fired_at timestamptz DEFAULT now() NOT NULL,
    resolved_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    acknowledged_by_id uuid,
    acknowledged_at timestamptz
);


--
-- Name: control_plane_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.control_plane_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(64) DEFAULT 'default'::character varying NOT NULL,
    monitoring_queue_depth_threshold integer DEFAULT 10 NOT NULL,
    delivery_queue_depth_threshold integer DEFAULT 10 NOT NULL,
    tools_queue_depth_threshold integer DEFAULT 10 NOT NULL,
    catalog_queue_depth_threshold integer DEFAULT 10 NOT NULL,
    monitoring_stale_running_threshold integer DEFAULT 1 NOT NULL,
    delivery_stale_running_threshold integer DEFAULT 1 NOT NULL,
    tools_stale_running_threshold integer DEFAULT 1 NOT NULL,
    catalog_stale_running_threshold integer DEFAULT 1 NOT NULL,
    monitoring_recent_failure_threshold integer DEFAULT 3 NOT NULL,
    delivery_recent_failure_threshold integer DEFAULT 3 NOT NULL,
    tools_recent_failure_threshold integer DEFAULT 3 NOT NULL,
    catalog_recent_failure_threshold integer DEFAULT 3 NOT NULL,
    recent_failure_window_minutes integer DEFAULT 30 NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: control_plane_silences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.control_plane_silences (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    controller character varying(32) NOT NULL,
    condition_type character varying(32) DEFAULT ''::character varying NOT NULL,
    reason text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: control_plane_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.control_plane_snapshots (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    name character varying(253) NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    location character varying(16) DEFAULT 'local'::character varying NOT NULL,
    size_bytes bigint,
    requested_by_id uuid,
    error text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    completed_at timestamptz
);


--
-- Name: dashboard_widgets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.dashboard_widgets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    widget_type character varying(32) NOT NULL,
    spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    scope character varying(16) DEFAULT 'global'::character varying NOT NULL,
    scope_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    grid_x integer DEFAULT 0 NOT NULL,
    grid_y integer DEFAULT 0 NOT NULL,
    grid_w integer DEFAULT 4 NOT NULL,
    grid_h integer DEFAULT 2 NOT NULL,
    refresh_seconds integer DEFAULT 60 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT scope_valid CHECK (((scope)::text = ANY ((ARRAY['global'::character varying, 'cluster'::character varying, 'project'::character varying])::text[]))),
    CONSTRAINT widget_type_valid CHECK (((widget_type)::text = ANY ((ARRAY['grafana_panel'::character varying, 'prom_sparkline'::character varying, 'prom_stat'::character varying, 'url_iframe'::character varying])::text[])))
);


--
-- Name: deferred_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.deferred_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    window_id uuid NOT NULL,
    operation_type character varying(64) NOT NULL,
    operation_spec jsonb DEFAULT '{}'::jsonb NOT NULL,
    target_cluster_id uuid,
    target_project_id uuid,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    deferred_until timestamptz,
    expires_at timestamptz,
    requested_by uuid,
    last_error text DEFAULT ''::text NOT NULL,
    dispatched_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: delivery_assignment_receipts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_assignment_receipts (
    cluster_id uuid NOT NULL,
    desired_snapshot_generation bigint DEFAULT 0 NOT NULL,
    desired_content_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    desired_snapshot_etag character varying(80) DEFAULT ''::character varying NOT NULL,
    acknowledged_snapshot_generation bigint DEFAULT 0 NOT NULL,
    acknowledged_snapshot_etag character varying(80) DEFAULT ''::character varying NOT NULL,
    credential_content_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    credential_epoch bigint DEFAULT 0 NOT NULL,
    agent_session_id character varying(128) DEFAULT ''::character varying NOT NULL,
    agent_sequence bigint DEFAULT 0 NOT NULL,
    last_protocol_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    acknowledged_at timestamptz,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_receipt_digest_valid CHECK (((((desired_content_digest)::text = ''::text) OR ((desired_content_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)) AND (((desired_snapshot_etag)::text = ''::text) OR ((desired_snapshot_etag)::text ~ '^sha256:[0-9a-f]{64}$'::text)) AND (((acknowledged_snapshot_etag)::text = ''::text) OR ((acknowledged_snapshot_etag)::text ~ '^sha256:[0-9a-f]{64}$'::text)) AND (((credential_content_digest)::text = ''::text) OR ((credential_content_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)))),
    CONSTRAINT delivery_receipt_numbers_valid CHECK (((desired_snapshot_generation >= 0) AND (acknowledged_snapshot_generation >= 0) AND (credential_epoch >= 0) AND (agent_sequence >= 0)))
);


--
-- Name: delivery_controller_inventory; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_controller_inventory (
    cluster_id uuid NOT NULL,
    agent_version character varying(64) DEFAULT ''::character varying NOT NULL,
    flux_version character varying(64) DEFAULT ''::character varying NOT NULL,
    components jsonb DEFAULT '{}'::jsonb NOT NULL,
    api_versions jsonb DEFAULT '[]'::jsonb NOT NULL,
    distribution_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    kubernetes_version character varying(64) DEFAULT ''::character varying NOT NULL,
    ready boolean DEFAULT false NOT NULL,
    compatibility_status character varying(24) DEFAULT 'unknown'::character varying NOT NULL,
    error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    observed_at timestamptz,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_controller_compatibility_valid CHECK (((compatibility_status)::text = ANY ((ARRAY['unknown'::character varying, 'compatible'::character varying, 'incompatible'::character varying, 'upgrade_required'::character varying, 'degraded'::character varying])::text[])))
);


--
-- Name: delivery_rollout_approvals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_rollout_approvals (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rollout_id uuid NOT NULL,
    cohort integer DEFAULT '-1'::integer NOT NULL,
    binding_digest character varying(80) NOT NULL,
    decision character varying(16) NOT NULL,
    decided_by uuid,
    decided_at timestamptz DEFAULT now() NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_rollout_approval_cohort_valid CHECK ((cohort >= '-1'::integer)),
    CONSTRAINT delivery_rollout_approval_decision_valid CHECK (((decision)::text = ANY ((ARRAY['approved'::character varying, 'rejected'::character varying])::text[]))),
    CONSTRAINT delivery_rollout_approval_digest_valid CHECK (((binding_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)),
    CONSTRAINT delivery_rollout_approval_expiry_valid CHECK ((expires_at > decided_at))
);


--
-- Name: delivery_rollout_clusters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_rollout_clusters (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rollout_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    cohort integer DEFAULT 0 NOT NULL,
    release_order integer DEFAULT 0 NOT NULL,
    previous_bundle_version_id uuid,
    desired_bundle_version_id uuid NOT NULL,
    desired_spec_digest character varying(80) NOT NULL,
    state character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    assignment_action character varying(16) DEFAULT 'apply'::character varying NOT NULL,
    attempt integer DEFAULT 0 NOT NULL,
    fence bigint DEFAULT 0 NOT NULL,
    released_at timestamptz,
    acknowledged_at timestamptz,
    ready_at timestamptz,
    completed_at timestamptz,
    deadline timestamptz,
    last_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_rollout_cluster_action_valid CHECK (((assignment_action)::text = ANY ((ARRAY['apply'::character varying, 'rollback'::character varying])::text[]))),
    CONSTRAINT delivery_rollout_cluster_digest_valid CHECK (((desired_spec_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)),
    CONSTRAINT delivery_rollout_cluster_numbers_valid CHECK (((cohort >= 0) AND (release_order >= 0) AND (attempt >= 0) AND (fence >= 0))),
    CONSTRAINT delivery_rollout_cluster_state_valid CHECK (((state)::text = ANY ((ARRAY['pending'::character varying, 'released'::character varying, 'acknowledged'::character varying, 'reconciling'::character varying, 'ready'::character varying, 'blocked'::character varying, 'timed_out'::character varying, 'failed'::character varying, 'skipped'::character varying, 'rolling_back'::character varying, 'ready_previous'::character varying, 'rollback_failed'::character varying, 'aborted'::character varying])::text[])))
);


--
-- Name: delivery_rollout_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_rollout_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rollout_id uuid NOT NULL,
    cluster_id uuid,
    decision_digest character varying(80) NOT NULL,
    event_type character varying(48) NOT NULL,
    from_state character varying(24) DEFAULT ''::character varying NOT NULL,
    to_state character varying(24) DEFAULT ''::character varying NOT NULL,
    reason_code character varying(96) DEFAULT ''::character varying NOT NULL,
    fence bigint NOT NULL,
    occurred_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_rollout_event_digest_valid CHECK (((decision_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)),
    CONSTRAINT delivery_rollout_event_fence_valid CHECK ((fence >= 0))
);


--
-- Name: delivery_rollouts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_rollouts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_id uuid NOT NULL,
    target_generation bigint NOT NULL,
    from_bundle_version_id uuid,
    to_bundle_version_id uuid NOT NULL,
    placement_digest character varying(80) NOT NULL,
    placement_snapshot jsonb DEFAULT '{}'::jsonb NOT NULL,
    strategy jsonb DEFAULT '{}'::jsonb NOT NULL,
    strategy_digest character varying(80) NOT NULL,
    approval_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    request_digest character varying(80) NOT NULL,
    plan_digest character varying(80) NOT NULL,
    frozen_plan jsonb NOT NULL,
    state character varying(24) DEFAULT 'resolving'::character varying NOT NULL,
    fencing_generation bigint DEFAULT 1 NOT NULL,
    lease_owner character varying(253) DEFAULT ''::character varying NOT NULL,
    lease_expires_at timestamptz,
    last_decision_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    idempotency_key character varying(128) NOT NULL,
    total_clusters integer DEFAULT 0 NOT NULL,
    ready_clusters integer DEFAULT 0 NOT NULL,
    failed_clusters integer DEFAULT 0 NOT NULL,
    blocked_clusters integer DEFAULT 0 NOT NULL,
    released_clusters integer DEFAULT 0 NOT NULL,
    progress_deadline timestamptz,
    started_at timestamptz,
    completed_at timestamptz,
    last_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    initiated_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_rollout_counters_valid CHECK (((total_clusters >= 0) AND (ready_clusters >= 0) AND (failed_clusters >= 0) AND (blocked_clusters >= 0) AND (released_clusters >= 0) AND (((ready_clusters + failed_clusters) + blocked_clusters) <= total_clusters) AND (released_clusters <= total_clusters))),
    CONSTRAINT delivery_rollout_digests_valid CHECK ((((placement_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text) AND ((strategy_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text) AND ((request_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text) AND ((plan_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text) AND (((last_decision_digest)::text = ''::text) OR ((last_decision_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)))),
    CONSTRAINT delivery_rollout_generation_valid CHECK (((target_generation > 0) AND (fencing_generation > 0))),
    CONSTRAINT delivery_rollout_state_valid CHECK (((state)::text = ANY ((ARRAY['draft'::character varying, 'resolving'::character varying, 'awaiting_approval'::character varying, 'queued'::character varying, 'progressing'::character varying, 'paused'::character varying, 'rejected'::character varying, 'aborted'::character varying, 'succeeded'::character varying, 'failed'::character varying, 'rolling_back'::character varying, 'rolled_back'::character varying, 'rollback_failed'::character varying])::text[])))
);


--
-- Name: delivery_source_resolutions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_source_resolutions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    source_id uuid NOT NULL,
    bundle_version_id uuid,
    requested_revision character varying(256) NOT NULL,
    chart_name character varying(253) DEFAULT ''::character varying NOT NULL,
    resolved_revision character varying(256) DEFAULT ''::character varying NOT NULL,
    artifact_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    verification_status character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    verification_identity character varying(512) DEFAULT ''::character varying NOT NULL,
    status character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    resolution_attempt integer DEFAULT 0 NOT NULL,
    lease_owner character varying(128) DEFAULT ''::character varying NOT NULL,
    lease_expires_at timestamptz,
    fence bigint DEFAULT 0 NOT NULL,
    next_attempt_at timestamptz DEFAULT now() NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_resolution_claim_valid CHECK (((resolution_attempt >= 0) AND (fence >= 0))),
    CONSTRAINT delivery_resolution_status_valid CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, 'running'::character varying, 'succeeded'::character varying, 'failed'::character varying])::text[]))),
    CONSTRAINT delivery_resolution_verification_valid CHECK (((verification_status)::text = ANY ((ARRAY['pending'::character varying, 'verified'::character varying, 'failed'::character varying, 'unsigned'::character varying])::text[])))
);


--
-- Name: delivery_sources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_sources (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    source_type character varying(24) NOT NULL,
    url character varying(2048) NOT NULL,
    auth_mode character varying(24) DEFAULT 'none'::character varying NOT NULL,
    credential_encrypted text DEFAULT ''::text NOT NULL,
    credential_key_version integer DEFAULT 0 NOT NULL,
    credential_epoch bigint DEFAULT 0 NOT NULL,
    ca_bundle_encrypted text DEFAULT ''::text NOT NULL,
    proxy_ref character varying(128) DEFAULT ''::character varying NOT NULL,
    trust_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    status character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    last_resolved_at timestamptz,
    last_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    created_by uuid,
    updated_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_source_auth_valid CHECK (((auth_mode)::text = ANY ((ARRAY['none'::character varying, 'basic'::character varying, 'bearer'::character varying, 'ssh'::character varying, 'workload_identity'::character varying])::text[]))),
    CONSTRAINT delivery_source_credential_pair_valid CHECK (((((auth_mode)::text = 'none'::text) AND (credential_encrypted = ''::text) AND (credential_key_version = 0)) OR ((auth_mode)::text <> 'none'::text))),
    CONSTRAINT delivery_source_credential_version_valid CHECK (((credential_key_version >= 0) AND (credential_epoch >= 0))),
    CONSTRAINT delivery_source_status_valid CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, 'ready'::character varying, 'degraded'::character varying, 'revoked'::character varying])::text[]))),
    CONSTRAINT delivery_source_type_valid CHECK (((source_type)::text = ANY ((ARRAY['git'::character varying, 'oci_artifact'::character varying, 'helm_http'::character varying, 'helm_oci'::character varying])::text[])))
);


--
-- Name: COLUMN delivery_sources.credential_encrypted; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.delivery_sources.credential_encrypted IS 'Encrypted source credential envelope. Never select into list/detail/status/audit APIs.';


--
-- Name: COLUMN delivery_sources.ca_bundle_encrypted; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.delivery_sources.ca_bundle_encrypted IS 'Encrypted private CA bundle. Never expose through read APIs or agent status.';


--
-- Name: delivery_system_cluster_assignments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_system_cluster_assignments (
    cluster_id uuid NOT NULL,
    desired_release_id uuid NOT NULL,
    previous_release_id uuid,
    rollout_id uuid,
    generation bigint DEFAULT 1 NOT NULL,
    cohort integer DEFAULT 0 NOT NULL,
    release_order integer DEFAULT 0 NOT NULL,
    phase character varying(24) DEFAULT 'pending'::character varying NOT NULL,
    fence bigint DEFAULT 1 NOT NULL,
    released_at timestamptz,
    acknowledged_at timestamptz,
    ready_at timestamptz,
    deadline timestamptz,
    observed_distribution_digest character varying(80) DEFAULT ''::character varying NOT NULL,
    observed_agent_version character varying(64) DEFAULT ''::character varying NOT NULL,
    last_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    last_observed_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_system_assignment_digest_valid CHECK ((((observed_distribution_digest)::text = ''::text) OR ((observed_distribution_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text))),
    CONSTRAINT delivery_system_assignment_numbers_valid CHECK (((generation > 0) AND (cohort >= 0) AND (release_order >= 0) AND (fence > 0))),
    CONSTRAINT delivery_system_assignment_phase_valid CHECK (((phase)::text = ANY ((ARRAY['pending'::character varying, 'released'::character varying, 'applying'::character varying, 'ready'::character varying, 'failed'::character varying, 'rolling_back'::character varying, 'rolled_back'::character varying, 'rollback_failed'::character varying, 'aborted'::character varying])::text[])))
);


--
-- Name: delivery_system_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_system_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    rollout_id uuid,
    cluster_id uuid,
    release_id uuid NOT NULL,
    generation bigint NOT NULL,
    event_type character varying(48) NOT NULL,
    from_phase character varying(24) DEFAULT ''::character varying NOT NULL,
    to_phase character varying(24) DEFAULT ''::character varying NOT NULL,
    reason_code character varying(96) DEFAULT ''::character varying NOT NULL,
    decision_digest character varying(80) NOT NULL,
    occurred_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_system_event_digest_valid CHECK (((decision_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)),
    CONSTRAINT delivery_system_event_generation_valid CHECK ((generation > 0))
);


--
-- Name: delivery_system_releases; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_system_releases (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    release_sequence bigint NOT NULL,
    version character varying(64) NOT NULL,
    artifact_url character varying(2048) NOT NULL,
    artifact_digest character varying(80) NOT NULL,
    distribution_digest character varying(80) NOT NULL,
    agent_version character varying(64) NOT NULL,
    agent_image character varying(2048) NOT NULL,
    minimum_kubernetes character varying(32) NOT NULL,
    maximum_kubernetes character varying(32) NOT NULL,
    crd_storage_version character varying(64) NOT NULL,
    previous_storage_version character varying(64) DEFAULT ''::character varying NOT NULL,
    "interval" character varying(32) DEFAULT '5m'::character varying NOT NULL,
    timeout character varying(32) DEFAULT '15m'::character varying NOT NULL,
    verification_policy jsonb NOT NULL,
    registry_credential_encrypted text DEFAULT ''::text NOT NULL,
    credential_key_version integer DEFAULT 0 NOT NULL,
    credential_epoch bigint DEFAULT 0 NOT NULL,
    spec_digest character varying(80) NOT NULL,
    state character varying(16) DEFAULT 'draft'::character varying NOT NULL,
    released_at timestamptz,
    retired_at timestamptz,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_system_release_credential_valid CHECK (((credential_key_version >= 0) AND (credential_epoch >= 0))),
    CONSTRAINT delivery_system_release_digests_valid CHECK ((((artifact_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text) AND ((distribution_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text) AND ((spec_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text))),
    CONSTRAINT delivery_system_release_state_valid CHECK (((state)::text = ANY ((ARRAY['draft'::character varying, 'released'::character varying, 'retired'::character varying, 'revoked'::character varying])::text[])))
);


--
-- Name: COLUMN delivery_system_releases.registry_credential_encrypted; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.delivery_system_releases.registry_credential_encrypted IS 'Encrypted private-registry credential. Never expose through release, status, audit, or event APIs.';


--
-- Name: delivery_system_releases_release_sequence_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.delivery_system_releases ALTER COLUMN release_sequence ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.delivery_system_releases_release_sequence_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: delivery_system_rollouts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_system_rollouts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    release_id uuid NOT NULL,
    previous_release_id uuid,
    strategy jsonb NOT NULL,
    strategy_digest character varying(80) NOT NULL,
    state character varying(24) DEFAULT 'draft'::character varying NOT NULL,
    fencing_generation bigint DEFAULT 1 NOT NULL,
    lease_owner character varying(253) DEFAULT ''::character varying NOT NULL,
    lease_expires_at timestamptz,
    total_clusters integer DEFAULT 0 NOT NULL,
    ready_clusters integer DEFAULT 0 NOT NULL,
    failed_clusters integer DEFAULT 0 NOT NULL,
    released_clusters integer DEFAULT 0 NOT NULL,
    idempotency_key character varying(128) NOT NULL,
    progress_deadline timestamptz,
    started_at timestamptz,
    completed_at timestamptz,
    last_error_code character varying(96) DEFAULT ''::character varying NOT NULL,
    initiated_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_system_rollout_digest_valid CHECK (((strategy_digest)::text ~ '^sha256:[0-9a-f]{64}$'::text)),
    CONSTRAINT delivery_system_rollout_numbers_valid CHECK (((fencing_generation > 0) AND (total_clusters >= 0) AND (ready_clusters >= 0) AND (failed_clusters >= 0) AND (released_clusters >= 0) AND ((ready_clusters + failed_clusters) <= total_clusters) AND (released_clusters <= total_clusters))),
    CONSTRAINT delivery_system_rollout_state_valid CHECK (((state)::text = ANY ((ARRAY['draft'::character varying, 'awaiting_approval'::character varying, 'queued'::character varying, 'progressing'::character varying, 'paused'::character varying, 'aborted'::character varying, 'succeeded'::character varying, 'failed'::character varying, 'rolling_back'::character varying, 'rolled_back'::character varying, 'rollback_failed'::character varying])::text[])))
);


--
-- Name: delivery_targets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.delivery_targets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    bundle_version_id uuid NOT NULL,
    placement jsonb DEFAULT '{}'::jsonb NOT NULL,
    rollout_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    reconciliation_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    maintenance_window_policy jsonb DEFAULT '{}'::jsonb NOT NULL,
    suspended boolean DEFAULT false NOT NULL,
    generation bigint DEFAULT 1 NOT NULL,
    resource_version bigint DEFAULT 1 NOT NULL,
    deletion_state character varying(16) DEFAULT 'active'::character varying NOT NULL,
    created_by uuid,
    updated_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT delivery_target_deletion_valid CHECK (((deletion_state)::text = ANY ((ARRAY['active'::character varying, 'deleting'::character varying, 'deleted'::character varying])::text[]))),
    CONSTRAINT delivery_target_generation_valid CHECK (((generation > 0) AND (resource_version > 0)))
);


--
-- Name: dex_connectors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.dex_connectors (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(64) NOT NULL,
    type character varying(32) NOT NULL,
    display_name character varying(255) DEFAULT ''::character varying NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: dex_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.dex_settings (
    id uuid NOT NULL,
    issuer_url text DEFAULT ''::text NOT NULL,
    cluster_id uuid,
    namespace character varying(64) DEFAULT 'dex'::character varying NOT NULL,
    release_name character varying(64) DEFAULT 'dex'::character varying NOT NULL,
    configmap_name character varying(253) DEFAULT 'astronomer-dex-config'::character varying NOT NULL,
    public_clients jsonb DEFAULT '[]'::jsonb NOT NULL,
    expiry jsonb DEFAULT '{}'::jsonb NOT NULL,
    extra jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    runtime_secret_name character varying(253) DEFAULT 'astronomer-dex-runtime'::character varying NOT NULL,
    public_clients_encrypted text DEFAULT ''::text NOT NULL,
    public_clients_cutover_at timestamptz,
    chart_release_name character varying(64) DEFAULT ''::character varying NOT NULL,
    deployment_name character varying(253) DEFAULT ''::character varying NOT NULL,
    service_name character varying(253) DEFAULT ''::character varying NOT NULL,
    runtime_generation bigint DEFAULT 1 NOT NULL,
    runtime_applied_generation bigint DEFAULT 0 NOT NULL,
    runtime_phase text DEFAULT 'fresh'::text NOT NULL,
    runtime_staged_generation bigint DEFAULT 0 NOT NULL,
    saga_previous_sso_enabled boolean DEFAULT false NOT NULL,
    CONSTRAINT dex_settings_public_clients_cutover CHECK (((public_clients_cutover_at IS NULL) OR (public_clients = '[]'::jsonb))),
    CONSTRAINT dex_settings_runtime_applied_generation_valid CHECK (((runtime_applied_generation >= 0) AND (runtime_applied_generation <= runtime_generation))),
    CONSTRAINT dex_settings_runtime_generation_positive CHECK ((runtime_generation > 0)),
    CONSTRAINT dex_settings_runtime_phase_valid CHECK ((runtime_phase = ANY (ARRAY['fresh'::text, 'prepare'::text, 'cutover'::text]))),
    CONSTRAINT dex_settings_runtime_stage_order_valid CHECK (((runtime_staged_generation >= 0) AND (runtime_applied_generation <= runtime_staged_generation) AND (runtime_staged_generation <= runtime_generation)))
);


--
-- Name: COLUMN dex_settings.configmap_name; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.configmap_name IS 'Deprecated compatibility alias. Must never identify a ConfigMap containing Dex runtime configuration.';


--
-- Name: COLUMN dex_settings.public_clients; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.public_clients IS 'Compatibility copy dual-written until an explicit quiesced Fernet cutover; must be [] after public_clients_cutover_at.';


--
-- Name: COLUMN dex_settings.runtime_secret_name; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.runtime_secret_name IS 'Stable retained Secret mounted read-only by Dex; owned by the Dex runtime reconciler.';


--
-- Name: COLUMN dex_settings.public_clients_encrypted; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.public_clients_encrypted IS 'Fernet-encrypted canonical JSON array of Dex static clients.';


--
-- Name: COLUMN dex_settings.public_clients_cutover_at; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.public_clients_cutover_at IS 'Durable cutover marker; non-null means old writers are prohibited and public_clients is scrubbed.';


--
-- Name: COLUMN dex_settings.chart_release_name; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.chart_release_name IS 'Immutable Helm release identity for bundled Dex; empty for operator-managed Dex.';


--
-- Name: COLUMN dex_settings.deployment_name; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.deployment_name IS 'Exact Kubernetes Deployment name reconciled by DexHandler.';


--
-- Name: COLUMN dex_settings.service_name; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.service_name IS 'Exact Kubernetes Service name used for the verified health proxy.';


--
-- Name: COLUMN dex_settings.runtime_generation; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.runtime_generation IS 'Opaque monotonic generation for Dex runtime candidates; never content-derived.';


--
-- Name: COLUMN dex_settings.runtime_applied_generation; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.runtime_applied_generation IS 'Last generation conditionally verified in Secret, Deployment, and health checks.';


--
-- Name: COLUMN dex_settings.runtime_phase; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.runtime_phase IS 'fresh/cutover require Secret-mounted rollout+health; prepare stops after verified Secret staging.';


--
-- Name: COLUMN dex_settings.runtime_staged_generation; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.runtime_staged_generation IS 'Last generation verified in the owned runtime Secret, before Deployment activation.';


--
-- Name: COLUMN dex_settings.saga_previous_sso_enabled; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.dex_settings.saga_previous_sso_enabled IS 'SSO enabled snapshot captured atomically with generation staging; restoration is generation-CAS guarded.';


--
-- Name: email_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.email_messages (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    to_address character varying(255) NOT NULL,
    cc_address text DEFAULT ''::text NOT NULL,
    subject character varying(998) NOT NULL,
    template character varying(64) NOT NULL,
    body_text text DEFAULT ''::text NOT NULL,
    body_html text DEFAULT ''::text NOT NULL,
    user_id uuid,
    status character varying(16) DEFAULT 'queued'::character varying NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    sent_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: gitops_registered_clusters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.gitops_registered_clusters (
    cluster_id uuid NOT NULL,
    source_id uuid NOT NULL,
    repo_path text NOT NULL,
    last_yaml_sha character varying(64) DEFAULT ''::character varying NOT NULL,
    last_applied_at timestamptz DEFAULT now() NOT NULL,
    status character varying(16) DEFAULT 'active'::character varying NOT NULL,
    tombstoned_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT status_valid CHECK (((status)::text = ANY ((ARRAY['active'::character varying, 'tombstoned'::character varying])::text[])))
);


--
-- Name: gitops_registration_sources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.gitops_registration_sources (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    repo_url text NOT NULL,
    branch character varying(64) DEFAULT 'main'::character varying NOT NULL,
    path_prefix character varying(256) DEFAULT ''::character varying NOT NULL,
    auth_mode character varying(16) DEFAULT 'none'::character varying NOT NULL,
    auth_encrypted text DEFAULT ''::text NOT NULL,
    sync_mode character varying(16) DEFAULT 'interval'::character varying NOT NULL,
    sync_interval_seconds integer DEFAULT 60 NOT NULL,
    on_delete character varying(16) DEFAULT 'log'::character varying NOT NULL,
    last_synced_at timestamptz,
    last_synced_sha character varying(64) DEFAULT ''::character varying NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    allow_mass_decommission boolean DEFAULT false NOT NULL,
    CONSTRAINT auth_mode_valid CHECK (((auth_mode)::text = ANY ((ARRAY['none'::character varying, 'https_token'::character varying, 'ssh_key'::character varying])::text[]))),
    CONSTRAINT on_delete_valid CHECK (((on_delete)::text = ANY ((ARRAY['log'::character varying, 'tombstone'::character varying, 'decommission'::character varying])::text[]))),
    CONSTRAINT sync_mode_valid CHECK (((sync_mode)::text = ANY ((ARRAY['manual'::character varying, 'interval'::character varying])::text[])))
);


--
-- Name: global_role_bindings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.global_role_bindings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid,
    "group" character varying(255) DEFAULT ''::character varying NOT NULL,
    role_id uuid NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    source character varying(32) DEFAULT 'manual'::character varying NOT NULL,
    group_sync_connector_id uuid
);


--
-- Name: global_roles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.global_roles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    permissions jsonb DEFAULT '{}'::jsonb NOT NULL,
    rules jsonb DEFAULT '[]'::jsonb NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    display_name character varying(255) DEFAULT ''::character varying NOT NULL
);


--
-- Name: helm_chart_tags; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.helm_chart_tags (
    chart_id uuid NOT NULL,
    tag character varying(64) NOT NULL
);


--
-- Name: helm_chart_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.helm_chart_versions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    chart_id uuid NOT NULL,
    version character varying(100) NOT NULL,
    app_version character varying(100) DEFAULT ''::character varying NOT NULL,
    digest character varying(256) DEFAULT ''::character varying NOT NULL,
    urls jsonb DEFAULT '[]'::jsonb NOT NULL,
    values_schema jsonb DEFAULT '{}'::jsonb NOT NULL,
    default_values text DEFAULT ''::text NOT NULL,
    readme text DEFAULT ''::text NOT NULL,
    created_at_upstream timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    content_hydrated_at timestamptz
);


--
-- Name: helm_charts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.helm_charts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    repository_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    display_name character varying(255) DEFAULT ''::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    icon_url character varying(500) DEFAULT ''::character varying NOT NULL,
    home_url character varying(500) DEFAULT ''::character varying NOT NULL,
    category character varying(100) DEFAULT ''::character varying NOT NULL,
    keywords jsonb DEFAULT '[]'::jsonb NOT NULL,
    maintainers jsonb DEFAULT '[]'::jsonb NOT NULL,
    deprecated boolean DEFAULT false NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: helm_repositories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.helm_repositories (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    url character varying(500) NOT NULL,
    repo_type character varying(20) DEFAULT 'helm'::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    auth_type character varying(20) DEFAULT 'none'::character varying NOT NULL,
    auth_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_synced_at timestamptz,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    owner_project_id uuid,
    last_sync_error text DEFAULT ''::text NOT NULL,
    last_sync_attempted_at timestamptz,
    auth_config_encrypted text DEFAULT ''::text NOT NULL
);


--
-- Name: identity_group_mappings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.identity_group_mappings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connector_id uuid,
    group_name text NOT NULL,
    scope character varying(16) NOT NULL,
    role_id uuid NOT NULL,
    cluster_id uuid,
    project_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT scope_matches CHECK (((((scope)::text = 'global'::text) AND (cluster_id IS NULL) AND (project_id IS NULL)) OR (((scope)::text = 'cluster'::text) AND (cluster_id IS NOT NULL) AND (project_id IS NULL)) OR (((scope)::text = 'project'::text) AND (cluster_id IS NULL) AND (project_id IS NOT NULL)))),
    CONSTRAINT scope_valid CHECK (((scope)::text = ANY ((ARRAY['global'::character varying, 'cluster'::character varying, 'project'::character varying])::text[])))
);


--
-- Name: image_vulnerabilities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.image_vulnerabilities (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    report_id uuid NOT NULL,
    vulnerability_id character varying(64) NOT NULL,
    severity character varying(16) NOT NULL,
    pkg_name character varying(256) DEFAULT ''::character varying NOT NULL,
    installed_version character varying(128) DEFAULT ''::character varying NOT NULL,
    fixed_version character varying(128) DEFAULT ''::character varying NOT NULL,
    primary_link text DEFAULT ''::text NOT NULL,
    cvss_score numeric(4,1),
    title text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: image_vulnerability_report_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.image_vulnerability_report_snapshots (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    report_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    critical_count integer DEFAULT 0 NOT NULL,
    high_count integer DEFAULT 0 NOT NULL,
    medium_count integer DEFAULT 0 NOT NULL,
    low_count integer DEFAULT 0 NOT NULL,
    unknown_count integer DEFAULT 0 NOT NULL,
    scanned_at timestamptz NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: image_vulnerability_reports; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.image_vulnerability_reports (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    report_name character varying(256) NOT NULL,
    namespace character varying(253) NOT NULL,
    workload_kind character varying(64) DEFAULT ''::character varying NOT NULL,
    workload_name character varying(253) DEFAULT ''::character varying NOT NULL,
    container_name character varying(253) DEFAULT ''::character varying NOT NULL,
    image_registry character varying(253) DEFAULT ''::character varying NOT NULL,
    image_repo character varying(253) DEFAULT ''::character varying NOT NULL,
    image_tag character varying(128) DEFAULT ''::character varying NOT NULL,
    image_digest character varying(128) DEFAULT ''::character varying NOT NULL,
    scanner character varying(64) DEFAULT 'trivy'::character varying NOT NULL,
    scanner_version character varying(64) DEFAULT ''::character varying NOT NULL,
    critical_count integer DEFAULT 0 NOT NULL,
    high_count integer DEFAULT 0 NOT NULL,
    medium_count integer DEFAULT 0 NOT NULL,
    low_count integer DEFAULT 0 NOT NULL,
    unknown_count integer DEFAULT 0 NOT NULL,
    scanned_at timestamptz NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: installed_charts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.installed_charts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    chart_version_id uuid,
    release_name character varying(255) NOT NULL,
    namespace character varying(255) DEFAULT 'default'::character varying NOT NULL,
    values_override text DEFAULT ''::text NOT NULL,
    status character varying(50) DEFAULT 'pending_install'::character varying NOT NULL,
    revision integer DEFAULT 1 NOT NULL,
    notes text DEFAULT ''::text NOT NULL,
    installed_by_id uuid,
    request_id uuid,
    tool_slug character varying(50),
    preset_used character varying(20),
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    drift_detected boolean DEFAULT false NOT NULL,
    drift_detail text DEFAULT ''::text NOT NULL,
    drift_checked_at timestamptz
);


--
-- Name: jwt_revocations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.jwt_revocations (
    jti character varying(64) NOT NULL,
    user_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz DEFAULT now() NOT NULL,
    reason text DEFAULT 'user_logout'::text NOT NULL
);


--
-- Name: kubectl_session_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.kubectl_session_commands (
    id bigint NOT NULL,
    session_id uuid NOT NULL,
    command_at timestamptz DEFAULT now() NOT NULL,
    command_line text NOT NULL
);


--
-- Name: kubectl_session_commands_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.kubectl_session_commands_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: kubectl_session_commands_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.kubectl_session_commands_id_seq OWNED BY public.kubectl_session_commands.id;


--
-- Name: kubectl_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.kubectl_sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    sa_namespace character varying(253) DEFAULT 'kube-system'::character varying NOT NULL,
    sa_name character varying(253) NOT NULL,
    pod_namespace character varying(253) DEFAULT 'kube-system'::character varying NOT NULL,
    pod_name character varying(253) NOT NULL,
    status character varying(16) DEFAULT 'starting'::character varying NOT NULL,
    started_at timestamptz DEFAULT now() NOT NULL,
    last_input_at timestamptz DEFAULT now() NOT NULL,
    closed_at timestamptz,
    expires_at timestamptz DEFAULT (now() + '04:00:00'::interval) NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    client_ip inet,
    user_agent text DEFAULT ''::text NOT NULL,
    CONSTRAINT kubectl_status_valid CHECK (((status)::text = ANY ((ARRAY['starting'::character varying, 'active'::character varying, 'closed'::character varying, 'expired'::character varying, 'failed'::character varying])::text[])))
);


--
-- Name: logging_operation_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.logging_operation_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    operation_id uuid NOT NULL,
    level character varying(16) NOT NULL,
    stage character varying(64) NOT NULL,
    message text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: logging_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.logging_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_type character varying(32) NOT NULL,
    target_key character varying(255) NOT NULL,
    operation_type character varying(32) NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    status character varying(32) DEFAULT 'pending'::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: logging_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.logging_outputs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    output_type character varying(16) NOT NULL,
    configuration jsonb NOT NULL,
    cluster_id uuid,
    enabled boolean DEFAULT true NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: logging_pipeline_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.logging_pipeline_outputs (
    logging_pipeline_id uuid NOT NULL,
    logging_output_id uuid NOT NULL
);


--
-- Name: logging_pipelines; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.logging_pipelines (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    cluster_id uuid NOT NULL,
    namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    filters jsonb DEFAULT '[]'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: maintenance_windows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.maintenance_windows (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    mode character varying(16) DEFAULT 'blackout'::character varying NOT NULL,
    cron_open character varying(64) NOT NULL,
    duration_minutes integer DEFAULT 60 NOT NULL,
    timezone character varying(64) DEFAULT 'UTC'::character varying NOT NULL,
    cluster_selector jsonb DEFAULT '{}'::jsonb NOT NULL,
    operation_types jsonb DEFAULT '[]'::jsonb NOT NULL,
    on_block character varying(16) DEFAULT 'refuse'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT mode_valid CHECK (((mode)::text = ANY ((ARRAY['blackout'::character varying, 'permitted'::character varying])::text[]))),
    CONSTRAINT on_block_valid CHECK (((on_block)::text = ANY ((ARRAY['refuse'::character varying, 'defer'::character varying])::text[])))
);


--
-- Name: mirrored_gateway_classes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mirrored_gateway_classes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    name character varying(253) NOT NULL,
    controller_name character varying(253) DEFAULT ''::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    parameters jsonb DEFAULT '{}'::jsonb NOT NULL,
    accepted_status character varying(64) DEFAULT ''::character varying NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    annotations jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_seen_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: mirrored_ingress_classes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mirrored_ingress_classes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    name character varying(253) NOT NULL,
    controller character varying(253) DEFAULT ''::character varying NOT NULL,
    parameters jsonb DEFAULT '{}'::jsonb NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    annotations jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_seen_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: mirrored_limit_ranges; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mirrored_limit_ranges (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    namespace character varying(253) NOT NULL,
    name character varying(253) NOT NULL,
    limits jsonb DEFAULT '[]'::jsonb NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    annotations jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_seen_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: mirrored_network_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mirrored_network_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    namespace character varying(253) NOT NULL,
    name character varying(253) NOT NULL,
    pod_selector jsonb DEFAULT '{}'::jsonb NOT NULL,
    policy_types jsonb DEFAULT '[]'::jsonb NOT NULL,
    ingress_rules jsonb DEFAULT '[]'::jsonb NOT NULL,
    egress_rules jsonb DEFAULT '[]'::jsonb NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    annotations jsonb DEFAULT '{}'::jsonb NOT NULL,
    is_managed boolean DEFAULT false NOT NULL,
    last_seen_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: mirrored_resource_quotas; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mirrored_resource_quotas (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    namespace character varying(253) NOT NULL,
    name character varying(253) NOT NULL,
    hard jsonb DEFAULT '{}'::jsonb NOT NULL,
    used jsonb DEFAULT '{}'::jsonb NOT NULL,
    scopes jsonb DEFAULT '[]'::jsonb NOT NULL,
    labels jsonb DEFAULT '{}'::jsonb NOT NULL,
    annotations jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_seen_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: monitoring_backends; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.monitoring_backends (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    backend_type character varying(32) DEFAULT 'thanos'::character varying NOT NULL,
    query_url character varying(500) NOT NULL,
    alertmanager_url character varying(500) DEFAULT ''::character varying NOT NULL,
    tenant_id character varying(255) DEFAULT ''::character varying NOT NULL,
    auth_type character varying(32) DEFAULT 'none'::character varying NOT NULL,
    auth_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    default_step_seconds integer DEFAULT 300 NOT NULL,
    timeout_seconds integer DEFAULT 30 NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    auth_config_encrypted text DEFAULT ''::text NOT NULL
);


--
-- Name: monitoring_operation_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.monitoring_operation_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    operation_id uuid NOT NULL,
    level character varying(16) DEFAULT 'info'::character varying NOT NULL,
    stage character varying(64) NOT NULL,
    message text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: monitoring_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.monitoring_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_type character varying(64) NOT NULL,
    target_key character varying(255) NOT NULL,
    operation_type character varying(32) NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    status character varying(32) DEFAULT 'pending'::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: native_rbac_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.native_rbac_rules (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    cluster_id uuid,
    namespace text DEFAULT ''::text NOT NULL,
    api_group text DEFAULT ''::text NOT NULL,
    resource text NOT NULL,
    verbs text[] NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    created_by_id uuid
);


--
-- Name: network_policy_applications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.network_policy_applications (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    template_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    namespace character varying(253) NOT NULL,
    policy_name character varying(253) NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    last_applied_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    applied_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT np_status_valid CHECK (((status)::text = ANY ((ARRAY['pending'::character varying, 'applied'::character varying, 'failed'::character varying, 'drifting'::character varying])::text[])))
);


--
-- Name: network_policy_templates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.network_policy_templates (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug character varying(64) NOT NULL,
    name character varying(128) NOT NULL,
    description text NOT NULL,
    kind character varying(16) DEFAULT 'custom'::character varying NOT NULL,
    spec_template text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT np_kind_valid CHECK (((kind)::text = ANY ((ARRAY['builtin'::character varying, 'custom'::character varying])::text[])))
);


--
-- Name: notification_channels; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notification_channels (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    channel_type character varying(16) NOT NULL,
    configuration jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: notification_templates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notification_templates (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    template_key character varying(128) NOT NULL,
    channel character varying(16) NOT NULL,
    subject_tpl text DEFAULT ''::text NOT NULL,
    body_tpl text NOT NULL,
    body_format character varying(16) DEFAULT 'markdown'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    updated_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT body_format_valid CHECK (((body_format)::text = ANY ((ARRAY['text'::character varying, 'markdown'::character varying, 'html'::character varying, 'json'::character varying])::text[]))),
    CONSTRAINT channel_valid CHECK (((channel)::text = ANY ((ARRAY['email'::character varying, 'webhook'::character varying])::text[])))
);


--
-- Name: operation_idempotency_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.operation_idempotency_keys (
    scope text NOT NULL,
    idempotency_key text NOT NULL,
    operation_table text DEFAULT ''::text NOT NULL,
    operation_id uuid,
    response jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT operation_idempotency_keys_key_check CHECK ((idempotency_key <> ''::text)),
    CONSTRAINT operation_idempotency_keys_scope_check CHECK ((scope <> ''::text))
);


--
-- Name: password_reset_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.password_reset_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    token_hash character varying(64) NOT NULL,
    password_hash_at_issue character varying(128) NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: platform_configuration; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.platform_configuration (
    id integer DEFAULT 1 NOT NULL,
    server_url character varying(500) DEFAULT ''::character varying NOT NULL,
    platform_name character varying(255) DEFAULT 'Astronomer'::character varying NOT NULL,
    telemetry_enabled boolean DEFAULT false NOT NULL,
    bootstrapped_at timestamptz,
    instance_id uuid DEFAULT gen_random_uuid() NOT NULL,
    default_cluster_template_id uuid,
    CONSTRAINT platform_configuration_id_check CHECK ((id = 1))
);


--
-- Name: platform_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.platform_settings (
    key character varying(64) NOT NULL,
    value jsonb DEFAULT 'null'::jsonb NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    updated_by uuid,
    updated_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: pod_security_templates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pod_security_templates (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    enforce_level character varying(20) DEFAULT 'baseline'::character varying NOT NULL,
    enforce_version character varying(20) DEFAULT 'latest'::character varying NOT NULL,
    audit_level character varying(20) DEFAULT 'restricted'::character varying NOT NULL,
    audit_version character varying(20) DEFAULT 'latest'::character varying NOT NULL,
    warn_level character varying(20) DEFAULT 'restricted'::character varying NOT NULL,
    warn_version character varying(20) DEFAULT 'latest'::character varying NOT NULL,
    exempt_usernames jsonb DEFAULT '[]'::jsonb NOT NULL,
    exempt_runtime_classes jsonb DEFAULT '[]'::jsonb NOT NULL,
    exempt_namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL
);


--
-- Name: project_catalog_subscriptions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_catalog_subscriptions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    project_id uuid NOT NULL,
    catalog_id uuid NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: project_namespaces; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_namespaces (
    project_id uuid NOT NULL,
    cluster_id uuid NOT NULL,
    namespace character varying(253) NOT NULL,
    last_reconciled_at timestamptz,
    last_reconcile_error text DEFAULT ''::text NOT NULL,
    locked_until timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: project_role_bindings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_role_bindings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid,
    "group" character varying(255) DEFAULT ''::character varying NOT NULL,
    role_id uuid NOT NULL,
    project_id uuid NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    source character varying(32) DEFAULT 'manual'::character varying NOT NULL,
    group_sync_connector_id uuid
);


--
-- Name: project_roles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_roles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    permissions jsonb DEFAULT '{}'::jsonb NOT NULL,
    rules jsonb DEFAULT '[]'::jsonb NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    display_name character varying(255) DEFAULT ''::character varying NOT NULL
);


--
-- Name: projects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projects (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    display_name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    cluster_id uuid NOT NULL,
    namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    resource_quota jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    limit_range jsonb DEFAULT '{}'::jsonb NOT NULL,
    network_policy_mode character varying(32) DEFAULT 'none'::character varying NOT NULL,
    pod_security_profile character varying(16) DEFAULT 'privileged'::character varying NOT NULL,
    resource_quota_cpu_limit character varying(64) DEFAULT ''::character varying NOT NULL,
    resource_quota_memory_limit character varying(64) DEFAULT ''::character varying NOT NULL,
    resource_quota_pod_count integer DEFAULT 0 NOT NULL,
    quota_plan character varying(64) DEFAULT 'free'::character varying NOT NULL,
    quota_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    default_vault_connection_id uuid,
    managed_by character varying(16) DEFAULT 'api'::character varying NOT NULL,
    external_ref_api_version character varying(128) DEFAULT ''::character varying NOT NULL,
    external_ref_kind character varying(64) DEFAULT ''::character varying NOT NULL,
    external_ref_namespace character varying(253) DEFAULT ''::character varying NOT NULL,
    external_ref_name character varying(253) DEFAULT ''::character varying NOT NULL,
    observed_generation bigint DEFAULT 0 NOT NULL,
    CONSTRAINT pod_security_profile_valid CHECK (((pod_security_profile)::text = ANY ((ARRAY['privileged'::character varying, 'baseline'::character varying, 'restricted'::character varying])::text[]))),
    CONSTRAINT projects_external_ref_all_or_none CHECK (((((external_ref_api_version)::text = ''::text) AND ((external_ref_kind)::text = ''::text) AND ((external_ref_namespace)::text = ''::text) AND ((external_ref_name)::text = ''::text)) OR (((external_ref_api_version)::text <> ''::text) AND ((external_ref_kind)::text <> ''::text) AND ((external_ref_namespace)::text <> ''::text) AND ((external_ref_name)::text <> ''::text)))),
    CONSTRAINT projects_managed_by_valid CHECK (((managed_by)::text = ANY ((ARRAY['ui'::character varying, 'api'::character varying, 'crd'::character varying, 'system'::character varying, 'flux'::character varying])::text[])))
);


--
-- Name: prometheus_datasources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.prometheus_datasources (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(64) NOT NULL,
    url text NOT NULL,
    auth_encrypted text DEFAULT ''::text NOT NULL,
    tls_skip_verify boolean DEFAULT false NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: quota_plans; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.quota_plans (
    name character varying(64) NOT NULL,
    enforcement character varying(8) DEFAULT 'hard'::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    max_clusters_per_project integer DEFAULT 0 NOT NULL,
    max_namespaces_per_project integer DEFAULT 0 NOT NULL,
    max_members_per_project integer DEFAULT 0 NOT NULL,
    max_projects_per_user integer DEFAULT 0 NOT NULL,
    max_tokens_per_user integer DEFAULT 0 NOT NULL,
    max_streams_per_user integer DEFAULT 0 NOT NULL,
    max_total_clusters integer DEFAULT 0 NOT NULL,
    max_total_users integer DEFAULT 0 NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT enforcement_valid CHECK (((enforcement)::text = ANY ((ARRAY['soft'::character varying, 'hard'::character varying])::text[])))
);


--
-- Name: read_audit_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.read_audit_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    path_pattern character varying(256) NOT NULL,
    verbs character varying(64) DEFAULT 'GET'::character varying NOT NULL,
    sample_rate numeric(3,2) DEFAULT 1.00 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT sample_rate_valid CHECK (((sample_rate >= 0.0) AND (sample_rate <= 1.0)))
);


--
-- Name: repair_job_states; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.repair_job_states (
    job_name text NOT NULL,
    scope text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'unknown'::text NOT NULL,
    last_successful_reconcile_at timestamptz,
    last_error_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    success_count bigint DEFAULT 0 NOT NULL,
    error_count bigint DEFAULT 0 NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT repair_job_states_status_check CHECK ((status = ANY (ARRAY['unknown'::text, 'success'::text, 'failed'::text, 'skipped'::text])))
);


--
-- Name: restore_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.restore_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    backup_id uuid NOT NULL,
    status character varying(20) DEFAULT 'pending'::character varying NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    initiated_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    cluster_id uuid,
    velero_namespace character varying(63) DEFAULT 'velero'::character varying NOT NULL,
    velero_restore_name character varying(253) DEFAULT ''::character varying NOT NULL,
    included_namespaces jsonb DEFAULT '[]'::jsonb NOT NULL,
    namespace_mapping jsonb DEFAULT '{}'::jsonb NOT NULL,
    poll_attempts integer DEFAULT 0 NOT NULL,
    last_polled_at timestamptz
);


--
-- Name: scim_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.scim_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    token_hash character varying(128) NOT NULL,
    prefix character varying(16) DEFAULT ''::character varying NOT NULL,
    last_used_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: security_scan_results; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.security_scan_results (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cluster_id uuid NOT NULL,
    scan_type character varying(50) NOT NULL,
    status character varying(20) DEFAULT 'running'::character varying NOT NULL,
    summary jsonb DEFAULT '{}'::jsonb NOT NULL,
    results jsonb DEFAULT '[]'::jsonb NOT NULL,
    started_at timestamptz DEFAULT now() NOT NULL,
    completed_at timestamptz,
    initiated_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    cluster_scan_name text DEFAULT ''::text NOT NULL,
    passed integer DEFAULT 0 NOT NULL,
    failed integer DEFAULT 0 NOT NULL,
    warned integer DEFAULT 0 NOT NULL,
    skipped integer DEFAULT 0 NOT NULL,
    findings jsonb DEFAULT '[]'::jsonb NOT NULL
);


--
-- Name: siem_forward_queue; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.siem_forward_queue (
    id bigint NOT NULL,
    forwarder_id uuid NOT NULL,
    event_name character varying(128) NOT NULL,
    payload jsonb NOT NULL,
    severity character varying(16) DEFAULT 'info'::character varying NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: siem_forward_queue_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.siem_forward_queue_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: siem_forward_queue_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.siem_forward_queue_id_seq OWNED BY public.siem_forward_queue.id;


--
-- Name: siem_forwarder_status; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.siem_forwarder_status (
    forwarder_id uuid NOT NULL,
    last_sent_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    queue_depth integer DEFAULT 0 NOT NULL,
    dropped_total bigint DEFAULT 0 NOT NULL,
    dispatched_total bigint DEFAULT 0 NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: siem_forwarders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.siem_forwarders (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    transport character varying(32) NOT NULL,
    endpoint text NOT NULL,
    auth_encrypted text DEFAULT ''::text NOT NULL,
    event_filters jsonb DEFAULT '[]'::jsonb NOT NULL,
    format character varying(16) DEFAULT ''::character varying NOT NULL,
    tls_skip_verify boolean DEFAULT false NOT NULL,
    ca_cert_pem text DEFAULT ''::text NOT NULL,
    batch_size integer DEFAULT 100 NOT NULL,
    flush_interval_ms integer DEFAULT 2000 NOT NULL,
    timeout_seconds integer DEFAULT 10 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT transport_valid CHECK (((transport)::text = ANY ((ARRAY['syslog_udp'::character varying, 'syslog_tcp'::character varying, 'syslog_tls'::character varying, 'splunk_hec'::character varying, 'ndjson_https'::character varying])::text[])))
);


--
-- Name: smtp_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.smtp_settings (
    id uuid NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    host character varying(255) DEFAULT ''::character varying NOT NULL,
    port integer DEFAULT 587 NOT NULL,
    username character varying(255) DEFAULT ''::character varying NOT NULL,
    password_encrypted text DEFAULT ''::text NOT NULL,
    from_address character varying(255) DEFAULT ''::character varying NOT NULL,
    from_name character varying(255) DEFAULT 'Astronomer'::character varying NOT NULL,
    auth_mechanism character varying(32) DEFAULT 'plain'::character varying NOT NULL,
    encryption character varying(32) DEFAULT 'starttls'::character varying NOT NULL,
    require_tls boolean DEFAULT true NOT NULL,
    timeout_seconds integer DEFAULT 30 NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: sso_configurations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sso_configurations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    provider character varying(16) NOT NULL,
    is_enabled boolean DEFAULT false NOT NULL,
    display_name character varying(255) DEFAULT ''::character varying NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    client_id character varying(255) DEFAULT ''::character varying NOT NULL,
    client_secret_encrypted text DEFAULT ''::text NOT NULL,
    allowed_organizations jsonb DEFAULT '[]'::jsonb NOT NULL,
    allowed_domains jsonb DEFAULT '[]'::jsonb NOT NULL,
    auto_create_users boolean DEFAULT true NOT NULL,
    default_global_role_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    migrated_to_dex_at timestamptz
);


--
-- Name: sso_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sso_sessions (
    jti character varying(64) NOT NULL,
    user_id uuid NOT NULL,
    provider_name character varying(64) NOT NULL,
    upstream_id_token_encrypted text NOT NULL,
    end_session_endpoint text DEFAULT ''::text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: task_outbox; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_outbox (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    dedupe_key text,
    task_type text NOT NULL,
    payload bytea DEFAULT '\x'::bytea NOT NULL,
    queue_name text DEFAULT 'default'::text NOT NULL,
    max_retry integer DEFAULT 3 NOT NULL,
    timeout_seconds integer DEFAULT 0 NOT NULL,
    unique_seconds integer DEFAULT 0 NOT NULL,
    max_delivery_attempts integer DEFAULT 20 NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamptz DEFAULT now() NOT NULL,
    locked_until timestamptz,
    delivered_at timestamptz,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT task_outbox_max_delivery_attempts_valid CHECK ((max_delivery_attempts > 0)),
    CONSTRAINT task_outbox_max_retry_valid CHECK ((max_retry >= 0)),
    CONSTRAINT task_outbox_queue_name_nonempty CHECK ((length(TRIM(BOTH FROM queue_name)) > 0)),
    CONSTRAINT task_outbox_status_valid CHECK ((status = ANY (ARRAY['pending'::text, 'delivering'::text, 'failed'::text, 'delivered'::text, 'dead'::text]))),
    CONSTRAINT task_outbox_task_type_nonempty CHECK ((length(TRIM(BOTH FROM task_type)) > 0)),
    CONSTRAINT task_outbox_timeout_seconds_valid CHECK ((timeout_seconds >= 0)),
    CONSTRAINT task_outbox_unique_seconds_valid CHECK ((unique_seconds >= 0))
);


--
-- Name: tool_operation_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tool_operation_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    operation_id uuid NOT NULL,
    level character varying(16) NOT NULL,
    stage character varying(64) NOT NULL,
    message text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: tool_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tool_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_type character varying(64) NOT NULL,
    target_key character varying(255) NOT NULL,
    operation_type character varying(32) NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    status character varying(32) DEFAULT 'pending'::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: tunnel_locator_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tunnel_locator_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    connection_id character varying(128) DEFAULT ''::character varying NOT NULL,
    cluster_id uuid,
    event_type character varying(32) NOT NULL,
    reason_code character varying(64) NOT NULL,
    server_replica character varying(128) DEFAULT ''::character varying NOT NULL,
    occurred_at timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT tunnel_locator_event_type CHECK (((event_type)::text = ANY ((ARRAY['lookup_failed'::character varying, 'owner_missing'::character varying, 'owner_unreachable'::character varying, 'registration_failed'::character varying, 'state_stale'::character varying, 'recovered'::character varying])::text[])))
);


--
-- Name: ui_extensions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ui_extensions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    display_name text NOT NULL,
    version text NOT NULL,
    source text DEFAULT 'manual'::text NOT NULL,
    checksum text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    compatibility_status text DEFAULT 'unknown'::text NOT NULL,
    manifest jsonb NOT NULL,
    installed_by uuid,
    installed_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    bundle_verified boolean DEFAULT false NOT NULL,
    CONSTRAINT ui_extensions_compatibility_status_check CHECK ((compatibility_status = ANY (ARRAY['compatible'::text, 'incompatible'::text, 'unknown'::text])))
);


--
-- Name: user_idp_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_idp_groups (
    user_id uuid NOT NULL,
    connector_id uuid,
    groups jsonb DEFAULT '[]'::jsonb NOT NULL,
    synced_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: user_totp_enrollments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_totp_enrollments (
    user_id uuid NOT NULL,
    secret_encrypted text NOT NULL,
    label character varying(255) DEFAULT ''::character varying NOT NULL,
    confirmed_at timestamptz NOT NULL,
    last_used_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: user_totp_recovery_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_totp_recovery_codes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    code_hash character varying(64) NOT NULL,
    used_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email character varying(254) NOT NULL,
    username character varying(150) NOT NULL,
    first_name character varying(150) DEFAULT ''::character varying NOT NULL,
    last_name character varying(150) DEFAULT ''::character varying NOT NULL,
    password character varying(128) DEFAULT ''::character varying NOT NULL,
    is_active boolean DEFAULT true NOT NULL,
    is_staff boolean DEFAULT false NOT NULL,
    is_superuser boolean DEFAULT false NOT NULL,
    last_login timestamptz,
    date_joined timestamptz DEFAULT now() NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    must_change_password boolean DEFAULT false NOT NULL,
    failed_login_count integer DEFAULT 0 NOT NULL,
    failed_login_at timestamptz,
    locked_until timestamptz,
    locked_reason text DEFAULT ''::text NOT NULL,
    tokens_invalidated_at timestamptz,
    quota_plan character varying(64) DEFAULT 'free'::character varying NOT NULL,
    quota_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    is_service boolean DEFAULT false NOT NULL
);


--
-- Name: vault_connections; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.vault_connections (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    addr text NOT NULL,
    auth_method character varying(32) NOT NULL,
    auth_encrypted text DEFAULT ''::text NOT NULL,
    namespace character varying(128) DEFAULT ''::character varying NOT NULL,
    tls_skip_verify boolean DEFAULT false NOT NULL,
    ca_cert_pem text DEFAULT ''::text NOT NULL,
    default_mount character varying(64) DEFAULT 'secret'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    cached_token_expires_at timestamptz,
    last_health_at timestamptz,
    last_health_ok boolean DEFAULT false NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL,
    CONSTRAINT auth_method_valid CHECK (((auth_method)::text = ANY ((ARRAY['token'::character varying, 'approle'::character varying, 'kubernetes'::character varying])::text[])))
);


--
-- Name: webhook_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.webhook_deliveries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    subscription_id uuid NOT NULL,
    event_name character varying(128) NOT NULL,
    event_id character varying(128) DEFAULT ''::character varying NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    payload_size integer DEFAULT 0 NOT NULL,
    status character varying(16) DEFAULT 'queued'::character varying NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    response_status integer DEFAULT 0 NOT NULL,
    response_body text DEFAULT ''::text NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    delivered_at timestamptz,
    next_attempt_at timestamptz,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: webhook_subscriptions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.webhook_subscriptions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(128) NOT NULL,
    url text NOT NULL,
    secret_encrypted text NOT NULL,
    event_filters jsonb DEFAULT '[]'::jsonb NOT NULL,
    payload_template text DEFAULT ''::text NOT NULL,
    extra_headers jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    max_retries integer DEFAULT 5 NOT NULL,
    timeout_seconds integer DEFAULT 10 NOT NULL,
    created_by uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: workload_operation_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workload_operation_events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    operation_id uuid NOT NULL,
    level character varying(16) NOT NULL,
    stage character varying(64) NOT NULL,
    message text NOT NULL,
    detail jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: workload_operations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workload_operations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    target_type character varying(64) NOT NULL,
    target_key character varying(255) NOT NULL,
    operation_type character varying(32) NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    status character varying(32) DEFAULT 'pending'::character varying NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text DEFAULT ''::text NOT NULL,
    created_by_id uuid,
    created_at timestamptz DEFAULT now() NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: xcluster_anomaly_baselines; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.xcluster_anomaly_baselines (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    metric_name character varying(128) NOT NULL,
    window_seconds integer DEFAULT 86400 NOT NULL,
    cluster_count integer DEFAULT 0 NOT NULL,
    population_mean double precision DEFAULT 0 NOT NULL,
    population_stddev double precision DEFAULT 0 NOT NULL,
    population_min double precision DEFAULT 0 NOT NULL,
    population_max double precision DEFAULT 0 NOT NULL,
    stddev_mult double precision DEFAULT 3.0 NOT NULL,
    outlier_cluster_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    updated_at timestamptz DEFAULT now() NOT NULL
);


--
-- Name: audit_log_default; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log ATTACH PARTITION public.audit_log_default DEFAULT;


--
-- Name: apiserver_allowlist_snapshots id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_allowlist_snapshots ALTER COLUMN id SET DEFAULT nextval('public.apiserver_allowlist_snapshots_id_seq'::regclass);


--
-- Name: kubectl_session_commands id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.kubectl_session_commands ALTER COLUMN id SET DEFAULT nextval('public.kubectl_session_commands_id_seq'::regclass);


--
-- Name: siem_forward_queue id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forward_queue ALTER COLUMN id SET DEFAULT nextval('public.siem_forward_queue_id_seq'::regclass);


--
-- Data for Name: agent_connection_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: agent_connections; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: agent_lifecycle_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: agent_operational_statuses; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: alert_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: alert_inhibitions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: alert_rule_channels; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: alert_rules; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: alert_silences; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: anomaly_baselines; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: api_tokens; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: apiserver_allowlist_snapshots; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: apiserver_allowlists; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: apiserver_audit_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: audit_archive; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: audit_log_default; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: authored_constraints; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: backup_drill_results; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: backup_schedules; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: backup_storage_configs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: backups; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: catalog_blessed_charts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: catalog_operation_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: catalog_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_action_approvals; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_action_deferrals; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_action_receipts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_alert_deliveries; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_alert_policies; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_alert_policy_channels; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_artifact_credential_state; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_automation_policies; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_connections; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_delegations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_finding_decisions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_finding_projection_cursors; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_finding_resources; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_findings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_interactive_threads; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_session_resources; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_sessions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_thread_sessions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_trigger_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: charlie_trigger_rules; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chart_co_installation; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chart_rating_aggregates; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: chart_ratings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cloud_credential_materializations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cloud_credentials; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_agent_tokens; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_condition_remediation_attempts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_conditions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_decommissions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_deployment_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_deployments; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_groups; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_health_statuses; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_monitoring_configs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_registration_policies; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_registration_steps; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_registration_tokens; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_registry_configs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_restores; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_role_bindings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_roles; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('e3a5deab-4e34-4491-9143-978918fb37db', 'Cluster Owner', 'Full access to a specific cluster', '{}', '[{"verbs": ["*"], "resource": "*"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:16.088087+00', '');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('9ebfb568-32a0-4837-a3f0-7afee0807f1b', 'Cluster Troubleshooter', 'Inspect workloads and use pod-level diagnostics within a cluster', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs", "exec", "proxy"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}, {"verbs": ["read", "list"], "resource": "alerts"}]', true, '2026-08-17 02:45:16.426416+00', '2026-08-17 02:45:16.426416+00', '');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('b8e6c653-c669-481f-bae4-841f1012f17e', 'Workload Editor', 'Create, update, scale, and delete workloads in a cluster; read-only elsewhere', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["create", "read", "update", "delete", "list", "scale", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('1a63597b-442b-43fa-8d23-442b848e3846', 'Pod Incident Responder', 'Diagnose and remediate pod-level incidents (logs, exec, delete pods) without broader cluster write access', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["read", "list", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs", "exec", "delete"], "resource": "pods"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('5215098d-f7a2-4626-aae9-af1c5ed5de44', 'Cluster Monitoring Admin', 'Manage monitoring config and alert rules scoped to a cluster', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "monitoring"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "alerts"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('272a0274-66a4-475c-8453-cfef82aeffb7', 'Cluster Backup Operator', 'Manage backup schedules and backup runs for a single cluster', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "backups"}, {"verbs": ["read", "list"], "resource": "projects"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Cluster Backup Operator');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('7fb58569-9a43-43fa-a810-e2492f146b8d', 'Service Mesh Operator', 'Manage service mesh traffic policy, mTLS policy, and mesh health for a cluster', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "service_mesh"}, {"verbs": ["read", "list"], "resource": "services"}, {"verbs": ["read", "list"], "resource": "ingresses"}, {"verbs": ["read"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "monitoring"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Service Mesh Operator');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('3543367e-3236-4035-a7ff-c24466048b91', 'Storage Manager', 'Manage persistent volume claims, storage classes, and storage health for an adopted cluster', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "storage"}, {"verbs": ["read"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Storage Manager');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('7b43bf11-dba7-49f5-8f76-5f2a9aab50d3', 'Catalog Installer', 'Install and upgrade approved catalog tools in one cluster without global catalog administration', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["read", "list", "create", "update", "delete"], "resource": "catalog"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Catalog Installer');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('c4614d65-d719-4447-a733-c8579add026a', 'Node Operator', 'Perform node maintenance in an adopted cluster', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["read", "list", "update", "manage"], "resource": "nodes"}, {"verbs": ["read", "list", "watch"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "workloads"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Node Operator');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('1b51634c-0b15-4b1c-a7c0-ab3bf7c4f4dc', 'Cluster Member', 'Can view cluster resources and manage workloads', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["read", "list", "create", "update", "delete", "scale", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:17.237733+00', '');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('ef79cbbd-3514-45a4-b471-1a385dfd6830', 'Cluster Viewer', 'Read-only access to a cluster', '{}', '[{"verbs": ["read", "list", "watch"], "resource": "*"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:17.237733+00', '');
INSERT INTO public.cluster_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('d9ed95a8-d498-4d21-9e70-5981397346c1', 'Cluster Operator', 'Operate workloads and cluster application delivery without full administrative access', '{}', '[{"verbs": ["read"], "resource": "clusters"}, {"verbs": ["create", "read", "update", "delete", "list", "scale", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs", "exec", "proxy"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}, {"verbs": ["read", "list"], "resource": "alerts"}, {"verbs": ["read", "list"], "resource": "backups"}]', true, '2026-08-17 02:45:16.426416+00', '2026-08-17 04:02:51.634769+00', '');


--
-- Data for Name: cluster_security_policies; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_service_mesh; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_snapshot_schedules; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_snapshots; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_template_applications; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: cluster_templates; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.cluster_templates (id, name, description, spec, created_by, created_at, updated_at) VALUES ('9c13ee16-6c2d-4624-adab-bca25cb351f5', 'Platform baseline', 'Astronomer-recommended baseline operators applied to every newly-registered cluster: trivy-operator (image vuln scans), kube-state-metrics + node-exporter (metrics), fluent-bit (log forwarding), cert-manager (TLS). Builtin; clone before customizing.', '{"tools": [{"slug": "trivy-operator", "preset": "default", "namespace": "astronomer-trivy-system", "create_namespace": true}, {"slug": "kube-state-metrics", "preset": "default", "namespace": "astronomer-monitoring", "create_namespace": true}, {"slug": "prometheus-node-exporter", "preset": "default", "namespace": "astronomer-monitoring", "create_namespace": true}, {"slug": "fluent-bit", "preset": "default", "namespace": "astronomer-logging", "create_namespace": true}, {"slug": "cert-manager", "preset": "default", "namespace": "astronomer-cert-manager", "create_namespace": true}], "builtin": true}', NULL, '2026-08-17 02:45:16.82856+00', '2026-08-17 02:45:16.839787+00');


--
-- Data for Name: cluster_tools; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('71659e40-2d20-4ffb-bb40-d3af2f45c88c', 'cis-operator', 'CIS Scanner (Rancher)', 'Run CIS Kubernetes Benchmark scans on this cluster. Operator surfaces ClusterScan / ClusterScanProfile / ClusterScanReport CRDs which Astronomer ingests into the Security console.', 'shield-check', 'security', '[{"order": 0, "repo_url": "https://charts.rancher.io", "namespace": "cis-operator-system", "chart_name": "rancher-cis-benchmark"}]', '', 'cis-operator-system', true, true, NULL, '{}', '', NULL, '/', '[]', '2026-08-17 02:45:16.359147+00', '2026-08-17 02:45:16.359147+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('88f88c00-ea49-461c-8f2a-44882556dfe9', 'dex', 'Dex Identity Broker', 'OpenID Connect identity broker. Astronomer talks OIDC to Dex; Dex brokers SAML, LDAP, Active Directory, Azure AD, Okta, GitHub, GitLab, and more. Configure connectors under Settings > Authentication.', 'shield-key', 'auth', '[{"order": 0, "repo_url": "https://charts.dexidp.io", "namespace": "dex", "chart_name": "dex"}]', '', 'dex', true, true, NULL, '{"in-cluster": {"https": {"enabled": false}, "config": {"storage": {"type": "kubernetes", "config": {"inCluster": true}}}, "envFrom": [{"secretRef": {"name": "astronomer-dex-config"}}], "volumes": [{"name": "config", "configMap": {"name": "astronomer-dex-config"}}], "volumeMounts": [{"name": "config", "mountPath": "/etc/dex/cfg"}]}}', 'dex', NULL, '/', '[]', '2026-08-17 02:45:16.366248+00', '2026-08-17 02:45:16.366248+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('c9fa21b4-03a4-4d8d-b6d5-cf3f1941e9de', 'fluent-bit', 'Fluent Bit', 'Lightweight log forwarder. Tails container stdout/stderr and ships records to the Astronomer log sink (or any operator-configured backend).', 'file-text', 'observability', '[{"order": 0, "repo_url": "https://fluent.github.io/helm-charts", "namespace": "astronomer-logging", "chart_name": "fluent-bit"}]', '', 'astronomer-logging', true, true, NULL, '{"default": "existingConfigMap: astronomer-fluent-bit-config\nhotReload:\n  enabled: true\n"}', '', NULL, '', '[]', '2026-08-17 02:45:16.856042+00', '2026-08-17 02:45:17.018674+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('b4a5491d-3c83-4bae-add8-b993fd1e7a81', 'trivy-operator', 'Trivy Operator', 'Continuous image vulnerability + misconfiguration scanning. Drives Astronomer''s Image Scans dashboard via the Trivy CRD ingester.', 'shield', 'security', '[{"order": 0, "repo_url": "https://aquasecurity.github.io/helm-charts", "namespace": "astronomer-trivy-system", "chart_name": "trivy-operator"}]', '', 'astronomer-trivy-system', true, true, NULL, '{"default": "trivy:\n  ignoreUnfixed: true\noperator:\n  scanJobTimeout: 5m\n"}', '', NULL, '', '[]', '2026-08-17 02:45:16.856042+00', '2026-08-17 02:45:17.101517+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('f7d235d9-0f35-4c50-9b10-9edbda29c047', 'cert-manager', 'cert-manager', 'Automated certificate management for Kubernetes. Use it to issue and renew TLS certificates for Astronomer''s Gateway and other cluster workloads.', 'lock', 'security', '[{"order": 0, "repo_url": "https://charts.jetstack.io", "namespace": "astronomer-cert-manager", "chart_name": "cert-manager"}]', '', 'astronomer-cert-manager', true, true, NULL, '{"default": "crds:\n  enabled: true\nprometheus:\n  enabled: true\nstartupapicheck:\n  enabled: false\n"}', 'cert-manager-webhook', NULL, '/', '[]', '2026-08-17 02:45:16.43222+00', '2026-08-17 02:45:17.101517+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('8f207aca-d233-4765-a8db-1ccfeaccef5f', 'gatekeeper', 'Gatekeeper', 'Open Policy Agent Gatekeeper admission policy engine for baseline policy enforcement and future constraint bundles.', 'shield-check', 'security', '[{"order": 0, "repo_url": "https://open-policy-agent.github.io/gatekeeper/charts", "namespace": "astronomer-gatekeeper-system", "chart_name": "gatekeeper"}]', '', 'astronomer-gatekeeper-system', true, true, NULL, '{"default": ""}', '', NULL, '', '[]', '2026-08-17 02:45:17.012176+00', '2026-08-17 02:45:17.101517+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('4850e904-ce1a-4e7b-98ae-33f1d76f63db', 'kube-state-metrics', 'kube-state-metrics', 'Cluster object metrics (Deployments, Pods, Nodes, …) exposed in Prometheus format. Backs Astronomer dashboards and SLO rules.', 'activity', 'observability', '[{"order": 0, "repo_url": "https://prometheus-community.github.io/helm-charts", "namespace": "astronomer-monitoring", "chart_name": "kube-state-metrics"}]', '8.0.0', 'astronomer-monitoring', true, true, NULL, '{"default": "metricLabelsAllowlist:\n  - pods=[*]\n  - deployments=[*]\n"}', '', NULL, '', '[]', '2026-08-17 02:45:16.856042+00', '2026-08-17 02:45:17.242579+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('6e642fbb-1a79-4754-8a15-ecef76b87ac1', 'prometheus-node-exporter', 'Prometheus Node Exporter', 'Host-level metrics (cpu/mem/disk/net) exported by a DaemonSet on every node. Pairs with kube-state-metrics for full cluster observability.', 'cpu', 'observability', '[{"order": 0, "repo_url": "https://prometheus-community.github.io/helm-charts", "namespace": "astronomer-monitoring", "chart_name": "prometheus-node-exporter"}]', '4.56.1', 'astronomer-monitoring', true, true, NULL, '{"default": "hostRootFsMount:\n  enabled: true\n"}', '', NULL, '', '[]', '2026-08-17 02:45:16.856042+00', '2026-08-17 02:45:17.242579+00');
INSERT INTO public.cluster_tools (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, helm_chart_id, presets, service_name, service_port, service_path, sub_services, created_at, updated_at) VALUES ('54f9d4b1-8c0f-47f5-8a0e-ce21a71c5ad0', 'ingress-nginx', 'ingress-nginx', 'Ingress controller for adopted clusters. The delivery-managed platform baseline installs it with metrics enabled.', 'route', 'networking', '[{"order": 0, "repo_url": "https://kubernetes.github.io/ingress-nginx", "namespace": "astronomer-ingress-nginx", "chart_name": "ingress-nginx"}]', '', 'astronomer-ingress-nginx', true, true, NULL, '{"default": "controller:\n  metrics:\n    enabled: true\n"}', '', NULL, '', '[]', '2026-08-17 02:45:17.012176+00', '2026-08-17 04:02:51.637088+00');


--
-- Data for Name: clusters; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: compliance_baseline_applications; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: compliance_baselines; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.compliance_baselines (id, slug, name, description, version, spec, enabled, created_at, updated_at) VALUES ('0dddab87-044e-41d5-8a10-c313480753fb', 'pci_dss_4_0', 'PCI-DSS 4.0', 'Payment card industry — cardholder data scope', '1.0', '{}', true, '2026-08-17 02:45:16.72864+00', '2026-08-17 02:45:16.72864+00');
INSERT INTO public.compliance_baselines (id, slug, name, description, version, spec, enabled, created_at, updated_at) VALUES ('80bbc8c9-8a37-4b11-bd8b-53eccd29e70e', 'hipaa', 'HIPAA', 'US healthcare — protected health information', '1.0', '{}', true, '2026-08-17 02:45:16.72864+00', '2026-08-17 02:45:16.72864+00');
INSERT INTO public.compliance_baselines (id, slug, name, description, version, spec, enabled, created_at, updated_at) VALUES ('ab06c5e9-bced-49bf-aec3-c373e8a29199', 'fedramp_moderate', 'FedRAMP Moderate', 'US federal cloud — moderate-impact baseline', '1.0', '{}', true, '2026-08-17 02:45:16.72864+00', '2026-08-17 02:45:16.72864+00');
INSERT INTO public.compliance_baselines (id, slug, name, description, version, spec, enabled, created_at, updated_at) VALUES ('c0cef3d5-5338-4977-8a88-1a28d2a672d1', 'soc2', 'SOC 2', 'Service organization controls (Type II)', '1.0', '{}', true, '2026-08-17 02:45:16.72864+00', '2026-08-17 02:45:16.72864+00');


--
-- Data for Name: component_bundle_versions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: component_bundles; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: control_plane_alerts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: control_plane_policies; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.control_plane_policies (id, name, monitoring_queue_depth_threshold, delivery_queue_depth_threshold, tools_queue_depth_threshold, catalog_queue_depth_threshold, monitoring_stale_running_threshold, delivery_stale_running_threshold, tools_stale_running_threshold, catalog_stale_running_threshold, monitoring_recent_failure_threshold, delivery_recent_failure_threshold, tools_recent_failure_threshold, catalog_recent_failure_threshold, recent_failure_window_minutes, created_at, updated_at) VALUES ('55225206-6a67-4230-a05b-0e268017f758', 'default', 10, 10, 10, 10, 1, 1, 1, 1, 3, 3, 3, 3, 30, '2026-08-17 02:45:16.283608+00', '2026-08-17 02:45:16.283608+00');


--
-- Data for Name: control_plane_silences; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: control_plane_snapshots; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: dashboard_widgets; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.dashboard_widgets (id, name, description, widget_type, spec, scope, scope_ids, grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled, created_by, created_at, updated_at) VALUES ('33a22ca4-a957-4f7a-8f34-d9bbd0d30f4a', 'Pod CPU saturation', 'Per-cluster pod CPU usage as a percentage of requests. Edit the datasource + query when wiring Prometheus.', 'prom_sparkline', '{"step": "60s", "query": "sum(rate(container_cpu_usage_seconds_total[5m])) / sum(kube_pod_container_resource_requests{resource=\"cpu\"})", "duration": "1h", "datasource": "default"}', 'cluster', '{}', 0, 0, 6, 2, 60, true, NULL, '2026-08-17 02:45:16.667483+00', '2026-08-17 02:45:16.667483+00');
INSERT INTO public.dashboard_widgets (id, name, description, widget_type, spec, scope, scope_ids, grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled, created_by, created_at, updated_at) VALUES ('533283f3-775f-4104-8f6d-c81be05a4257', 'API server p99 latency', 'Apiserver request latency 99th percentile, scoped per cluster.', 'prom_stat', '{"unit": "s", "query": "histogram_quantile(0.99, sum(rate(apiserver_request_duration_seconds_bucket[5m])) by (le))", "format": ".3f", "datasource": "default"}', 'cluster', '{}', 6, 0, 3, 1, 30, true, NULL, '2026-08-17 02:45:16.667483+00', '2026-08-17 02:45:16.667483+00');
INSERT INTO public.dashboard_widgets (id, name, description, widget_type, spec, scope, scope_ids, grid_x, grid_y, grid_w, grid_h, refresh_seconds, enabled, created_by, created_at, updated_at) VALUES ('09c3c9f5-3aee-4402-9ed5-b844773b6742', 'Cluster health rollup', 'Fleet-wide rollup of healthy clusters / total clusters.', 'prom_sparkline', '{"step": "5m", "query": "count(up{job=\"kubernetes-nodes\"} == 1) / count(up{job=\"kubernetes-nodes\"})", "duration": "6h", "datasource": "default"}', 'global', '{}', 0, 0, 12, 2, 120, true, NULL, '2026-08-17 02:45:16.667483+00', '2026-08-17 02:45:16.667483+00');


--
-- Data for Name: deferred_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_assignment_receipts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_controller_inventory; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_rollout_approvals; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_rollout_clusters; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_rollout_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_rollouts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_source_resolutions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_sources; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_system_cluster_assignments; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_system_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_system_releases; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_system_rollouts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: delivery_targets; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: dex_connectors; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: dex_settings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: email_messages; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: gitops_registered_clusters; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: gitops_registration_sources; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: global_role_bindings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: global_roles; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('5cbe7f7a-7d93-4df0-a34a-a8f41ef16a4a', 'Administrator', 'Full platform access', '{}', '[{"verbs": ["*"], "resource": "*"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:16.088087+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('f0ff3e64-6f16-4619-800f-261af8e4b53c', 'Standard User', 'Can view clusters and manage assigned resources', '{}', '[{"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list"], "resource": "monitoring"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:16.088087+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('561fa783-f3b2-4dfb-89e9-3e9a5912f4cc', 'User Administrator', 'Manage platform users and inspect role assignments', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "users"}, {"verbs": ["read", "list"], "resource": "rbac"}]', true, '2026-08-17 02:45:16.426416+00', '2026-08-17 02:45:16.426416+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('0de69e6d-edb4-4a62-ad74-ae93382b03b0', 'RBAC Administrator', 'Manage roles and role bindings across the platform', '{}', '[{"verbs": ["*"], "resource": "rbac"}, {"verbs": ["read", "list"], "resource": "users"}]', true, '2026-08-17 02:45:16.426416+00', '2026-08-17 02:45:16.426416+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('3a1accd5-1478-4788-8c28-572a3912cf91', 'Backup Operator', 'Manage backup storage, schedules, runs, and restores across the platform', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "backups"}, {"verbs": ["read", "list"], "resource": "clusters"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('eb20e305-3df9-45e1-8492-06be5ab84dcc', 'Alerts Manager', 'Manage alert rules, channels, silences, and events platform-wide', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "alerts"}, {"verbs": ["read", "list"], "resource": "monitoring"}, {"verbs": ["read", "list"], "resource": "clusters"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('2329d0bd-494d-4880-9c3b-9aa52774e723', 'Catalog Maintainer', 'Manage Helm/OCI repositories, available charts, and installed-chart lifecycle across the platform', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "catalog"}, {"verbs": ["read", "list"], "resource": "clusters"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('97d57053-7b2b-4c53-837c-644d07b84499', 'Cluster Registrar', 'Register new clusters and manage agent lifecycle; cannot edit cluster workloads', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "clusters"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "agents"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('bf77737e-60bf-4c51-ab88-00ad0fbf8dfb', 'Support Bundle Operator', 'Generate redacted support bundles and inspect agent/platform health data', '{}', '[{"verbs": ["create", "read", "list"], "resource": "support_bundles"}, {"verbs": ["read", "list"], "resource": "agents"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "audit_logs"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Support Bundle Operator');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('042f3ef2-41c1-48a4-9a10-a9d2d254e3ad', 'Logging Viewer', 'Read-only access to cluster and management-plane logging views', '{}', '[{"verbs": ["read", "list"], "resource": "logging"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}, {"verbs": ["read", "list", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Logging Viewer');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('af5ee64f-9bb3-46e2-b587-d7db2d07eead', 'Compliance Manager', 'Manage compliance baselines, exports, evidence collection, and security posture workflows', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "security"}, {"verbs": ["read", "list"], "resource": "audit_logs"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "rbac"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Compliance Manager');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('255112ee-ad21-4ac6-896f-6da73a99e331', 'Restore Operator', 'Execute restore workflows after incidents or drills', '{}', '[{"verbs": ["read", "list", "update", "manage"], "resource": "backups"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "audit_logs"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Restore Operator');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('96356fb1-ffdc-43d0-a68f-254c3461d1c5', 'Monitoring Viewer', 'Read-only access to metrics, dashboards, alert state, and cluster health summaries', '{}', '[{"verbs": ["read", "list"], "resource": "monitoring"}, {"verbs": ["read", "list"], "resource": "alerts"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Monitoring Viewer');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('1cae5a2f-5b08-4d9a-8e75-2e2bde0adde1', 'Monitoring Admin', 'Manage monitoring stack configuration, dashboards, rules, and alert delivery policy', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "monitoring"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "alerts"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Monitoring Admin');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('2d2d914f-1e33-4090-b98f-1235484be6b9', 'Catalog Admin', 'Curate Helm/OCI repositories, chart metadata, ratings, and platform tool catalog entries', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "catalog"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "cluster_templates"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "audit_logs"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Catalog Admin');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('65f725d9-d005-4c8d-a8d8-1aa1b47dc51c', 'Read Only', 'View-only access across the platform', '{}', '[{"verbs": ["read", "list"], "resource": "*"}, {"verbs": ["create", "read"], "resource": "charlie"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:17.260934+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('17b54297-04ec-496b-9431-304689c7f949', 'Audit Viewer', 'Read-only access to audit evidence and surrounding platform metadata', '{}', '[{"verbs": ["read", "list"], "resource": "audit_logs"}, {"verbs": ["read", "list"], "resource": "users"}, {"verbs": ["read", "list"], "resource": "rbac"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Audit Viewer');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('c8dfd20e-22a8-4a2f-88ca-6c1f6292b805', 'Security Auditor', 'Read-only security posture, vulnerability, policy, and compliance review across the fleet', '{}', '[{"verbs": ["read", "list"], "resource": "security"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list"], "resource": "audit_logs"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Security Auditor');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('30536106-5410-4ca6-b65d-2c1301134c75', 'Charlie Automation', 'Unbound template for the hidden Charlie service identity; explicit target grants are required.', '{}', '[{"verbs": ["create", "read"], "resource": "charlie"}]', true, '2026-08-17 02:45:17.260934+00', '2026-08-17 02:45:17.260934+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('f945ffd4-9550-49e0-96f3-8a49dd66c716', 'Platform Operator', 'Operate day-2 platform workflows across adopted clusters without full superuser access', '{}', '[{"verbs": ["create", "read", "update", "list"], "resource": "clusters"}, {"verbs": ["create", "read", "update", "list"], "resource": "agents"}, {"verbs": ["create", "read", "update", "list"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list"], "resource": "monitoring"}, {"verbs": ["create", "read", "update", "list"], "resource": "alerts"}, {"verbs": ["read", "list"], "resource": "catalog"}, {"verbs": ["create", "read", "update", "list"], "resource": "backups"}, {"verbs": ["read", "list"], "resource": "audit_logs"}, {"verbs": ["create", "read"], "resource": "charlie"}, {"verbs": ["update"], "resource": "charlie"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:17.336376+00', 'Platform Operator');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('8846df0f-0f97-4a07-a0cf-ec981d77a9e5', 'Charlie Approver', 'May use Charlie and approve one exact action; target permissions are still required.', '{}', '[{"verbs": ["create", "read", "approve"], "resource": "charlie"}, {"verbs": ["update"], "resource": "charlie"}]', true, '2026-08-17 02:45:17.260934+00', '2026-08-17 02:45:17.336376+00', '');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('f2888065-94f4-44d9-b71e-d2469dea1dd7', 'GitOps Admin', 'Full administration of immutable Flux-native delivery sources, bundles, targets, rollouts, and platform releases', '{}', '[{"verbs": ["create", "read", "update", "delete", "list"], "resource": "delivery_sources"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "delivery_bundles"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "delivery_targets"}, {"verbs": ["create", "read", "update", "delete", "list", "watch"], "resource": "delivery_rollouts"}, {"verbs": ["read", "update", "delete", "list", "watch"], "resource": "delivery_deployments"}, {"verbs": ["read", "list"], "resource": "delivery_inventory"}, {"verbs": ["approve"], "resource": "delivery_approvals"}, {"verbs": ["rollback"], "resource": "delivery_rollbacks"}, {"verbs": ["orphan"], "resource": "delivery_orphans"}, {"verbs": ["read", "update"], "resource": "delivery_platform"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "audit_logs"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 04:02:51.628388+00', 'GitOps Admin');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('e4b85cd1-abbf-41b9-9058-43f57cf849b4', 'GitOps Viewer', 'Read-only visibility into Flux-native delivery sources, bundles, targets, rollouts, deployments, and drift', '{}', '[{"verbs": ["read", "list"], "resource": "delivery_sources"}, {"verbs": ["read", "list"], "resource": "delivery_bundles"}, {"verbs": ["read", "list"], "resource": "delivery_targets"}, {"verbs": ["read", "list", "watch"], "resource": "delivery_rollouts"}, {"verbs": ["read", "list", "watch"], "resource": "delivery_deployments"}, {"verbs": ["read", "list"], "resource": "delivery_inventory"}, {"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 04:02:51.630922+00', 'GitOps Viewer');
INSERT INTO public.global_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('77766461-75a9-45c5-b0d4-19f5909aa413', 'Auditor', 'Read-only visibility into platform state, security posture, and audit evidence', '{}', '[{"verbs": ["read", "list"], "resource": "clusters"}, {"verbs": ["read", "list"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}, {"verbs": ["read", "list"], "resource": "alerts"}, {"verbs": ["read", "list"], "resource": "catalog"}, {"verbs": ["read", "list"], "resource": "backups"}, {"verbs": ["read", "list"], "resource": "security"}, {"verbs": ["read", "list"], "resource": "settings"}, {"verbs": ["read", "list"], "resource": "sso"}, {"verbs": ["read", "list"], "resource": "users"}, {"verbs": ["read", "list"], "resource": "rbac"}, {"verbs": ["read", "list"], "resource": "audit_logs"}, {"verbs": ["read", "list"], "resource": "agents"}, {"verbs": ["read", "list"], "resource": "delivery_sources"}, {"verbs": ["read", "list"], "resource": "delivery_bundles"}, {"verbs": ["read", "list"], "resource": "delivery_targets"}, {"verbs": ["read", "list", "watch"], "resource": "delivery_rollouts"}, {"verbs": ["read", "list", "watch"], "resource": "delivery_deployments"}, {"verbs": ["read", "list"], "resource": "delivery_inventory"}]', true, '2026-08-17 02:45:16.426416+00', '2026-08-17 04:02:51.63252+00', '');


--
-- Data for Name: helm_chart_tags; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: helm_chart_versions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: helm_charts; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.helm_charts (id, repository_id, name, display_name, description, icon_url, home_url, category, keywords, maintainers, deprecated, created_at, updated_at) VALUES ('d5d20e28-0a6d-4116-b900-ae3488ff6185', 'fa1bff7b-f34b-4e2c-a72a-62a452acc3ca', 'trivy-operator', 'Trivy Operator', 'Kubernetes operator that scans workload images for vulnerabilities and exposes the results as VulnerabilityReport CRDs. Astronomer ingests those CRDs into the per-cluster Image Scans view.', 'https://aquasecurity.github.io/trivy-operator/v0.18.4/images/trivy-operator-logo.png', 'https://aquasecurity.github.io/trivy-operator/', 'security', '["security", "vulnerability", "trivy", "cve", "image-scanning"]', '[]', false, '2026-08-17 02:45:16.704847+00', '2026-08-17 02:45:16.704847+00');


--
-- Data for Name: helm_repositories; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('fa1bff7b-f34b-4e2c-a72a-62a452acc3ca', 'aqua', 'https://aquasecurity.github.io/helm-charts/', 'helm', 'Aqua Security Helm charts (trivy-operator, kube-bench, ...).', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:16.704847+00', '2026-08-17 02:45:16.704847+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('93a30284-cd5f-4ba1-9072-267908d5f63d', 'jetstack', 'https://charts.jetstack.io', 'helm', 'Jetstack — cert-manager. Seeded by sprint 075.', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:16.834747+00', '2026-08-17 02:45:16.834747+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('6fc83261-0b8a-41c4-986b-19b67dff73a8', 'prometheus-community', 'https://prometheus-community.github.io/helm-charts', 'helm', 'Prometheus community charts — prometheus-node-exporter, prometheus, alertmanager, kube-prometheus-stack. Seeded by sprint 077.', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:16.844728+00', '2026-08-17 02:45:16.844728+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('f347bf5f-84bc-4edd-839a-58c12a9993ca', 'fluent', 'https://fluent.github.io/helm-charts', 'helm', 'Fluent helm charts — fluent-bit log forwarder + fluentd. Seeded by sprint 079 so the platform-baseline apply can resolve fluent-bit.', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:16.856042+00', '2026-08-17 02:45:16.856042+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('b1861ca1-31a3-462f-9036-31d0f5015265', 'grafana', 'https://grafana.github.io/helm-charts', 'helm', 'Grafana Labs — loki-stack, tempo, mimir, grafana itself. Seeded by sprint 082 for the Apps tab.', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:16.872192+00', '2026-08-17 02:45:16.872192+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('41f57bbb-f0c0-41ac-97c6-24051334943a', 'docker-hardened-images', 'https://github.com/docker-hardened-images/catalog/raw/main/chart', 'helm', 'Docker Hardened Images — CIS-baselined minimal-image charts. Seeded by sprint 089.', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:16.903135+00', '2026-08-17 02:45:16.903135+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('ff46cfc8-d912-44d8-8991-38ee0b1c8638', 'ingress-nginx', 'https://kubernetes.github.io/ingress-nginx', 'helm', 'ingress-nginx Helm charts. Seeded so the platform baseline and manual tool install surfaces resolve ingress-nginx consistently.', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:17.012176+00', '2026-08-17 02:45:17.012176+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('35e88d49-7716-4516-8751-5f63bcb7972d', 'open-policy-agent', 'https://open-policy-agent.github.io/gatekeeper/charts', 'helm', 'Open Policy Agent Gatekeeper Helm charts. Seeded for the platform baseline policy-stack component.', false, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:17.012176+00', '2026-08-17 02:45:17.012176+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('cbef993c-00fe-4b3d-b61c-39d56f099bde', 'kyverno', 'https://kyverno.github.io/kyverno/', 'helm', 'Kyverno — Kubernetes-native policy engine (admission control, mutation, generation).', true, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:17.032277+00', '2026-08-17 02:45:17.032277+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('09a25b37-ab0f-4039-a231-60cd5c303e8c', 'external-secrets', 'https://charts.external-secrets.io', 'helm', 'External Secrets Operator — sync secrets from Vault, AWS/GCP/Azure secret managers.', true, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:17.032277+00', '2026-08-17 02:45:17.032277+00', NULL, '', NULL, '');
INSERT INTO public.helm_repositories (id, name, url, repo_type, description, is_default, auth_type, auth_config, enabled, last_synced_at, created_by_id, created_at, updated_at, owner_project_id, last_sync_error, last_sync_attempted_at, auth_config_encrypted) VALUES ('6a7657ed-0bf2-4f5a-a40f-69550c6bf20c', 'longhorn', 'https://charts.longhorn.io', 'helm', 'Longhorn — cloud-native distributed block storage for Kubernetes.', true, 'none', '{}', true, NULL, NULL, '2026-08-17 02:45:17.032277+00', '2026-08-17 02:45:17.032277+00', NULL, '', NULL, '');


--
-- Data for Name: identity_group_mappings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: image_vulnerabilities; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: image_vulnerability_report_snapshots; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: image_vulnerability_reports; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: installed_charts; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: jwt_revocations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: kubectl_session_commands; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: kubectl_sessions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: logging_operation_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: logging_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: logging_outputs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: logging_pipeline_outputs; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: logging_pipelines; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: maintenance_windows; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: mirrored_gateway_classes; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: mirrored_ingress_classes; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: mirrored_limit_ranges; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: mirrored_network_policies; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: mirrored_resource_quotas; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: monitoring_backends; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: monitoring_operation_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: monitoring_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: native_rbac_rules; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: network_policy_applications; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: network_policy_templates; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.network_policy_templates (id, slug, name, description, kind, spec_template, enabled, created_by, created_at, updated_at) VALUES ('883c4258-93d8-465c-a601-509254cbd3a4', 'deny_all_ingress', 'Deny all ingress', 'Blocks all inbound traffic. Use as a base layer with explicit allow rules layered on.', 'builtin', 'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: deny_all_ingress
spec:
  podSelector: {}
  policyTypes: [Ingress]
', true, NULL, '2026-08-17 02:45:16.766503+00', '2026-08-17 02:45:16.766503+00');
INSERT INTO public.network_policy_templates (id, slug, name, description, kind, spec_template, enabled, created_by, created_at, updated_at) VALUES ('b4c0775e-dbda-4f8b-b643-bbf6790f0f97', 'project_isolated', 'Project isolated', 'Only allow ingress from pods labeled astronomer.io/project=<this>.', 'builtin', 'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: project_isolated
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels:
              astronomer.io/project: {{.Project}}
', true, NULL, '2026-08-17 02:45:16.766503+00', '2026-08-17 02:45:16.766503+00');
INSERT INTO public.network_policy_templates (id, slug, name, description, kind, spec_template, enabled, created_by, created_at, updated_at) VALUES ('8ff3adc5-ab38-4929-b7e0-a40bf475842d', 'namespace_only', 'Namespace only', 'Only allow ingress from pods in the same namespace.', 'builtin', 'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: namespace_only
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector: {}
', true, NULL, '2026-08-17 02:45:16.766503+00', '2026-08-17 02:45:16.766503+00');
INSERT INTO public.network_policy_templates (id, slug, name, description, kind, spec_template, enabled, created_by, created_at, updated_at) VALUES ('96095dd2-fd8d-4dc7-bab4-8fc2439420c8', 'allow_ingress_controllers', 'Allow ingress controllers', 'Permit traffic only from common ingress controllers (nginx, traefik). Egress unrestricted.', 'builtin', 'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: allow_ingress_controllers
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
  ingress:
    - from:
        - namespaceSelector:
            matchExpressions:
              - {key: kubernetes.io/metadata.name, operator: In, values: [ingress-nginx, traefik]}
  egress:
    - {}
', true, NULL, '2026-08-17 02:45:16.766503+00', '2026-08-17 02:45:16.766503+00');


--
-- Data for Name: notification_channels; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: notification_templates; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: operation_idempotency_keys; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: password_reset_tokens; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: platform_configuration; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.platform_configuration (id, server_url, platform_name, telemetry_enabled, bootstrapped_at, instance_id, default_cluster_template_id) VALUES (1, '', 'Astronomer', false, NULL, 'db97a1ae-a892-43ff-9f6e-6472149bd900', '9c13ee16-6c2d-4624-adab-bca25cb351f5');


--
-- Data for Name: platform_settings; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('branding.product_name', '"Astronomer"', 'Product display name shown in the header and tab title', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('branding.logo_url', '""', 'URL of the logo PNG/SVG; empty string falls back to the built-in mark', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('branding.primary_color', '"#0066CC"', 'Primary brand color (hex); applied as a CSS variable across the SPA', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('branding.support_url', '""', 'Link rendered in the in-app help menu; empty = hide the menu entry', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('branding.copyright', '""', 'Footer copyright text; empty = hide the footer line', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('banner.login_text', '""', 'Pre-login banner text; markdown supported. Empty = no banner', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('banner.global_text', '""', 'Persistent in-app banner text; markdown supported. Empty = no banner', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('banner.global_color', '"info"', 'Banner severity: info | warning | critical', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('feature.catalog', 'true', 'Helm chart catalog tab', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('feature.projects', 'true', 'Projects (multi-tenancy) tab', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('feature.monitoring', 'true', 'Cluster monitoring tab', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('feature.security', 'true', 'Security / CIS scans tab', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('feature.backups', 'true', 'Backup and restore tab', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('token.default_ttl_min', '60', 'API token default expiry in minutes; 0 = no expiry', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('token.max_ttl_min', '525600', 'Maximum allowed API token expiry in minutes (1 year default)', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('telemetry.enabled', 'false', 'Opt-in: send anonymized aggregate telemetry nightly', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('telemetry.endpoint', '"https://telemetry.alphabravo.io/astronomer"', 'HTTPS endpoint that anonymized telemetry POSTs land at', NULL, '2026-08-17 02:45:16.526187+00', '2026-08-17 02:45:16.526187+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('feature.charlie', 'false', 'Charlie SRE assistant integration', NULL, '2026-08-17 02:45:17.260934+00', '2026-08-17 02:45:17.260934+00');
INSERT INTO public.platform_settings (key, value, description, updated_by, updated_at, created_at) VALUES ('feature.delivery', 'true', 'Flux-native continuous delivery', NULL, '2026-08-17 04:02:51.627399+00', '2026-08-17 04:02:51.627399+00');


--
-- Data for Name: pod_security_templates; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.pod_security_templates (id, name, description, is_default, enforce_level, enforce_version, audit_level, audit_version, warn_level, warn_version, exempt_usernames, exempt_runtime_classes, exempt_namespaces, created_by_id, created_at, updated_at, is_builtin) VALUES ('e13f70c4-2ce2-4034-a7d4-efac1763a6e6', 'Privileged (PSA off)', 'Built-in starter template. The unrestricted Pod Security Standard — no restrictions on pod capabilities. Use for trusted/system namespaces or to explicitly opt a namespace out of PSA. Delivered but not applied to any cluster.', false, 'privileged', 'latest', 'privileged', 'latest', 'privileged', 'latest', '[]', '[]', '["kube-system", "kube-node-lease"]', NULL, '2026-08-17 02:45:17.023872+00', '2026-08-17 02:45:17.023872+00', true);
INSERT INTO public.pod_security_templates (id, name, description, is_default, enforce_level, enforce_version, audit_level, audit_version, warn_level, warn_version, exempt_usernames, exempt_runtime_classes, exempt_namespaces, created_by_id, created_at, updated_at, is_builtin) VALUES ('4b603727-437a-4391-9fc0-f319367df0d6', 'Baseline', 'Built-in starter template. Enforces the Baseline Pod Security Standard (blocks known privilege escalations, broadly compatible) while auditing and warning against the stricter Restricted standard. A safe first step. Delivered but not applied to any cluster.', true, 'baseline', 'latest', 'restricted', 'latest', 'restricted', 'latest', '[]', '[]', '["kube-system", "kube-node-lease"]', NULL, '2026-08-17 02:45:17.023872+00', '2026-08-17 02:45:17.023872+00', true);
INSERT INTO public.pod_security_templates (id, name, description, is_default, enforce_level, enforce_version, audit_level, audit_version, warn_level, warn_version, exempt_usernames, exempt_runtime_classes, exempt_namespaces, created_by_id, created_at, updated_at, is_builtin) VALUES ('dbc398d0-67c2-4af9-ad45-28ec194bcb4e', 'Restricted', 'Built-in starter template. Enforces, audits, and warns at the Restricted Pod Security Standard — the hardened policy following current pod-hardening best practices. Recommended for production workloads. Delivered but not applied to any cluster.', false, 'restricted', 'latest', 'restricted', 'latest', 'restricted', 'latest', '[]', '[]', '["kube-system", "kube-node-lease"]', NULL, '2026-08-17 02:45:17.023872+00', '2026-08-17 02:45:17.023872+00', true);


--
-- Data for Name: project_catalog_subscriptions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: project_namespaces; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: project_role_bindings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: project_roles; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('cc1d4054-937b-497d-99ef-661a2f778b28', 'Project Owner', 'Full access within a project scope', '{}', '[{"verbs": ["*"], "resource": "*"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:16.088087+00', '');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('7ba0c359-dc4a-4e92-92cf-c8b78abceca1', 'Project Member', 'Can manage workloads within a project', '{}', '[{"verbs": ["read", "list", "create", "update", "delete", "scale", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch"], "resource": "pods"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:16.088087+00', '');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('c504e273-255c-4c0e-97e5-fe109091daad', 'Project Troubleshooter', 'Inspect workloads and use pod-level diagnostics within a project', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs", "exec", "proxy"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}]', true, '2026-08-17 02:45:16.426416+00', '2026-08-17 02:45:16.426416+00', '');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('a757f37d-0178-43a6-b5f0-c056354f98d5', 'Project Deployer', 'Create and update workloads within a project; cannot delete or scale', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["create", "read", "update", "list", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('4d53251d-1a53-423c-b0c5-0b84427c7967', 'Project Auditor', 'Read-only visibility into project workloads, pods, and recent logs; no exec', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}]', true, '2026-08-17 02:45:16.449705+00', '2026-08-17 02:45:16.449705+00', '');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('bb808135-e330-4a46-8e8a-1b78e390aae8', 'Secret Manager', 'Manage Kubernetes Secret objects within a project', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "secrets"}, {"verbs": ["read", "list", "restart"], "resource": "workloads"}, {"verbs": ["read", "list"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Secret Manager');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('a9160e3f-c854-4eba-bac3-d54791598945', 'Namespace Operator', 'Manage namespace-level labels, annotations, quotas, limit ranges, and network-policy templates within a project', '{}', '[{"verbs": ["read", "update"], "resource": "projects"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "network_policies"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Namespace Operator');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('d4fa7fcc-06e0-4e47-aea2-2c7c60db1196', 'Config Manager', 'Manage non-secret configuration objects in a project scope', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "configmaps"}, {"verbs": ["read", "list", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Config Manager');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('bff6ab60-8956-4b2e-8222-07e90b48daad', 'Service and Ingress Manager', 'Manage Services, Ingresses, Gateway-style entry points, and service proxy exposure within a project', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["create", "read", "update", "delete", "list", "proxy"], "resource": "services"}, {"verbs": ["create", "read", "update", "delete", "list"], "resource": "ingresses"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 02:45:16.964558+00', 'Service and Ingress Manager');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('d0a9d32f-870f-4e04-9f62-ff613c4e27f6', 'Project Viewer', 'Read-only access within a project', '{}', '[{"verbs": ["read", "list", "watch"], "resource": "*"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.088087+00', '2026-08-17 02:45:17.237733+00', '');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('ff51b963-58e0-436c-ace3-58e8b1c198e9', 'GitOps Deployer', 'Trigger and inspect immutable Flux-native delivery rollouts for a project', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "delivery_sources"}, {"verbs": ["create", "read", "list"], "resource": "delivery_bundles"}, {"verbs": ["create", "read", "update", "list"], "resource": "delivery_targets"}, {"verbs": ["create", "read", "update", "list", "watch"], "resource": "delivery_rollouts"}, {"verbs": ["read", "update", "list", "watch"], "resource": "delivery_deployments"}, {"verbs": ["read", "list"], "resource": "delivery_inventory"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 04:02:51.631561+00', 'GitOps Deployer');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('9f2e4d5e-63a1-4b1e-b056-6d87b14795ae', 'Project Operator', 'Operate workloads within a project without full project administration', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["create", "read", "update", "delete", "list", "scale", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs", "exec", "proxy"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}, {"verbs": ["read", "list"], "resource": "delivery_sources"}, {"verbs": ["read", "list"], "resource": "delivery_bundles"}, {"verbs": ["read", "list"], "resource": "delivery_targets"}, {"verbs": ["read", "list", "watch"], "resource": "delivery_rollouts"}, {"verbs": ["read", "list", "watch"], "resource": "delivery_deployments"}, {"verbs": ["read", "list"], "resource": "delivery_inventory"}]', true, '2026-08-17 02:45:16.426416+00', '2026-08-17 04:02:51.633864+00', '');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('33e5256a-fb9d-49ee-bbf2-9914fd64b2b1', 'Workload Viewer', 'Read-only access to workloads, pods, logs, metrics, and GitOps state within a project', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["read", "list"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 04:02:51.636069+00', 'Workload Viewer');
INSERT INTO public.project_roles (id, name, description, permissions, rules, is_builtin, created_at, updated_at, display_name) VALUES ('a8f4fa75-c641-4538-adba-2050f50a6649', 'Workload Deployer', 'Create, update, scale, restart, and observe workloads within a project', '{}', '[{"verbs": ["read"], "resource": "projects"}, {"verbs": ["create", "read", "update", "list", "scale", "restart"], "resource": "workloads"}, {"verbs": ["read", "list", "watch", "logs"], "resource": "pods"}, {"verbs": ["read", "list"], "resource": "monitoring"}]', true, '2026-08-17 02:45:16.964558+00', '2026-08-17 04:02:51.636069+00', 'Workload Deployer');


--
-- Data for Name: projects; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: prometheus_datasources; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: quota_plans; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.quota_plans (name, enforcement, description, max_clusters_per_project, max_namespaces_per_project, max_members_per_project, max_projects_per_user, max_tokens_per_user, max_streams_per_user, max_total_clusters, max_total_users, created_at, updated_at) VALUES ('free', 'hard', 'Free tier - small footprint', 5, 10, 10, 3, 5, 3, 0, 0, '2026-08-17 02:45:16.59341+00', '2026-08-17 02:45:16.59341+00');
INSERT INTO public.quota_plans (name, enforcement, description, max_clusters_per_project, max_namespaces_per_project, max_members_per_project, max_projects_per_user, max_tokens_per_user, max_streams_per_user, max_total_clusters, max_total_users, created_at, updated_at) VALUES ('team', 'hard', 'Team tier - moderate fleet', 20, 50, 50, 10, 20, 5, 0, 0, '2026-08-17 02:45:16.59341+00', '2026-08-17 02:45:16.59341+00');
INSERT INTO public.quota_plans (name, enforcement, description, max_clusters_per_project, max_namespaces_per_project, max_members_per_project, max_projects_per_user, max_tokens_per_user, max_streams_per_user, max_total_clusters, max_total_users, created_at, updated_at) VALUES ('enterprise', 'soft', 'Enterprise tier - generous defaults, alerts only', 0, 0, 0, 0, 0, 0, 0, 0, '2026-08-17 02:45:16.59341+00', '2026-08-17 02:45:16.59341+00');
INSERT INTO public.quota_plans (name, enforcement, description, max_clusters_per_project, max_namespaces_per_project, max_members_per_project, max_projects_per_user, max_tokens_per_user, max_streams_per_user, max_total_clusters, max_total_users, created_at, updated_at) VALUES ('global', 'hard', 'Singleton fleet-wide cap', 0, 0, 0, 0, 0, 0, 0, 0, '2026-08-17 02:45:16.59341+00', '2026-08-17 02:45:16.59341+00');


--
-- Data for Name: read_audit_policies; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('987d0a3f-417e-4fc2-9227-217ab01512c9', 'cloud_credentials_read', 'Reads of cloud_credentials rows surface or expose AWS/GCP/Azure secrets.', '/projects/*/cloud-credentials', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');
INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('afbadb26-4bf1-4948-a1c2-84ca8a2ecb0b', 'registry_credentials_read', 'Reads of cluster_registry rows surface dockerconfigjson contents.', '/clusters/*/registries', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');
INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('bdc71dbd-a76d-440c-b8db-809182b44007', 'sso_secrets_read', 'Reads of SSO connector secrets.', '/admin/sso', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');
INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('7a671528-6e9d-4b96-8cf0-8f20fe17e908', 'webhook_auth_read', 'Reads of webhook auth_encrypted blobs.', '/admin/webhooks', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');
INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('907c53d3-9b80-4d30-a489-ab61bca440c2', 'siem_auth_read', 'Reads of SIEM forwarder auth blobs.', '/admin/siem-forwarders', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');
INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('e6836149-e300-4ac9-88d9-99f1fb3e7dde', 'audit_log_read', 'Reads of the audit log itself.', '/audit', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');
INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('acda9ea9-7f97-4264-84a9-60b5e97b7a8c', 'support_bundle_download', 'Support bundle downloads.', '/support-bundle', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');
INSERT INTO public.read_audit_policies (id, name, description, path_pattern, verbs, sample_rate, enabled, created_by, created_at, updated_at) VALUES ('47229441-0c27-4195-bb0b-e478ccd85062', 'admin_settings_read', 'Reads of platform_configuration / SMTP / branding.', '/admin/settings', 'GET', 1.00, true, NULL, '2026-08-17 02:45:16.716342+00', '2026-08-17 02:45:16.716342+00');


--
-- Data for Name: repair_job_states; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: restore_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: scim_tokens; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: security_scan_results; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: siem_forward_queue; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: siem_forwarder_status; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: siem_forwarders; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: smtp_settings; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: sso_configurations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: sso_sessions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: task_outbox; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: tool_operation_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: tool_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: tunnel_locator_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: ui_extensions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: user_idp_groups; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: user_totp_enrollments; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: user_totp_recovery_codes; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: vault_connections; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: webhook_deliveries; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: webhook_subscriptions; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: workload_operation_events; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: workload_operations; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Data for Name: xcluster_anomaly_baselines; Type: TABLE DATA; Schema: public; Owner: -
--



--
-- Name: apiserver_allowlist_snapshots_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.apiserver_allowlist_snapshots_id_seq', 1, false);


--
-- Name: delivery_system_releases_release_sequence_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.delivery_system_releases_release_sequence_seq', 1, false);


--
-- Name: kubectl_session_commands_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.kubectl_session_commands_id_seq', 1, false);


--
-- Name: siem_forward_queue_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.siem_forward_queue_id_seq', 1, false);


--
-- Name: agent_connection_events agent_connection_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_connection_events
    ADD CONSTRAINT agent_connection_events_pkey PRIMARY KEY (id);


--
-- Name: agent_connections agent_connections_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_connections
    ADD CONSTRAINT agent_connections_pkey PRIMARY KEY (id);


--
-- Name: agent_lifecycle_operations agent_lifecycle_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_lifecycle_operations
    ADD CONSTRAINT agent_lifecycle_operations_pkey PRIMARY KEY (id);


--
-- Name: agent_operational_statuses agent_operational_statuses_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_operational_statuses
    ADD CONSTRAINT agent_operational_statuses_pkey PRIMARY KEY (cluster_id);


--
-- Name: alert_events alert_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_pkey PRIMARY KEY (id);


--
-- Name: alert_inhibitions alert_inhibitions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_inhibitions
    ADD CONSTRAINT alert_inhibitions_pkey PRIMARY KEY (id);


--
-- Name: alert_rule_channels alert_rule_channels_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rule_channels
    ADD CONSTRAINT alert_rule_channels_pkey PRIMARY KEY (alert_rule_id, notification_channel_id);


--
-- Name: alert_rules alert_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_pkey PRIMARY KEY (id);


--
-- Name: alert_silences alert_silences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_silences
    ADD CONSTRAINT alert_silences_pkey PRIMARY KEY (id);


--
-- Name: anomaly_baselines anomaly_baselines_cluster_id_metric_name_window_seconds_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.anomaly_baselines
    ADD CONSTRAINT anomaly_baselines_cluster_id_metric_name_window_seconds_key UNIQUE (cluster_id, metric_name, window_seconds);


--
-- Name: anomaly_baselines anomaly_baselines_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.anomaly_baselines
    ADD CONSTRAINT anomaly_baselines_pkey PRIMARY KEY (id);


--
-- Name: api_tokens api_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_tokens
    ADD CONSTRAINT api_tokens_pkey PRIMARY KEY (id);


--
-- Name: api_tokens api_tokens_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_tokens
    ADD CONSTRAINT api_tokens_token_hash_key UNIQUE (token_hash);


--
-- Name: apiserver_allowlist_snapshots apiserver_allowlist_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_allowlist_snapshots
    ADD CONSTRAINT apiserver_allowlist_snapshots_pkey PRIMARY KEY (id);


--
-- Name: apiserver_allowlists apiserver_allowlists_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_allowlists
    ADD CONSTRAINT apiserver_allowlists_pkey PRIMARY KEY (cluster_id);


--
-- Name: apiserver_audit_events apiserver_audit_events_cluster_id_audit_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_audit_events
    ADD CONSTRAINT apiserver_audit_events_cluster_id_audit_id_key UNIQUE (cluster_id, audit_id);


--
-- Name: apiserver_audit_events apiserver_audit_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_audit_events
    ADD CONSTRAINT apiserver_audit_events_pkey PRIMARY KEY (id);


--
-- Name: audit_archive audit_archive_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_archive
    ADD CONSTRAINT audit_archive_pkey PRIMARY KEY (id, created_at);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id, created_at);


--
-- Name: audit_log_default audit_log_default_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log_default
    ADD CONSTRAINT audit_log_default_pkey PRIMARY KEY (id, created_at);


--
-- Name: authored_constraints authored_constraints_cluster_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.authored_constraints
    ADD CONSTRAINT authored_constraints_cluster_id_name_key UNIQUE (cluster_id, name);


--
-- Name: authored_constraints authored_constraints_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.authored_constraints
    ADD CONSTRAINT authored_constraints_pkey PRIMARY KEY (id);


--
-- Name: backup_drill_results backup_drill_results_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_drill_results
    ADD CONSTRAINT backup_drill_results_pkey PRIMARY KEY (id);


--
-- Name: backup_schedules backup_schedules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_schedules
    ADD CONSTRAINT backup_schedules_pkey PRIMARY KEY (id);


--
-- Name: backup_storage_configs backup_storage_configs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_storage_configs
    ADD CONSTRAINT backup_storage_configs_pkey PRIMARY KEY (id);


--
-- Name: backups backups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backups
    ADD CONSTRAINT backups_pkey PRIMARY KEY (id);


--
-- Name: catalog_blessed_charts catalog_blessed_charts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.catalog_blessed_charts
    ADD CONSTRAINT catalog_blessed_charts_pkey PRIMARY KEY (id);


--
-- Name: catalog_blessed_charts catalog_blessed_charts_repo_url_chart_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.catalog_blessed_charts
    ADD CONSTRAINT catalog_blessed_charts_repo_url_chart_name_key UNIQUE (repo_url, chart_name);


--
-- Name: catalog_operation_events catalog_operation_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.catalog_operation_events
    ADD CONSTRAINT catalog_operation_events_pkey PRIMARY KEY (id);


--
-- Name: catalog_operations catalog_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.catalog_operations
    ADD CONSTRAINT catalog_operations_pkey PRIMARY KEY (id);


--
-- Name: charlie_action_approvals charlie_action_approvals_approval_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_approvals
    ADD CONSTRAINT charlie_action_approvals_approval_id_key UNIQUE (approval_id);


--
-- Name: charlie_action_approvals charlie_action_approvals_charlie_action_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_approvals
    ADD CONSTRAINT charlie_action_approvals_charlie_action_id_key UNIQUE (charlie_action_id);


--
-- Name: charlie_action_approvals charlie_action_approvals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_approvals
    ADD CONSTRAINT charlie_action_approvals_pkey PRIMARY KEY (id);


--
-- Name: charlie_action_deferrals charlie_action_deferrals_charlie_action_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_deferrals
    ADD CONSTRAINT charlie_action_deferrals_charlie_action_id_key UNIQUE (charlie_action_id);


--
-- Name: charlie_action_deferrals charlie_action_deferrals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_deferrals
    ADD CONSTRAINT charlie_action_deferrals_pkey PRIMARY KEY (id);


--
-- Name: charlie_action_receipts charlie_action_receipts_charlie_action_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_receipts
    ADD CONSTRAINT charlie_action_receipts_charlie_action_id_key UNIQUE (charlie_action_id);


--
-- Name: charlie_action_receipts charlie_action_receipts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_receipts
    ADD CONSTRAINT charlie_action_receipts_pkey PRIMARY KEY (id);


--
-- Name: charlie_action_receipts charlie_action_receipts_product_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_receipts
    ADD CONSTRAINT charlie_action_receipts_product_idempotency_key_key UNIQUE (product_idempotency_key);


--
-- Name: charlie_alert_deliveries charlie_alert_deliveries_finding_id_notification_channel_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_deliveries
    ADD CONSTRAINT charlie_alert_deliveries_finding_id_notification_channel_id_key UNIQUE (finding_id, notification_channel_id, delivery_kind, dedupe_bucket);


--
-- Name: charlie_alert_deliveries charlie_alert_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_deliveries
    ADD CONSTRAINT charlie_alert_deliveries_pkey PRIMARY KEY (id);


--
-- Name: charlie_alert_policies charlie_alert_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_policies
    ADD CONSTRAINT charlie_alert_policies_pkey PRIMARY KEY (connection_id);


--
-- Name: charlie_alert_policy_channels charlie_alert_policy_channels_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_policy_channels
    ADD CONSTRAINT charlie_alert_policy_channels_pkey PRIMARY KEY (connection_id, notification_channel_id);


--
-- Name: charlie_action_approvals charlie_approval_decision_request_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_approvals
    ADD CONSTRAINT charlie_approval_decision_request_unique UNIQUE (connection_id, decision_request_id);


--
-- Name: charlie_artifact_credential_state charlie_artifact_credential_state_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_artifact_credential_state
    ADD CONSTRAINT charlie_artifact_credential_state_pkey PRIMARY KEY (connection_id);


--
-- Name: charlie_automation_policies charlie_automation_policies_connection_id_capability_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_automation_policies
    ADD CONSTRAINT charlie_automation_policies_connection_id_capability_key UNIQUE (connection_id, capability);


--
-- Name: charlie_automation_policies charlie_automation_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_automation_policies
    ADD CONSTRAINT charlie_automation_policies_pkey PRIMARY KEY (id);


--
-- Name: charlie_connections charlie_connections_onboarding_package_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_connections
    ADD CONSTRAINT charlie_connections_onboarding_package_id_key UNIQUE (onboarding_package_id);


--
-- Name: charlie_connections charlie_connections_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_connections
    ADD CONSTRAINT charlie_connections_pkey PRIMARY KEY (id);


--
-- Name: charlie_delegations charlie_delegations_authorization_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_delegations
    ADD CONSTRAINT charlie_delegations_authorization_hash_key UNIQUE (authorization_hash);


--
-- Name: charlie_delegations charlie_delegations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_delegations
    ADD CONSTRAINT charlie_delegations_pkey PRIMARY KEY (id);


--
-- Name: charlie_finding_decisions charlie_finding_decisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_finding_decisions
    ADD CONSTRAINT charlie_finding_decisions_pkey PRIMARY KEY (request_id);


--
-- Name: charlie_finding_projection_cursors charlie_finding_projection_cursors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_finding_projection_cursors
    ADD CONSTRAINT charlie_finding_projection_cursors_pkey PRIMARY KEY (connection_id);


--
-- Name: charlie_finding_resources charlie_finding_resources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_finding_resources
    ADD CONSTRAINT charlie_finding_resources_pkey PRIMARY KEY (finding_id, resource_type, resource_id, required_verb);


--
-- Name: charlie_findings charlie_findings_approval_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_approval_id_key UNIQUE (approval_id);


--
-- Name: charlie_findings charlie_findings_connection_id_charlie_finding_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_connection_id_charlie_finding_id_key UNIQUE (connection_id, charlie_finding_id);


--
-- Name: charlie_findings charlie_findings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_pkey PRIMARY KEY (id);


--
-- Name: charlie_interactive_threads charlie_interactive_threads_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_interactive_threads
    ADD CONSTRAINT charlie_interactive_threads_pkey PRIMARY KEY (id);


--
-- Name: charlie_session_resources charlie_session_resources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_session_resources
    ADD CONSTRAINT charlie_session_resources_pkey PRIMARY KEY (session_id, resource_type, resource_id, required_verb);


--
-- Name: charlie_session_resources charlie_session_resources_session_id_resource_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_session_resources
    ADD CONSTRAINT charlie_session_resources_session_id_resource_id_key UNIQUE (session_id, resource_id);


--
-- Name: charlie_sessions charlie_sessions_connection_id_client_session_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_sessions
    ADD CONSTRAINT charlie_sessions_connection_id_client_session_id_key UNIQUE (connection_id, client_session_id);


--
-- Name: charlie_sessions charlie_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_sessions
    ADD CONSTRAINT charlie_sessions_pkey PRIMARY KEY (id);


--
-- Name: charlie_thread_sessions charlie_thread_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_thread_sessions
    ADD CONSTRAINT charlie_thread_sessions_pkey PRIMARY KEY (thread_id, session_id);


--
-- Name: charlie_thread_sessions charlie_thread_sessions_thread_id_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_thread_sessions
    ADD CONSTRAINT charlie_thread_sessions_thread_id_sequence_key UNIQUE (thread_id, sequence);


--
-- Name: charlie_trigger_events charlie_trigger_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_events
    ADD CONSTRAINT charlie_trigger_events_pkey PRIMARY KEY (id);


--
-- Name: charlie_trigger_rules charlie_trigger_rules_connection_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_rules
    ADD CONSTRAINT charlie_trigger_rules_connection_id_name_key UNIQUE (connection_id, name);


--
-- Name: charlie_trigger_rules charlie_trigger_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_rules
    ADD CONSTRAINT charlie_trigger_rules_pkey PRIMARY KEY (id);


--
-- Name: chart_co_installation chart_co_installation_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_co_installation
    ADD CONSTRAINT chart_co_installation_pkey PRIMARY KEY (chart_a_id, chart_b_id);


--
-- Name: chart_rating_aggregates chart_rating_aggregates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_rating_aggregates
    ADD CONSTRAINT chart_rating_aggregates_pkey PRIMARY KEY (chart_id);


--
-- Name: chart_ratings chart_ratings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_ratings
    ADD CONSTRAINT chart_ratings_pkey PRIMARY KEY (id);


--
-- Name: chart_ratings chart_ratings_user_id_installation_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_ratings
    ADD CONSTRAINT chart_ratings_user_id_installation_id_key UNIQUE (user_id, installation_id);


--
-- Name: cloud_credential_materializations cloud_credential_materializat_credential_id_cluster_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credential_materializations
    ADD CONSTRAINT cloud_credential_materializat_credential_id_cluster_id_name_key UNIQUE (credential_id, cluster_id, namespace);


--
-- Name: cloud_credential_materializations cloud_credential_materializations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credential_materializations
    ADD CONSTRAINT cloud_credential_materializations_pkey PRIMARY KEY (id);


--
-- Name: cloud_credentials cloud_credentials_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credentials
    ADD CONSTRAINT cloud_credentials_pkey PRIMARY KEY (id);


--
-- Name: cloud_credentials cloud_credentials_project_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credentials
    ADD CONSTRAINT cloud_credentials_project_id_name_key UNIQUE (project_id, name);


--
-- Name: cluster_agent_tokens cluster_agent_tokens_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_agent_tokens
    ADD CONSTRAINT cluster_agent_tokens_cluster_id_key UNIQUE (cluster_id);


--
-- Name: cluster_agent_tokens cluster_agent_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_agent_tokens
    ADD CONSTRAINT cluster_agent_tokens_pkey PRIMARY KEY (id);


--
-- Name: cluster_condition_remediation_attempts cluster_condition_remediation_attempts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_condition_remediation_attempts
    ADD CONSTRAINT cluster_condition_remediation_attempts_pkey PRIMARY KEY (id);


--
-- Name: cluster_conditions cluster_conditions_cluster_id_type_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_conditions
    ADD CONSTRAINT cluster_conditions_cluster_id_type_key UNIQUE (cluster_id, type);


--
-- Name: cluster_conditions cluster_conditions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_conditions
    ADD CONSTRAINT cluster_conditions_pkey PRIMARY KEY (id);


--
-- Name: cluster_decommissions cluster_decommissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_decommissions
    ADD CONSTRAINT cluster_decommissions_pkey PRIMARY KEY (id);


--
-- Name: cluster_deployment_events cluster_deployment_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployment_events
    ADD CONSTRAINT cluster_deployment_events_pkey PRIMARY KEY (id);


--
-- Name: cluster_deployments cluster_deployments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_pkey PRIMARY KEY (id);


--
-- Name: cluster_deployments cluster_deployments_target_id_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_target_id_cluster_id_key UNIQUE (target_id, cluster_id);


--
-- Name: cluster_groups cluster_groups_parent_id_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_groups
    ADD CONSTRAINT cluster_groups_parent_id_slug_key UNIQUE (parent_id, slug);


--
-- Name: cluster_groups cluster_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_groups
    ADD CONSTRAINT cluster_groups_pkey PRIMARY KEY (id);


--
-- Name: cluster_health_statuses cluster_health_statuses_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_health_statuses
    ADD CONSTRAINT cluster_health_statuses_cluster_id_key UNIQUE (cluster_id);


--
-- Name: cluster_health_statuses cluster_health_statuses_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_health_statuses
    ADD CONSTRAINT cluster_health_statuses_pkey PRIMARY KEY (id);


--
-- Name: cluster_monitoring_configs cluster_monitoring_configs_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_monitoring_configs
    ADD CONSTRAINT cluster_monitoring_configs_cluster_id_key UNIQUE (cluster_id);


--
-- Name: cluster_monitoring_configs cluster_monitoring_configs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_monitoring_configs
    ADD CONSTRAINT cluster_monitoring_configs_pkey PRIMARY KEY (id);


--
-- Name: cluster_registration_policies cluster_registration_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registration_policies
    ADD CONSTRAINT cluster_registration_policies_pkey PRIMARY KEY (cluster_id);


--
-- Name: cluster_registration_steps cluster_registration_steps_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registration_steps
    ADD CONSTRAINT cluster_registration_steps_pkey PRIMARY KEY (id);


--
-- Name: cluster_registration_tokens cluster_registration_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registration_tokens
    ADD CONSTRAINT cluster_registration_tokens_pkey PRIMARY KEY (id);


--
-- Name: cluster_registry_configs cluster_registry_configs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registry_configs
    ADD CONSTRAINT cluster_registry_configs_pkey PRIMARY KEY (id);


--
-- Name: cluster_restores cluster_restores_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_restores
    ADD CONSTRAINT cluster_restores_pkey PRIMARY KEY (id);


--
-- Name: cluster_role_bindings cluster_role_bindings_group_role_id_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_role_bindings
    ADD CONSTRAINT cluster_role_bindings_group_role_id_cluster_id_key UNIQUE ("group", role_id, cluster_id);


--
-- Name: cluster_role_bindings cluster_role_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_role_bindings
    ADD CONSTRAINT cluster_role_bindings_pkey PRIMARY KEY (id);


--
-- Name: cluster_role_bindings cluster_role_bindings_user_id_role_id_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_role_bindings
    ADD CONSTRAINT cluster_role_bindings_user_id_role_id_cluster_id_key UNIQUE (user_id, role_id, cluster_id);


--
-- Name: cluster_roles cluster_roles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_roles
    ADD CONSTRAINT cluster_roles_pkey PRIMARY KEY (id);


--
-- Name: cluster_security_policies cluster_security_policies_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_security_policies
    ADD CONSTRAINT cluster_security_policies_cluster_id_key UNIQUE (cluster_id);


--
-- Name: cluster_security_policies cluster_security_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_security_policies
    ADD CONSTRAINT cluster_security_policies_pkey PRIMARY KEY (id);


--
-- Name: cluster_service_mesh cluster_service_mesh_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_service_mesh
    ADD CONSTRAINT cluster_service_mesh_pkey PRIMARY KEY (cluster_id);


--
-- Name: cluster_snapshot_schedules cluster_snapshot_schedules_cluster_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_snapshot_schedules
    ADD CONSTRAINT cluster_snapshot_schedules_cluster_id_name_key UNIQUE (cluster_id, name);


--
-- Name: cluster_snapshot_schedules cluster_snapshot_schedules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_snapshot_schedules
    ADD CONSTRAINT cluster_snapshot_schedules_pkey PRIMARY KEY (id);


--
-- Name: cluster_snapshots cluster_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_snapshots
    ADD CONSTRAINT cluster_snapshots_pkey PRIMARY KEY (id);


--
-- Name: cluster_template_applications cluster_template_applications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_template_applications
    ADD CONSTRAINT cluster_template_applications_pkey PRIMARY KEY (cluster_id);


--
-- Name: cluster_templates cluster_templates_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_templates
    ADD CONSTRAINT cluster_templates_name_key UNIQUE (name);


--
-- Name: cluster_templates cluster_templates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_templates
    ADD CONSTRAINT cluster_templates_pkey PRIMARY KEY (id);


--
-- Name: cluster_tools cluster_tools_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_tools
    ADD CONSTRAINT cluster_tools_pkey PRIMARY KEY (id);


--
-- Name: cluster_tools cluster_tools_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_tools
    ADD CONSTRAINT cluster_tools_slug_key UNIQUE (slug);


--
-- Name: clusters clusters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clusters
    ADD CONSTRAINT clusters_pkey PRIMARY KEY (id);


--
-- Name: compliance_baseline_applications compliance_baseline_applications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compliance_baseline_applications
    ADD CONSTRAINT compliance_baseline_applications_pkey PRIMARY KEY (id);


--
-- Name: compliance_baselines compliance_baselines_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compliance_baselines
    ADD CONSTRAINT compliance_baselines_pkey PRIMARY KEY (id);


--
-- Name: compliance_baselines compliance_baselines_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compliance_baselines
    ADD CONSTRAINT compliance_baselines_slug_key UNIQUE (slug);


--
-- Name: component_bundle_versions component_bundle_versions_bundle_id_spec_digest_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundle_versions
    ADD CONSTRAINT component_bundle_versions_bundle_id_spec_digest_key UNIQUE (bundle_id, spec_digest);


--
-- Name: component_bundle_versions component_bundle_versions_bundle_id_version_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundle_versions
    ADD CONSTRAINT component_bundle_versions_bundle_id_version_key UNIQUE (bundle_id, version);


--
-- Name: component_bundle_versions component_bundle_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundle_versions
    ADD CONSTRAINT component_bundle_versions_pkey PRIMARY KEY (id);


--
-- Name: component_bundles component_bundles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundles
    ADD CONSTRAINT component_bundles_pkey PRIMARY KEY (id);


--
-- Name: component_bundles component_bundles_project_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundles
    ADD CONSTRAINT component_bundles_project_id_name_key UNIQUE (project_id, name);


--
-- Name: control_plane_alerts control_plane_alerts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_alerts
    ADD CONSTRAINT control_plane_alerts_pkey PRIMARY KEY (id);


--
-- Name: control_plane_policies control_plane_policies_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_policies
    ADD CONSTRAINT control_plane_policies_name_key UNIQUE (name);


--
-- Name: control_plane_policies control_plane_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_policies
    ADD CONSTRAINT control_plane_policies_pkey PRIMARY KEY (id);


--
-- Name: control_plane_silences control_plane_silences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_silences
    ADD CONSTRAINT control_plane_silences_pkey PRIMARY KEY (id);


--
-- Name: control_plane_snapshots control_plane_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_snapshots
    ADD CONSTRAINT control_plane_snapshots_pkey PRIMARY KEY (id);


--
-- Name: dashboard_widgets dashboard_widgets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dashboard_widgets
    ADD CONSTRAINT dashboard_widgets_pkey PRIMARY KEY (id);


--
-- Name: deferred_operations deferred_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deferred_operations
    ADD CONSTRAINT deferred_operations_pkey PRIMARY KEY (id);


--
-- Name: delivery_assignment_receipts delivery_assignment_receipts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_assignment_receipts
    ADD CONSTRAINT delivery_assignment_receipts_pkey PRIMARY KEY (cluster_id);


--
-- Name: delivery_controller_inventory delivery_controller_inventory_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_controller_inventory
    ADD CONSTRAINT delivery_controller_inventory_pkey PRIMARY KEY (cluster_id);


--
-- Name: delivery_rollout_approvals delivery_rollout_approvals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_approvals
    ADD CONSTRAINT delivery_rollout_approvals_pkey PRIMARY KEY (id);


--
-- Name: delivery_rollout_clusters delivery_rollout_clusters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_clusters
    ADD CONSTRAINT delivery_rollout_clusters_pkey PRIMARY KEY (id);


--
-- Name: delivery_rollout_clusters delivery_rollout_clusters_rollout_id_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_clusters
    ADD CONSTRAINT delivery_rollout_clusters_rollout_id_cluster_id_key UNIQUE (rollout_id, cluster_id);


--
-- Name: delivery_rollout_events delivery_rollout_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_events
    ADD CONSTRAINT delivery_rollout_events_pkey PRIMARY KEY (id);


--
-- Name: delivery_rollouts delivery_rollouts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollouts
    ADD CONSTRAINT delivery_rollouts_pkey PRIMARY KEY (id);


--
-- Name: delivery_rollouts delivery_rollouts_target_id_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollouts
    ADD CONSTRAINT delivery_rollouts_target_id_idempotency_key_key UNIQUE (target_id, idempotency_key);


--
-- Name: delivery_source_resolutions delivery_source_resolutions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_source_resolutions
    ADD CONSTRAINT delivery_source_resolutions_pkey PRIMARY KEY (id);


--
-- Name: delivery_sources delivery_sources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_sources
    ADD CONSTRAINT delivery_sources_pkey PRIMARY KEY (id);


--
-- Name: delivery_sources delivery_sources_project_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_sources
    ADD CONSTRAINT delivery_sources_project_id_name_key UNIQUE (project_id, name);


--
-- Name: delivery_system_cluster_assignments delivery_system_cluster_assignments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_cluster_assignments
    ADD CONSTRAINT delivery_system_cluster_assignments_pkey PRIMARY KEY (cluster_id);


--
-- Name: delivery_system_events delivery_system_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_events
    ADD CONSTRAINT delivery_system_events_pkey PRIMARY KEY (id);


--
-- Name: delivery_system_releases delivery_system_releases_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_releases
    ADD CONSTRAINT delivery_system_releases_pkey PRIMARY KEY (id);


--
-- Name: delivery_system_releases delivery_system_releases_release_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_releases
    ADD CONSTRAINT delivery_system_releases_release_sequence_key UNIQUE (release_sequence);


--
-- Name: delivery_system_releases delivery_system_releases_spec_digest_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_releases
    ADD CONSTRAINT delivery_system_releases_spec_digest_key UNIQUE (spec_digest);


--
-- Name: delivery_system_releases delivery_system_releases_version_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_releases
    ADD CONSTRAINT delivery_system_releases_version_key UNIQUE (version);


--
-- Name: delivery_system_rollouts delivery_system_rollouts_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_rollouts
    ADD CONSTRAINT delivery_system_rollouts_idempotency_key_key UNIQUE (idempotency_key);


--
-- Name: delivery_system_rollouts delivery_system_rollouts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_rollouts
    ADD CONSTRAINT delivery_system_rollouts_pkey PRIMARY KEY (id);


--
-- Name: delivery_targets delivery_targets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_targets
    ADD CONSTRAINT delivery_targets_pkey PRIMARY KEY (id);


--
-- Name: delivery_targets delivery_targets_project_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_targets
    ADD CONSTRAINT delivery_targets_project_id_name_key UNIQUE (project_id, name);


--
-- Name: dex_connectors dex_connectors_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dex_connectors
    ADD CONSTRAINT dex_connectors_name_key UNIQUE (name);


--
-- Name: dex_connectors dex_connectors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dex_connectors
    ADD CONSTRAINT dex_connectors_pkey PRIMARY KEY (id);


--
-- Name: dex_settings dex_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dex_settings
    ADD CONSTRAINT dex_settings_pkey PRIMARY KEY (id);


--
-- Name: email_messages email_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_messages
    ADD CONSTRAINT email_messages_pkey PRIMARY KEY (id);


--
-- Name: gitops_registered_clusters gitops_registered_clusters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gitops_registered_clusters
    ADD CONSTRAINT gitops_registered_clusters_pkey PRIMARY KEY (cluster_id);


--
-- Name: gitops_registration_sources gitops_registration_sources_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gitops_registration_sources
    ADD CONSTRAINT gitops_registration_sources_name_key UNIQUE (name);


--
-- Name: gitops_registration_sources gitops_registration_sources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gitops_registration_sources
    ADD CONSTRAINT gitops_registration_sources_pkey PRIMARY KEY (id);


--
-- Name: global_role_bindings global_role_bindings_group_role_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_role_bindings
    ADD CONSTRAINT global_role_bindings_group_role_id_key UNIQUE ("group", role_id);


--
-- Name: global_role_bindings global_role_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_role_bindings
    ADD CONSTRAINT global_role_bindings_pkey PRIMARY KEY (id);


--
-- Name: global_role_bindings global_role_bindings_user_id_role_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_role_bindings
    ADD CONSTRAINT global_role_bindings_user_id_role_id_key UNIQUE (user_id, role_id);


--
-- Name: global_roles global_roles_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_roles
    ADD CONSTRAINT global_roles_name_key UNIQUE (name);


--
-- Name: global_roles global_roles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_roles
    ADD CONSTRAINT global_roles_pkey PRIMARY KEY (id);


--
-- Name: helm_chart_tags helm_chart_tags_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_chart_tags
    ADD CONSTRAINT helm_chart_tags_pkey PRIMARY KEY (chart_id, tag);


--
-- Name: helm_chart_versions helm_chart_versions_chart_id_version_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_chart_versions
    ADD CONSTRAINT helm_chart_versions_chart_id_version_key UNIQUE (chart_id, version);


--
-- Name: helm_chart_versions helm_chart_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_chart_versions
    ADD CONSTRAINT helm_chart_versions_pkey PRIMARY KEY (id);


--
-- Name: helm_charts helm_charts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_charts
    ADD CONSTRAINT helm_charts_pkey PRIMARY KEY (id);


--
-- Name: helm_charts helm_charts_repository_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_charts
    ADD CONSTRAINT helm_charts_repository_id_name_key UNIQUE (repository_id, name);


--
-- Name: helm_repositories helm_repositories_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_repositories
    ADD CONSTRAINT helm_repositories_name_key UNIQUE (name);


--
-- Name: helm_repositories helm_repositories_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_repositories
    ADD CONSTRAINT helm_repositories_pkey PRIMARY KEY (id);


--
-- Name: identity_group_mappings identity_group_mappings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.identity_group_mappings
    ADD CONSTRAINT identity_group_mappings_pkey PRIMARY KEY (id);


--
-- Name: image_vulnerabilities image_vulnerabilities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerabilities
    ADD CONSTRAINT image_vulnerabilities_pkey PRIMARY KEY (id);


--
-- Name: image_vulnerabilities image_vulnerabilities_report_id_vulnerability_id_pkg_name_i_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerabilities
    ADD CONSTRAINT image_vulnerabilities_report_id_vulnerability_id_pkg_name_i_key UNIQUE (report_id, vulnerability_id, pkg_name, installed_version);


--
-- Name: image_vulnerability_report_snapshots image_vulnerability_report_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerability_report_snapshots
    ADD CONSTRAINT image_vulnerability_report_snapshots_pkey PRIMARY KEY (id);


--
-- Name: image_vulnerability_reports image_vulnerability_reports_cluster_id_report_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerability_reports
    ADD CONSTRAINT image_vulnerability_reports_cluster_id_report_name_key UNIQUE (cluster_id, report_name);


--
-- Name: image_vulnerability_reports image_vulnerability_reports_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerability_reports
    ADD CONSTRAINT image_vulnerability_reports_pkey PRIMARY KEY (id);


--
-- Name: installed_charts installed_charts_cluster_id_release_name_namespace_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.installed_charts
    ADD CONSTRAINT installed_charts_cluster_id_release_name_namespace_key UNIQUE (cluster_id, release_name, namespace);


--
-- Name: installed_charts installed_charts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.installed_charts
    ADD CONSTRAINT installed_charts_pkey PRIMARY KEY (id);


--
-- Name: jwt_revocations jwt_revocations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jwt_revocations
    ADD CONSTRAINT jwt_revocations_pkey PRIMARY KEY (jti);


--
-- Name: kubectl_session_commands kubectl_session_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.kubectl_session_commands
    ADD CONSTRAINT kubectl_session_commands_pkey PRIMARY KEY (id);


--
-- Name: kubectl_sessions kubectl_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.kubectl_sessions
    ADD CONSTRAINT kubectl_sessions_pkey PRIMARY KEY (id);


--
-- Name: logging_operation_events logging_operation_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_operation_events
    ADD CONSTRAINT logging_operation_events_pkey PRIMARY KEY (id);


--
-- Name: logging_operations logging_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_operations
    ADD CONSTRAINT logging_operations_pkey PRIMARY KEY (id);


--
-- Name: logging_outputs logging_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_outputs
    ADD CONSTRAINT logging_outputs_pkey PRIMARY KEY (id);


--
-- Name: logging_pipeline_outputs logging_pipeline_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_pipeline_outputs
    ADD CONSTRAINT logging_pipeline_outputs_pkey PRIMARY KEY (logging_pipeline_id, logging_output_id);


--
-- Name: logging_pipelines logging_pipelines_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_pipelines
    ADD CONSTRAINT logging_pipelines_pkey PRIMARY KEY (id);


--
-- Name: maintenance_windows maintenance_windows_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.maintenance_windows
    ADD CONSTRAINT maintenance_windows_name_key UNIQUE (name);


--
-- Name: maintenance_windows maintenance_windows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.maintenance_windows
    ADD CONSTRAINT maintenance_windows_pkey PRIMARY KEY (id);


--
-- Name: mirrored_gateway_classes mirrored_gateway_classes_cluster_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_gateway_classes
    ADD CONSTRAINT mirrored_gateway_classes_cluster_id_name_key UNIQUE (cluster_id, name);


--
-- Name: mirrored_gateway_classes mirrored_gateway_classes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_gateway_classes
    ADD CONSTRAINT mirrored_gateway_classes_pkey PRIMARY KEY (id);


--
-- Name: mirrored_ingress_classes mirrored_ingress_classes_cluster_id_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_ingress_classes
    ADD CONSTRAINT mirrored_ingress_classes_cluster_id_name_key UNIQUE (cluster_id, name);


--
-- Name: mirrored_ingress_classes mirrored_ingress_classes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_ingress_classes
    ADD CONSTRAINT mirrored_ingress_classes_pkey PRIMARY KEY (id);


--
-- Name: mirrored_limit_ranges mirrored_limit_ranges_cluster_id_namespace_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_limit_ranges
    ADD CONSTRAINT mirrored_limit_ranges_cluster_id_namespace_name_key UNIQUE (cluster_id, namespace, name);


--
-- Name: mirrored_limit_ranges mirrored_limit_ranges_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_limit_ranges
    ADD CONSTRAINT mirrored_limit_ranges_pkey PRIMARY KEY (id);


--
-- Name: mirrored_network_policies mirrored_network_policies_cluster_id_namespace_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_network_policies
    ADD CONSTRAINT mirrored_network_policies_cluster_id_namespace_name_key UNIQUE (cluster_id, namespace, name);


--
-- Name: mirrored_network_policies mirrored_network_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_network_policies
    ADD CONSTRAINT mirrored_network_policies_pkey PRIMARY KEY (id);


--
-- Name: mirrored_resource_quotas mirrored_resource_quotas_cluster_id_namespace_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_resource_quotas
    ADD CONSTRAINT mirrored_resource_quotas_cluster_id_namespace_name_key UNIQUE (cluster_id, namespace, name);


--
-- Name: mirrored_resource_quotas mirrored_resource_quotas_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_resource_quotas
    ADD CONSTRAINT mirrored_resource_quotas_pkey PRIMARY KEY (id);


--
-- Name: monitoring_backends monitoring_backends_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.monitoring_backends
    ADD CONSTRAINT monitoring_backends_name_key UNIQUE (name);


--
-- Name: monitoring_backends monitoring_backends_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.monitoring_backends
    ADD CONSTRAINT monitoring_backends_pkey PRIMARY KEY (id);


--
-- Name: monitoring_operation_events monitoring_operation_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.monitoring_operation_events
    ADD CONSTRAINT monitoring_operation_events_pkey PRIMARY KEY (id);


--
-- Name: monitoring_operations monitoring_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.monitoring_operations
    ADD CONSTRAINT monitoring_operations_pkey PRIMARY KEY (id);


--
-- Name: native_rbac_rules native_rbac_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.native_rbac_rules
    ADD CONSTRAINT native_rbac_rules_pkey PRIMARY KEY (id);


--
-- Name: network_policy_applications network_policy_applications_cluster_id_namespace_template_i_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_applications
    ADD CONSTRAINT network_policy_applications_cluster_id_namespace_template_i_key UNIQUE (cluster_id, namespace, template_id);


--
-- Name: network_policy_applications network_policy_applications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_applications
    ADD CONSTRAINT network_policy_applications_pkey PRIMARY KEY (id);


--
-- Name: network_policy_templates network_policy_templates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_templates
    ADD CONSTRAINT network_policy_templates_pkey PRIMARY KEY (id);


--
-- Name: network_policy_templates network_policy_templates_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_templates
    ADD CONSTRAINT network_policy_templates_slug_key UNIQUE (slug);


--
-- Name: notification_channels notification_channels_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_channels
    ADD CONSTRAINT notification_channels_pkey PRIMARY KEY (id);


--
-- Name: notification_templates notification_templates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_templates
    ADD CONSTRAINT notification_templates_pkey PRIMARY KEY (id);


--
-- Name: notification_templates notification_templates_template_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_templates
    ADD CONSTRAINT notification_templates_template_key_key UNIQUE (template_key);


--
-- Name: operation_idempotency_keys operation_idempotency_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operation_idempotency_keys
    ADD CONSTRAINT operation_idempotency_keys_pkey PRIMARY KEY (scope, idempotency_key);


--
-- Name: password_reset_tokens password_reset_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_pkey PRIMARY KEY (id);


--
-- Name: platform_configuration platform_configuration_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_configuration
    ADD CONSTRAINT platform_configuration_pkey PRIMARY KEY (id);


--
-- Name: platform_settings platform_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_settings
    ADD CONSTRAINT platform_settings_pkey PRIMARY KEY (key);


--
-- Name: pod_security_templates pod_security_templates_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pod_security_templates
    ADD CONSTRAINT pod_security_templates_name_key UNIQUE (name);


--
-- Name: pod_security_templates pod_security_templates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pod_security_templates
    ADD CONSTRAINT pod_security_templates_pkey PRIMARY KEY (id);


--
-- Name: project_catalog_subscriptions project_catalog_subscriptions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_catalog_subscriptions
    ADD CONSTRAINT project_catalog_subscriptions_pkey PRIMARY KEY (id);


--
-- Name: project_catalog_subscriptions project_catalog_subscriptions_project_id_catalog_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_catalog_subscriptions
    ADD CONSTRAINT project_catalog_subscriptions_project_id_catalog_id_key UNIQUE (project_id, catalog_id);


--
-- Name: project_namespaces project_namespaces_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_namespaces
    ADD CONSTRAINT project_namespaces_pkey PRIMARY KEY (project_id, cluster_id, namespace);


--
-- Name: project_role_bindings project_role_bindings_group_role_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_role_bindings
    ADD CONSTRAINT project_role_bindings_group_role_id_project_id_key UNIQUE ("group", role_id, project_id);


--
-- Name: project_role_bindings project_role_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_role_bindings
    ADD CONSTRAINT project_role_bindings_pkey PRIMARY KEY (id);


--
-- Name: project_role_bindings project_role_bindings_user_id_role_id_project_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_role_bindings
    ADD CONSTRAINT project_role_bindings_user_id_role_id_project_id_key UNIQUE (user_id, role_id, project_id);


--
-- Name: project_roles project_roles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_roles
    ADD CONSTRAINT project_roles_pkey PRIMARY KEY (id);


--
-- Name: projects projects_name_cluster_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_name_cluster_id_key UNIQUE (name, cluster_id);


--
-- Name: projects projects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);


--
-- Name: prometheus_datasources prometheus_datasources_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prometheus_datasources
    ADD CONSTRAINT prometheus_datasources_name_key UNIQUE (name);


--
-- Name: prometheus_datasources prometheus_datasources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prometheus_datasources
    ADD CONSTRAINT prometheus_datasources_pkey PRIMARY KEY (id);


--
-- Name: quota_plans quota_plans_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quota_plans
    ADD CONSTRAINT quota_plans_pkey PRIMARY KEY (name);


--
-- Name: read_audit_policies read_audit_policies_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.read_audit_policies
    ADD CONSTRAINT read_audit_policies_name_key UNIQUE (name);


--
-- Name: read_audit_policies read_audit_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.read_audit_policies
    ADD CONSTRAINT read_audit_policies_pkey PRIMARY KEY (id);


--
-- Name: repair_job_states repair_job_states_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.repair_job_states
    ADD CONSTRAINT repair_job_states_pkey PRIMARY KEY (job_name, scope);


--
-- Name: restore_operations restore_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.restore_operations
    ADD CONSTRAINT restore_operations_pkey PRIMARY KEY (id);


--
-- Name: scim_tokens scim_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scim_tokens
    ADD CONSTRAINT scim_tokens_pkey PRIMARY KEY (id);


--
-- Name: scim_tokens scim_tokens_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scim_tokens
    ADD CONSTRAINT scim_tokens_token_hash_key UNIQUE (token_hash);


--
-- Name: security_scan_results security_scan_results_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.security_scan_results
    ADD CONSTRAINT security_scan_results_pkey PRIMARY KEY (id);


--
-- Name: siem_forward_queue siem_forward_queue_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forward_queue
    ADD CONSTRAINT siem_forward_queue_pkey PRIMARY KEY (id);


--
-- Name: siem_forwarder_status siem_forwarder_status_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forwarder_status
    ADD CONSTRAINT siem_forwarder_status_pkey PRIMARY KEY (forwarder_id);


--
-- Name: siem_forwarders siem_forwarders_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forwarders
    ADD CONSTRAINT siem_forwarders_name_key UNIQUE (name);


--
-- Name: siem_forwarders siem_forwarders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forwarders
    ADD CONSTRAINT siem_forwarders_pkey PRIMARY KEY (id);


--
-- Name: smtp_settings smtp_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.smtp_settings
    ADD CONSTRAINT smtp_settings_pkey PRIMARY KEY (id);


--
-- Name: sso_configurations sso_configurations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_configurations
    ADD CONSTRAINT sso_configurations_pkey PRIMARY KEY (id);


--
-- Name: sso_configurations sso_configurations_provider_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_configurations
    ADD CONSTRAINT sso_configurations_provider_key UNIQUE (provider);


--
-- Name: sso_sessions sso_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_sessions
    ADD CONSTRAINT sso_sessions_pkey PRIMARY KEY (jti);


--
-- Name: task_outbox task_outbox_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_outbox
    ADD CONSTRAINT task_outbox_pkey PRIMARY KEY (id);


--
-- Name: tool_operation_events tool_operation_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_operation_events
    ADD CONSTRAINT tool_operation_events_pkey PRIMARY KEY (id);


--
-- Name: tool_operations tool_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_operations
    ADD CONSTRAINT tool_operations_pkey PRIMARY KEY (id);


--
-- Name: tunnel_locator_events tunnel_locator_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tunnel_locator_events
    ADD CONSTRAINT tunnel_locator_events_pkey PRIMARY KEY (id);


--
-- Name: ui_extensions ui_extensions_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ui_extensions
    ADD CONSTRAINT ui_extensions_name_key UNIQUE (name);


--
-- Name: ui_extensions ui_extensions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ui_extensions
    ADD CONSTRAINT ui_extensions_pkey PRIMARY KEY (id);


--
-- Name: user_idp_groups user_idp_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_idp_groups
    ADD CONSTRAINT user_idp_groups_pkey PRIMARY KEY (user_id);


--
-- Name: user_totp_enrollments user_totp_enrollments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_totp_enrollments
    ADD CONSTRAINT user_totp_enrollments_pkey PRIMARY KEY (user_id);


--
-- Name: user_totp_recovery_codes user_totp_recovery_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_totp_recovery_codes
    ADD CONSTRAINT user_totp_recovery_codes_pkey PRIMARY KEY (id);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: users users_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_username_key UNIQUE (username);


--
-- Name: vault_connections vault_connections_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vault_connections
    ADD CONSTRAINT vault_connections_name_key UNIQUE (name);


--
-- Name: vault_connections vault_connections_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vault_connections
    ADD CONSTRAINT vault_connections_pkey PRIMARY KEY (id);


--
-- Name: webhook_deliveries webhook_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_pkey PRIMARY KEY (id);


--
-- Name: webhook_subscriptions webhook_subscriptions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webhook_subscriptions
    ADD CONSTRAINT webhook_subscriptions_pkey PRIMARY KEY (id);


--
-- Name: workload_operation_events workload_operation_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workload_operation_events
    ADD CONSTRAINT workload_operation_events_pkey PRIMARY KEY (id);


--
-- Name: workload_operations workload_operations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workload_operations
    ADD CONSTRAINT workload_operations_pkey PRIMARY KEY (id);


--
-- Name: xcluster_anomaly_baselines xcluster_anomaly_baselines_metric_name_window_seconds_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.xcluster_anomaly_baselines
    ADD CONSTRAINT xcluster_anomaly_baselines_metric_name_window_seconds_key UNIQUE (metric_name, window_seconds);


--
-- Name: xcluster_anomaly_baselines xcluster_anomaly_baselines_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.xcluster_anomaly_baselines
    ADD CONSTRAINT xcluster_anomaly_baselines_pkey PRIMARY KEY (id);


--
-- Name: agent_connection_events_cluster_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_connection_events_cluster_time_idx ON public.agent_connection_events USING btree (cluster_id, occurred_at DESC);


--
-- Name: agent_connection_events_replica_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_connection_events_replica_time_idx ON public.agent_connection_events USING btree (server_replica, occurred_at DESC) WHERE ((server_replica)::text <> ''::text);


--
-- Name: agent_connection_events_type_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_connection_events_type_time_idx ON public.agent_connection_events USING btree (event_type, occurred_at DESC);


--
-- Name: agent_operational_statuses_health_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_operational_statuses_health_idx ON public.agent_operational_statuses USING btree (audit_ingestion_state, metrics_ingestion_state, state_ingestion_state);


--
-- Name: agent_operational_statuses_replica_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_operational_statuses_replica_idx ON public.agent_operational_statuses USING btree (owning_server_replica) WHERE ((owning_server_replica)::text <> ''::text);


--
-- Name: idx_audit_log_class; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_class ON ONLY public.audit_log USING btree (action_class, created_at DESC);


--
-- Name: audit_log_default_action_class_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_action_class_created_at_idx ON public.audit_log_default USING btree (action_class, created_at DESC);


--
-- Name: idx_audit_log_action_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_action_created ON ONLY public.audit_log USING btree (action, created_at DESC);


--
-- Name: audit_log_default_action_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_action_created_at_idx ON public.audit_log_default USING btree (action, created_at DESC);


--
-- Name: idx_audit_log_correlation_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_correlation_created ON ONLY public.audit_log USING btree (correlation_id, created_at DESC);


--
-- Name: audit_log_default_correlation_id_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_correlation_id_created_at_idx ON public.audit_log_default USING btree (correlation_id, created_at DESC);


--
-- Name: idx_audit_log_request_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_request_id ON ONLY public.audit_log USING btree (request_id);


--
-- Name: audit_log_default_request_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_request_id_idx ON public.audit_log_default USING btree (request_id);


--
-- Name: idx_audit_log_resource; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_resource ON ONLY public.audit_log USING btree (resource_type, resource_id);


--
-- Name: audit_log_default_resource_type_resource_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_resource_type_resource_id_idx ON public.audit_log_default USING btree (resource_type, resource_id);


--
-- Name: idx_audit_log_schema_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_schema_created ON ONLY public.audit_log USING btree (schema_version, created_at DESC);


--
-- Name: audit_log_default_schema_version_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_schema_version_created_at_idx ON public.audit_log_default USING btree (schema_version, created_at DESC);


--
-- Name: idx_audit_log_source_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_source_created ON ONLY public.audit_log USING btree (source, created_at DESC);


--
-- Name: audit_log_default_source_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_source_created_at_idx ON public.audit_log_default USING btree (source, created_at DESC);


--
-- Name: idx_audit_log_user_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_log_user_created ON ONLY public.audit_log USING btree (user_id, created_at DESC);


--
-- Name: audit_log_default_user_id_created_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_default_user_id_created_at_idx ON public.audit_log_default USING btree (user_id, created_at DESC);


--
-- Name: charlie_action_approvals_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_action_approvals_active_idx ON public.charlie_action_approvals USING btree (session_id, expires_at) WHERE ((state)::text = ANY ((ARRAY['pending'::character varying, 'approved'::character varying])::text[]));


--
-- Name: charlie_action_deferrals_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_action_deferrals_due_idx ON public.charlie_action_deferrals USING btree (deferred_until, expires_at);


--
-- Name: charlie_alert_deliveries_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_alert_deliveries_due_idx ON public.charlie_alert_deliveries USING btree (next_attempt_at, created_at) WHERE ((status)::text = ANY ((ARRAY['queued'::character varying, 'retry'::character varying])::text[]));


--
-- Name: charlie_alert_deliveries_finding_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_alert_deliveries_finding_idx ON public.charlie_alert_deliveries USING btree (finding_id, created_at DESC);


--
-- Name: charlie_artifact_credential_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_artifact_credential_due_idx ON public.charlie_artifact_credential_state USING btree (renew_after) WHERE ((pending_state)::text = 'idle'::text);


--
-- Name: charlie_connections_reconcile_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_connections_reconcile_idx ON public.charlie_connections USING btree (onboarding_state, reconciliation_due_at) WHERE ((onboarding_state)::text <> ALL ((ARRAY['active'::character varying, 'failed'::character varying])::text[]));


--
-- Name: charlie_delegations_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_delegations_active_idx ON public.charlie_delegations USING btree (session_id, expires_at) WHERE (revoked_at IS NULL);


--
-- Name: charlie_finding_active_dedupe_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX charlie_finding_active_dedupe_idx ON public.charlie_findings USING btree (connection_id, dedupe_fingerprint) WHERE ((status)::text = ANY ((ARRAY['open'::character varying, 'acknowledged'::character varying])::text[]));


--
-- Name: charlie_finding_decisions_finding_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_finding_decisions_finding_idx ON public.charlie_finding_decisions USING btree (finding_id, created_at DESC);


--
-- Name: charlie_finding_resources_target_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_finding_resources_target_idx ON public.charlie_finding_resources USING btree (resource_type, resource_id);


--
-- Name: charlie_findings_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_findings_status_idx ON public.charlie_findings USING btree (status, severity, updated_at DESC);


--
-- Name: charlie_interactive_threads_one_active; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX charlie_interactive_threads_one_active ON public.charlie_interactive_threads USING btree (connection_id, owner_user_id) WHERE ((state)::text = 'active'::text);


--
-- Name: charlie_interactive_threads_owner_updated; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_interactive_threads_owner_updated ON public.charlie_interactive_threads USING btree (owner_user_id, updated_at DESC);


--
-- Name: charlie_one_active_connection_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX charlie_one_active_connection_idx ON public.charlie_connections USING btree ((true)) WHERE active;


--
-- Name: charlie_receipts_reconcile_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_receipts_reconcile_idx ON public.charlie_action_receipts USING btree (state, lease_expires_at) WHERE ((state)::text = ANY ((ARRAY['claimed'::character varying, 'dispatched'::character varying, 'ambiguous'::character varying, 'verifying'::character varying])::text[]));


--
-- Name: charlie_receipts_safety_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_receipts_safety_idx ON public.charlie_action_receipts USING btree (session_id, capability, resource_digest, updated_at DESC);


--
-- Name: charlie_receipts_session_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_receipts_session_idx ON public.charlie_action_receipts USING btree (session_id, created_at DESC);


--
-- Name: charlie_session_resources_target_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_session_resources_target_idx ON public.charlie_session_resources USING btree (resource_type, resource_id);


--
-- Name: charlie_sessions_central_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX charlie_sessions_central_id_idx ON public.charlie_sessions USING btree (connection_id, charlie_session_id) WHERE ((charlie_session_id)::text <> ''::text);


--
-- Name: charlie_sessions_owner_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_sessions_owner_idx ON public.charlie_sessions USING btree (owner_user_id, updated_at DESC) WHERE (owner_user_id IS NOT NULL);


--
-- Name: charlie_sessions_state_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_sessions_state_idx ON public.charlie_sessions USING btree (state, updated_at DESC);


--
-- Name: charlie_sessions_thread_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_sessions_thread_idx ON public.charlie_sessions USING btree (thread_id) WHERE (thread_id IS NOT NULL);


--
-- Name: charlie_thread_sessions_session_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_thread_sessions_session_idx ON public.charlie_thread_sessions USING btree (session_id);


--
-- Name: charlie_trigger_event_active_dedupe_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX charlie_trigger_event_active_dedupe_idx ON public.charlie_trigger_events USING btree (rule_id, fingerprint) WHERE ((state)::text = ANY ((ARRAY['pending'::character varying, 'dispatching'::character varying, 'dispatched'::character varying, 'retry'::character varying])::text[]));


--
-- Name: charlie_trigger_event_active_operator_retry_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX charlie_trigger_event_active_operator_retry_idx ON public.charlie_trigger_events USING btree (retry_of_event_id) WHERE ((retry_of_event_id IS NOT NULL) AND ((state)::text = ANY ((ARRAY['pending'::character varying, 'dispatching'::character varying, 'dispatched'::character varying, 'retry'::character varying])::text[])));


--
-- Name: charlie_trigger_event_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX charlie_trigger_event_due_idx ON public.charlie_trigger_events USING btree (state, next_attempt_at) WHERE ((state)::text = ANY ((ARRAY['pending'::character varying, 'retry'::character varying])::text[]));


--
-- Name: cluster_deployment_events_deployment_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cluster_deployment_events_deployment_idx ON public.cluster_deployment_events USING btree (deployment_id, created_at DESC);


--
-- Name: cluster_deployment_events_retention_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cluster_deployment_events_retention_idx ON public.cluster_deployment_events USING btree (created_at);


--
-- Name: cluster_deployments_cluster_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cluster_deployments_cluster_idx ON public.cluster_deployments USING btree (cluster_id, phase);


--
-- Name: cluster_deployments_stale_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cluster_deployments_stale_idx ON public.cluster_deployments USING btree (last_observed_at) WHERE ((phase)::text <> ALL ((ARRAY['removed'::character varying, 'pending'::character varying])::text[]));


--
-- Name: cluster_deployments_target_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cluster_deployments_target_idx ON public.cluster_deployments USING btree (target_id, phase);


--
-- Name: clusters_external_ref_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX clusters_external_ref_unique ON public.clusters USING btree (external_ref_api_version, external_ref_kind, external_ref_namespace, external_ref_name) WHERE (((external_ref_name)::text <> ''::text) AND (decommissioned_at IS NULL));


--
-- Name: clusters_managed_by_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX clusters_managed_by_idx ON public.clusters USING btree (managed_by);


--
-- Name: clusters_name_alive_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX clusters_name_alive_unique ON public.clusters USING btree (name) WHERE (decommissioned_at IS NULL);


--
-- Name: clusters_one_local; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX clusters_one_local ON public.clusters USING btree (is_local) WHERE (is_local = true);


--
-- Name: component_bundle_versions_bundle_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX component_bundle_versions_bundle_idx ON public.component_bundle_versions USING btree (bundle_id, created_at DESC);


--
-- Name: component_bundle_versions_resolving_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX component_bundle_versions_resolving_idx ON public.component_bundle_versions USING btree (created_at) WHERE ((state)::text = 'resolving'::text);


--
-- Name: component_bundles_project_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX component_bundles_project_idx ON public.component_bundles USING btree (project_id, created_at DESC);


--
-- Name: delivery_controller_inventory_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_controller_inventory_status_idx ON public.delivery_controller_inventory USING btree (compatibility_status, ready);


--
-- Name: delivery_rollout_approvals_rollout_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_rollout_approvals_rollout_idx ON public.delivery_rollout_approvals USING btree (rollout_id, cohort);


--
-- Name: delivery_rollout_clusters_rollout_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_rollout_clusters_rollout_idx ON public.delivery_rollout_clusters USING btree (rollout_id, cohort, release_order);


--
-- Name: delivery_rollout_clusters_runnable_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_rollout_clusters_runnable_idx ON public.delivery_rollout_clusters USING btree (rollout_id, cohort, release_order) WHERE ((state)::text = ANY ((ARRAY['pending'::character varying, 'released'::character varying, 'acknowledged'::character varying, 'reconciling'::character varying, 'rolling_back'::character varying])::text[]));


--
-- Name: delivery_rollout_events_rollout_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_rollout_events_rollout_idx ON public.delivery_rollout_events USING btree (rollout_id, occurred_at, id);


--
-- Name: delivery_rollouts_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_rollouts_active_idx ON public.delivery_rollouts USING btree (created_at) WHERE (((state)::text = ANY ((ARRAY['resolving'::character varying, 'awaiting_approval'::character varying, 'queued'::character varying, 'progressing'::character varying, 'paused'::character varying, 'rolling_back'::character varying])::text[])) OR (((state)::text = 'failed'::text) AND ((strategy ->> 'on_failure'::text) = 'rollback'::text)));


--
-- Name: delivery_rollouts_lease_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_rollouts_lease_idx ON public.delivery_rollouts USING btree (lease_expires_at) WHERE (((state)::text = ANY ((ARRAY['resolving'::character varying, 'awaiting_approval'::character varying, 'queued'::character varying, 'progressing'::character varying, 'rolling_back'::character varying])::text[])) OR (((state)::text = 'failed'::text) AND ((strategy ->> 'on_failure'::text) = 'rollback'::text)));


--
-- Name: delivery_rollouts_target_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_rollouts_target_idx ON public.delivery_rollouts USING btree (target_id, created_at DESC);


--
-- Name: delivery_source_resolutions_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_source_resolutions_pending_idx ON public.delivery_source_resolutions USING btree (next_attempt_at, created_at) WHERE ((status)::text = ANY ((ARRAY['pending'::character varying, 'running'::character varying])::text[]));


--
-- Name: delivery_source_resolutions_source_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_source_resolutions_source_idx ON public.delivery_source_resolutions USING btree (source_id, created_at DESC);


--
-- Name: delivery_sources_project_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_sources_project_idx ON public.delivery_sources USING btree (project_id, created_at DESC);


--
-- Name: delivery_sources_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_sources_status_idx ON public.delivery_sources USING btree (status) WHERE ((status)::text <> 'ready'::text);


--
-- Name: delivery_system_assignments_phase_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_system_assignments_phase_idx ON public.delivery_system_cluster_assignments USING btree (phase, deadline);


--
-- Name: delivery_system_assignments_rollout_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_system_assignments_rollout_idx ON public.delivery_system_cluster_assignments USING btree (rollout_id, cohort, release_order);


--
-- Name: delivery_system_events_cluster_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_system_events_cluster_idx ON public.delivery_system_events USING btree (cluster_id, occurred_at DESC);


--
-- Name: delivery_system_events_rollout_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_system_events_rollout_idx ON public.delivery_system_events USING btree (rollout_id, occurred_at, id);


--
-- Name: delivery_system_one_released_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX delivery_system_one_released_idx ON public.delivery_system_releases USING btree (state) WHERE ((state)::text = 'released'::text);


--
-- Name: delivery_system_rollouts_active_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_system_rollouts_active_idx ON public.delivery_system_rollouts USING btree (created_at) WHERE ((state)::text = ANY ((ARRAY['awaiting_approval'::character varying, 'queued'::character varying, 'progressing'::character varying, 'rolling_back'::character varying])::text[]));


--
-- Name: delivery_targets_bundle_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_targets_bundle_idx ON public.delivery_targets USING btree (bundle_version_id);


--
-- Name: delivery_targets_project_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX delivery_targets_project_idx ON public.delivery_targets USING btree (project_id, created_at DESC);


--
-- Name: idx_agent_conns_agent_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_conns_agent_id ON public.agent_connections USING btree (agent_id);


--
-- Name: idx_agent_conns_cluster_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_conns_cluster_status ON public.agent_connections USING btree (cluster_id, status);


--
-- Name: idx_agent_conns_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_conns_session_id ON public.agent_connections USING btree (session_id);


--
-- Name: idx_agent_lifecycle_operations_cluster_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_lifecycle_operations_cluster_created ON public.agent_lifecycle_operations USING btree (cluster_id, created_at DESC);


--
-- Name: idx_agent_lifecycle_operations_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_lifecycle_operations_status ON public.agent_lifecycle_operations USING btree (status, created_at);


--
-- Name: idx_alert_events_cluster_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_events_cluster_status ON public.alert_events USING btree (cluster_id, status);


--
-- Name: idx_alert_events_fired; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_events_fired ON public.alert_events USING btree (fired_at);


--
-- Name: idx_alert_events_rule_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_events_rule_status ON public.alert_events USING btree (rule_id, status);


--
-- Name: idx_alert_events_status_fired; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_events_status_fired ON public.alert_events USING btree (status, fired_at);


--
-- Name: idx_alert_inhibitions_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_inhibitions_enabled ON public.alert_inhibitions USING btree (enabled);


--
-- Name: idx_alert_rules_cluster_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_rules_cluster_enabled ON public.alert_rules USING btree (cluster_id, enabled);


--
-- Name: idx_alert_rules_kind_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_rules_kind_enabled ON public.alert_rules USING btree (rule_kind, enabled);


--
-- Name: idx_alert_rules_severity_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_rules_severity_enabled ON public.alert_rules USING btree (severity, enabled);


--
-- Name: idx_alert_rules_type_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_rules_type_enabled ON public.alert_rules USING btree (rule_type, enabled);


--
-- Name: idx_alert_silences_rule_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_silences_rule_cluster ON public.alert_silences USING btree (rule_id, cluster_id);


--
-- Name: idx_alert_silences_window; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_alert_silences_window ON public.alert_silences USING btree (starts_at, ends_at);


--
-- Name: idx_allowlist_snapshots_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_allowlist_snapshots_cluster ON public.apiserver_allowlist_snapshots USING btree (cluster_id, captured_at DESC);


--
-- Name: idx_anomaly_baselines_lookup; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_anomaly_baselines_lookup ON public.anomaly_baselines USING btree (cluster_id, metric_name);


--
-- Name: idx_anomaly_baselines_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_anomaly_baselines_updated_at ON public.anomaly_baselines USING btree (updated_at);


--
-- Name: idx_api_tokens_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_tokens_hash ON public.api_tokens USING btree (token_hash);


--
-- Name: idx_api_tokens_user_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_tokens_user_active ON public.api_tokens USING btree (user_id) WHERE (is_revoked = false);


--
-- Name: idx_api_tokens_user_revoked; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_api_tokens_user_revoked ON public.api_tokens USING btree (user_id, is_revoked);


--
-- Name: idx_apiserver_audit_events_cluster_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_apiserver_audit_events_cluster_time ON public.apiserver_audit_events USING btree (cluster_id, event_time DESC);


--
-- Name: idx_apiserver_audit_events_event_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_apiserver_audit_events_event_time ON public.apiserver_audit_events USING btree (event_time);


--
-- Name: idx_audit_archive_archived_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_archive_archived_at ON public.audit_archive USING btree (archived_at DESC);


--
-- Name: idx_audit_archive_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_archive_cluster ON public.audit_archive USING btree (archived_cluster_id, created_at DESC);


--
-- Name: idx_audit_archive_resource; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_archive_resource ON public.audit_archive USING btree (resource_type, resource_id);


--
-- Name: idx_authored_constraints_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_authored_constraints_cluster ON public.authored_constraints USING btree (cluster_id);


--
-- Name: idx_backup_drill_started; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_backup_drill_started ON public.backup_drill_results USING btree (started_at DESC);


--
-- Name: idx_backup_storage_configs_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_backup_storage_configs_cluster ON public.backup_storage_configs USING btree (cluster_id);


--
-- Name: idx_backups_running_poll; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_backups_running_poll ON public.backups USING btree (status, last_polled_at) WHERE ((status)::text = 'running'::text);


--
-- Name: idx_blessed_charts_category; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_blessed_charts_category ON public.catalog_blessed_charts USING btree (category);


--
-- Name: idx_catalog_operation_events_operation; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_catalog_operation_events_operation ON public.catalog_operation_events USING btree (operation_id, created_at);


--
-- Name: idx_catalog_operations_status_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_catalog_operations_status_created ON public.catalog_operations USING btree (status, created_at);


--
-- Name: idx_catalog_operations_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_catalog_operations_target ON public.catalog_operations USING btree (target_type, target_key, created_at DESC);


--
-- Name: idx_ccra_attempted_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ccra_attempted_at ON public.cluster_condition_remediation_attempts USING btree (attempted_at);


--
-- Name: idx_ccra_cluster_type_attempted; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ccra_cluster_type_attempted ON public.cluster_condition_remediation_attempts USING btree (cluster_id, condition_type, attempted_at DESC);


--
-- Name: idx_chart_co_a; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_chart_co_a ON public.chart_co_installation USING btree (chart_a_id, weight DESC);


--
-- Name: idx_chart_co_b; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_chart_co_b ON public.chart_co_installation USING btree (chart_b_id, weight DESC);


--
-- Name: idx_chart_rating_aggregates_bayesian; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_chart_rating_aggregates_bayesian ON public.chart_rating_aggregates USING btree (bayesian_score DESC, rating_count DESC);


--
-- Name: idx_chart_ratings_chart; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_chart_ratings_chart ON public.chart_ratings USING btree (chart_id);


--
-- Name: idx_chart_ratings_user_chart_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_chart_ratings_user_chart_unique ON public.chart_ratings USING btree (user_id, chart_id) WHERE (installation_id IS NULL);


--
-- Name: idx_cloud_credential_materializations_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cloud_credential_materializations_cluster ON public.cloud_credential_materializations USING btree (cluster_id);


--
-- Name: idx_cloud_credential_materializations_credential; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cloud_credential_materializations_credential ON public.cloud_credential_materializations USING btree (credential_id);


--
-- Name: idx_cloud_credentials_project; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cloud_credentials_project ON public.cloud_credentials USING btree (project_id);


--
-- Name: idx_cluster_agent_tokens_last_rotated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_agent_tokens_last_rotated_at ON public.cluster_agent_tokens USING btree (last_rotated_at);


--
-- Name: idx_cluster_agent_tokens_previous_token_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_agent_tokens_previous_token_hash ON public.cluster_agent_tokens USING btree (previous_token_hash) WHERE (previous_token_hash IS NOT NULL);


--
-- Name: idx_cluster_agent_tokens_revoked_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_agent_tokens_revoked_at ON public.cluster_agent_tokens USING btree (revoked_at);


--
-- Name: idx_cluster_agent_tokens_token_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_cluster_agent_tokens_token_hash ON public.cluster_agent_tokens USING btree (token_hash) WHERE ((token_hash)::text <> ''::text);


--
-- Name: idx_cluster_conditions_cluster_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_conditions_cluster_id ON public.cluster_conditions USING btree (cluster_id);


--
-- Name: idx_cluster_conditions_type_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_conditions_type_status ON public.cluster_conditions USING btree (type, status);


--
-- Name: idx_cluster_decommissions_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_decommissions_cluster ON public.cluster_decommissions USING btree (cluster_id, created_at DESC);


--
-- Name: idx_cluster_decommissions_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_decommissions_status ON public.cluster_decommissions USING btree (status, created_at);


--
-- Name: idx_cluster_groups_parent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_groups_parent ON public.cluster_groups USING btree (parent_id) WHERE (enabled = true);


--
-- Name: idx_cluster_groups_toplevel_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_cluster_groups_toplevel_slug ON public.cluster_groups USING btree (slug) WHERE (parent_id IS NULL);


--
-- Name: idx_cluster_monitoring_configs_backend; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_monitoring_configs_backend ON public.cluster_monitoring_configs USING btree (backend_id);


--
-- Name: idx_cluster_registration_tokens_token_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_cluster_registration_tokens_token_hash ON public.cluster_registration_tokens USING btree (token_hash) WHERE ((token_hash)::text <> ''::text);


--
-- Name: idx_cluster_registration_tokens_token_nonempty; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_registration_tokens_token_nonempty ON public.cluster_registration_tokens USING btree (token) WHERE ((token)::text <> ''::text);


--
-- Name: idx_cluster_registry_configs_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_registry_configs_cluster ON public.cluster_registry_configs USING btree (cluster_id);


--
-- Name: idx_cluster_restores_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_restores_cluster ON public.cluster_restores USING btree (target_cluster_id, created_at DESC);


--
-- Name: idx_cluster_restores_phase; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_restores_phase ON public.cluster_restores USING btree (phase) WHERE ((phase)::text = ANY ((ARRAY['InProgress'::character varying, 'New'::character varying])::text[]));


--
-- Name: idx_cluster_service_mesh_detected_mesh; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_service_mesh_detected_mesh ON public.cluster_service_mesh USING btree (detected_mesh);


--
-- Name: idx_cluster_snapshot_schedules_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_snapshot_schedules_cluster ON public.cluster_snapshot_schedules USING btree (cluster_id);


--
-- Name: idx_cluster_snapshot_schedules_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_snapshot_schedules_enabled ON public.cluster_snapshot_schedules USING btree (enabled) WHERE (enabled = true);


--
-- Name: idx_cluster_snapshots_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_snapshots_cluster ON public.cluster_snapshots USING btree (cluster_id, created_at DESC);


--
-- Name: idx_cluster_snapshots_phase; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_snapshots_phase ON public.cluster_snapshots USING btree (phase) WHERE ((phase)::text = ANY ((ARRAY['InProgress'::character varying, 'New'::character varying])::text[]));


--
-- Name: idx_cluster_template_applications_template; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_cluster_template_applications_template ON public.cluster_template_applications USING btree (template_id);


--
-- Name: idx_clusters_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_created_at ON public.clusters USING btree (created_at);


--
-- Name: idx_clusters_decommissioned_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_decommissioned_at ON public.clusters USING btree (decommissioned_at);


--
-- Name: idx_clusters_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_group ON public.clusters USING btree (group_id) WHERE (group_id IS NOT NULL);


--
-- Name: idx_clusters_heartbeat; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_heartbeat ON public.clusters USING btree (last_heartbeat);


--
-- Name: idx_clusters_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_name ON public.clusters USING btree (name);


--
-- Name: idx_clusters_provider_region; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_provider_region ON public.clusters USING btree (provider, region);


--
-- Name: idx_clusters_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_status ON public.clusters USING btree (status);


--
-- Name: idx_clusters_status_env; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_clusters_status_env ON public.clusters USING btree (status, environment);


--
-- Name: idx_compliance_apps_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_compliance_apps_active ON public.compliance_baseline_applications USING btree (applied_at DESC) WHERE ((status)::text = 'applied'::text);


--
-- Name: idx_compliance_apps_baseline; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_compliance_apps_baseline ON public.compliance_baseline_applications USING btree (baseline_id, applied_at DESC);


--
-- Name: idx_control_plane_alerts_active_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_control_plane_alerts_active_unique ON public.control_plane_alerts USING btree (controller, condition_type) WHERE ((status)::text = 'active'::text);


--
-- Name: idx_control_plane_alerts_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_control_plane_alerts_status ON public.control_plane_alerts USING btree (status, fired_at DESC);


--
-- Name: idx_control_plane_silences_window; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_control_plane_silences_window ON public.control_plane_silences USING btree (starts_at, ends_at);


--
-- Name: idx_control_plane_snapshots_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_control_plane_snapshots_cluster ON public.control_plane_snapshots USING btree (cluster_id, created_at DESC);


--
-- Name: idx_control_plane_snapshots_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_control_plane_snapshots_status ON public.control_plane_snapshots USING btree (status) WHERE ((status)::text = ANY ((ARRAY['pending'::character varying, 'running'::character varying])::text[]));


--
-- Name: idx_crb_cluster_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_crb_cluster_user ON public.cluster_role_bindings USING btree (cluster_id, user_id);


--
-- Name: idx_crb_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_crb_group ON public.cluster_role_bindings USING btree ("group");


--
-- Name: idx_crb_group_sync; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_crb_group_sync ON public.cluster_role_bindings USING btree (user_id) WHERE ((source)::text = 'group_sync'::text);


--
-- Name: idx_crb_group_sync_connector; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_crb_group_sync_connector ON public.cluster_role_bindings USING btree (user_id, group_sync_connector_id) WHERE ((source)::text = 'group_sync'::text);


--
-- Name: idx_dashboard_widgets_scope; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_dashboard_widgets_scope ON public.dashboard_widgets USING btree (scope) WHERE (enabled = true);


--
-- Name: idx_deferred_operations_pending; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_deferred_operations_pending ON public.deferred_operations USING btree (status, deferred_until) WHERE ((status)::text = 'pending'::text);


--
-- Name: idx_deferred_operations_window; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_deferred_operations_window ON public.deferred_operations USING btree (window_id);


--
-- Name: idx_dex_connectors_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_dex_connectors_enabled ON public.dex_connectors USING btree (enabled);


--
-- Name: idx_dex_connectors_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_dex_connectors_type ON public.dex_connectors USING btree (type);


--
-- Name: idx_email_messages_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_email_messages_status ON public.email_messages USING btree (status) WHERE ((status)::text = ANY ((ARRAY['queued'::character varying, 'failed'::character varying])::text[]));


--
-- Name: idx_email_messages_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_email_messages_user ON public.email_messages USING btree (user_id);


--
-- Name: idx_gitops_registered_clusters_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_gitops_registered_clusters_source ON public.gitops_registered_clusters USING btree (source_id);


--
-- Name: idx_gitops_registration_sources_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_gitops_registration_sources_enabled ON public.gitops_registration_sources USING btree (enabled);


--
-- Name: idx_gitops_tombstoned_clusters; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_gitops_tombstoned_clusters ON public.gitops_registered_clusters USING btree (tombstoned_at) WHERE ((status)::text = 'tombstoned'::text);


--
-- Name: idx_grb_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_grb_group ON public.global_role_bindings USING btree ("group");


--
-- Name: idx_grb_group_sync; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_grb_group_sync ON public.global_role_bindings USING btree (user_id) WHERE ((source)::text = 'group_sync'::text);


--
-- Name: idx_grb_group_sync_connector; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_grb_group_sync_connector ON public.global_role_bindings USING btree (user_id, group_sync_connector_id) WHERE ((source)::text = 'group_sync'::text);


--
-- Name: idx_group_map_lookup; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_group_map_lookup ON public.identity_group_mappings USING btree (connector_id, group_name);


--
-- Name: idx_helm_chart_tags_tag; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_helm_chart_tags_tag ON public.helm_chart_tags USING btree (tag);


--
-- Name: idx_helm_charts_category; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_helm_charts_category ON public.helm_charts USING btree (category);


--
-- Name: idx_helm_charts_deprecated; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_helm_charts_deprecated ON public.helm_charts USING btree (deprecated);


--
-- Name: idx_helm_repos_type_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_helm_repos_type_enabled ON public.helm_repositories USING btree (repo_type, enabled);


--
-- Name: idx_helm_repositories_owner_project; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_helm_repositories_owner_project ON public.helm_repositories USING btree (owner_project_id) WHERE (owner_project_id IS NOT NULL);


--
-- Name: idx_image_vulns_severity; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_image_vulns_severity ON public.image_vulnerabilities USING btree (severity);


--
-- Name: idx_installed_charts_cluster_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_installed_charts_cluster_status ON public.installed_charts USING btree (cluster_id, status);


--
-- Name: idx_installed_charts_release; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_installed_charts_release ON public.installed_charts USING btree (release_name);


--
-- Name: idx_installed_charts_tool_slug; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_installed_charts_tool_slug ON public.installed_charts USING btree (tool_slug);


--
-- Name: idx_ivr_cluster_ns; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ivr_cluster_ns ON public.image_vulnerability_reports USING btree (cluster_id, namespace);


--
-- Name: idx_ivr_cluster_severity; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ivr_cluster_severity ON public.image_vulnerability_reports USING btree (cluster_id, critical_count DESC, high_count DESC);


--
-- Name: idx_ivrs_cluster_scanned; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ivrs_cluster_scanned ON public.image_vulnerability_report_snapshots USING btree (cluster_id, scanned_at DESC);


--
-- Name: idx_ivrs_dedup; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_ivrs_dedup ON public.image_vulnerability_report_snapshots USING btree (report_id, scanned_at);


--
-- Name: idx_ivrs_report_scanned; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ivrs_report_scanned ON public.image_vulnerability_report_snapshots USING btree (report_id, scanned_at DESC);


--
-- Name: idx_jwt_revocations_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_jwt_revocations_expires ON public.jwt_revocations USING btree (expires_at);


--
-- Name: idx_jwt_revocations_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_jwt_revocations_user ON public.jwt_revocations USING btree (user_id);


--
-- Name: idx_kubectl_session_commands_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_kubectl_session_commands_session ON public.kubectl_session_commands USING btree (session_id, command_at);


--
-- Name: idx_kubectl_sessions_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_kubectl_sessions_active ON public.kubectl_sessions USING btree (cluster_id, last_input_at DESC) WHERE ((status)::text = ANY ((ARRAY['starting'::character varying, 'active'::character varying])::text[]));


--
-- Name: idx_kubectl_sessions_reap; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_kubectl_sessions_reap ON public.kubectl_sessions USING btree (expires_at) WHERE ((status)::text = ANY ((ARRAY['starting'::character varying, 'active'::character varying])::text[]));


--
-- Name: idx_kubectl_sessions_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_kubectl_sessions_user ON public.kubectl_sessions USING btree (user_id, started_at DESC);


--
-- Name: idx_logging_operation_events_operation; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_logging_operation_events_operation ON public.logging_operation_events USING btree (operation_id, created_at);


--
-- Name: idx_logging_operations_status_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_logging_operations_status_created ON public.logging_operations USING btree (status, created_at);


--
-- Name: idx_logging_operations_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_logging_operations_target ON public.logging_operations USING btree (target_type, target_key, created_at DESC);


--
-- Name: idx_logging_outputs_cluster_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_logging_outputs_cluster_enabled ON public.logging_outputs USING btree (cluster_id, enabled);


--
-- Name: idx_logging_outputs_type_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_logging_outputs_type_enabled ON public.logging_outputs USING btree (output_type, enabled);


--
-- Name: idx_logging_pipelines_cluster_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_logging_pipelines_cluster_enabled ON public.logging_pipelines USING btree (cluster_id, enabled);


--
-- Name: idx_maintenance_windows_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_maintenance_windows_enabled ON public.maintenance_windows USING btree (enabled);


--
-- Name: idx_mirrored_gwc_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mirrored_gwc_cluster ON public.mirrored_gateway_classes USING btree (cluster_id);


--
-- Name: idx_mirrored_ic_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mirrored_ic_cluster ON public.mirrored_ingress_classes USING btree (cluster_id);


--
-- Name: idx_mirrored_lr_cluster_ns; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mirrored_lr_cluster_ns ON public.mirrored_limit_ranges USING btree (cluster_id, namespace);


--
-- Name: idx_mirrored_np_cluster_ns; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mirrored_np_cluster_ns ON public.mirrored_network_policies USING btree (cluster_id, namespace);


--
-- Name: idx_mirrored_rq_cluster_ns; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mirrored_rq_cluster_ns ON public.mirrored_resource_quotas USING btree (cluster_id, namespace);


--
-- Name: idx_monitoring_backends_default; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_monitoring_backends_default ON public.monitoring_backends USING btree (is_default) WHERE (is_default = true);


--
-- Name: idx_monitoring_operation_events_operation_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_monitoring_operation_events_operation_created ON public.monitoring_operation_events USING btree (operation_id, created_at);


--
-- Name: idx_monitoring_operations_status_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_monitoring_operations_status_created ON public.monitoring_operations USING btree (status, created_at);


--
-- Name: idx_monitoring_operations_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_monitoring_operations_target ON public.monitoring_operations USING btree (target_type, target_key, created_at DESC);


--
-- Name: idx_native_rbac_rules_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_native_rbac_rules_user ON public.native_rbac_rules USING btree (user_id);


--
-- Name: idx_notif_channels_type_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_notif_channels_type_enabled ON public.notification_channels USING btree (channel_type, enabled);


--
-- Name: idx_np_apps_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_np_apps_cluster ON public.network_policy_applications USING btree (cluster_id, status);


--
-- Name: idx_np_apps_template; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_np_apps_template ON public.network_policy_applications USING btree (template_id);


--
-- Name: idx_operation_idempotency_keys_operation; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_operation_idempotency_keys_operation ON public.operation_idempotency_keys USING btree (operation_table, operation_id) WHERE (operation_id IS NOT NULL);


--
-- Name: idx_password_reset_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_password_reset_user ON public.password_reset_tokens USING btree (user_id) WHERE (used_at IS NULL);


--
-- Name: idx_platform_settings_key_prefix; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_platform_settings_key_prefix ON public.platform_settings USING btree (key text_pattern_ops);


--
-- Name: idx_prb_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_prb_group ON public.project_role_bindings USING btree ("group");


--
-- Name: idx_prb_group_sync; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_prb_group_sync ON public.project_role_bindings USING btree (user_id) WHERE ((source)::text = 'group_sync'::text);


--
-- Name: idx_prb_group_sync_connector; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_prb_group_sync_connector ON public.project_role_bindings USING btree (user_id, group_sync_connector_id) WHERE ((source)::text = 'group_sync'::text);


--
-- Name: idx_prb_project_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_prb_project_user ON public.project_role_bindings USING btree (project_id, user_id);


--
-- Name: idx_project_catalog_subs_catalog; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_project_catalog_subs_catalog ON public.project_catalog_subscriptions USING btree (catalog_id);


--
-- Name: idx_project_catalog_subs_project; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_project_catalog_subs_project ON public.project_catalog_subscriptions USING btree (project_id);


--
-- Name: idx_project_namespaces_lease; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_project_namespaces_lease ON public.project_namespaces USING btree (locked_until);


--
-- Name: idx_projects_cluster_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_projects_cluster_name ON public.projects USING btree (cluster_id, name);


--
-- Name: idx_projects_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_projects_created_at ON public.projects USING btree (created_at);


--
-- Name: idx_projects_quota_plan; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_projects_quota_plan ON public.projects USING btree (quota_plan);


--
-- Name: idx_read_audit_policies_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_read_audit_policies_enabled ON public.read_audit_policies USING btree (enabled);


--
-- Name: idx_reg_steps_cluster; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reg_steps_cluster ON public.cluster_registration_steps USING btree (cluster_id, step_order, created_at);


--
-- Name: idx_reg_tokens_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reg_tokens_expires ON public.cluster_registration_tokens USING btree (expires_at);


--
-- Name: idx_reg_tokens_token_used; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_reg_tokens_token_used ON public.cluster_registration_tokens USING btree (token, is_used);


--
-- Name: idx_repair_job_states_status_updated; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_repair_job_states_status_updated ON public.repair_job_states USING btree (status, updated_at DESC);


--
-- Name: idx_restore_operations_running_poll; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_restore_operations_running_poll ON public.restore_operations USING btree (status, last_polled_at) WHERE ((status)::text = 'running'::text);


--
-- Name: idx_scim_tokens_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_scim_tokens_hash ON public.scim_tokens USING btree (token_hash);


--
-- Name: idx_security_scan_results_cluster_scan_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_security_scan_results_cluster_scan_name ON public.security_scan_results USING btree (cluster_scan_name);


--
-- Name: idx_siem_forward_queue_forwarder; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_siem_forward_queue_forwarder ON public.siem_forward_queue USING btree (forwarder_id, id);


--
-- Name: idx_sso_sessions_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sso_sessions_expires ON public.sso_sessions USING btree (expires_at);


--
-- Name: idx_sso_sessions_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sso_sessions_user ON public.sso_sessions USING btree (user_id);


--
-- Name: idx_tool_operation_events_operation; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tool_operation_events_operation ON public.tool_operation_events USING btree (operation_id, created_at);


--
-- Name: idx_tool_operations_status_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tool_operations_status_created ON public.tool_operations USING btree (status, created_at);


--
-- Name: idx_tool_operations_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tool_operations_target ON public.tool_operations USING btree (target_type, target_key, created_at DESC);


--
-- Name: idx_totp_recovery_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_totp_recovery_user ON public.user_totp_recovery_codes USING btree (user_id) WHERE (used_at IS NULL);


--
-- Name: idx_ui_extensions_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ui_extensions_enabled ON public.ui_extensions USING btree (enabled, compatibility_status);


--
-- Name: idx_users_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_created_at ON public.users USING btree (created_at);


--
-- Name: idx_users_email; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_email ON public.users USING btree (email);


--
-- Name: idx_users_locked_until; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_locked_until ON public.users USING btree (locked_until) WHERE (locked_until IS NOT NULL);


--
-- Name: idx_users_quota_plan; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_quota_plan ON public.users USING btree (quota_plan);


--
-- Name: idx_users_username; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_username ON public.users USING btree (username);


--
-- Name: idx_vault_connections_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_vault_connections_enabled ON public.vault_connections USING btree (enabled);


--
-- Name: idx_webhook_deliveries_pending; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhook_deliveries_pending ON public.webhook_deliveries USING btree (next_attempt_at) WHERE ((status)::text = ANY ((ARRAY['queued'::character varying, 'failed'::character varying])::text[]));


--
-- Name: idx_webhook_deliveries_sub_recent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_webhook_deliveries_sub_recent ON public.webhook_deliveries USING btree (subscription_id, created_at DESC);


--
-- Name: idx_workload_operation_events_operation; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workload_operation_events_operation ON public.workload_operation_events USING btree (operation_id, created_at);


--
-- Name: idx_workload_operations_status_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workload_operations_status_created ON public.workload_operations USING btree (status, created_at);


--
-- Name: idx_workload_operations_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workload_operations_target ON public.workload_operations USING btree (target_type, target_key, created_at DESC);


--
-- Name: idx_xcluster_anomaly_baselines_metric; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_xcluster_anomaly_baselines_metric ON public.xcluster_anomaly_baselines USING btree (metric_name);


--
-- Name: idx_xcluster_anomaly_baselines_updated_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_xcluster_anomaly_baselines_updated_at ON public.xcluster_anomaly_baselines USING btree (updated_at);


--
-- Name: notification_templates_channel_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX notification_templates_channel_idx ON public.notification_templates USING btree (channel);


--
-- Name: projects_external_ref_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX projects_external_ref_unique ON public.projects USING btree (external_ref_api_version, external_ref_kind, external_ref_namespace, external_ref_name) WHERE ((external_ref_name)::text <> ''::text);


--
-- Name: projects_managed_by_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX projects_managed_by_idx ON public.projects USING btree (managed_by);


--
-- Name: task_outbox_dead_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX task_outbox_dead_idx ON public.task_outbox USING btree (updated_at) WHERE (status = 'dead'::text);


--
-- Name: task_outbox_dedupe_key_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX task_outbox_dedupe_key_unique ON public.task_outbox USING btree (dedupe_key) WHERE (dedupe_key IS NOT NULL);


--
-- Name: task_outbox_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX task_outbox_due_idx ON public.task_outbox USING btree (status, next_attempt_at, created_at) WHERE (status = ANY (ARRAY['pending'::text, 'failed'::text, 'delivering'::text]));


--
-- Name: tunnel_locator_events_cluster_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tunnel_locator_events_cluster_time_idx ON public.tunnel_locator_events USING btree (cluster_id, occurred_at DESC) WHERE (cluster_id IS NOT NULL);


--
-- Name: tunnel_locator_events_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tunnel_locator_events_time_idx ON public.tunnel_locator_events USING btree (occurred_at DESC);


--
-- Name: uidx_group_map_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uidx_group_map_unique ON public.identity_group_mappings USING btree (COALESCE((connector_id)::text, ''::text), group_name, scope, role_id, COALESCE((cluster_id)::text, ''::text), COALESCE((project_id)::text, ''::text));


--
-- Name: uidx_password_reset_token_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uidx_password_reset_token_hash ON public.password_reset_tokens USING btree (token_hash);


--
-- Name: uidx_totp_recovery_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uidx_totp_recovery_hash ON public.user_totp_recovery_codes USING btree (code_hash);


--
-- Name: uidx_webhook_subscriptions_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uidx_webhook_subscriptions_name ON public.webhook_subscriptions USING btree (name);


--
-- Name: uq_project_namespaces_cluster_namespace; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_project_namespaces_cluster_namespace ON public.project_namespaces USING btree (cluster_id, namespace);


--
-- Name: audit_log_default_action_class_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_class ATTACH PARTITION public.audit_log_default_action_class_created_at_idx;


--
-- Name: audit_log_default_action_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_action_created ATTACH PARTITION public.audit_log_default_action_created_at_idx;


--
-- Name: audit_log_default_correlation_id_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_correlation_created ATTACH PARTITION public.audit_log_default_correlation_id_created_at_idx;


--
-- Name: audit_log_default_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.audit_log_pkey ATTACH PARTITION public.audit_log_default_pkey;


--
-- Name: audit_log_default_request_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_request_id ATTACH PARTITION public.audit_log_default_request_id_idx;


--
-- Name: audit_log_default_resource_type_resource_id_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_resource ATTACH PARTITION public.audit_log_default_resource_type_resource_id_idx;


--
-- Name: audit_log_default_schema_version_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_schema_created ATTACH PARTITION public.audit_log_default_schema_version_created_at_idx;


--
-- Name: audit_log_default_source_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_source_created ATTACH PARTITION public.audit_log_default_source_created_at_idx;


--
-- Name: audit_log_default_user_id_created_at_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_audit_log_user_created ATTACH PARTITION public.audit_log_default_user_id_created_at_idx;


--
-- Name: dex_connectors dex_connectors_runtime_generation; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER dex_connectors_runtime_generation AFTER INSERT OR DELETE OR UPDATE ON public.dex_connectors FOR EACH ROW EXECUTE FUNCTION public.bump_dex_runtime_generation();


--
-- Name: cluster_role_bindings revoke_charlie_delegations_cluster_role_bindings; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_cluster_role_bindings AFTER INSERT OR DELETE OR UPDATE ON public.cluster_role_bindings FOR EACH STATEMENT EXECUTE FUNCTION public.revoke_charlie_delegations_on_rbac_change();


--
-- Name: cluster_roles revoke_charlie_delegations_cluster_roles; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_cluster_roles AFTER INSERT OR DELETE OR UPDATE ON public.cluster_roles FOR EACH STATEMENT EXECUTE FUNCTION public.revoke_charlie_delegations_on_rbac_change();


--
-- Name: charlie_connections revoke_charlie_delegations_connection_inactive; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_connection_inactive AFTER UPDATE OF active ON public.charlie_connections FOR EACH ROW WHEN (((old.active IS DISTINCT FROM new.active) AND (new.active = false))) EXECUTE FUNCTION public.revoke_charlie_delegations_for_inactive_connection();


--
-- Name: global_role_bindings revoke_charlie_delegations_global_role_bindings; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_global_role_bindings AFTER INSERT OR DELETE OR UPDATE ON public.global_role_bindings FOR EACH STATEMENT EXECUTE FUNCTION public.revoke_charlie_delegations_on_rbac_change();


--
-- Name: global_roles revoke_charlie_delegations_global_roles; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_global_roles AFTER INSERT OR DELETE OR UPDATE ON public.global_roles FOR EACH STATEMENT EXECUTE FUNCTION public.revoke_charlie_delegations_on_rbac_change();


--
-- Name: project_role_bindings revoke_charlie_delegations_project_role_bindings; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_project_role_bindings AFTER INSERT OR DELETE OR UPDATE ON public.project_role_bindings FOR EACH STATEMENT EXECUTE FUNCTION public.revoke_charlie_delegations_on_rbac_change();


--
-- Name: project_roles revoke_charlie_delegations_project_roles; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_project_roles AFTER INSERT OR DELETE OR UPDATE ON public.project_roles FOR EACH STATEMENT EXECUTE FUNCTION public.revoke_charlie_delegations_on_rbac_change();


--
-- Name: users revoke_charlie_delegations_user_deactivated; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER revoke_charlie_delegations_user_deactivated AFTER UPDATE OF is_active ON public.users FOR EACH ROW WHEN (((old.is_active IS DISTINCT FROM new.is_active) AND (new.is_active = false))) EXECUTE FUNCTION public.revoke_charlie_delegations_for_deactivated_user();


--
-- Name: agent_connections set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.agent_connections FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: alert_events set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.alert_events FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: alert_rules set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.alert_rules FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: alert_silences set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.alert_silences FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: api_tokens set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.api_tokens FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: backup_schedules set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.backup_schedules FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: backup_storage_configs set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.backup_storage_configs FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: backups set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.backups FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_agent_tokens set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_agent_tokens FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_health_statuses set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_health_statuses FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_registration_tokens set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_registration_tokens FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_registry_configs set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_registry_configs FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_role_bindings set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_role_bindings FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_roles set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_roles FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_security_policies set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_security_policies FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_tools set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.cluster_tools FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: clusters set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.clusters FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: global_role_bindings set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.global_role_bindings FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: global_roles set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.global_roles FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: helm_chart_versions set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.helm_chart_versions FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: helm_charts set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.helm_charts FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: helm_repositories set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.helm_repositories FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: installed_charts set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.installed_charts FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: logging_outputs set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.logging_outputs FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: logging_pipelines set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.logging_pipelines FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: notification_channels set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.notification_channels FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: pod_security_templates set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.pod_security_templates FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: project_role_bindings set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.project_role_bindings FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: project_roles set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.project_roles FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: projects set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.projects FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: restore_operations set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.restore_operations FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: security_scan_results set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.security_scan_results FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: sso_configurations set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.sso_configurations FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: users set_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at BEFORE UPDATE ON public.users FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_agent_tokens set_updated_at_cluster_agent_tokens; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_cluster_agent_tokens BEFORE UPDATE ON public.cluster_agent_tokens FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: cluster_deployments set_updated_at_cluster_deployments; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_cluster_deployments BEFORE UPDATE ON public.cluster_deployments FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: component_bundles set_updated_at_component_bundles; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_component_bundles BEFORE UPDATE ON public.component_bundles FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_assignment_receipts set_updated_at_delivery_assignment_receipts; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_assignment_receipts BEFORE UPDATE ON public.delivery_assignment_receipts FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_controller_inventory set_updated_at_delivery_controller_inventory; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_controller_inventory BEFORE UPDATE ON public.delivery_controller_inventory FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_rollout_clusters set_updated_at_delivery_rollout_clusters; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_rollout_clusters BEFORE UPDATE ON public.delivery_rollout_clusters FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_rollouts set_updated_at_delivery_rollouts; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_rollouts BEFORE UPDATE ON public.delivery_rollouts FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_sources set_updated_at_delivery_sources; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_sources BEFORE UPDATE ON public.delivery_sources FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_system_cluster_assignments set_updated_at_delivery_system_cluster_assignments; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_system_cluster_assignments BEFORE UPDATE ON public.delivery_system_cluster_assignments FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_system_rollouts set_updated_at_delivery_system_rollouts; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_system_rollouts BEFORE UPDATE ON public.delivery_system_rollouts FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: delivery_targets set_updated_at_delivery_targets; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER set_updated_at_delivery_targets BEFORE UPDATE ON public.delivery_targets FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();


--
-- Name: agent_connection_events agent_connection_events_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_connection_events
    ADD CONSTRAINT agent_connection_events_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: agent_connection_events agent_connection_events_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_connection_events
    ADD CONSTRAINT agent_connection_events_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.agent_connections(id) ON DELETE SET NULL;


--
-- Name: agent_connections agent_connections_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_connections
    ADD CONSTRAINT agent_connections_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: agent_lifecycle_operations agent_lifecycle_operations_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_lifecycle_operations
    ADD CONSTRAINT agent_lifecycle_operations_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: agent_lifecycle_operations agent_lifecycle_operations_requested_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_lifecycle_operations
    ADD CONSTRAINT agent_lifecycle_operations_requested_by_fkey FOREIGN KEY (requested_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: agent_operational_statuses agent_operational_statuses_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_operational_statuses
    ADD CONSTRAINT agent_operational_statuses_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: alert_events alert_events_acknowledged_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_acknowledged_by_id_fkey FOREIGN KEY (acknowledged_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: alert_events alert_events_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: alert_events alert_events_rule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_events
    ADD CONSTRAINT alert_events_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.alert_rules(id) ON DELETE CASCADE;


--
-- Name: alert_inhibitions alert_inhibitions_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_inhibitions
    ADD CONSTRAINT alert_inhibitions_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: alert_rule_channels alert_rule_channels_alert_rule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rule_channels
    ADD CONSTRAINT alert_rule_channels_alert_rule_id_fkey FOREIGN KEY (alert_rule_id) REFERENCES public.alert_rules(id) ON DELETE CASCADE;


--
-- Name: alert_rule_channels alert_rule_channels_notification_channel_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rule_channels
    ADD CONSTRAINT alert_rule_channels_notification_channel_id_fkey FOREIGN KEY (notification_channel_id) REFERENCES public.notification_channels(id) ON DELETE CASCADE;


--
-- Name: alert_rules alert_rules_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: alert_rules alert_rules_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_rules
    ADD CONSTRAINT alert_rules_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: alert_silences alert_silences_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_silences
    ADD CONSTRAINT alert_silences_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: alert_silences alert_silences_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_silences
    ADD CONSTRAINT alert_silences_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: alert_silences alert_silences_rule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.alert_silences
    ADD CONSTRAINT alert_silences_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.alert_rules(id) ON DELETE CASCADE;


--
-- Name: anomaly_baselines anomaly_baselines_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.anomaly_baselines
    ADD CONSTRAINT anomaly_baselines_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: api_tokens api_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_tokens
    ADD CONSTRAINT api_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: apiserver_allowlist_snapshots apiserver_allowlist_snapshots_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_allowlist_snapshots
    ADD CONSTRAINT apiserver_allowlist_snapshots_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: apiserver_allowlists apiserver_allowlists_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_allowlists
    ADD CONSTRAINT apiserver_allowlists_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: apiserver_audit_events apiserver_audit_events_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.apiserver_audit_events
    ADD CONSTRAINT apiserver_audit_events_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: audit_log audit_log_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE public.audit_log
    ADD CONSTRAINT audit_log_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: authored_constraints authored_constraints_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.authored_constraints
    ADD CONSTRAINT authored_constraints_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: authored_constraints authored_constraints_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.authored_constraints
    ADD CONSTRAINT authored_constraints_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: backup_schedules backup_schedules_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_schedules
    ADD CONSTRAINT backup_schedules_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: backup_schedules backup_schedules_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_schedules
    ADD CONSTRAINT backup_schedules_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: backup_schedules backup_schedules_last_backup_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_schedules
    ADD CONSTRAINT backup_schedules_last_backup_id_fkey FOREIGN KEY (last_backup_id) REFERENCES public.backups(id) ON DELETE SET NULL;


--
-- Name: backup_schedules backup_schedules_storage_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_schedules
    ADD CONSTRAINT backup_schedules_storage_id_fkey FOREIGN KEY (storage_id) REFERENCES public.backup_storage_configs(id) ON DELETE RESTRICT;


--
-- Name: backup_storage_configs backup_storage_configs_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_storage_configs
    ADD CONSTRAINT backup_storage_configs_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: backup_storage_configs backup_storage_configs_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backup_storage_configs
    ADD CONSTRAINT backup_storage_configs_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: backups backups_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backups
    ADD CONSTRAINT backups_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: backups backups_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backups
    ADD CONSTRAINT backups_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: backups backups_storage_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.backups
    ADD CONSTRAINT backups_storage_id_fkey FOREIGN KEY (storage_id) REFERENCES public.backup_storage_configs(id) ON DELETE RESTRICT;


--
-- Name: catalog_operation_events catalog_operation_events_operation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.catalog_operation_events
    ADD CONSTRAINT catalog_operation_events_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.catalog_operations(id) ON DELETE CASCADE;


--
-- Name: catalog_operations catalog_operations_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.catalog_operations
    ADD CONSTRAINT catalog_operations_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_action_approvals charlie_action_approvals_approver_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_approvals
    ADD CONSTRAINT charlie_action_approvals_approver_id_fkey FOREIGN KEY (approver_id) REFERENCES public.users(id) ON DELETE RESTRICT;


--
-- Name: charlie_action_approvals charlie_action_approvals_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_approvals
    ADD CONSTRAINT charlie_action_approvals_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_action_approvals charlie_action_approvals_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_approvals
    ADD CONSTRAINT charlie_action_approvals_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.charlie_sessions(id) ON DELETE CASCADE;


--
-- Name: charlie_action_deferrals charlie_action_deferrals_charlie_action_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_deferrals
    ADD CONSTRAINT charlie_action_deferrals_charlie_action_id_fkey FOREIGN KEY (charlie_action_id) REFERENCES public.charlie_action_receipts(charlie_action_id) ON DELETE CASCADE;


--
-- Name: charlie_action_deferrals charlie_action_deferrals_window_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_deferrals
    ADD CONSTRAINT charlie_action_deferrals_window_id_fkey FOREIGN KEY (window_id) REFERENCES public.maintenance_windows(id) ON DELETE RESTRICT;


--
-- Name: charlie_action_receipts charlie_action_receipts_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_receipts
    ADD CONSTRAINT charlie_action_receipts_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_action_receipts charlie_action_receipts_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_action_receipts
    ADD CONSTRAINT charlie_action_receipts_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.charlie_sessions(id) ON DELETE CASCADE;


--
-- Name: charlie_alert_deliveries charlie_alert_deliveries_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_deliveries
    ADD CONSTRAINT charlie_alert_deliveries_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_alert_deliveries charlie_alert_deliveries_finding_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_deliveries
    ADD CONSTRAINT charlie_alert_deliveries_finding_id_fkey FOREIGN KEY (finding_id) REFERENCES public.charlie_findings(id) ON DELETE CASCADE;


--
-- Name: charlie_alert_deliveries charlie_alert_deliveries_notification_channel_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_deliveries
    ADD CONSTRAINT charlie_alert_deliveries_notification_channel_id_fkey FOREIGN KEY (notification_channel_id) REFERENCES public.notification_channels(id) ON DELETE SET NULL;


--
-- Name: charlie_alert_policies charlie_alert_policies_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_policies
    ADD CONSTRAINT charlie_alert_policies_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_alert_policies charlie_alert_policies_updated_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_policies
    ADD CONSTRAINT charlie_alert_policies_updated_by_id_fkey FOREIGN KEY (updated_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_alert_policy_channels charlie_alert_policy_channels_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_policy_channels
    ADD CONSTRAINT charlie_alert_policy_channels_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_alert_policies(connection_id) ON DELETE CASCADE;


--
-- Name: charlie_alert_policy_channels charlie_alert_policy_channels_notification_channel_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_alert_policy_channels
    ADD CONSTRAINT charlie_alert_policy_channels_notification_channel_id_fkey FOREIGN KEY (notification_channel_id) REFERENCES public.notification_channels(id) ON DELETE CASCADE;


--
-- Name: charlie_artifact_credential_state charlie_artifact_credential_state_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_artifact_credential_state
    ADD CONSTRAINT charlie_artifact_credential_state_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_automation_policies charlie_automation_policies_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_automation_policies
    ADD CONSTRAINT charlie_automation_policies_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_connections charlie_connections_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_connections
    ADD CONSTRAINT charlie_connections_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_connections charlie_connections_emergency_disabled_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_connections
    ADD CONSTRAINT charlie_connections_emergency_disabled_by_id_fkey FOREIGN KEY (emergency_disabled_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_delegations charlie_delegations_principal_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_delegations
    ADD CONSTRAINT charlie_delegations_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: charlie_delegations charlie_delegations_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_delegations
    ADD CONSTRAINT charlie_delegations_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.charlie_sessions(id) ON DELETE CASCADE;


--
-- Name: charlie_finding_decisions charlie_finding_decisions_finding_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_finding_decisions
    ADD CONSTRAINT charlie_finding_decisions_finding_id_fkey FOREIGN KEY (finding_id) REFERENCES public.charlie_findings(id) ON DELETE CASCADE;


--
-- Name: charlie_finding_projection_cursors charlie_finding_projection_cursors_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_finding_projection_cursors
    ADD CONSTRAINT charlie_finding_projection_cursors_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_finding_resources charlie_finding_resources_finding_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_finding_resources
    ADD CONSTRAINT charlie_finding_resources_finding_id_fkey FOREIGN KEY (finding_id) REFERENCES public.charlie_findings(id) ON DELETE CASCADE;


--
-- Name: charlie_findings charlie_findings_acknowledged_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_acknowledged_by_id_fkey FOREIGN KEY (acknowledged_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_findings charlie_findings_alert_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_alert_event_id_fkey FOREIGN KEY (alert_event_id) REFERENCES public.alert_events(id) ON DELETE SET NULL;


--
-- Name: charlie_findings charlie_findings_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_findings charlie_findings_dismissed_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_dismissed_by_id_fkey FOREIGN KEY (dismissed_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_findings charlie_findings_resolved_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_resolved_by_id_fkey FOREIGN KEY (resolved_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_findings charlie_findings_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_findings
    ADD CONSTRAINT charlie_findings_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.charlie_sessions(id) ON DELETE SET NULL;


--
-- Name: charlie_interactive_threads charlie_interactive_threads_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_interactive_threads
    ADD CONSTRAINT charlie_interactive_threads_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_interactive_threads charlie_interactive_threads_current_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_interactive_threads
    ADD CONSTRAINT charlie_interactive_threads_current_session_id_fkey FOREIGN KEY (current_session_id) REFERENCES public.charlie_sessions(id) ON DELETE SET NULL;


--
-- Name: charlie_interactive_threads charlie_interactive_threads_owner_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_interactive_threads
    ADD CONSTRAINT charlie_interactive_threads_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: charlie_session_resources charlie_session_resources_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_session_resources
    ADD CONSTRAINT charlie_session_resources_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.charlie_sessions(id) ON DELETE CASCADE;


--
-- Name: charlie_sessions charlie_sessions_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_sessions
    ADD CONSTRAINT charlie_sessions_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_sessions charlie_sessions_owner_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_sessions
    ADD CONSTRAINT charlie_sessions_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_sessions charlie_sessions_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_sessions
    ADD CONSTRAINT charlie_sessions_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.charlie_interactive_threads(id) ON DELETE SET NULL;


--
-- Name: charlie_thread_sessions charlie_thread_sessions_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_thread_sessions
    ADD CONSTRAINT charlie_thread_sessions_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.charlie_sessions(id) ON DELETE CASCADE;


--
-- Name: charlie_thread_sessions charlie_thread_sessions_thread_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_thread_sessions
    ADD CONSTRAINT charlie_thread_sessions_thread_id_fkey FOREIGN KEY (thread_id) REFERENCES public.charlie_interactive_threads(id) ON DELETE CASCADE;


--
-- Name: charlie_trigger_events charlie_trigger_events_retry_of_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_events
    ADD CONSTRAINT charlie_trigger_events_retry_of_event_id_fkey FOREIGN KEY (retry_of_event_id) REFERENCES public.charlie_trigger_events(id) ON DELETE SET NULL;


--
-- Name: charlie_trigger_events charlie_trigger_events_rule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_events
    ADD CONSTRAINT charlie_trigger_events_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES public.charlie_trigger_rules(id) ON DELETE CASCADE;


--
-- Name: charlie_trigger_events charlie_trigger_events_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_events
    ADD CONSTRAINT charlie_trigger_events_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.charlie_sessions(id) ON DELETE SET NULL;


--
-- Name: charlie_trigger_rules charlie_trigger_rules_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_rules
    ADD CONSTRAINT charlie_trigger_rules_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES public.charlie_connections(id) ON DELETE CASCADE;


--
-- Name: charlie_trigger_rules charlie_trigger_rules_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_rules
    ADD CONSTRAINT charlie_trigger_rules_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: charlie_trigger_rules charlie_trigger_rules_service_identity_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.charlie_trigger_rules
    ADD CONSTRAINT charlie_trigger_rules_service_identity_id_fkey FOREIGN KEY (service_identity_id) REFERENCES public.users(id) ON DELETE RESTRICT;


--
-- Name: chart_co_installation chart_co_installation_chart_a_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_co_installation
    ADD CONSTRAINT chart_co_installation_chart_a_id_fkey FOREIGN KEY (chart_a_id) REFERENCES public.helm_charts(id) ON DELETE CASCADE;


--
-- Name: chart_co_installation chart_co_installation_chart_b_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_co_installation
    ADD CONSTRAINT chart_co_installation_chart_b_id_fkey FOREIGN KEY (chart_b_id) REFERENCES public.helm_charts(id) ON DELETE CASCADE;


--
-- Name: chart_rating_aggregates chart_rating_aggregates_chart_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_rating_aggregates
    ADD CONSTRAINT chart_rating_aggregates_chart_id_fkey FOREIGN KEY (chart_id) REFERENCES public.helm_charts(id) ON DELETE CASCADE;


--
-- Name: chart_ratings chart_ratings_chart_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_ratings
    ADD CONSTRAINT chart_ratings_chart_id_fkey FOREIGN KEY (chart_id) REFERENCES public.helm_charts(id) ON DELETE CASCADE;


--
-- Name: chart_ratings chart_ratings_installation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_ratings
    ADD CONSTRAINT chart_ratings_installation_id_fkey FOREIGN KEY (installation_id) REFERENCES public.installed_charts(id) ON DELETE SET NULL;


--
-- Name: chart_ratings chart_ratings_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_ratings
    ADD CONSTRAINT chart_ratings_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: cloud_credential_materializations cloud_credential_materializations_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credential_materializations
    ADD CONSTRAINT cloud_credential_materializations_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cloud_credential_materializations cloud_credential_materializations_credential_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credential_materializations
    ADD CONSTRAINT cloud_credential_materializations_credential_id_fkey FOREIGN KEY (credential_id) REFERENCES public.cloud_credentials(id) ON DELETE CASCADE;


--
-- Name: cloud_credentials cloud_credentials_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credentials
    ADD CONSTRAINT cloud_credentials_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cloud_credentials cloud_credentials_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloud_credentials
    ADD CONSTRAINT cloud_credentials_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: cluster_agent_tokens cluster_agent_tokens_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_agent_tokens
    ADD CONSTRAINT cluster_agent_tokens_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_condition_remediation_attempts cluster_condition_remediation_attempts_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_condition_remediation_attempts
    ADD CONSTRAINT cluster_condition_remediation_attempts_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_conditions cluster_conditions_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_conditions
    ADD CONSTRAINT cluster_conditions_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_decommissions cluster_decommissions_requested_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_decommissions
    ADD CONSTRAINT cluster_decommissions_requested_by_id_fkey FOREIGN KEY (requested_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cluster_deployment_events cluster_deployment_events_deployment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployment_events
    ADD CONSTRAINT cluster_deployment_events_deployment_id_fkey FOREIGN KEY (deployment_id) REFERENCES public.cluster_deployments(id) ON DELETE CASCADE;


--
-- Name: cluster_deployment_events cluster_deployment_events_rollout_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployment_events
    ADD CONSTRAINT cluster_deployment_events_rollout_id_fkey FOREIGN KEY (rollout_id) REFERENCES public.delivery_rollouts(id) ON DELETE SET NULL;


--
-- Name: cluster_deployments cluster_deployments_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE RESTRICT;


--
-- Name: cluster_deployments cluster_deployments_current_rollout_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_current_rollout_id_fkey FOREIGN KEY (current_rollout_id) REFERENCES public.delivery_rollouts(id) ON DELETE SET NULL;


--
-- Name: cluster_deployments cluster_deployments_desired_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_desired_bundle_version_id_fkey FOREIGN KEY (desired_bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE RESTRICT;


--
-- Name: cluster_deployments cluster_deployments_previous_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_previous_bundle_version_id_fkey FOREIGN KEY (previous_bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE RESTRICT;


--
-- Name: cluster_deployments cluster_deployments_target_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_target_id_fkey FOREIGN KEY (target_id) REFERENCES public.delivery_targets(id) ON DELETE RESTRICT;


--
-- Name: cluster_groups cluster_groups_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_groups
    ADD CONSTRAINT cluster_groups_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cluster_groups cluster_groups_parent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_groups
    ADD CONSTRAINT cluster_groups_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES public.cluster_groups(id) ON DELETE CASCADE;


--
-- Name: cluster_health_statuses cluster_health_statuses_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_health_statuses
    ADD CONSTRAINT cluster_health_statuses_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_monitoring_configs cluster_monitoring_configs_backend_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_monitoring_configs
    ADD CONSTRAINT cluster_monitoring_configs_backend_id_fkey FOREIGN KEY (backend_id) REFERENCES public.monitoring_backends(id) ON DELETE CASCADE;


--
-- Name: cluster_monitoring_configs cluster_monitoring_configs_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_monitoring_configs
    ADD CONSTRAINT cluster_monitoring_configs_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_monitoring_configs cluster_monitoring_configs_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_monitoring_configs
    ADD CONSTRAINT cluster_monitoring_configs_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cluster_monitoring_configs cluster_monitoring_configs_storage_config_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_monitoring_configs
    ADD CONSTRAINT cluster_monitoring_configs_storage_config_id_fkey FOREIGN KEY (storage_config_id) REFERENCES public.backup_storage_configs(id) ON DELETE SET NULL;


--
-- Name: cluster_registration_policies cluster_registration_policies_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registration_policies
    ADD CONSTRAINT cluster_registration_policies_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_registration_policies cluster_registration_policies_source_template_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registration_policies
    ADD CONSTRAINT cluster_registration_policies_source_template_id_fkey FOREIGN KEY (source_template_id) REFERENCES public.cluster_templates(id) ON DELETE SET NULL;


--
-- Name: cluster_registration_steps cluster_registration_steps_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registration_steps
    ADD CONSTRAINT cluster_registration_steps_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_registration_tokens cluster_registration_tokens_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registration_tokens
    ADD CONSTRAINT cluster_registration_tokens_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_registry_configs cluster_registry_configs_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_registry_configs
    ADD CONSTRAINT cluster_registry_configs_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_restores cluster_restores_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_restores
    ADD CONSTRAINT cluster_restores_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cluster_restores cluster_restores_snapshot_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_restores
    ADD CONSTRAINT cluster_restores_snapshot_id_fkey FOREIGN KEY (snapshot_id) REFERENCES public.cluster_snapshots(id) ON DELETE CASCADE;


--
-- Name: cluster_restores cluster_restores_target_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_restores
    ADD CONSTRAINT cluster_restores_target_cluster_id_fkey FOREIGN KEY (target_cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_role_bindings cluster_role_bindings_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_role_bindings
    ADD CONSTRAINT cluster_role_bindings_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_role_bindings cluster_role_bindings_group_sync_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_role_bindings
    ADD CONSTRAINT cluster_role_bindings_group_sync_connector_id_fkey FOREIGN KEY (group_sync_connector_id) REFERENCES public.dex_connectors(id) ON DELETE SET NULL;


--
-- Name: cluster_role_bindings cluster_role_bindings_role_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_role_bindings
    ADD CONSTRAINT cluster_role_bindings_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.cluster_roles(id) ON DELETE CASCADE;


--
-- Name: cluster_role_bindings cluster_role_bindings_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_role_bindings
    ADD CONSTRAINT cluster_role_bindings_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: cluster_security_policies cluster_security_policies_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_security_policies
    ADD CONSTRAINT cluster_security_policies_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_security_policies cluster_security_policies_template_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_security_policies
    ADD CONSTRAINT cluster_security_policies_template_id_fkey FOREIGN KEY (template_id) REFERENCES public.pod_security_templates(id) ON DELETE RESTRICT;


--
-- Name: cluster_service_mesh cluster_service_mesh_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_service_mesh
    ADD CONSTRAINT cluster_service_mesh_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_snapshot_schedules cluster_snapshot_schedules_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_snapshot_schedules
    ADD CONSTRAINT cluster_snapshot_schedules_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_snapshot_schedules cluster_snapshot_schedules_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_snapshot_schedules
    ADD CONSTRAINT cluster_snapshot_schedules_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cluster_snapshots cluster_snapshots_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_snapshots
    ADD CONSTRAINT cluster_snapshots_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_snapshots cluster_snapshots_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_snapshots
    ADD CONSTRAINT cluster_snapshots_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cluster_template_applications cluster_template_applications_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_template_applications
    ADD CONSTRAINT cluster_template_applications_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: cluster_template_applications cluster_template_applications_template_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_template_applications
    ADD CONSTRAINT cluster_template_applications_template_id_fkey FOREIGN KEY (template_id) REFERENCES public.cluster_templates(id) ON DELETE RESTRICT;


--
-- Name: cluster_templates cluster_templates_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_templates
    ADD CONSTRAINT cluster_templates_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: cluster_tools cluster_tools_helm_chart_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cluster_tools
    ADD CONSTRAINT cluster_tools_helm_chart_id_fkey FOREIGN KEY (helm_chart_id) REFERENCES public.helm_charts(id) ON DELETE SET NULL;


--
-- Name: clusters clusters_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clusters
    ADD CONSTRAINT clusters_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: clusters clusters_group_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.clusters
    ADD CONSTRAINT clusters_group_id_fkey FOREIGN KEY (group_id) REFERENCES public.cluster_groups(id) ON DELETE SET NULL;


--
-- Name: compliance_baseline_applications compliance_baseline_applications_applied_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compliance_baseline_applications
    ADD CONSTRAINT compliance_baseline_applications_applied_by_fkey FOREIGN KEY (applied_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: compliance_baseline_applications compliance_baseline_applications_baseline_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compliance_baseline_applications
    ADD CONSTRAINT compliance_baseline_applications_baseline_id_fkey FOREIGN KEY (baseline_id) REFERENCES public.compliance_baselines(id) ON DELETE RESTRICT;


--
-- Name: compliance_baseline_applications compliance_baseline_applications_reverted_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.compliance_baseline_applications
    ADD CONSTRAINT compliance_baseline_applications_reverted_by_fkey FOREIGN KEY (reverted_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: component_bundle_versions component_bundle_versions_bundle_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundle_versions
    ADD CONSTRAINT component_bundle_versions_bundle_id_fkey FOREIGN KEY (bundle_id) REFERENCES public.component_bundles(id) ON DELETE CASCADE;


--
-- Name: component_bundle_versions component_bundle_versions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundle_versions
    ADD CONSTRAINT component_bundle_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: component_bundle_versions component_bundle_versions_source_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundle_versions
    ADD CONSTRAINT component_bundle_versions_source_id_fkey FOREIGN KEY (source_id) REFERENCES public.delivery_sources(id) ON DELETE RESTRICT;


--
-- Name: component_bundles component_bundles_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundles
    ADD CONSTRAINT component_bundles_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: component_bundles component_bundles_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundles
    ADD CONSTRAINT component_bundles_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;


--
-- Name: component_bundles component_bundles_updated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.component_bundles
    ADD CONSTRAINT component_bundles_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: control_plane_alerts control_plane_alerts_acknowledged_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_alerts
    ADD CONSTRAINT control_plane_alerts_acknowledged_by_id_fkey FOREIGN KEY (acknowledged_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: control_plane_silences control_plane_silences_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_silences
    ADD CONSTRAINT control_plane_silences_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: control_plane_snapshots control_plane_snapshots_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_snapshots
    ADD CONSTRAINT control_plane_snapshots_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: control_plane_snapshots control_plane_snapshots_requested_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.control_plane_snapshots
    ADD CONSTRAINT control_plane_snapshots_requested_by_id_fkey FOREIGN KEY (requested_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: dashboard_widgets dashboard_widgets_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dashboard_widgets
    ADD CONSTRAINT dashboard_widgets_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: deferred_operations deferred_operations_requested_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deferred_operations
    ADD CONSTRAINT deferred_operations_requested_by_fkey FOREIGN KEY (requested_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: deferred_operations deferred_operations_target_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deferred_operations
    ADD CONSTRAINT deferred_operations_target_cluster_id_fkey FOREIGN KEY (target_cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: deferred_operations deferred_operations_target_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deferred_operations
    ADD CONSTRAINT deferred_operations_target_project_id_fkey FOREIGN KEY (target_project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: deferred_operations deferred_operations_window_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.deferred_operations
    ADD CONSTRAINT deferred_operations_window_id_fkey FOREIGN KEY (window_id) REFERENCES public.maintenance_windows(id) ON DELETE CASCADE;


--
-- Name: delivery_assignment_receipts delivery_assignment_receipts_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_assignment_receipts
    ADD CONSTRAINT delivery_assignment_receipts_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: delivery_controller_inventory delivery_controller_inventory_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_controller_inventory
    ADD CONSTRAINT delivery_controller_inventory_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: delivery_rollout_approvals delivery_rollout_approvals_decided_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_approvals
    ADD CONSTRAINT delivery_rollout_approvals_decided_by_fkey FOREIGN KEY (decided_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: delivery_rollout_approvals delivery_rollout_approvals_rollout_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_approvals
    ADD CONSTRAINT delivery_rollout_approvals_rollout_id_fkey FOREIGN KEY (rollout_id) REFERENCES public.delivery_rollouts(id) ON DELETE CASCADE;


--
-- Name: delivery_rollout_clusters delivery_rollout_clusters_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_clusters
    ADD CONSTRAINT delivery_rollout_clusters_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE RESTRICT;


--
-- Name: delivery_rollout_clusters delivery_rollout_clusters_desired_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_clusters
    ADD CONSTRAINT delivery_rollout_clusters_desired_bundle_version_id_fkey FOREIGN KEY (desired_bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE RESTRICT;


--
-- Name: delivery_rollout_clusters delivery_rollout_clusters_previous_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_clusters
    ADD CONSTRAINT delivery_rollout_clusters_previous_bundle_version_id_fkey FOREIGN KEY (previous_bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE RESTRICT;


--
-- Name: delivery_rollout_clusters delivery_rollout_clusters_rollout_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_clusters
    ADD CONSTRAINT delivery_rollout_clusters_rollout_id_fkey FOREIGN KEY (rollout_id) REFERENCES public.delivery_rollouts(id) ON DELETE CASCADE;


--
-- Name: delivery_rollout_events delivery_rollout_events_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_events
    ADD CONSTRAINT delivery_rollout_events_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: delivery_rollout_events delivery_rollout_events_rollout_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollout_events
    ADD CONSTRAINT delivery_rollout_events_rollout_id_fkey FOREIGN KEY (rollout_id) REFERENCES public.delivery_rollouts(id) ON DELETE CASCADE;


--
-- Name: delivery_rollouts delivery_rollouts_from_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollouts
    ADD CONSTRAINT delivery_rollouts_from_bundle_version_id_fkey FOREIGN KEY (from_bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE RESTRICT;


--
-- Name: delivery_rollouts delivery_rollouts_initiated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollouts
    ADD CONSTRAINT delivery_rollouts_initiated_by_fkey FOREIGN KEY (initiated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: delivery_rollouts delivery_rollouts_target_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollouts
    ADD CONSTRAINT delivery_rollouts_target_id_fkey FOREIGN KEY (target_id) REFERENCES public.delivery_targets(id) ON DELETE RESTRICT;


--
-- Name: delivery_rollouts delivery_rollouts_to_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_rollouts
    ADD CONSTRAINT delivery_rollouts_to_bundle_version_id_fkey FOREIGN KEY (to_bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE RESTRICT;


--
-- Name: delivery_source_resolutions delivery_source_resolutions_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_source_resolutions
    ADD CONSTRAINT delivery_source_resolutions_bundle_version_id_fkey FOREIGN KEY (bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE CASCADE;


--
-- Name: delivery_source_resolutions delivery_source_resolutions_source_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_source_resolutions
    ADD CONSTRAINT delivery_source_resolutions_source_id_fkey FOREIGN KEY (source_id) REFERENCES public.delivery_sources(id) ON DELETE CASCADE;


--
-- Name: delivery_sources delivery_sources_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_sources
    ADD CONSTRAINT delivery_sources_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: delivery_sources delivery_sources_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_sources
    ADD CONSTRAINT delivery_sources_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;


--
-- Name: delivery_sources delivery_sources_updated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_sources
    ADD CONSTRAINT delivery_sources_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: delivery_system_cluster_assignments delivery_system_cluster_assignments_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_cluster_assignments
    ADD CONSTRAINT delivery_system_cluster_assignments_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: delivery_system_cluster_assignments delivery_system_cluster_assignments_desired_release_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_cluster_assignments
    ADD CONSTRAINT delivery_system_cluster_assignments_desired_release_id_fkey FOREIGN KEY (desired_release_id) REFERENCES public.delivery_system_releases(id) ON DELETE RESTRICT;


--
-- Name: delivery_system_cluster_assignments delivery_system_cluster_assignments_previous_release_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_cluster_assignments
    ADD CONSTRAINT delivery_system_cluster_assignments_previous_release_id_fkey FOREIGN KEY (previous_release_id) REFERENCES public.delivery_system_releases(id) ON DELETE RESTRICT;


--
-- Name: delivery_system_cluster_assignments delivery_system_cluster_assignments_rollout_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_cluster_assignments
    ADD CONSTRAINT delivery_system_cluster_assignments_rollout_id_fkey FOREIGN KEY (rollout_id) REFERENCES public.delivery_system_rollouts(id) ON DELETE SET NULL;


--
-- Name: delivery_system_events delivery_system_events_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_events
    ADD CONSTRAINT delivery_system_events_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: delivery_system_events delivery_system_events_release_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_events
    ADD CONSTRAINT delivery_system_events_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.delivery_system_releases(id) ON DELETE RESTRICT;


--
-- Name: delivery_system_events delivery_system_events_rollout_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_events
    ADD CONSTRAINT delivery_system_events_rollout_id_fkey FOREIGN KEY (rollout_id) REFERENCES public.delivery_system_rollouts(id) ON DELETE SET NULL;


--
-- Name: delivery_system_releases delivery_system_releases_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_releases
    ADD CONSTRAINT delivery_system_releases_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: delivery_system_rollouts delivery_system_rollouts_initiated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_rollouts
    ADD CONSTRAINT delivery_system_rollouts_initiated_by_fkey FOREIGN KEY (initiated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: delivery_system_rollouts delivery_system_rollouts_previous_release_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_rollouts
    ADD CONSTRAINT delivery_system_rollouts_previous_release_id_fkey FOREIGN KEY (previous_release_id) REFERENCES public.delivery_system_releases(id) ON DELETE RESTRICT;


--
-- Name: delivery_system_rollouts delivery_system_rollouts_release_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_system_rollouts
    ADD CONSTRAINT delivery_system_rollouts_release_id_fkey FOREIGN KEY (release_id) REFERENCES public.delivery_system_releases(id) ON DELETE RESTRICT;


--
-- Name: delivery_targets delivery_targets_bundle_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_targets
    ADD CONSTRAINT delivery_targets_bundle_version_id_fkey FOREIGN KEY (bundle_version_id) REFERENCES public.component_bundle_versions(id) ON DELETE RESTRICT;


--
-- Name: delivery_targets delivery_targets_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_targets
    ADD CONSTRAINT delivery_targets_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: delivery_targets delivery_targets_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_targets
    ADD CONSTRAINT delivery_targets_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE RESTRICT;


--
-- Name: delivery_targets delivery_targets_updated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.delivery_targets
    ADD CONSTRAINT delivery_targets_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: dex_settings dex_settings_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.dex_settings
    ADD CONSTRAINT dex_settings_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: email_messages email_messages_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_messages
    ADD CONSTRAINT email_messages_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: sso_configurations fk_sso_default_role; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_configurations
    ADD CONSTRAINT fk_sso_default_role FOREIGN KEY (default_global_role_id) REFERENCES public.global_roles(id) ON DELETE SET NULL;


--
-- Name: gitops_registered_clusters gitops_registered_clusters_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gitops_registered_clusters
    ADD CONSTRAINT gitops_registered_clusters_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: gitops_registered_clusters gitops_registered_clusters_source_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gitops_registered_clusters
    ADD CONSTRAINT gitops_registered_clusters_source_id_fkey FOREIGN KEY (source_id) REFERENCES public.gitops_registration_sources(id) ON DELETE CASCADE;


--
-- Name: gitops_registration_sources gitops_registration_sources_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.gitops_registration_sources
    ADD CONSTRAINT gitops_registration_sources_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: global_role_bindings global_role_bindings_group_sync_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_role_bindings
    ADD CONSTRAINT global_role_bindings_group_sync_connector_id_fkey FOREIGN KEY (group_sync_connector_id) REFERENCES public.dex_connectors(id) ON DELETE SET NULL;


--
-- Name: global_role_bindings global_role_bindings_role_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_role_bindings
    ADD CONSTRAINT global_role_bindings_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.global_roles(id) ON DELETE CASCADE;


--
-- Name: global_role_bindings global_role_bindings_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.global_role_bindings
    ADD CONSTRAINT global_role_bindings_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: helm_chart_tags helm_chart_tags_chart_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_chart_tags
    ADD CONSTRAINT helm_chart_tags_chart_id_fkey FOREIGN KEY (chart_id) REFERENCES public.helm_charts(id) ON DELETE CASCADE;


--
-- Name: helm_chart_versions helm_chart_versions_chart_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_chart_versions
    ADD CONSTRAINT helm_chart_versions_chart_id_fkey FOREIGN KEY (chart_id) REFERENCES public.helm_charts(id) ON DELETE CASCADE;


--
-- Name: helm_charts helm_charts_repository_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_charts
    ADD CONSTRAINT helm_charts_repository_id_fkey FOREIGN KEY (repository_id) REFERENCES public.helm_repositories(id) ON DELETE CASCADE;


--
-- Name: helm_repositories helm_repositories_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_repositories
    ADD CONSTRAINT helm_repositories_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: helm_repositories helm_repositories_owner_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.helm_repositories
    ADD CONSTRAINT helm_repositories_owner_project_id_fkey FOREIGN KEY (owner_project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: identity_group_mappings identity_group_mappings_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.identity_group_mappings
    ADD CONSTRAINT identity_group_mappings_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: identity_group_mappings identity_group_mappings_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.identity_group_mappings
    ADD CONSTRAINT identity_group_mappings_connector_id_fkey FOREIGN KEY (connector_id) REFERENCES public.dex_connectors(id) ON DELETE CASCADE;


--
-- Name: identity_group_mappings identity_group_mappings_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.identity_group_mappings
    ADD CONSTRAINT identity_group_mappings_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: image_vulnerabilities image_vulnerabilities_report_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerabilities
    ADD CONSTRAINT image_vulnerabilities_report_id_fkey FOREIGN KEY (report_id) REFERENCES public.image_vulnerability_reports(id) ON DELETE CASCADE;


--
-- Name: image_vulnerability_report_snapshots image_vulnerability_report_snapshots_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerability_report_snapshots
    ADD CONSTRAINT image_vulnerability_report_snapshots_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: image_vulnerability_report_snapshots image_vulnerability_report_snapshots_report_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerability_report_snapshots
    ADD CONSTRAINT image_vulnerability_report_snapshots_report_id_fkey FOREIGN KEY (report_id) REFERENCES public.image_vulnerability_reports(id) ON DELETE CASCADE;


--
-- Name: image_vulnerability_reports image_vulnerability_reports_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_vulnerability_reports
    ADD CONSTRAINT image_vulnerability_reports_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: installed_charts installed_charts_chart_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.installed_charts
    ADD CONSTRAINT installed_charts_chart_version_id_fkey FOREIGN KEY (chart_version_id) REFERENCES public.helm_chart_versions(id) ON DELETE SET NULL;


--
-- Name: installed_charts installed_charts_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.installed_charts
    ADD CONSTRAINT installed_charts_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: installed_charts installed_charts_installed_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.installed_charts
    ADD CONSTRAINT installed_charts_installed_by_id_fkey FOREIGN KEY (installed_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: jwt_revocations jwt_revocations_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.jwt_revocations
    ADD CONSTRAINT jwt_revocations_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: kubectl_session_commands kubectl_session_commands_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.kubectl_session_commands
    ADD CONSTRAINT kubectl_session_commands_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.kubectl_sessions(id) ON DELETE CASCADE;


--
-- Name: kubectl_sessions kubectl_sessions_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.kubectl_sessions
    ADD CONSTRAINT kubectl_sessions_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: kubectl_sessions kubectl_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.kubectl_sessions
    ADD CONSTRAINT kubectl_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: logging_operation_events logging_operation_events_operation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_operation_events
    ADD CONSTRAINT logging_operation_events_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.logging_operations(id) ON DELETE CASCADE;


--
-- Name: logging_operations logging_operations_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_operations
    ADD CONSTRAINT logging_operations_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: logging_outputs logging_outputs_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_outputs
    ADD CONSTRAINT logging_outputs_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: logging_outputs logging_outputs_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_outputs
    ADD CONSTRAINT logging_outputs_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: logging_pipeline_outputs logging_pipeline_outputs_logging_output_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_pipeline_outputs
    ADD CONSTRAINT logging_pipeline_outputs_logging_output_id_fkey FOREIGN KEY (logging_output_id) REFERENCES public.logging_outputs(id) ON DELETE CASCADE;


--
-- Name: logging_pipeline_outputs logging_pipeline_outputs_logging_pipeline_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_pipeline_outputs
    ADD CONSTRAINT logging_pipeline_outputs_logging_pipeline_id_fkey FOREIGN KEY (logging_pipeline_id) REFERENCES public.logging_pipelines(id) ON DELETE CASCADE;


--
-- Name: logging_pipelines logging_pipelines_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_pipelines
    ADD CONSTRAINT logging_pipelines_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: logging_pipelines logging_pipelines_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.logging_pipelines
    ADD CONSTRAINT logging_pipelines_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: maintenance_windows maintenance_windows_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.maintenance_windows
    ADD CONSTRAINT maintenance_windows_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: mirrored_gateway_classes mirrored_gateway_classes_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_gateway_classes
    ADD CONSTRAINT mirrored_gateway_classes_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: mirrored_ingress_classes mirrored_ingress_classes_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_ingress_classes
    ADD CONSTRAINT mirrored_ingress_classes_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: mirrored_limit_ranges mirrored_limit_ranges_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_limit_ranges
    ADD CONSTRAINT mirrored_limit_ranges_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: mirrored_network_policies mirrored_network_policies_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_network_policies
    ADD CONSTRAINT mirrored_network_policies_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: mirrored_resource_quotas mirrored_resource_quotas_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mirrored_resource_quotas
    ADD CONSTRAINT mirrored_resource_quotas_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: monitoring_backends monitoring_backends_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.monitoring_backends
    ADD CONSTRAINT monitoring_backends_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: monitoring_operation_events monitoring_operation_events_operation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.monitoring_operation_events
    ADD CONSTRAINT monitoring_operation_events_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.monitoring_operations(id) ON DELETE CASCADE;


--
-- Name: monitoring_operations monitoring_operations_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.monitoring_operations
    ADD CONSTRAINT monitoring_operations_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: native_rbac_rules native_rbac_rules_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.native_rbac_rules
    ADD CONSTRAINT native_rbac_rules_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: native_rbac_rules native_rbac_rules_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.native_rbac_rules
    ADD CONSTRAINT native_rbac_rules_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: native_rbac_rules native_rbac_rules_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.native_rbac_rules
    ADD CONSTRAINT native_rbac_rules_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: network_policy_applications network_policy_applications_applied_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_applications
    ADD CONSTRAINT network_policy_applications_applied_by_fkey FOREIGN KEY (applied_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: network_policy_applications network_policy_applications_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_applications
    ADD CONSTRAINT network_policy_applications_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: network_policy_applications network_policy_applications_template_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_applications
    ADD CONSTRAINT network_policy_applications_template_id_fkey FOREIGN KEY (template_id) REFERENCES public.network_policy_templates(id) ON DELETE CASCADE;


--
-- Name: network_policy_templates network_policy_templates_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.network_policy_templates
    ADD CONSTRAINT network_policy_templates_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: notification_channels notification_channels_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_channels
    ADD CONSTRAINT notification_channels_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: notification_templates notification_templates_updated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notification_templates
    ADD CONSTRAINT notification_templates_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: password_reset_tokens password_reset_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.password_reset_tokens
    ADD CONSTRAINT password_reset_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: platform_configuration platform_configuration_default_cluster_template_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_configuration
    ADD CONSTRAINT platform_configuration_default_cluster_template_id_fkey FOREIGN KEY (default_cluster_template_id) REFERENCES public.cluster_templates(id) ON DELETE SET NULL;


--
-- Name: platform_settings platform_settings_updated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_settings
    ADD CONSTRAINT platform_settings_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: pod_security_templates pod_security_templates_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pod_security_templates
    ADD CONSTRAINT pod_security_templates_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: project_catalog_subscriptions project_catalog_subscriptions_catalog_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_catalog_subscriptions
    ADD CONSTRAINT project_catalog_subscriptions_catalog_id_fkey FOREIGN KEY (catalog_id) REFERENCES public.helm_repositories(id) ON DELETE CASCADE;


--
-- Name: project_catalog_subscriptions project_catalog_subscriptions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_catalog_subscriptions
    ADD CONSTRAINT project_catalog_subscriptions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: project_catalog_subscriptions project_catalog_subscriptions_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_catalog_subscriptions
    ADD CONSTRAINT project_catalog_subscriptions_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: project_namespaces project_namespaces_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_namespaces
    ADD CONSTRAINT project_namespaces_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: project_namespaces project_namespaces_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_namespaces
    ADD CONSTRAINT project_namespaces_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: project_role_bindings project_role_bindings_group_sync_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_role_bindings
    ADD CONSTRAINT project_role_bindings_group_sync_connector_id_fkey FOREIGN KEY (group_sync_connector_id) REFERENCES public.dex_connectors(id) ON DELETE SET NULL;


--
-- Name: project_role_bindings project_role_bindings_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_role_bindings
    ADD CONSTRAINT project_role_bindings_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: project_role_bindings project_role_bindings_role_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_role_bindings
    ADD CONSTRAINT project_role_bindings_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.project_roles(id) ON DELETE CASCADE;


--
-- Name: project_role_bindings project_role_bindings_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_role_bindings
    ADD CONSTRAINT project_role_bindings_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: projects projects_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: projects projects_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: projects projects_default_vault_connection_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_default_vault_connection_id_fkey FOREIGN KEY (default_vault_connection_id) REFERENCES public.vault_connections(id) ON DELETE SET NULL;


--
-- Name: projects projects_quota_plan_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_quota_plan_fkey FOREIGN KEY (quota_plan) REFERENCES public.quota_plans(name) ON DELETE SET DEFAULT;


--
-- Name: read_audit_policies read_audit_policies_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.read_audit_policies
    ADD CONSTRAINT read_audit_policies_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: restore_operations restore_operations_backup_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.restore_operations
    ADD CONSTRAINT restore_operations_backup_id_fkey FOREIGN KEY (backup_id) REFERENCES public.backups(id) ON DELETE CASCADE;


--
-- Name: restore_operations restore_operations_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.restore_operations
    ADD CONSTRAINT restore_operations_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: restore_operations restore_operations_initiated_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.restore_operations
    ADD CONSTRAINT restore_operations_initiated_by_id_fkey FOREIGN KEY (initiated_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: security_scan_results security_scan_results_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.security_scan_results
    ADD CONSTRAINT security_scan_results_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE CASCADE;


--
-- Name: security_scan_results security_scan_results_initiated_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.security_scan_results
    ADD CONSTRAINT security_scan_results_initiated_by_id_fkey FOREIGN KEY (initiated_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: siem_forward_queue siem_forward_queue_forwarder_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forward_queue
    ADD CONSTRAINT siem_forward_queue_forwarder_id_fkey FOREIGN KEY (forwarder_id) REFERENCES public.siem_forwarders(id) ON DELETE CASCADE;


--
-- Name: siem_forwarder_status siem_forwarder_status_forwarder_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forwarder_status
    ADD CONSTRAINT siem_forwarder_status_forwarder_id_fkey FOREIGN KEY (forwarder_id) REFERENCES public.siem_forwarders(id) ON DELETE CASCADE;


--
-- Name: siem_forwarders siem_forwarders_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.siem_forwarders
    ADD CONSTRAINT siem_forwarders_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: sso_sessions sso_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_sessions
    ADD CONSTRAINT sso_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: tool_operation_events tool_operation_events_operation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_operation_events
    ADD CONSTRAINT tool_operation_events_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.tool_operations(id) ON DELETE CASCADE;


--
-- Name: tool_operations tool_operations_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_operations
    ADD CONSTRAINT tool_operations_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: tunnel_locator_events tunnel_locator_events_cluster_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tunnel_locator_events
    ADD CONSTRAINT tunnel_locator_events_cluster_id_fkey FOREIGN KEY (cluster_id) REFERENCES public.clusters(id) ON DELETE SET NULL;


--
-- Name: ui_extensions ui_extensions_installed_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ui_extensions
    ADD CONSTRAINT ui_extensions_installed_by_fkey FOREIGN KEY (installed_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: user_idp_groups user_idp_groups_connector_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_idp_groups
    ADD CONSTRAINT user_idp_groups_connector_id_fkey FOREIGN KEY (connector_id) REFERENCES public.dex_connectors(id) ON DELETE SET NULL;


--
-- Name: user_idp_groups user_idp_groups_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_idp_groups
    ADD CONSTRAINT user_idp_groups_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: user_totp_enrollments user_totp_enrollments_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_totp_enrollments
    ADD CONSTRAINT user_totp_enrollments_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: user_totp_recovery_codes user_totp_recovery_codes_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_totp_recovery_codes
    ADD CONSTRAINT user_totp_recovery_codes_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: users users_quota_plan_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_quota_plan_fkey FOREIGN KEY (quota_plan) REFERENCES public.quota_plans(name) ON DELETE SET DEFAULT;


--
-- Name: vault_connections vault_connections_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vault_connections
    ADD CONSTRAINT vault_connections_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: webhook_deliveries webhook_deliveries_subscription_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webhook_deliveries
    ADD CONSTRAINT webhook_deliveries_subscription_id_fkey FOREIGN KEY (subscription_id) REFERENCES public.webhook_subscriptions(id) ON DELETE CASCADE;


--
-- Name: webhook_subscriptions webhook_subscriptions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.webhook_subscriptions
    ADD CONSTRAINT webhook_subscriptions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: workload_operation_events workload_operation_events_operation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workload_operation_events
    ADD CONSTRAINT workload_operation_events_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES public.workload_operations(id) ON DELETE CASCADE;


--
-- Name: workload_operations workload_operations_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workload_operations
    ADD CONSTRAINT workload_operations_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- PostgreSQL database dump complete
--



-- Create rolling audit partitions relative to install time.
SELECT public.create_audit_log_partition(now());
SELECT public.create_audit_log_partition(now() + INTERVAL '1 month');
