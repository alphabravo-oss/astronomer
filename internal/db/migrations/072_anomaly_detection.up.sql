-- Sprint 072: rolling-window baselines + "deviation from baseline"
-- alert rules.
--
-- Why a dedicated table (rather than re-using the metric ingest
-- table): the recompute worker runs every 5m and the alert evaluator
-- reads on every tick. A pre-aggregated row keeps that path to a
-- single indexed lookup. The trailing-window samples live in the
-- canonical metric table; this table is the cached aggregate.
--
-- Cold-start guard: `sample_count < anomaly_min_samples` short-circuits
-- to no-fire in the evaluator. The min_samples default of 50 catches
-- the most common false-positive — a freshly-installed rule firing
-- the moment it sees its first datapoint because mean=0/stddev=0
-- makes EVERY value look like an "infinite-stddev" anomaly.

CREATE TABLE anomaly_baselines (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- "cluster_cpu_percent" | "cluster_memory_percent" | "pod_restart_rate" | ...
    metric_name     VARCHAR(128) NOT NULL,
    -- Window length the baseline covers, in seconds.
    window_seconds  INTEGER NOT NULL DEFAULT 86400,
    sample_count    INTEGER NOT NULL DEFAULT 0,
    mean            DOUBLE PRECISION NOT NULL DEFAULT 0,
    stddev          DOUBLE PRECISION NOT NULL DEFAULT 0,
    min_value       DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_value       DOUBLE PRECISION NOT NULL DEFAULT 0,
    p50             DOUBLE PRECISION NOT NULL DEFAULT 0,
    p95             DOUBLE PRECISION NOT NULL DEFAULT 0,
    p99             DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_value      DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_value_at   TIMESTAMPTZ,
    -- A compact ring buffer of recent sample values (JSONB array of
    -- floats). Cap at 1000; older samples drop off. Used only to
    -- bootstrap a recompute when the metric ingest table doesn't
    -- have full history.
    recent_samples  JSONB NOT NULL DEFAULT '[]',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, metric_name, window_seconds)
);
CREATE INDEX idx_anomaly_baselines_lookup ON anomaly_baselines (cluster_id, metric_name);
CREATE INDEX idx_anomaly_baselines_updated_at ON anomaly_baselines (updated_at);

-- Extend alert_rules with the rule-kind switch + anomaly-rule
-- columns. The rule_kind default of 'threshold' makes existing rows
-- behave identically — no data migration needed.
--
-- Why a separate rule_kind column when there's already a rule_type
-- column: rule_type carries the SEMANTIC type ('absence', 'change',
-- 'anomaly', '...') for the existing evaluator path. rule_kind is
-- the new STORAGE/EVAL strategy switch — 'threshold' uses the
-- static-threshold logic, 'anomaly' uses the baseline lookup. They
-- are orthogonal: a 'change' rule could in principle be 'anomaly'
-- kind, though we don't ship that combination v1.
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS rule_kind VARCHAR(16) NOT NULL DEFAULT 'threshold';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_stddev DOUBLE PRECISION;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_window_seconds INTEGER;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_min_samples INTEGER NOT NULL DEFAULT 50;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS anomaly_direction VARCHAR(8) NOT NULL DEFAULT 'above';

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rule_kind_valid;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rule_kind_valid
    CHECK (rule_kind IN ('threshold','anomaly'));

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_anomaly_dir_valid;
ALTER TABLE alert_rules ADD CONSTRAINT alert_anomaly_dir_valid
    CHECK (anomaly_direction IN ('above','below','either'));

CREATE INDEX IF NOT EXISTS idx_alert_rules_kind_enabled ON alert_rules (rule_kind, enabled);
