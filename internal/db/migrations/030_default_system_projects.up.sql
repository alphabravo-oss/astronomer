CREATE OR REPLACE FUNCTION public.ensure_cluster_default_projects(p_cluster_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    default_project_id uuid;
    system_project_id uuid;
BEGIN
    INSERT INTO public.projects (
        name, display_name, description, cluster_id, namespaces,
        resource_quota, limit_range, network_policy_mode,
        pod_security_profile, managed_by
    ) VALUES (
        'default', 'Default',
        'Default project for application namespaces not assigned elsewhere.',
        p_cluster_id, '[]'::jsonb, '{}'::jsonb, '{}'::jsonb,
        'none', 'baseline', 'system'
    )
    ON CONFLICT (name, cluster_id) DO NOTHING;

    SELECT id INTO default_project_id
    FROM public.projects
    WHERE name = 'default' AND cluster_id = p_cluster_id;

    INSERT INTO public.projects (
        name, display_name, description, cluster_id, namespaces,
        resource_quota, limit_range, network_policy_mode,
        pod_security_profile, managed_by
    ) VALUES (
        'system', 'System',
        'System project for Kubernetes infrastructure namespaces.',
        p_cluster_id, '[]'::jsonb, '{}'::jsonb, '{}'::jsonb,
        'none', 'privileged', 'system'
    )
    ON CONFLICT (name, cluster_id) DO NOTHING;

    SELECT id INTO system_project_id
    FROM public.projects
    WHERE name = 'system' AND cluster_id = p_cluster_id;

    INSERT INTO public.project_namespaces (project_id, cluster_id, namespace)
    VALUES (default_project_id, p_cluster_id, 'default')
    ON CONFLICT (cluster_id, namespace) DO NOTHING;

    INSERT INTO public.project_namespaces (project_id, cluster_id, namespace)
    SELECT system_project_id, p_cluster_id, namespace
    FROM (VALUES ('kube-system'), ('kube-public'), ('kube-node-lease')) AS system_namespaces(namespace)
    ON CONFLICT (cluster_id, namespace) DO NOTHING;

    UPDATE public.projects project
    SET namespaces = COALESCE((
        SELECT jsonb_agg(project_namespace.namespace ORDER BY project_namespace.namespace)
        FROM public.project_namespaces project_namespace
        WHERE project_namespace.project_id = project.id
    ), '[]'::jsonb),
    updated_at = now()
    WHERE project.id IN (default_project_id, system_project_id);
END;
$$;

CREATE OR REPLACE FUNCTION public.ensure_cluster_default_projects_trigger()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM public.ensure_cluster_default_projects(NEW.id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER ensure_cluster_default_projects_after_insert
AFTER INSERT ON public.clusters
FOR EACH ROW
EXECUTE FUNCTION public.ensure_cluster_default_projects_trigger();

DO $$
DECLARE
    cluster_row record;
BEGIN
    FOR cluster_row IN SELECT id FROM public.clusters LOOP
        PERFORM public.ensure_cluster_default_projects(cluster_row.id);
    END LOOP;
END;
$$;

