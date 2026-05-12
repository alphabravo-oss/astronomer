-- Migration 063 — read-side API audit (Sprint 17).
--
-- Extends the existing audit_log (migration 010 / repartitioned in 025)
-- with an action_class column so reads and mutations live in the same
-- table but stay filterable. Adds read_audit_policies — the operator-
-- tunable list of path-prefix + verb combinations whose GET requests
-- should be audited.
--
-- HIPAA / PCI compliance requires "who saw what credential and when".
-- The read-side audit middleware (internal/server/middleware/read_audit.go)
-- consumes these rows on every request.

-- Action class. "mutation" stays the default so pre-existing rows are
-- accurately labelled without a backfill (the only other historical
-- class is "auth" — fixed in the same migration via a UPDATE).
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS action_class VARCHAR(16) NOT NULL DEFAULT 'mutation';

ALTER TABLE audit_log
    ADD CONSTRAINT audit_action_class_valid
    CHECK (action_class IN ('mutation','read','auth','system'));

-- Backfill the obvious auth rows so operators can filter immediately.
-- Anything else with a non-mutation action prefix can be bulk-fixed
-- later if compliance asks, but this is the highest-volume class.
UPDATE audit_log SET action_class = 'auth' WHERE action LIKE 'auth.%';

CREATE INDEX IF NOT EXISTS idx_audit_log_class
    ON audit_log (action_class, created_at DESC);

-- Operator-configurable read-side policy. Each row is a path-prefix +
-- verbs combination that, when matched, fires the read auditor. Default
-- seeds cover the highest-risk endpoints; operators can disable, refine,
-- or add their own.
CREATE TABLE IF NOT EXISTS read_audit_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    -- Path prefix. Match is "starts with" against the chi-matched route
    -- pattern (NOT the raw URL — so /clusters/{id}/registries not the
    -- expanded UUID). Use "*" for any path.
    path_pattern    VARCHAR(256) NOT NULL,
    -- Verbs to audit. Always at least one of GET/HEAD; "*" for all.
    verbs           VARCHAR(64) NOT NULL DEFAULT 'GET',
    -- Sample rate 0.0–1.0. 1.0 = audit every match. 0.1 = sample 10%.
    sample_rate     NUMERIC(3,2) NOT NULL DEFAULT 1.00,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT sample_rate_valid CHECK (sample_rate >= 0.0 AND sample_rate <= 1.0)
);

CREATE INDEX IF NOT EXISTS idx_read_audit_policies_enabled
    ON read_audit_policies (enabled);

-- Seed the must-audit-by-default policies. Operators can disable or
-- refine, but the install-fresh defaults are PCI/HIPAA-safe.
INSERT INTO read_audit_policies (name, description, path_pattern, verbs, sample_rate) VALUES
    ('cloud_credentials_read',   'Reads of cloud_credentials rows surface or expose AWS/GCP/Azure secrets.', '/projects/*/cloud-credentials', 'GET', 1.00),
    ('registry_credentials_read','Reads of cluster_registry rows surface dockerconfigjson contents.',        '/clusters/*/registries',        'GET', 1.00),
    ('sso_secrets_read',         'Reads of SSO connector secrets.',                                          '/admin/sso',                    'GET', 1.00),
    ('webhook_auth_read',        'Reads of webhook auth_encrypted blobs.',                                   '/admin/webhooks',               'GET', 1.00),
    ('siem_auth_read',           'Reads of SIEM forwarder auth blobs.',                                      '/admin/siem-forwarders',        'GET', 1.00),
    ('audit_log_read',           'Reads of the audit log itself.',                                           '/audit',                        'GET', 1.00),
    ('support_bundle_download',  'Support bundle downloads.',                                                '/support-bundle',               'GET', 1.00),
    ('admin_settings_read',      'Reads of platform_configuration / SMTP / branding.',                       '/admin/settings',               'GET', 1.00)
ON CONFLICT (name) DO NOTHING;
