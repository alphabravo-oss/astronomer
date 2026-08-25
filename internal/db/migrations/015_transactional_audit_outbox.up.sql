-- Durable audit intent written in the same transaction as domain state.
--
-- The dispatcher copies each row into the partitioned audit_log table with
-- the same (id, event_created_at) key and marks it delivered in one SQL
-- statement. A process crash can therefore retry without creating duplicate
-- audit evidence. The outbox deliberately stores already-sanitized detail;
-- raw request bodies and credentials never belong here.
CREATE TABLE public.audit_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dedupe_key text NOT NULL,
    event_created_at timestamptz NOT NULL DEFAULT now(),
    schema_version varchar(32) NOT NULL DEFAULT 'audit-v1',
    user_id uuid,
    actor_auth_method varchar(32) NOT NULL DEFAULT '',
    action varchar(64) NOT NULL,
    resource_type varchar(64) NOT NULL,
    resource_id varchar(255) NOT NULL DEFAULT '',
    resource_name varchar(255) NOT NULL DEFAULT '',
    http_method varchar(16) NOT NULL DEFAULT '',
    path text NOT NULL DEFAULT '',
    status_code integer NOT NULL DEFAULT 0,
    duration_ms bigint NOT NULL DEFAULT 0,
    request_id varchar(64) NOT NULL DEFAULT '',
    ip_address inet,
    user_agent text NOT NULL DEFAULT '',
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    source varchar(16) NOT NULL DEFAULT 'service',
    correlation_id varchar(64) NOT NULL DEFAULT '',
    action_class varchar(16) NOT NULL DEFAULT 'mutation',
    status text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 20,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until timestamptz,
    delivered_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_outbox_dedupe_key_nonempty CHECK (length(btrim(dedupe_key)) BETWEEN 1 AND 512),
    CONSTRAINT audit_outbox_action_nonempty CHECK (length(btrim(action)) > 0),
    CONSTRAINT audit_outbox_resource_type_nonempty CHECK (length(btrim(resource_type)) > 0),
    CONSTRAINT audit_outbox_action_class_valid CHECK (action_class IN ('mutation', 'read', 'auth', 'system')),
    CONSTRAINT audit_outbox_status_valid CHECK (status IN ('pending', 'delivering', 'failed', 'delivered', 'dead')),
    CONSTRAINT audit_outbox_attempt_count_valid CHECK (attempt_count >= 0),
    CONSTRAINT audit_outbox_max_attempts_valid CHECK (max_attempts > 0),
    CONSTRAINT audit_outbox_detail_bounded CHECK (octet_length(detail::text) <= 65536)
);

CREATE UNIQUE INDEX audit_outbox_dedupe_key_idx
    ON public.audit_outbox (dedupe_key);

CREATE INDEX audit_outbox_due_idx
    ON public.audit_outbox (next_attempt_at, created_at, id)
    WHERE status IN ('pending', 'failed', 'delivering');

CREATE INDEX audit_outbox_dead_idx
    ON public.audit_outbox (updated_at DESC, id DESC)
    WHERE status = 'dead';

-- SIEM retries use the stable audit event UUID as a destination dedupe key.
-- Existing non-audit producers may continue to leave this column NULL.
ALTER TABLE public.siem_forward_queue
    ADD COLUMN dedupe_key text;

CREATE UNIQUE INDEX siem_forward_queue_dedupe_idx
    ON public.siem_forward_queue (forwarder_id, dedupe_key)
    WHERE dedupe_key IS NOT NULL;
