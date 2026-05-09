-- Phase B4: Dex shim for enterprise auth.
--
-- Astronomer-go itself only ever speaks OIDC (see Phase A1's
-- internal/auth/oauth.go generic OIDC path). Dex brokers the messy IdPs —
-- Azure AD, LDAP, SAML, Okta, GitLab, etc. The two tables below let the
-- operator describe their Dex setup through our UI/API: each connector is
-- one entry in dex_connectors; the singleton dex_settings row holds the
-- top-level Dex config (issuer URL, expiry, public clients).
--
-- Sensitive config values (clientSecret, bindPW, ...) are encrypted at
-- write time via the Fernet Encryptor and stored back into config JSONB
-- under the same key. The handler is responsible for round-tripping that
-- encryption. The schema deliberately stays opaque to which fields are
-- secret so we can add new connector types without a migration.
--
-- Idempotently inserts a `dex` row into `cluster_tools` so the existing
-- tools UI can install Dex like any other catalog entry. ON CONFLICT keeps
-- migration re-runs harmless.

CREATE TABLE IF NOT EXISTS dex_connectors (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(64) NOT NULL UNIQUE,
    type         VARCHAR(32) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    config       JSONB NOT NULL DEFAULT '{}',
    enabled      BOOLEAN NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dex_connectors_type ON dex_connectors (type);
CREATE INDEX IF NOT EXISTS idx_dex_connectors_enabled ON dex_connectors (enabled);

-- Singleton settings row. We enforce singleton in code (the handler upserts
-- the row with id = '00000000-0000-0000-0000-000000000001'). Storing it as
-- a normal table avoids a brittle "config" key/value bucket and lets us
-- evolve the shape with regular migrations.
CREATE TABLE IF NOT EXISTS dex_settings (
    id              UUID PRIMARY KEY,
    issuer_url      TEXT NOT NULL DEFAULT '',
    cluster_id      UUID REFERENCES clusters(id) ON DELETE SET NULL,
    namespace       VARCHAR(64) NOT NULL DEFAULT 'dex',
    release_name    VARCHAR(64) NOT NULL DEFAULT 'dex',
    configmap_name  VARCHAR(64) NOT NULL DEFAULT 'astronomer-dex-config',
    public_clients  JSONB NOT NULL DEFAULT '[]',
    expiry          JSONB NOT NULL DEFAULT '{}',
    extra           JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'dex',
    'Dex Identity Broker',
    'OpenID Connect identity broker. Astronomer talks OIDC to Dex; Dex brokers SAML, LDAP, Active Directory, Azure AD, Okta, GitHub, GitLab, and more. Configure connectors under Settings > Authentication.',
    'shield-key',
    'auth',
    '[{"chart_name":"dex","repo_url":"https://charts.dexidp.io","namespace":"dex","order":0}]'::jsonb,
    '',
    'dex',
    true,
    true,
    '{"in-cluster":{"config":{"storage":{"type":"kubernetes","config":{"inCluster":true}}},"https":{"enabled":false},"envFrom":[{"secretRef":{"name":"astronomer-dex-config"}}],"volumeMounts":[{"name":"config","mountPath":"/etc/dex/cfg"}],"volumes":[{"name":"config","configMap":{"name":"astronomer-dex-config"}}]}}'::jsonb,
    'dex',
    '/',
    '[]'::jsonb
)
ON CONFLICT (slug) DO NOTHING;
