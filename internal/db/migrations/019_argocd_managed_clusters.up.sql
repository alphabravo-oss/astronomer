-- Phase B1: track which of our managed clusters are registered into each
-- upstream ArgoCD instance. This lets us list, label-target via ApplicationSet
-- generators, and unregister cleanly. The upstream truth lives in ArgoCD's
-- own cluster Secret (in the argocd namespace); this is just our index.

CREATE TABLE IF NOT EXISTS argocd_managed_clusters (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    argocd_instance_id    UUID NOT NULL REFERENCES argocd_instances(id) ON DELETE CASCADE,
    cluster_id            UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- ArgoCD identifies cluster secrets by name (a stable hash of the server URL
    -- in older versions; an arbitrary name now). We track whatever name we used
    -- on the POST /api/v1/clusters call so we can DELETE it later.
    cluster_secret_name   VARCHAR(253) NOT NULL DEFAULT '',
    -- The k8s API server URL we registered with ArgoCD. Either the cluster's
    -- ApiServerUrl (direct), or the Astronomer proxy URL (for agent-connected
    -- clusters that are not directly reachable from ArgoCD).
    server_url            VARCHAR(512) NOT NULL DEFAULT '',
    -- Free-form labels we set on the upstream Cluster secret. The
    -- ApplicationSet `cluster` generator matches against these.
    labels                JSONB NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (argocd_instance_id, cluster_id)
);

CREATE INDEX IF NOT EXISTS idx_argocd_managed_clusters_instance
    ON argocd_managed_clusters (argocd_instance_id);

CREATE INDEX IF NOT EXISTS idx_argocd_managed_clusters_cluster
    ON argocd_managed_clusters (cluster_id);

-- Phase B1: seed the ArgoCD entry into the cluster_tools catalog. Idempotent
-- so re-running the migration is harmless on existing installs that already
-- registered the tool out-of-band. Coordinates here are mirrored as Go
-- constants in internal/handler/tools.go (ArgoCD* block) for handler use.
INSERT INTO cluster_tools (slug, name, description, icon, category, charts, version_constraint, default_namespace, is_builtin, is_enabled, presets, service_name, service_path)
VALUES (
    'argocd',
    'ArgoCD',
    'GitOps continuous delivery for Kubernetes. Astronomer''s native CD engine; manages Applications, AppProjects, ApplicationSets, cluster registrations, and repo credentials.',
    'argo',
    'gitops',
    '[{"chart_name":"argo-cd","repo_url":"https://argoproj.github.io/argo-helm","namespace":"argocd","order":0}]'::jsonb,
    '',
    'argocd',
    true,
    true,
    '{"default":"server:\n  service:\n    type: ClusterIP\n  ingress:\n    enabled: false\ncontroller:\n  replicas: 1\nredis-ha:\n  enabled: false\napplicationSet:\n  enabled: true\ndex:\n  enabled: false\n"}'::jsonb,
    'argocd-server',
    '/'
)
ON CONFLICT (slug) DO NOTHING;
