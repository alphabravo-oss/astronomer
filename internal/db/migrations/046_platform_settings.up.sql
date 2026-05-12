-- Rancher-style global settings hub.
--
-- One table, key/value/JSONB shape so booleans, ints, strings, and small
-- objects all fit without adding a column per setting. The handler-side
-- registry (internal/handler/platform_settings.go) enumerates the keys
-- we accept, their type spec, defaults, and per-key documentation; the
-- table is the persistent backing store.
--
-- A row missing from this table is not an error — the handler falls
-- back to the registry default. The rows inserted below are operator
-- convenience: a fresh DB returns sane values without forcing the
-- handler to write defaults on first read.
--
-- updated_by points at users(id) with ON DELETE SET NULL so removing
-- the user who last touched a setting doesn't strand the row.
CREATE TABLE platform_settings (
    key         VARCHAR(64) PRIMARY KEY,
    value       JSONB       NOT NULL DEFAULT 'null',
    description TEXT        NOT NULL DEFAULT '',
    updated_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Prefix index for ListPlatformSettingsByPrefix — the public branding /
-- banner pre-auth endpoints filter by 'branding.%' / 'banner.%', and
-- the FeatureGate middleware reads 'feature.%'. The index makes the
-- prefix scan a range read instead of a full table scan even after the
-- registry grows.
CREATE INDEX IF NOT EXISTS idx_platform_settings_key_prefix
    ON platform_settings (key text_pattern_ops);

-- Seed the canonical defaults. ON CONFLICT DO NOTHING so this migration
-- is idempotent on a re-run + so an operator who already populated rows
-- via a future bootstrap path keeps their values.
INSERT INTO platform_settings (key, value, description) VALUES
    ('branding.product_name',  '"Astronomer"',                                'Product display name shown in the header and tab title'),
    ('branding.logo_url',      '""',                                          'URL of the logo PNG/SVG; empty string falls back to the built-in mark'),
    ('branding.primary_color', '"#0066CC"',                                   'Primary brand color (hex); applied as a CSS variable across the SPA'),
    ('branding.support_url',   '""',                                          'Link rendered in the in-app help menu; empty = hide the menu entry'),
    ('branding.copyright',     '""',                                          'Footer copyright text; empty = hide the footer line'),
    ('banner.login_text',      '""',                                          'Pre-login banner text; markdown supported. Empty = no banner'),
    ('banner.global_text',     '""',                                          'Persistent in-app banner text; markdown supported. Empty = no banner'),
    ('banner.global_color',    '"info"',                                      'Banner severity: info | warning | critical'),
    ('feature.catalog',        'true',                                        'Helm chart catalog tab'),
    ('feature.projects',       'true',                                        'Projects (multi-tenancy) tab'),
    ('feature.monitoring',     'true',                                        'Cluster monitoring tab'),
    ('feature.argocd',         'true',                                        'ArgoCD GitOps integration tab'),
    ('feature.security',       'true',                                        'Security / CIS scans tab'),
    ('feature.backups',        'true',                                        'Backup and restore tab'),
    ('token.default_ttl_min',  '60',                                          'API token default expiry in minutes; 0 = no expiry'),
    ('token.max_ttl_min',      '525600',                                      'Maximum allowed API token expiry in minutes (1 year default)'),
    ('telemetry.enabled',      'false',                                       'Opt-in: send anonymized aggregate telemetry nightly'),
    ('telemetry.endpoint',     '"https://telemetry.alphabravo.io/astronomer"', 'HTTPS endpoint that anonymized telemetry POSTs land at')
ON CONFLICT (key) DO NOTHING;
