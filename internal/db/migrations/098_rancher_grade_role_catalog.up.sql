-- Expand the built-in RBAC catalog for Rancher-grade day-2 operations.
--
-- This migration intentionally does not alter existing built-in or custom
-- roles. It only inserts missing role names so upgraded installs gain the
-- same delegation surface as fresh installs using the embedded template
-- catalog.

INSERT INTO global_roles (name, display_name, description, rules, is_builtin)
SELECT v.name, v.display_name, v.description, v.rules::jsonb, true
FROM (
VALUES
('Platform Operator', 'Platform Operator', 'Operate day-2 platform workflows across adopted clusters without full superuser access', '[{"resource":"clusters","verbs":["create","read","update","list"]},{"resource":"agents","verbs":["create","read","update","list"]},{"resource":"projects","verbs":["create","read","update","list"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"alerts","verbs":["create","read","update","list"]},{"resource":"catalog","verbs":["read","list"]},{"resource":"backups","verbs":["create","read","update","list"]},{"resource":"audit_logs","verbs":["read","list"]}]'),
('Security Auditor', 'Security Auditor', 'Read-only security posture, vulnerability, policy, and compliance review across the fleet', '[{"resource":"security","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"audit_logs","verbs":["read","list"]}]'),
('Compliance Manager', 'Compliance Manager', 'Manage compliance baselines, exports, evidence collection, and security posture workflows', '[{"resource":"security","verbs":["create","read","update","delete","list"]},{"resource":"audit_logs","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"rbac","verbs":["read","list"]}]'),
('GitOps Admin', 'GitOps Admin', 'Administer ArgoCD instances, repositories, projects, applications, ApplicationSets, and sync operations', '[{"resource":"argocd","verbs":["create","read","update","delete","list","sync","manage"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"audit_logs","verbs":["read","list"]}]'),
('GitOps Viewer', 'GitOps Viewer', 'Read-only visibility into GitOps instances, applications, ApplicationSets, sync status, and drift', '[{"resource":"argocd","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]}]'),
('Logging Viewer', 'Logging Viewer', 'Read-only access to cluster and management-plane logging views', '[{"resource":"logging","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","logs"]}]'),
('Monitoring Admin', 'Monitoring Admin', 'Manage monitoring stack configuration, dashboards, rules, and alert delivery policy', '[{"resource":"monitoring","verbs":["create","read","update","delete","list"]},{"resource":"alerts","verbs":["create","read","update","delete","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]}]'),
('Monitoring Viewer', 'Monitoring Viewer', 'Read-only access to metrics, dashboards, alert state, and cluster health summaries', '[{"resource":"monitoring","verbs":["read","list"]},{"resource":"alerts","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]}]'),
('Restore Operator', 'Restore Operator', 'Execute restore workflows after incidents or drills', '[{"resource":"backups","verbs":["read","list","update","manage"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"audit_logs","verbs":["read","list"]}]'),
('Support Bundle Operator', 'Support Bundle Operator', 'Generate redacted support bundles and inspect agent/platform health data', '[{"resource":"support_bundles","verbs":["create","read","list"]},{"resource":"agents","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"audit_logs","verbs":["read","list"]}]'),
('Audit Viewer', 'Audit Viewer', 'Read-only access to audit evidence and surrounding platform metadata', '[{"resource":"audit_logs","verbs":["read","list"]},{"resource":"users","verbs":["read","list"]},{"resource":"rbac","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]}]'),
('Catalog Admin', 'Catalog Admin', 'Curate Helm/OCI repositories, chart metadata, ratings, and platform tool catalog entries', '[{"resource":"catalog","verbs":["create","read","update","delete","list"]},{"resource":"cluster_templates","verbs":["create","read","update","delete","list"]},{"resource":"clusters","verbs":["read","list"]},{"resource":"audit_logs","verbs":["read","list"]}]')
) AS v(name, display_name, description, rules)
WHERE NOT EXISTS (
    SELECT 1 FROM global_roles existing WHERE existing.name = v.name
);

