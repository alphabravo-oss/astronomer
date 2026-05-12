-- Cloud credentials (migration 053).
--
-- Rancher-style "Cloud Credentials" pattern: operators store cloud secrets
-- (AWS / GCP / Azure / generic) at the project level once, then reference
-- them by name from member-cluster workloads. Astronomer materializes the
-- creds as a Kubernetes Secret in the target namespace + cluster on
-- demand; the only cleartext copy lives in those k8s Secrets (gated by
-- k8s RBAC + node-level encryption-at-rest), with the persisted blob in
-- Postgres being Fernet-encrypted.
--
-- Model:
--   - cloud_credentials is one row per (project, name) pair. The
--     provider column selects which validation schema the registry
--     applies at write time (required keys must be present; unknown
--     keys are rejected except for 'generic'). data_encrypted is the
--     Fernet-encrypted JSON object {key: value} that the registry
--     decrypts at materialize time.
--   - target_refs is the JSONB list of "materialize me into these k8s
--     locations" entries. The handler upserts cloud_credential_-
--     materializations rows from this list on every write so the
--     periodic drift sweep can drive convergence without re-reading
--     the JSONB.
--   - cloud_credential_materializations is the per-(cluster, namespace)
--     status row. A worker task fans out across these rows on every
--     credential mutation (and every 30m as the drift sweep). status
--     is "pending" until the apply succeeds, then "applied"; failures
--     stamp last_error so the UI can surface "this credential failed
--     to materialize in cluster X".
--
-- Migration safety:
--   - Every NOT NULL has a DEFAULT on the same line (check-migrations.sh
--     T30 lint requirement). Operators upgrading carry zero rows in
--     either new table, so the defaults never run against populated
--     data.
--   - ON DELETE CASCADE on both FK pairs (credential→project, cred→
--     materializations) so removing a project cleanly removes its
--     creds; removing a credential cleanly removes its materialization
--     bookkeeping. The actual in-cluster Secret is deleted by the
--     worker task before the credential row goes away (handler-side
--     orchestration; see handler/cloud_credentials.go Delete).

CREATE TABLE cloud_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            VARCHAR(128) NOT NULL,
    -- "aws" | "gcp" | "azure" | "generic" — validated by the registry
    -- at write time. The DB doesn't constrain the set so a future
    -- provider plugin can be added without a migration.
    provider        VARCHAR(32) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    -- Fernet-encrypted JSON blob. Schema validated against the provider's
    -- registry at write time so a future GET can decrypt + return the
    -- structure without operator typos.
    data_encrypted  TEXT NOT NULL,
    -- Where this credential should be materialized as a k8s Secret.
    -- One element per (cluster_id, namespace, secret_name) the operator
    -- wants the secret in. Empty list means "store-only" (the creds
    -- exist for future reference but no k8s materialization runs).
    target_refs     JSONB NOT NULL DEFAULT '[]',
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
CREATE INDEX idx_cloud_credentials_project ON cloud_credentials (project_id);

CREATE TABLE cloud_credential_materializations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    credential_id       UUID NOT NULL REFERENCES cloud_credentials(id) ON DELETE CASCADE,
    cluster_id          UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespace           VARCHAR(63) NOT NULL,
    secret_name         VARCHAR(253) NOT NULL,
    -- "pending" | "applied" | "failed". Drift sweep flips applied→pending
    -- when the in-cluster Secret is missing; the apply path flips
    -- pending→applied on success and pending→failed (with last_error
    -- stamped) on a tunnel/k8s error.
    status              VARCHAR(16) NOT NULL DEFAULT 'pending',
    last_applied_at     TIMESTAMPTZ,
    last_error          TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (credential_id, cluster_id, namespace)
);
CREATE INDEX idx_cloud_credential_materializations_credential ON cloud_credential_materializations (credential_id);
CREATE INDEX idx_cloud_credential_materializations_cluster ON cloud_credential_materializations (cluster_id);
