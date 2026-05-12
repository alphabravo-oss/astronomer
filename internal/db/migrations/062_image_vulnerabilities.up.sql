-- Sprint 062 — image vulnerability scanning surface.
--
-- The matching CIS-benchmark surface (sprint B5 / migration 022) ingests
-- Rancher cis-operator's ClusterScanReport CRs into security_scan_results.
-- This sprint adds the *image* side: trivy-operator (or any compatible
-- scanner) emits VulnerabilityReport CRs in each managed cluster, and the
-- CRD-mirror watcher streams them into the two tables below.
--
-- Design notes:
--   - `image_vulnerability_reports.report_name` is the upstream Trivy
--     VulnerabilityReport metadata.name. It uniquely identifies
--     (namespace, workload, container, image-digest); we replicate the
--     upstream key 1:1 so re-ingest is idempotent on (cluster_id,
--     report_name).
--   - The per-severity aggregate counts are NOT computed from
--     image_vulnerabilities at read time. The Trivy operator emits them
--     with each report; we store them eagerly so the rollup queries are
--     a single index scan.
--   - `cvss_score` is nullable — the Trivy schema doesn't always supply
--     it (some vulnerabilities only carry a textual severity).
--   - The migration also seeds the Aqua Security helm repo + the
--     trivy-operator chart entry. The INSERT is ON CONFLICT DO NOTHING
--     so re-running the migration is harmless and operators can hand-
--     edit the catalog row without it being clobbered.

CREATE TABLE image_vulnerability_reports (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    report_name     VARCHAR(256) NOT NULL,
    namespace       VARCHAR(253) NOT NULL,
    workload_kind   VARCHAR(64) NOT NULL DEFAULT '',
    workload_name   VARCHAR(253) NOT NULL DEFAULT '',
    container_name  VARCHAR(253) NOT NULL DEFAULT '',
    image_registry  VARCHAR(253) NOT NULL DEFAULT '',
    image_repo      VARCHAR(253) NOT NULL DEFAULT '',
    image_tag       VARCHAR(128) NOT NULL DEFAULT '',
    image_digest    VARCHAR(128) NOT NULL DEFAULT '',
    scanner         VARCHAR(64) NOT NULL DEFAULT 'trivy',
    scanner_version VARCHAR(64) NOT NULL DEFAULT '',
    critical_count  INTEGER NOT NULL DEFAULT 0,
    high_count      INTEGER NOT NULL DEFAULT 0,
    medium_count    INTEGER NOT NULL DEFAULT 0,
    low_count       INTEGER NOT NULL DEFAULT 0,
    unknown_count   INTEGER NOT NULL DEFAULT 0,
    scanned_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, report_name)
);

-- (cluster_id, critical DESC, high DESC) supports the "top vulnerable
-- images for cluster" listing without a sort step.
CREATE INDEX idx_ivr_cluster_severity
    ON image_vulnerability_reports (cluster_id, critical_count DESC, high_count DESC);
-- (cluster_id, namespace) is the namespace filter on the per-cluster
-- listing.
CREATE INDEX idx_ivr_cluster_ns
    ON image_vulnerability_reports (cluster_id, namespace);

CREATE TABLE image_vulnerabilities (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id       UUID NOT NULL REFERENCES image_vulnerability_reports(id) ON DELETE CASCADE,
    vulnerability_id VARCHAR(64) NOT NULL,
    severity        VARCHAR(16) NOT NULL,
    pkg_name        VARCHAR(256) NOT NULL DEFAULT '',
    installed_version VARCHAR(128) NOT NULL DEFAULT '',
    fixed_version   VARCHAR(128) NOT NULL DEFAULT '',
    primary_link    TEXT NOT NULL DEFAULT '',
    cvss_score      NUMERIC(4,1),
    title           TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (report_id, vulnerability_id, pkg_name, installed_version)
);
CREATE INDEX idx_image_vulns_severity ON image_vulnerabilities (severity);

-- Catalog seed for the Aqua Security helm repo + trivy-operator chart.
-- Both inserts are idempotent so re-applying the migration (or running it
-- on a cluster that already hand-installed the repo) is harmless.
INSERT INTO helm_repositories (
    name, url, repo_type, description, is_default, auth_type, enabled
) VALUES (
    'aqua', 'https://aquasecurity.github.io/helm-charts/', 'helm',
    'Aqua Security Helm charts (trivy-operator, kube-bench, ...).',
    false, 'none', true
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO helm_charts (
    repository_id, name, display_name, description, icon_url, home_url,
    category, keywords, maintainers, deprecated
)
SELECT
    r.id,
    'trivy-operator',
    'Trivy Operator',
    'Kubernetes operator that scans workload images for vulnerabilities and exposes the results as VulnerabilityReport CRDs. Astronomer ingests those CRDs into the per-cluster Image Scans view.',
    'https://aquasecurity.github.io/trivy-operator/v0.18.4/images/trivy-operator-logo.png',
    'https://aquasecurity.github.io/trivy-operator/',
    'security',
    '["security","vulnerability","trivy","cve","image-scanning"]'::jsonb,
    '[]'::jsonb,
    false
FROM helm_repositories r
WHERE r.name = 'aqua'
ON CONFLICT (repository_id, name) DO NOTHING;