INSERT INTO cluster_roles (name, display_name, description, rules, is_builtin)
SELECT v.name, v.display_name, v.description, v.rules::jsonb, true
FROM (
VALUES
('Catalog Installer', 'Catalog Installer', 'Install and upgrade approved catalog tools in one cluster without global catalog administration', '[{"resource":"clusters","verbs":["read"]},{"resource":"catalog","verbs":["read","list","create","update","delete"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","watch","logs"]}]'),
('Cluster Backup Operator', 'Cluster Backup Operator', 'Manage backup schedules and backup runs for a single cluster', '[{"resource":"clusters","verbs":["read"]},{"resource":"backups","verbs":["create","read","update","delete","list"]},{"resource":"projects","verbs":["read","list"]}]'),
('Node Operator', 'Node Operator', 'Perform node maintenance in an adopted cluster', '[{"resource":"clusters","verbs":["read"]},{"resource":"nodes","verbs":["read","list","update","manage"]},{"resource":"pods","verbs":["read","list","watch"]},{"resource":"workloads","verbs":["read","list"]}]'),
('Service Mesh Operator', 'Service Mesh Operator', 'Manage service mesh traffic policy, mTLS policy, and mesh health for a cluster', '[{"resource":"service_mesh","verbs":["create","read","update","delete","list"]},{"resource":"services","verbs":["read","list"]},{"resource":"ingresses","verbs":["read","list"]},{"resource":"clusters","verbs":["read"]},{"resource":"monitoring","verbs":["read","list"]}]'),
('Storage Manager', 'Storage Manager', 'Manage persistent volume claims, storage classes, and storage health for an adopted cluster', '[{"resource":"storage","verbs":["create","read","update","delete","list"]},{"resource":"clusters","verbs":["read"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list"]}]')
) AS v(name, display_name, description, rules)
WHERE NOT EXISTS (
    SELECT 1 FROM cluster_roles existing WHERE existing.name = v.name
);

INSERT INTO project_roles (name, display_name, description, rules, is_builtin)
SELECT v.name, v.display_name, v.description, v.rules::jsonb, true
FROM (
VALUES
('Config Manager', 'Config Manager', 'Manage non-secret configuration objects in a project scope', '[{"resource":"projects","verbs":["read"]},{"resource":"configmaps","verbs":["create","read","update","delete","list"]},{"resource":"workloads","verbs":["read","list","restart"]},{"resource":"pods","verbs":["read","list","logs"]}]'),
('GitOps Deployer', 'GitOps Deployer', 'Trigger and inspect GitOps deployments for a project', '[{"resource":"projects","verbs":["read"]},{"resource":"argocd","verbs":["read","list","sync"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","watch","logs"]}]'),
('Namespace Operator', 'Namespace Operator', 'Manage namespace-level labels, annotations, quotas, limit ranges, and network-policy templates within a project', '[{"resource":"projects","verbs":["read","update"]},{"resource":"network_policies","verbs":["create","read","update","delete","list"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list"]}]'),
('Secret Manager', 'Secret Manager', 'Manage Kubernetes Secret objects within a project', '[{"resource":"projects","verbs":["read"]},{"resource":"secrets","verbs":["create","read","update","delete","list"]},{"resource":"workloads","verbs":["read","list","restart"]},{"resource":"pods","verbs":["read","list"]}]'),
('Service and Ingress Manager', 'Service and Ingress Manager', 'Manage Services, Ingresses, Gateway-style entry points, and service proxy exposure within a project', '[{"resource":"projects","verbs":["read"]},{"resource":"services","verbs":["create","read","update","delete","list","proxy"]},{"resource":"ingresses","verbs":["create","read","update","delete","list"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list"]}]'),
('Workload Deployer', 'Workload Deployer', 'Create, update, scale, restart, and observe workloads within a project', '[{"resource":"projects","verbs":["read"]},{"resource":"workloads","verbs":["create","read","update","list","scale","restart"]},{"resource":"pods","verbs":["read","list","watch","logs"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"argocd","verbs":["read","list","sync"]}]'),
('Workload Viewer', 'Workload Viewer', 'Read-only access to workloads, pods, logs, metrics, and GitOps state within a project', '[{"resource":"projects","verbs":["read"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","watch","logs"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"argocd","verbs":["read","list"]}]')
) AS v(name, display_name, description, rules)
WHERE NOT EXISTS (
    SELECT 1 FROM project_roles existing WHERE existing.name = v.name
);
