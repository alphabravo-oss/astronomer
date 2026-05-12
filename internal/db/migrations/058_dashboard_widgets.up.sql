-- Dashboard widgets (migration 058).
--
-- Rancher-style "monitoring overlay": operators define small widgets
-- (Grafana panel embeds, Prometheus-query sparklines/stats, or raw URL
-- iframes) and pin them to one of three scopes — global, cluster, or
-- project. The render handler resolves cluster-scoped widgets against
-- the cluster's cluster_uid so a single widget definition fans out
-- across every member cluster without per-cluster duplication.
--
-- Two tables + one columns ALTER on clusters:
--
--   dashboard_widgets        — widget definitions + grid layout
--   prometheus_datasources   — Prometheus connection rows the
--                              prom_sparkline / prom_stat widgets
--                              point at by name
--   clusters.cluster_uid     — short opaque ID the iframe specs
--                              template into Grafana URLs as
--                              {{cluster_uid}}; defaults to the first
--                              8 chars of the cluster row's UUID so
--                              existing fleets work zero-touch
--
-- Schema constraints:
--
--   - widget_type CHECK constrains the spec interpretation. New
--     widget types (e.g. "sparkline_from_loki") require a migration
--     to extend the CHECK list AND a handler-side renderer.
--   - scope CHECK is the three values the handler routes on; same
--     migration-or-nothing rule.
--   - scope_ids is a UUID[] (empty array = "applies to ALL in scope").
--     Postgres UUID arrays preserve set semantics for ANY() lookups
--     without a separate junction table, which keeps the per-render
--     query a single SELECT — junction-table joins would require N
--     extra round-trips at dashboard load.
--   - prometheus_datasources.name is UNIQUE so widget specs can
--     reference a datasource by name. The handler resolves the name
--     to a row at render time; renaming a datasource silently breaks
--     dependent widgets which is acceptable for an operator-facing
--     surface (Helm-ops-style "infra owners change infra").

CREATE TABLE dashboard_widgets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    -- "grafana_panel" | "prom_sparkline" | "prom_stat" | "url_iframe"
    -- For grafana_panel:    { base_url, dashboard_uid, panel_id, vars: { cluster: "$cluster_uid", ... } }
    -- For prom_sparkline:   { datasource: "default", query: "...", duration: "1h", step: "60s" }
    -- For prom_stat:        { datasource: "default", query: "...", unit: "%", format: ".2f" }
    -- For url_iframe:       { url: "https://billing/.../{{cluster_uid}}", height_px: 280 }
    widget_type     VARCHAR(32) NOT NULL,
    spec            JSONB NOT NULL DEFAULT '{}',
    scope           VARCHAR(16) NOT NULL DEFAULT 'global',
    -- Empty array = "every entity in scope". Otherwise = whitelist.
    scope_ids       UUID[] NOT NULL DEFAULT '{}',
    grid_x          INTEGER NOT NULL DEFAULT 0,
    grid_y          INTEGER NOT NULL DEFAULT 0,
    grid_w          INTEGER NOT NULL DEFAULT 4,
    grid_h          INTEGER NOT NULL DEFAULT 2,
    refresh_seconds INTEGER NOT NULL DEFAULT 60,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT widget_type_valid CHECK (widget_type IN ('grafana_panel','prom_sparkline','prom_stat','url_iframe')),
    CONSTRAINT scope_valid CHECK (scope IN ('global','cluster','project'))
);
-- Partial index covers the hot read path — render handler always
-- filters by scope AND enabled = true.
CREATE INDEX idx_dashboard_widgets_scope ON dashboard_widgets (scope) WHERE enabled = true;

CREATE TABLE prometheus_datasources (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(64) NOT NULL UNIQUE,
    url             TEXT NOT NULL,
    -- Fernet-encrypted JSON {basic_auth_username, basic_auth_password,
    -- bearer_token}. Empty string = no auth. The handler 503s on
    -- /test/ when both this is non-empty AND no encryptor is wired.
    auth_encrypted  TEXT NOT NULL DEFAULT '',
    tls_skip_verify BOOLEAN NOT NULL DEFAULT false,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Extend clusters with the cluster_uid column. The widget render
-- handler templates `{{cluster_uid}}` placeholders in iframe specs to
-- this value before sending them to the client. Default is empty so
-- the migration is non-blocking on a populated clusters table; the
-- backfill below seeds every existing row with the first 8 chars of
-- its UUID so existing fleets work zero-touch.
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS cluster_uid VARCHAR(64) NOT NULL DEFAULT '';
UPDATE clusters SET cluster_uid = SUBSTRING(id::text, 1, 8) WHERE cluster_uid = '';

-- Seed three demo widgets. They render harmlessly out-of-the-box —
-- with no prometheus_datasources rows wired, the prom_sparkline /
-- prom_stat widgets degrade to "no data" cells; the operator can
-- enable them by adding a datasource and editing the queries.
-- Idempotent inserts so re-running the migration in a test loop
-- doesn't duplicate.
INSERT INTO dashboard_widgets (name, description, widget_type, spec, scope, refresh_seconds, grid_x, grid_y, grid_w, grid_h)
SELECT 'Pod CPU saturation',
       'Per-cluster pod CPU usage as a percentage of requests. Edit the datasource + query when wiring Prometheus.',
       'prom_sparkline',
       '{"datasource":"default","query":"sum(rate(container_cpu_usage_seconds_total[5m])) / sum(kube_pod_container_resource_requests{resource=\"cpu\"})","duration":"1h","step":"60s"}'::jsonb,
       'cluster',
       60, 0, 0, 6, 2
WHERE NOT EXISTS (SELECT 1 FROM dashboard_widgets WHERE name = 'Pod CPU saturation');

INSERT INTO dashboard_widgets (name, description, widget_type, spec, scope, refresh_seconds, grid_x, grid_y, grid_w, grid_h)
SELECT 'API server p99 latency',
       'Apiserver request latency 99th percentile, scoped per cluster.',
       'prom_stat',
       '{"datasource":"default","query":"histogram_quantile(0.99, sum(rate(apiserver_request_duration_seconds_bucket[5m])) by (le))","unit":"s","format":".3f"}'::jsonb,
       'cluster',
       30, 6, 0, 3, 1
WHERE NOT EXISTS (SELECT 1 FROM dashboard_widgets WHERE name = 'API server p99 latency');

INSERT INTO dashboard_widgets (name, description, widget_type, spec, scope, refresh_seconds, grid_x, grid_y, grid_w, grid_h)
SELECT 'Cluster health rollup',
       'Fleet-wide rollup of healthy clusters / total clusters.',
       'prom_sparkline',
       '{"datasource":"default","query":"count(up{job=\"kubernetes-nodes\"} == 1) / count(up{job=\"kubernetes-nodes\"})","duration":"6h","step":"5m"}'::jsonb,
       'global',
       120, 0, 0, 12, 2
WHERE NOT EXISTS (SELECT 1 FROM dashboard_widgets WHERE name = 'Cluster health rollup');
