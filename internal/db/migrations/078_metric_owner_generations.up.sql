-- migration-phase: expand
-- Durable metric incarnation tokens only. These rows authorize no product
-- operation and contain no display names, credentials, endpoints or user IDs.
-- Ordinary updates (including rename/disable) retain the incarnation token;
-- deleting and recreating the same domain ID allocates a new token.
CREATE TABLE public.metric_owner_generations (
    family text NOT NULL,
    owner_id uuid NOT NULL,
    generation uuid NOT NULL DEFAULT pg_catalog.gen_random_uuid(),
    PRIMARY KEY (family, owner_id),
    CONSTRAINT metric_owner_family_valid CHECK (
        family IN ('sso_provider', 'siem_forwarder', 'cluster_group', 'cluster')),
    CONSTRAINT metric_owner_generation_nonzero CHECK (
        generation <> '00000000-0000-0000-0000-000000000000'::uuid)
);

-- Equivalent transactional lifecycle linkage across four different tables:
-- domain INSERT/DELETE and ID-changing UPDATE mutate the registry in the SAME
-- transaction. No application writer needs to know the new table during a
-- rolling upgrade. There is no history/tombstone accumulation in the registry.
CREATE FUNCTION public.maintain_metric_owner_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.id <> NEW.id) THEN
        DELETE FROM public.metric_owner_generations
        WHERE family = TG_ARGV[0] AND owner_id = OLD.id;
    END IF;
    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND OLD.id <> NEW.id) THEN
        INSERT INTO public.metric_owner_generations (family, owner_id)
        VALUES (TG_ARGV[0], NEW.id)
        ON CONFLICT (family, owner_id) DO UPDATE
            SET generation = pg_catalog.gen_random_uuid();
    END IF;
    RETURN NULL; -- AFTER trigger; never alters the domain row.
END;
$$;

CREATE FUNCTION public.truncate_metric_owner_generations() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM public.metric_owner_generations WHERE family = TG_ARGV[0];
    RETURN NULL;
END;
$$;

CREATE TRIGGER sso_configurations_metric_owner_lifecycle
AFTER INSERT OR DELETE OR UPDATE OF id ON public.sso_configurations
FOR EACH ROW EXECUTE FUNCTION public.maintain_metric_owner_generation('sso_provider');
CREATE TRIGGER sso_configurations_metric_owner_truncate
AFTER TRUNCATE ON public.sso_configurations
FOR EACH STATEMENT EXECUTE FUNCTION public.truncate_metric_owner_generations('sso_provider');

CREATE TRIGGER siem_forwarders_metric_owner_lifecycle
AFTER INSERT OR DELETE OR UPDATE OF id ON public.siem_forwarders
FOR EACH ROW EXECUTE FUNCTION public.maintain_metric_owner_generation('siem_forwarder');
CREATE TRIGGER siem_forwarders_metric_owner_truncate
AFTER TRUNCATE ON public.siem_forwarders
FOR EACH STATEMENT EXECUTE FUNCTION public.truncate_metric_owner_generations('siem_forwarder');

CREATE TRIGGER cluster_groups_metric_owner_lifecycle
AFTER INSERT OR DELETE OR UPDATE OF id ON public.cluster_groups
FOR EACH ROW EXECUTE FUNCTION public.maintain_metric_owner_generation('cluster_group');
CREATE TRIGGER cluster_groups_metric_owner_truncate
AFTER TRUNCATE ON public.cluster_groups
FOR EACH STATEMENT EXECUTE FUNCTION public.truncate_metric_owner_generations('cluster_group');

CREATE TRIGGER clusters_metric_owner_lifecycle
AFTER INSERT OR DELETE OR UPDATE OF id ON public.clusters
FOR EACH ROW EXECUTE FUNCTION public.maintain_metric_owner_generation('cluster');
CREATE TRIGGER clusters_metric_owner_truncate
AFTER TRUNCATE ON public.clusters
FOR EACH STATEMENT EXECUTE FUNCTION public.truncate_metric_owner_generations('cluster');

-- Backfill after triggers are installed. DDL takes the normal table lock;
-- concurrent old writers therefore cannot insert between observation and hook
-- installation. Repeated backfill preserves existing tokens rather than resets.
INSERT INTO public.metric_owner_generations (family, owner_id)
SELECT 'sso_provider', id FROM public.sso_configurations
ON CONFLICT (family, owner_id) DO NOTHING;

INSERT INTO public.metric_owner_generations (family, owner_id)
SELECT 'siem_forwarder', id FROM public.siem_forwarders
ON CONFLICT (family, owner_id) DO NOTHING;

INSERT INTO public.metric_owner_generations (family, owner_id)
SELECT 'cluster_group', id FROM public.cluster_groups
ON CONFLICT (family, owner_id) DO NOTHING;

INSERT INTO public.metric_owner_generations (family, owner_id)
SELECT 'cluster', id FROM public.clusters
ON CONFLICT (family, owner_id) DO NOTHING;
