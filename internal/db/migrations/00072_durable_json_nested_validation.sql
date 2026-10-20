-- +goose Up
-- +goose StatementBegin
-- migration-phase: expand
-- Nested wire shapes supplement the existing JSONB registry. NOT VALID leaves
-- historical rows untouched while checking new writes. Inventory and repair
-- require operator review before any later VALIDATE CONSTRAINT contract step.
-- Go domain validators remain authoritative for regex syntax, enum authority,
-- UUID/key syntax, duration semantics, duplicate selectors, and frozen digests.
-- No customer document is repaired, deleted, disabled, or normalized here.

CREATE FUNCTION public.durable_json_string_map_v1(document jsonb)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE item record;
BEGIN
    IF document IS NULL OR jsonb_typeof(document) <> 'object' THEN RETURN false; END IF;
    FOR item IN SELECT value FROM jsonb_each(document) LOOP
        IF jsonb_typeof(item.value) <> 'string' THEN RETURN false; END IF;
    END LOOP;
    RETURN true;
END;
$$;

CREATE FUNCTION public.durable_json_string_array_v1(document jsonb)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE item jsonb;
BEGIN
    IF document IS NULL OR jsonb_typeof(document) <> 'array' THEN RETURN false; END IF;
    FOR item IN SELECT value FROM jsonb_array_elements(document) LOOP
        IF jsonb_typeof(item) <> 'string' THEN RETURN false; END IF;
    END LOOP;
    RETURN true;
END;
$$;

