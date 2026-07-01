-- Native per-CRD/GVR RBAC rules — a fine-grained, ADDITIVE allow layer that
-- complements the coarse resource/verb model in the *_roles tables.
--
-- Why this exists: the coarse model collapses EVERY custom resource into a
-- single `custom_resources` bucket, so `custom_resources:read` grants read on
-- every CRD on the cluster. There is no way to say "this user may read
-- cert-manager Certificates but not ExternalSecrets". A native rule closes
-- that gap: it names an exact (api_group, resource, verb) — optionally scoped
-- to a cluster and/or namespace — and is consulted by the k8s-proxy
-- authorization hook ONLY when the coarse check has already denied. It can
-- therefore only ever GRANT access an operator explicitly authored; it never
-- widens a coarse grant on its own and never narrows one.
--
-- Subject: a specific user (groups are a later addition — the proxy authz
-- hook resolves the caller by user id, not group membership).
--
-- Scope:
--   cluster_id NULL  -> applies on every cluster the user can reach.
--   cluster_id set   -> applies only on that cluster.
--   namespace  ''    -> any namespace (subject to the request's own path).
--   namespace  set   -> only that namespace.
--
-- api_group '' is the core group (api/v1); resource '*' means every resource
-- in the group; a '*' entry in verbs means every verb. The evaluator refuses
-- to honor privilege-escalation api groups (rbac.authorization.k8s.io, etc.)
-- and the exec/logs verbs regardless of what is stored here, so a native rule
-- can never be used to escalate to cluster-admin or open a pod shell — those
-- still require an explicit coarse grant.

CREATE TABLE native_rbac_rules (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- NULL = all clusters.
    cluster_id    UUID REFERENCES clusters(id) ON DELETE CASCADE,
    -- '' = all namespaces (within the request's own path scope).
    namespace     TEXT NOT NULL DEFAULT '',
    -- '' = core group; otherwise the CRD's api group (e.g. cert-manager.io).
    api_group     TEXT NOT NULL DEFAULT '',
    -- '*' = every resource in the group; otherwise the plural resource name.
    resource      TEXT NOT NULL,
    -- Coarse verb vocabulary (read | list | watch | create | update | delete),
    -- matching rbac.Verb, so the authoring UI reuses one verb set. '*' = all.
    verbs         TEXT[] NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_id UUID REFERENCES users(id) ON DELETE SET NULL
);

-- Hot path: the authz hook loads a user's rules on (cache-miss) every proxied
-- request, so index by subject.
CREATE INDEX idx_native_rbac_rules_user ON native_rbac_rules (user_id);
