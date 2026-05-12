-- Platform baseline (sprint 074).
--
-- Closes the "register a cluster, get nothing" gap. After this
-- migration, every NEWLY-registered cluster gets a curated baseline of
-- platform operators applied automatically:
--
--     trivy-operator         (image vulnerability scans)
--     kube-state-metrics     (metrics)
--     node-exporter          (host-level metrics)
--     fluent-bit             (log forwarding)
--     cert-manager           (TLS)
--
-- The mechanism is two pieces:
--
--   1. A `platform_configuration.default_cluster_template_id` column.
--      NULL = no auto-attach (the legacy/opt-out behavior). When set,
--      the cluster Create handler (sprint 074 hook) records a
--      cluster_template_applications row pointing at that template, and
--      the existing sprint-049 apply worker reconciles it.
--
--   2. A seed cluster_templates row keyed on the stable name
--      'Platform baseline'. Future migrations can UPDATE the spec to
--      add/remove charts; operators who want a different baseline
--      duplicate the row and point platform_configuration at the copy.
--      (The sprint-049 handler enforces "name=Platform baseline" as
--      operator-read-only via a constant check; see
--      internal/handler/cluster_templates.go.)
--
-- Idempotent: a re-run of this migration on an already-migrated DB is a
-- no-op. The cluster_templates seed uses ON CONFLICT (name) DO NOTHING
-- (the table's UNIQUE constraint is on `name`), and the
-- platform_configuration UPDATE only fires when the operator hasn't set
-- a value yet. Operator overrides are NEVER clobbered.
--
-- Why platform_configuration (not platform_settings)? The
-- platform_settings key/value table (migration 046) is the right home
-- for branding / banner / feature flags — operator-tunable strings and
-- bools. A FK reference to cluster_templates(id) wants a real column
-- with referential integrity, not a JSONB blob in a generic K/V store;
-- platform_configuration is the singleton table designed for that.

-- Operator-controlled default template applied on cluster register.
-- NULL = no auto-attach (legacy behavior). ON DELETE SET NULL so an
-- operator-driven DELETE of the template gracefully degrades to "no
-- auto-attach" instead of cascading or erroring.
ALTER TABLE platform_configuration ADD COLUMN IF NOT EXISTS
    default_cluster_template_id UUID REFERENCES cluster_templates(id) ON DELETE SET NULL;

-- Seed the platform-baseline cluster_template. The sprint-049
-- cluster_templates table has only (name, description, spec, created_by,
-- created_at, updated_at) — no slug, no kind, no enabled column. We
-- use `name` as the stable lookup key (it carries a UNIQUE constraint).
-- Operators who want to customize MUST clone this row; the handler
-- treats name='Platform baseline' as builtin/read-only.
--
-- The spec.tools list uses chart slugs (not FK references), so this
-- INSERT does NOT depend on the catalog having any of these charts
-- preloaded. At apply time the sprint-049 worker resolves each slug
-- against the helm_charts catalog and writes a per-tool status row;
-- missing charts produce a single failed-tool entry the operator can
-- fix later by enabling the right catalog, without the whole template
-- application failing.
INSERT INTO cluster_templates (name, description, spec)
VALUES (
    'Platform baseline',
    'Astronomer-recommended baseline operators applied to every newly-registered cluster: trivy-operator (image vuln scans), kube-state-metrics + node-exporter (metrics), fluent-bit (log forwarding), cert-manager (TLS). Builtin; clone before customizing.',
    jsonb_build_object(
        'builtin', true,
        'tools', jsonb_build_array(
            jsonb_build_object(
                'slug', 'trivy-operator',
                'preset', 'default',
                'namespace', 'trivy-system',
                'create_namespace', true
            ),
            jsonb_build_object(
                'slug', 'kube-state-metrics',
                'preset', 'default',
                'namespace', 'monitoring',
                'create_namespace', true
            ),
            jsonb_build_object(
                'slug', 'node-exporter',
                'preset', 'default',
                'namespace', 'monitoring',
                'create_namespace', true
            ),
            jsonb_build_object(
                'slug', 'fluent-bit',
                'preset', 'default',
                'namespace', 'logging',
                'create_namespace', true
            ),
            jsonb_build_object(
                'slug', 'cert-manager',
                'preset', 'default',
                'namespace', 'cert-manager',
                'create_namespace', true
            )
        )
    )
)
ON CONFLICT (name) DO NOTHING;

-- Wire the seeded baseline as the platform default IF nothing's set yet
-- (an operator's existing choice always wins). Re-running this UPDATE is
-- a no-op because the WHERE clause shifts to false the moment the
-- column carries any non-NULL value.
UPDATE platform_configuration
SET default_cluster_template_id = (
        SELECT id FROM cluster_templates WHERE name = 'Platform baseline' LIMIT 1
    )
WHERE default_cluster_template_id IS NULL
  AND id = 1;
