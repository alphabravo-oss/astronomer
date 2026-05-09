-- Phase B5: CIS scans via cis-operator
--
-- Extends `security_scan_results` so it can mirror Rancher cis-operator's
-- ClusterScanReport CRD: per-scan totals (pass/fail/warn/skip) plus a
-- normalized findings array (test_id, severity, status, description,
-- remediation). Also tracks the upstream ClusterScan CR name so the worker
-- can poll the report by-name.
--
-- Idempotently inserts a `cis-operator` row into `cluster_tools` so the
-- existing tools UI can install it like any other catalog entry. ON
-- CONFLICT keeps re-runs harmless and lets the migration be the single
-- source of truth for the tool's chart coordinates.

ALTER TABLE security_scan_results
    ADD COLUMN IF NOT EXISTS cluster_scan_name TEXT NOT NULL DEFAULT '';

ALTER TABLE security_scan_results
    ADD COLUMN IF NOT EXISTS passed INTEGER NOT NULL DEFAULT 0;

ALTER TABLE security_scan_results
    ADD COLUMN IF NOT EXISTS failed INTEGER NOT NULL DEFAULT 0;

ALTER TABLE security_scan_results
    ADD COLUMN IF NOT EXISTS warned INTEGER NOT NULL DEFAULT 0;

ALTER TABLE security_scan_results
    ADD COLUMN IF NOT EXISTS skipped INTEGER NOT NULL DEFAULT 0;

ALTER TABLE security_scan_results
    ADD COLUMN IF NOT EXISTS findings JSONB NOT NULL DEFAULT '[]';

CREATE INDEX IF NOT EXISTS idx_security_scan_results_cluster_scan_name
    ON security_scan_results (cluster_scan_name);

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'cis-operator',
    'CIS Scanner (Rancher)',
    'Run CIS Kubernetes Benchmark scans on this cluster. Operator surfaces ClusterScan / ClusterScanProfile / ClusterScanReport CRDs which Astronomer ingests into the Security console.',
    'shield-check',
    'security',
    '[{"chart_name":"rancher-cis-benchmark","repo_url":"https://charts.rancher.io","namespace":"cis-operator-system","order":0}]'::jsonb,
    '',
    'cis-operator-system',
    true,
    true,
    '{}'::jsonb,
    '',
    '/',
    '[]'::jsonb
)
ON CONFLICT (slug) DO NOTHING;