CREATE FUNCTION public.durable_json_inhibition_v1(source jsonb, target jsonb, equal_labels jsonb)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE matcher jsonb; label jsonb;
BEGIN
    IF source IS NULL OR target IS NULL OR equal_labels IS NULL
       OR jsonb_typeof(source) <> 'array' OR jsonb_typeof(target) <> 'array'
       OR jsonb_typeof(equal_labels) <> 'array' THEN RETURN false; END IF;
    FOR matcher IN SELECT value FROM jsonb_array_elements(source || target) LOOP
        IF jsonb_typeof(matcher) <> 'object'
           OR jsonb_typeof(matcher -> 'label') IS DISTINCT FROM 'string'
           OR btrim(matcher ->> 'label') = ''
           OR jsonb_typeof(matcher -> 'value') IS DISTINCT FROM 'string'
           OR (matcher ? 'is_regex' AND jsonb_typeof(matcher -> 'is_regex') <> 'boolean')
           THEN RETURN false; END IF;
    END LOOP;
    FOR label IN SELECT value FROM jsonb_array_elements(equal_labels) LOOP
        IF jsonb_typeof(label) <> 'string' OR btrim(label #>> '{}') = '' THEN RETURN false; END IF;
    END LOOP;
    -- Empty arrays remain valid historical records; regex validation is Go/RE2.
    RETURN true;
END;
$$;

CREATE FUNCTION public.durable_json_placement_v1(document jsonb)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE key text; expression jsonb;
BEGIN
    IF document IS NULL OR jsonb_typeof(document) <> 'object' THEN RETURN false; END IF;
    IF document ? 'all_clusters' AND jsonb_typeof(document -> 'all_clusters') <> 'boolean' THEN RETURN false; END IF;
    FOREACH key IN ARRAY ARRAY['project_ids','cluster_ids','cluster_group_ids','exclude_cluster_ids'] LOOP
        IF document ? key AND jsonb_typeof(document -> key) <> 'null' THEN
            IF NOT public.durable_json_string_array_v1(document -> key) THEN RETURN false; END IF;
        END IF;
    END LOOP;
    IF document ? 'match_labels' AND jsonb_typeof(document -> 'match_labels') <> 'null'
       AND NOT public.durable_json_string_map_v1(document -> 'match_labels') THEN RETURN false; END IF;
    IF document ? 'match_expressions' AND jsonb_typeof(document -> 'match_expressions') <> 'null' THEN
        IF jsonb_typeof(document -> 'match_expressions') <> 'array' THEN RETURN false; END IF;
        FOR expression IN SELECT value FROM jsonb_array_elements(document -> 'match_expressions') LOOP
            IF jsonb_typeof(expression) <> 'object'
               OR jsonb_typeof(expression -> 'key') IS DISTINCT FROM 'string'
               OR jsonb_typeof(expression -> 'operator') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
            IF expression ? 'values' AND jsonb_typeof(expression -> 'values') <> 'null'
               AND NOT public.durable_json_string_array_v1(expression -> 'values') THEN RETURN false; END IF;
        END LOOP;
    END IF;
    RETURN true;
END;
$$;

CREATE FUNCTION public.durable_json_amount_v1(document jsonb)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE value numeric;
BEGIN
    IF document IS NULL OR jsonb_typeof(document) <> 'object'
       OR jsonb_typeof(document -> 'type') IS DISTINCT FROM 'string'
       OR jsonb_typeof(document -> 'value') IS DISTINCT FROM 'number' THEN RETURN false; END IF;
    value := (document ->> 'value')::numeric;
    RETURN value >= 0 AND value <= 4294967295 AND trunc(value) = value;
END;
$$;

CREATE FUNCTION public.durable_json_rollout_strategy_v1(document jsonb)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE value numeric; key text; partition jsonb; canary jsonb;
BEGIN
    IF document IS NULL OR jsonb_typeof(document) <> 'object'
       OR jsonb_typeof(document -> 'type') IS DISTINCT FROM 'string'
       OR jsonb_typeof(document -> 'max_concurrent') IS DISTINCT FROM 'number'
       OR NOT public.durable_json_amount_v1(document -> 'max_unavailable')
       OR NOT public.durable_json_amount_v1(document -> 'failure_threshold')
       OR jsonb_typeof(document -> 'progress_deadline') IS DISTINCT FROM 'string'
       OR jsonb_typeof(document -> 'on_failure') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
    value := (document ->> 'max_concurrent')::numeric;
    IF value <= 0 OR value > 4294967295 OR trunc(value) <> value THEN RETURN false; END IF;
    FOREACH key IN ARRAY ARRAY['min_ready','shuffle_seed'] LOOP
        IF document ? key AND jsonb_typeof(document -> key) <> 'string' THEN RETURN false; END IF;
    END LOOP;
    IF document ? 'respect_maintenance_windows' AND jsonb_typeof(document -> 'respect_maintenance_windows') <> 'boolean' THEN RETURN false; END IF;
    IF document ? 'canary' AND jsonb_typeof(document -> 'canary') <> 'null' THEN
        canary := document -> 'canary';
        IF jsonb_typeof(canary) <> 'object' OR NOT public.durable_json_amount_v1(canary -> 'size') THEN RETURN false; END IF;
        IF canary ? 'cluster_ids' AND jsonb_typeof(canary -> 'cluster_ids') <> 'null'
           AND NOT public.durable_json_string_array_v1(canary -> 'cluster_ids') THEN RETURN false; END IF;
        IF canary ? 'soak' AND jsonb_typeof(canary -> 'soak') <> 'string' THEN RETURN false; END IF;
        IF canary ? 'approval_after_canary' AND jsonb_typeof(canary -> 'approval_after_canary') <> 'boolean' THEN RETURN false; END IF;
    END IF;
    IF document ? 'partitions' AND jsonb_typeof(document -> 'partitions') <> 'null' THEN
        IF jsonb_typeof(document -> 'partitions') <> 'array' THEN RETURN false; END IF;
        FOR partition IN SELECT value FROM jsonb_array_elements(document -> 'partitions') LOOP
            IF jsonb_typeof(partition) <> 'object' OR jsonb_typeof(partition -> 'name') IS DISTINCT FROM 'string'
               OR NOT public.durable_json_placement_v1(partition -> 'selector') THEN RETURN false; END IF;
            IF partition ? 'soak' AND jsonb_typeof(partition -> 'soak') <> 'string' THEN RETURN false; END IF;
            IF partition ? 'approval_required' AND jsonb_typeof(partition -> 'approval_required') <> 'boolean' THEN RETURN false; END IF;
        END LOOP;
    END IF;
    RETURN true;
END;
$$;

CREATE FUNCTION public.durable_json_task_payload_v1(payload bytea)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE document json; key text;
BEGIN
    IF payload IS NULL OR octet_length(payload) > 16777216 THEN RETURN false; END IF;
    -- Explicit historical payload-less task adapter, never a row rewrite.
    IF octet_length(payload) = 0 THEN RETURN true; END IF;
    -- json (rather than jsonb) preserves opaque numbers beyond numeric's range.
    document := convert_from(payload, 'UTF8')::json;
    IF json_typeof(document) <> 'object' THEN RETURN false; END IF;
    FOREACH key IN ARRAY ARRAY['_correlation_id','_traceparent','_tracestate'] LOOP
        IF document -> key IS NOT NULL AND json_typeof(document -> key) <> 'string' THEN RETURN false; END IF;
    END LOOP;
    RETURN true;
EXCEPTION WHEN OTHERS THEN
    -- Invalid encoding/JSON is only a category; never expose database parse text.
    RETURN false;
END;
$$;

ALTER TABLE public.alert_inhibitions ADD CONSTRAINT alert_inhibitions_nested_v1
    CHECK (public.durable_json_inhibition_v1(source_matchers, target_matchers, equal_labels)) NOT VALID;
ALTER TABLE public.task_outbox ADD CONSTRAINT task_outbox_payload_v1
    CHECK (public.durable_json_task_payload_v1(payload)) NOT VALID;
ALTER TABLE public.logging_pipelines ADD CONSTRAINT logging_pipelines_labels_v1
    CHECK (public.durable_json_string_map_v1(labels)) NOT VALID;
ALTER TABLE public.delivery_targets ADD CONSTRAINT delivery_targets_placement_v1
    CHECK (public.durable_json_placement_v1(placement)) NOT VALID;
ALTER TABLE public.delivery_rollouts ADD CONSTRAINT delivery_rollouts_strategy_v1
    CHECK (public.durable_json_rollout_strategy_v1(strategy)) NOT VALID;
ALTER TABLE public.delivery_rollouts ADD CONSTRAINT delivery_rollouts_frozen_strategy_v1
    CHECK (public.durable_json_rollout_strategy_v1(frozen_plan -> 'strategy')) NOT VALID;
ALTER TABLE public.delivery_system_rollouts ADD CONSTRAINT delivery_system_rollouts_strategy_v1
    CHECK (public.durable_json_rollout_strategy_v1(strategy)) NOT VALID;
-- +goose StatementEnd
