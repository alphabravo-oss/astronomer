-- Project caps are declared on projects.resource_quota_{cpu,memory,pod}_*. A
-- ResourceQuota is namespace-scoped, so keep the last successfully applied,
-- deterministic allocation as first-class operational state. This makes drift
-- inspectable without treating one namespace's cap as the project total.
CREATE TABLE public.project_resource_quota_allocations (
    project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
    cluster_id uuid NOT NULL REFERENCES public.clusters(id) ON DELETE CASCADE,
    namespace varchar(253) NOT NULL,
    cpu_limit varchar(64) NOT NULL DEFAULT '',
    memory_limit varchar(64) NOT NULL DEFAULT '',
    pod_count integer NOT NULL DEFAULT 0 CHECK (pod_count >= 0),
    applied_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, cluster_id, namespace),
    CHECK (btrim(namespace) <> '')
);
