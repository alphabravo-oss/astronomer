-- +goose Up
-- Retain operation provenance after catalog/project deletion. Scope UUIDs are
-- immutable historical identifiers rather than cascading foreign keys. Creation
-- is checked against the current catalog target by the generated INSERT query.
CREATE TABLE public.identity_provisioning_operations (
    id uuid PRIMARY KEY,
    issuer text NOT NULL CHECK (length(issuer) BETWEEN 1 AND 2048),
    project_id uuid NOT NULL,
    installation_id uuid NOT NULL,
    credential_namespace text NOT NULL CHECK (length(credential_namespace) BETWEEN 1 AND 63),
    generation bigint NOT NULL CHECK (generation > 0),
    desired_spec_digest text NOT NULL CHECK (desired_spec_digest ~ '^sha256:[0-9a-f]{64}$'),
    operation text NOT NULL CHECK (operation IN ('register', 'rotate', 'retire')),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'provisioning', 'rotating', 'recovery_required', 'ready', 'retiring', 'retired')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    actor_id uuid NOT NULL,
    correlation_id uuid NOT NULL,
    upstream_org_id text NOT NULL DEFAULT '' CHECK (length(upstream_org_id) <= 1024),
    upstream_project_id text NOT NULL DEFAULT '' CHECK (length(upstream_project_id) <= 1024),
    upstream_app_id text NOT NULL DEFAULT '' CHECK (length(upstream_app_id) <= 1024),
    upstream_client_id text NOT NULL DEFAULT '' CHECK (length(upstream_client_id) <= 1024),
    credential_secret_name text NOT NULL DEFAULT '' CHECK (length(credential_secret_name) <= 253),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issuer, project_id, installation_id, generation),
    CHECK (state <> 'ready' OR (upstream_org_id <> '' AND upstream_project_id <> '' AND upstream_app_id <> '' AND upstream_client_id <> '' AND credential_secret_name <> ''))
);
CREATE INDEX identity_provisioning_operations_recovery_idx
ON public.identity_provisioning_operations (updated_at, id)
WHERE state IN ('provisioning', 'rotating', 'retiring', 'recovery_required');
