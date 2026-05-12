-- Vault integration (migration 067).
--
-- HashiCorp Vault as the operator's source-of-truth for secrets that
-- end up in helm install values / tool presets / cluster-template tool
-- blobs. Operators configure one or more Vault connections (URL + auth
-- method), optionally pin a default connection per project, and write
-- "${vault://<connection>/<engine>/<path>#<key>}" anywhere a values blob
-- is accepted. The resolver fetches each reference at install time and
-- substitutes the cleartext value INLINE, in memory only — the
-- resolved blob is never persisted to the DB or audit log. The ORIGINAL
-- blob (with "${vault://...}" still in it) is what we store in
-- helm_installations.values_override, so on every upgrade the operator
-- gets a fresh re-resolve and a rotated secret takes effect without
-- editing values.
--
-- Auth methods:
--   - token:      static token (dev / quick wiring)
--   - approle:    role_id + secret_id (recommended for static workloads)
--   - kubernetes: in-cluster SA JWT exchanged for a Vault token
--                 (recommended in production — the astronomer pod's SA
--                 is bound to the Vault role, no static creds).
--
-- The auth_encrypted column carries a Fernet-encrypted JSON blob whose
-- shape depends on auth_method (see column comment). The handler
-- decrypts + redacts on GET, treats the literal sentinel value
-- "<encrypted>" on PUT as "preserve the stored blob" so a natural
-- GET → edit → PUT loop doesn't blank the secret.
--
-- Token caching: vault_connections.cached_token_expires_at is the
-- handler's hint to re-auth proactively a few seconds before the
-- ttl runs out. On 403 from a kv read, the resolver drops the
-- cached token and re-authenticates once before failing.
--
-- Migration safety:
--   - Every NOT NULL column has a DEFAULT on the same line so the
--     check-migrations.sh T30 lint accepts the file. Operators
--     carry zero rows so the defaults never run against populated
--     data.
--   - ON DELETE SET NULL on projects.default_vault_connection_id so
--     deleting a Vault connection doesn't cascade-delete projects;
--     the project simply loses its default and any subsequent
--     install with an unqualified reference fails clear.

CREATE TABLE vault_connections (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    -- Vault URL. https:// required outside dev (the handler refuses
    -- http:// when tls_skip_verify is false to avoid the obvious
    -- mistake of "I set a real prod address but forgot the scheme").
    addr            TEXT NOT NULL,
    -- "token" | "approle" | "kubernetes". Constrained by CHECK below
    -- so a typo at write time fails fast rather than at resolve time.
    auth_method     VARCHAR(32) NOT NULL,
    -- Fernet-encrypted auth blob. Shape depends on auth_method:
    --   token:      { token: "..." }
    --   approle:    { role_id: "...", secret_id: "..." }
    --   kubernetes: { role: "...", jwt_path: "/var/run/secrets/..." }
    -- jwt_path defaults to the in-cluster SA token mount when empty.
    auth_encrypted  TEXT NOT NULL DEFAULT '',
    -- Optional Vault namespace (Vault Enterprise multi-tenancy).
    -- Sent in the X-Vault-Namespace header on every request.
    namespace       VARCHAR(128) NOT NULL DEFAULT '',
    -- TLS knobs. tls_skip_verify is for self-signed dev clusters; the
    -- handler refuses to honor it on http:// URLs to keep operator
    -- mistakes loud. ca_cert_pem is the PEM bundle the client trusts
    -- on top of the system roots.
    tls_skip_verify BOOLEAN NOT NULL DEFAULT false,
    ca_cert_pem     TEXT NOT NULL DEFAULT '',
    -- Default mount for "${vault://path}" without an explicit engine
    -- prefix. e.g. "secret" or "kv". The resolver attempts KV v2
    -- ("<mount>/data/<path>") first; on 404 it falls back to KV v1
    -- ("<mount>/<path>") so operators don't have to declare which
    -- engine version their mount is.
    default_mount   VARCHAR(64) NOT NULL DEFAULT 'secret',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    -- Cached token lifecycle. The resolver re-uses a cached client
    -- token while it's valid; on 403/expiry it re-authenticates.
    -- Refreshed every successful auth — never required to be
    -- accurate; the in-process cache is the authoritative source.
    cached_token_expires_at TIMESTAMPTZ,
    last_health_at  TIMESTAMPTZ,
    last_health_ok  BOOLEAN NOT NULL DEFAULT false,
    last_error      TEXT NOT NULL DEFAULT '',
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT auth_method_valid CHECK (auth_method IN ('token','approle','kubernetes'))
);
CREATE INDEX idx_vault_connections_enabled ON vault_connections (enabled);

-- A project may default to one Vault connection. The reference syntax
-- "${vault://path}" looks up against this project's default connection;
-- "${vault://prod/path}" looks up against the connection named "prod".
-- ON DELETE SET NULL so deleting a Vault connection doesn't cascade-
-- break the project — the project simply loses its default and the
-- next install with an unqualified reference fails clear.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS default_vault_connection_id UUID
    REFERENCES vault_connections(id) ON DELETE SET NULL;
