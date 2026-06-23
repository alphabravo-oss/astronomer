-- P1 item 7/22 — apiserver-audit-collect.
--
-- Per-cluster sink for kube-apiserver (and, later, kubelet) audit events
-- streamed back to the management plane by the agent. One row per audit
-- event. The agent tails the apiserver audit log, batches events, and
-- POSTs them to the ingest endpoint; the read endpoint lets operators
-- list what was collected for a cluster.
--
-- `audit_id` is the kube-apiserver-assigned event UUID (the `auditID`
-- field of an audit.k8s.io Event). It is unique per (cluster_id, audit_id)
-- so a re-delivered batch is idempotent and never double-counts.
--
-- `raw` keeps the full audit.k8s.io Event JSON so the schema can evolve
-- without a migration; the promoted columns are just the ones the list
-- view and any future filter path need indexed.

CREATE TABLE apiserver_audit_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL,
    audit_id    TEXT NOT NULL,
    -- audit.k8s.io stage: ResponseComplete, RequestReceived, etc.
    stage       TEXT NOT NULL DEFAULT '',
    -- HTTP verb (get/list/create/update/delete/...).
    verb        TEXT NOT NULL DEFAULT '',
    -- Requesting user (impersonated or real).
    username    TEXT NOT NULL DEFAULT '',
    -- Requested resource (e.g. "pods", "secrets").
    resource    TEXT NOT NULL DEFAULT '',
    namespace   TEXT NOT NULL DEFAULT '',
    -- HTTP response status code, 0 when unknown.
    status_code INTEGER NOT NULL DEFAULT 0,
    -- Event timestamp as reported by the apiserver.
    event_time  TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw         JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, audit_id)
);
CREATE INDEX idx_apiserver_audit_events_cluster_time
    ON apiserver_audit_events (cluster_id, event_time DESC);
