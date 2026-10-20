-- +goose Up
-- +goose StatementBegin
-- Ordered after reserved 079 distribution refresh migration.
-- Projection expansion; existing chart/SDK public layouts remain unchanged.
-- migration-phase: expand
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='120s';
-- Lock order is repositories then charts. Concurrent ingestion/ownership writes
-- cannot escape trigger installation/backfill. Timeout rolls back the whole DDL.
LOCK TABLE public.helm_repositories IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE public.helm_charts IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE public.catalog_global_chart_keys (
    chart_id uuid PRIMARY KEY REFERENCES public.helm_charts(id) ON DELETE CASCADE,
    repository_id uuid NOT NULL REFERENCES public.helm_repositories(id) ON DELETE CASCADE,
    name varchar(255) NOT NULL
);
CREATE FUNCTION public.lock_catalog_global_chart_repositories() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
    -- BEFORE also covers cascading key deletion. Both parents of a move are
    -- serialized with owner backfill before the first projection-key write.
    IF TG_OP='UPDATE' THEN
        PERFORM id FROM public.helm_repositories
        WHERE id=ANY(ARRAY[OLD.repository_id,NEW.repository_id]) ORDER BY id FOR SHARE;
    ELSIF TG_OP='DELETE' THEN
        PERFORM id FROM public.helm_repositories WHERE id=OLD.repository_id FOR SHARE;
        RETURN OLD;
    ELSE
        PERFORM id FROM public.helm_repositories WHERE id=NEW.repository_id FOR SHARE;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER catalog_global_chart_key_lock_parents
BEFORE INSERT OR UPDATE OF name,repository_id OR DELETE ON public.helm_charts
FOR EACH ROW EXECUTE FUNCTION public.lock_catalog_global_chart_repositories();
CREATE FUNCTION public.refresh_catalog_global_chart_key() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
DECLARE project_owner uuid;
BEGIN
    SELECT owner_project_id INTO STRICT project_owner FROM public.helm_repositories WHERE id=NEW.repository_id;
    IF project_owner IS NULL THEN
        INSERT INTO public.catalog_global_chart_keys(chart_id,repository_id,name)
        VALUES(NEW.id,NEW.repository_id,NEW.name)
        ON CONFLICT(chart_id) DO UPDATE SET repository_id=EXCLUDED.repository_id,name=EXCLUDED.name;
    ELSE
        DELETE FROM public.catalog_global_chart_keys WHERE chart_id=NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER catalog_global_chart_key_after_chart
AFTER INSERT OR UPDATE OF name,repository_id ON public.helm_charts
FOR EACH ROW EXECUTE FUNCTION public.refresh_catalog_global_chart_key();
CREATE FUNCTION public.refresh_catalog_global_repository_keys() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
    IF NEW.owner_project_id IS NOT DISTINCT FROM OLD.owner_project_id THEN RETURN NEW; END IF;
    IF NEW.owner_project_id IS NULL THEN
        INSERT INTO public.catalog_global_chart_keys(chart_id,repository_id,name)
        SELECT id,repository_id,name FROM public.helm_charts WHERE repository_id=NEW.id
        ON CONFLICT(chart_id) DO UPDATE SET repository_id=EXCLUDED.repository_id,name=EXCLUDED.name;
    ELSE
        DELETE FROM public.catalog_global_chart_keys WHERE repository_id=NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER catalog_global_chart_key_after_owner
AFTER UPDATE OF owner_project_id ON public.helm_repositories
FOR EACH ROW EXECUTE FUNCTION public.refresh_catalog_global_repository_keys();
INSERT INTO public.catalog_global_chart_keys(chart_id,repository_id,name)
SELECT chart.id,chart.repository_id,chart.name
FROM public.helm_charts chart JOIN public.helm_repositories repository ON repository.id=chart.repository_id
WHERE repository.owner_project_id IS NULL;
-- +goose StatementEnd
