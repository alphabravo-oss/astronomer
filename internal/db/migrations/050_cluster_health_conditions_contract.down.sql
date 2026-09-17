DO $$
BEGIN
    UPDATE public.durable_json_schemas
    SET schema_version = 1,
        json_type = 'array'
    WHERE table_schema = 'public'
      AND table_name = 'cluster_health_statuses'
      AND column_name = 'conditions';
    IF NOT FOUND THEN
        RAISE EXCEPTION 'cluster_health_statuses.conditions durable JSON contract is missing';
    END IF;
END;
$$;
