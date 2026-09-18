-- cluster_health_statuses.conditions has always been written and consumed as
-- an object (heartbeat capability fields plus liveness metadata). Migration
-- 049 inferred an array contract from the legacy empty-array default, which
-- rejected every subsequent heartbeat and metrics write. Correct the governed
-- shape without rewriting existing values.
DO $$
BEGIN
    UPDATE public.durable_json_schemas
    SET schema_version = 2,
        json_type = 'object'
    WHERE table_schema = 'public'
      AND table_name = 'cluster_health_statuses'
      AND column_name = 'conditions';
    IF NOT FOUND THEN
        RAISE EXCEPTION 'cluster_health_statuses.conditions durable JSON contract is missing';
    END IF;
END;
$$;
