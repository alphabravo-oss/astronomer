-- Network policy templates (migration 068).
--
-- Sister-feature of sprint 049 cluster_templates: those manage cluster-
-- level config (env, labels, tool installs); this is namespace-level
-- NetworkPolicy security baselines. Operators pick from a library of
-- pre-built templates (deny-all-ingress, project-isolated, namespace-
-- only, allow-ingress-controllers) and apply them to selected
-- namespaces. The reconciler server-side-applies the rendered manifest
-- through the existing tunnel K8sRequester.
--
-- Builtin rows are seeded by this migration and are READ-ONLY from the
-- handler (PUT/DELETE on a kind='builtin' row returns 403). Operators
-- can clone via POST to create a kind='custom' row they can edit.

CREATE TABLE network_policy_templates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Stable slug: "deny_all_ingress", "project_isolated", etc.
    -- Also used to derive the in-cluster NetworkPolicy name
    -- (astronomer-np-<slug>) and as the audit-detail correlation key.
    slug            VARCHAR(64) NOT NULL UNIQUE,
    name            VARCHAR(128) NOT NULL,
    description     TEXT NOT NULL,
    -- "builtin" rows are seeded by migration; operators can clone via
    -- POST to create "custom" rows they can edit.
    kind            VARCHAR(16) NOT NULL DEFAULT 'custom',
    -- Go text/template-rendered NetworkPolicy YAML. Template variables:
    --   {{.Namespace}}        target namespace
    --   {{.Project}}          owning project name (empty if unscoped)
    --   {{.PolicyName}}       resolved name like "astronomer-np-<slug>"
    spec_template   TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT np_kind_valid CHECK (kind IN ('builtin','custom'))
);

-- Per-(cluster, namespace, template) application. The reconciler walks
-- this table on a schedule + on writes, applying the SSA patch.
CREATE TABLE network_policy_applications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id     UUID NOT NULL REFERENCES network_policy_templates(id) ON DELETE CASCADE,
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    namespace       VARCHAR(253) NOT NULL,
    -- Resolved name of the in-cluster NetworkPolicy (so we can detect /
    -- delete it on revert). Generated as "astronomer-np-<slug>".
    policy_name     VARCHAR(253) NOT NULL,
    -- "pending" | "applied" | "failed" | "drifting"
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    last_applied_at TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    applied_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, namespace, template_id),
    CONSTRAINT np_status_valid CHECK (status IN ('pending','applied','failed','drifting'))
);
CREATE INDEX idx_np_apps_cluster ON network_policy_applications (cluster_id, status);
CREATE INDEX idx_np_apps_template ON network_policy_applications (template_id);

-- Seed the four built-in templates. Operators can clone but never edit
-- the builtin rows directly. PolicyName is rendered by Go text/template
-- at apply time, so the template YAML below uses {{.PolicyName}} etc.
INSERT INTO network_policy_templates (slug, name, description, kind, spec_template) VALUES
    ('deny_all_ingress', 'Deny all ingress',
     'Blocks all inbound traffic. Use as a base layer with explicit allow rules layered on.',
     'builtin',
     'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: deny_all_ingress
spec:
  podSelector: {}
  policyTypes: [Ingress]
'),
    ('project_isolated', 'Project isolated',
     'Only allow ingress from pods labeled astronomer.io/project=<this>.',
     'builtin',
     'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: project_isolated
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels:
              astronomer.io/project: {{.Project}}
'),
    ('namespace_only', 'Namespace only',
     'Only allow ingress from pods in the same namespace.',
     'builtin',
     'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: namespace_only
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector: {}
'),
    ('allow_ingress_controllers', 'Allow ingress controllers',
     'Permit traffic only from common ingress controllers (nginx, traefik). Egress unrestricted.',
     'builtin',
     'apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: allow_ingress_controllers
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
  ingress:
    - from:
        - namespaceSelector:
            matchExpressions:
              - {key: kubernetes.io/metadata.name, operator: In, values: [ingress-nginx, traefik]}
  egress:
    - {}
');
