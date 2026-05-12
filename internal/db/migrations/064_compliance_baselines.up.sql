-- Compliance baselines (migration 064).
--
-- Sprint 17 — bundles every individually-tunable compliance knob
-- (audit retention, quota plans, PSS profile, maintenance windows,
-- alert rules, platform_settings, webhook + TOTP requirements) into
-- four named preset profiles that an operator can apply in one click.
--
-- The motivation is operational: a hand-configured PCI-DSS or HIPAA
-- footprint takes hours of click-ops across the settings, quotas,
-- security, and monitoring tabs. The same operator hand-tuning ten
-- knobs against a printed control list is the single most common
-- source of "we thought we were compliant" misconfiguration. Bundling
-- the knobs into a registry-versioned spec, snapshotting the prior
-- state on apply, and recording WHEN-WHO-WHICH gives auditors a
-- one-line answer to "what baseline are you running?".
--
-- Tables:
--
--   compliance_baselines              — the registry. Slug + spec JSON.
--                                       Four rows seeded ('pci_dss_4_0',
--                                       'hipaa', 'fedramp_moderate',
--                                       'soc2'). The seeded `spec` is
--                                       intentionally empty here ('{}')
--                                       — the canonical spec lives in
--                                       internal/compliance/baselines.go
--                                       and the GET handler joins the
--                                       DB row with the registry so the
--                                       migration stays small and the
--                                       spec is code-reviewable as Go.
--
--   compliance_baseline_applications  — apply history. Each row is one
--                                       "operator clicked Apply" event.
--                                       Stores the previous_state JSON
--                                       captured RIGHT BEFORE the apply
--                                       so a Revert can restore it
--                                       atomically. Most-recent row's
--                                       status='applied' = currently
--                                       active baseline.
--
-- Migration safety: tables are new + no ADD COLUMN, so the T30 lint
-- doesn't apply.

CREATE TABLE compliance_baselines (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- "pci_dss_4_0" | "hipaa" | "fedramp_moderate" | "soc2".
    -- Stable, UI-routable identifier. New baselines added in future
    -- migrations append rows; never mutate existing slugs.
    slug            VARCHAR(64) NOT NULL UNIQUE,
    name            VARCHAR(128) NOT NULL,
    description     TEXT NOT NULL,
    version         VARCHAR(32) NOT NULL DEFAULT '1.0',
    -- The full bundle of state mutations the baseline applies, as a
    -- declarative JSON document. Schema is per-baseline-version; we
    -- evolve via new rows + version bump (not in-place updates).
    --
    -- Canonical fields (PCI example):
    --   {
    --     "audit_retention_days": 365,
    --     "quota_plans": [{ "name":"pci-prod", "max_clusters":50, ... }],
    --     "pss_profile": "restricted",
    --     "maintenance_window_template": { ... },
    --     "alert_rules":  [{ "name":"...", "metric":"...", "threshold":... }],
    --     "platform_settings": { "branding.banner_text":"PCI prod", ... },
    --     "required_webhooks":  ["audit_log_sink"],
    --     "required_smtp":      true,
    --     "required_totp":      true,
    --     "read_audit_policies": ["..."]
    --   }
    spec            JSONB NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A platform applies AT MOST one baseline at a time (the most-recently
-- applied wins). History is kept so "what did we apply last quarter"
-- is one query against this table.
CREATE TABLE compliance_baseline_applications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- ON DELETE RESTRICT: deleting a baseline that has an apply
    -- history is a foot-gun (auditors lose the "what was applied
    -- in Q3?" trail). Operators must delete the application rows
    -- first or just disable the baseline.
    baseline_id     UUID NOT NULL REFERENCES compliance_baselines(id) ON DELETE RESTRICT,
    -- A snapshot of the state we're about to overwrite, captured
    -- inside the SAME transaction as the writes. Used to restore on
    -- Revert. Shape mirrors BaselineSpec for orthogonality, but only
    -- the keys the baseline actually touched are populated.
    previous_state  JSONB NOT NULL,
    applied_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    applied_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- "applied" | "reverted".
    status          VARCHAR(16) NOT NULL DEFAULT 'applied',
    reverted_at     TIMESTAMPTZ,
    reverted_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    notes           TEXT NOT NULL DEFAULT '',
    CONSTRAINT app_status_valid CHECK (status IN ('applied','reverted'))
);
CREATE INDEX idx_compliance_apps_baseline ON compliance_baseline_applications (baseline_id, applied_at DESC);
-- Partial index on the active rows — the "find active baseline" query
-- is the per-request hot path (audit + metrics emit the active slug)
-- and a partial index keeps it index-only on a small set.
CREATE INDEX idx_compliance_apps_active   ON compliance_baseline_applications (applied_at DESC) WHERE status = 'applied';

-- Seed the four built-in baselines. Operators can clone + edit; never
-- edit the seeded rows directly — they're upgraded by new migrations.
-- The empty `'{}'::jsonb` spec is intentional: the canonical spec
-- lives in internal/compliance/baselines.go (the Registry()) so the
-- migration stays small and the spec is code-reviewable + versionable
-- alongside the apply logic. The GET endpoint joins the DB row with
-- the registry to surface the populated spec.
INSERT INTO compliance_baselines (slug, name, description, version, spec) VALUES
    ('pci_dss_4_0',     'PCI-DSS 4.0',     'Payment card industry — cardholder data scope', '1.0', '{}'::jsonb),
    ('hipaa',           'HIPAA',           'US healthcare — protected health information', '1.0', '{}'::jsonb),
    ('fedramp_moderate','FedRAMP Moderate','US federal cloud — moderate-impact baseline',  '1.0', '{}'::jsonb),
    ('soc2',            'SOC 2',           'Service organization controls (Type II)',      '1.0', '{}'::jsonb)
ON CONFLICT (slug) DO NOTHING;
