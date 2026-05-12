-- Migration 069 — CRD-mirror v2.
--
-- Sprint 014 introduced two management-plane CRDs (Cluster, Project) that
-- the controller reconciles against authoritative DB rows. Sprint 069 adds
-- the inverse direction: a per-cluster CRD-mirror agent streams READ-ONLY
-- snapshots of five cluster/namespace-scoped Kubernetes resources back to
-- the management plane so the cluster-detail UI can render a
-- "what's installed" view without a fresh kubectl round-trip per click.
--
-- The five resources mirrored here are:
--   - networking.k8s.io/v1 IngressClass        (cluster-scoped)
--   - gateway.networking.k8s.io/v1 GatewayClass (cluster-scoped)
--   - networking.k8s.io/v1 NetworkPolicy       (namespace-scoped)
--   - v1 ResourceQuota                         (namespace-scoped)
--   - v1 LimitRange                            (namespace-scoped)
--
-- All five tables are upsert-driven by the ingester (one INSERT ... ON
-- CONFLICT ... DO UPDATE per event). last_seen_at is touched on every
-- ingest so a periodic prune (every 30m) can drop rows older than 1h —
-- because the agent re-sends every object on reconnect, a stale row is
-- an unambiguous "this is gone" signal even when the watcher missed a
-- delete event.
--
-- Each row carries the FK to clusters(id) with ON DELETE CASCADE so a
-- cluster decommission walks the whole mirror set automatically.

-- IngressClasses are cluster-scoped. Each row is unique by (cluster_id, name).
CREATE TABLE mirrored_ingress_classes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    name            VARCHAR(253) NOT NULL,
    controller      VARCHAR(253) NOT NULL DEFAULT '',
    parameters      JSONB NOT NULL DEFAULT '{}',
    -- True when annotation ingressclass.kubernetes.io/is-default-class=true.
    is_default      BOOLEAN NOT NULL DEFAULT false,
    labels          JSONB NOT NULL DEFAULT '{}',
    annotations     JSONB NOT NULL DEFAULT '{}',
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);
CREATE INDEX idx_mirrored_ic_cluster ON mirrored_ingress_classes (cluster_id);

-- GatewayClasses — cluster-scoped. accepted_status mirrors the Accepted
-- condition on .status.conditions ("True" / "False" / "Unknown").
CREATE TABLE mirrored_gateway_classes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    name            VARCHAR(253) NOT NULL,
    controller_name VARCHAR(253) NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    parameters      JSONB NOT NULL DEFAULT '{}',
    accepted_status VARCHAR(64) NOT NULL DEFAULT '',
    labels          JSONB NOT NULL DEFAULT '{}',
    annotations     JSONB NOT NULL DEFAULT '{}',
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, name)
);
CREATE INDEX idx_mirrored_gwc_cluster ON mirrored_gateway_classes (cluster_id);

-- NetworkPolicies — namespace-scoped. Mirrors ALL NetworkPolicies in the
-- cluster, including operator-created ones. The is_managed marker is
-- computed at ingest time from labels (app.kubernetes.io/managed-by=
-- astronomer) so the UI can disambiguate astronomer-owned policies from
-- everything else without re-deriving the rule on every read.
CREATE TABLE mirrored_network_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespace       VARCHAR(253) NOT NULL,
    name            VARCHAR(253) NOT NULL,
    pod_selector    JSONB NOT NULL DEFAULT '{}',
    policy_types    JSONB NOT NULL DEFAULT '[]',
    ingress_rules   JSONB NOT NULL DEFAULT '[]',
    egress_rules    JSONB NOT NULL DEFAULT '[]',
    labels          JSONB NOT NULL DEFAULT '{}',
    annotations     JSONB NOT NULL DEFAULT '{}',
    is_managed      BOOLEAN NOT NULL DEFAULT false,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, namespace, name)
);
CREATE INDEX idx_mirrored_np_cluster_ns ON mirrored_network_policies (cluster_id, namespace);

-- ResourceQuotas — namespace-scoped. hard / used are kept as opaque JSONB
-- blobs so the UI can render arbitrary quota keys (cpu, memory, pods,
-- count/configmaps, ...) without a schema migration per new key.
CREATE TABLE mirrored_resource_quotas (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespace       VARCHAR(253) NOT NULL,
    name            VARCHAR(253) NOT NULL,
    hard            JSONB NOT NULL DEFAULT '{}',
    used            JSONB NOT NULL DEFAULT '{}',
    scopes          JSONB NOT NULL DEFAULT '[]',
    labels          JSONB NOT NULL DEFAULT '{}',
    annotations     JSONB NOT NULL DEFAULT '{}',
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, namespace, name)
);
CREATE INDEX idx_mirrored_rq_cluster_ns ON mirrored_resource_quotas (cluster_id, namespace);

-- LimitRanges — namespace-scoped. limits is the raw spec.limits array.
CREATE TABLE mirrored_limit_ranges (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespace       VARCHAR(253) NOT NULL,
    name            VARCHAR(253) NOT NULL,
    limits          JSONB NOT NULL DEFAULT '[]',
    labels          JSONB NOT NULL DEFAULT '{}',
    annotations     JSONB NOT NULL DEFAULT '{}',
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, namespace, name)
);
CREATE INDEX idx_mirrored_lr_cluster_ns ON mirrored_limit_ranges (cluster_id, namespace);
