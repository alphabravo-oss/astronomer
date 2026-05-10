-- Expand the built-in RBAC catalog for upgraded installs.
INSERT INTO global_roles (name, description, rules, is_builtin)
SELECT v.name, v.description, v.rules::jsonb, v.is_builtin
FROM (
VALUES
('User Administrator', 'Manage platform users and inspect role assignments', '[{"resource":"users","verbs":["create","read","update","delete","list"]},{"resource":"rbac","verbs":["read","list"]}]', true),
('RBAC Administrator', 'Manage roles and role bindings across the platform', '[{"resource":"rbac","verbs":["*"]},{"resource":"users","verbs":["read","list"]}]', true),
('Auditor', 'Read-only visibility into platform state, security posture, and audit evidence', '[{"resource":"clusters","verbs":["read","list"]},{"resource":"projects","verbs":["read","list"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","watch","logs"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"alerts","verbs":["read","list"]},{"resource":"catalog","verbs":["read","list"]},{"resource":"backups","verbs":["read","list"]},{"resource":"security","verbs":["read","list"]},{"resource":"argocd","verbs":["read","list"]},{"resource":"settings","verbs":["read","list"]},{"resource":"sso","verbs":["read","list"]},{"resource":"users","verbs":["read","list"]},{"resource":"rbac","verbs":["read","list"]},{"resource":"audit_logs","verbs":["read","list"]},{"resource":"agents","verbs":["read","list"]}]', true)
) AS v(name, description, rules, is_builtin)
WHERE NOT EXISTS (
    SELECT 1
    FROM global_roles existing
    WHERE existing.name = v.name
);

INSERT INTO cluster_roles (name, description, rules, is_builtin)
SELECT v.name, v.description, v.rules::jsonb, v.is_builtin
FROM (
VALUES
('Cluster Operator', 'Operate workloads and cluster application delivery without full administrative access', '[{"resource":"clusters","verbs":["read"]},{"resource":"workloads","verbs":["create","read","update","delete","list","scale","restart"]},{"resource":"pods","verbs":["read","list","watch","logs","exec","proxy"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"alerts","verbs":["read","list"]},{"resource":"backups","verbs":["read","list"]},{"resource":"argocd","verbs":["read","list","sync"]}]', true),
('Cluster Troubleshooter', 'Inspect workloads and use pod-level diagnostics within a cluster', '[{"resource":"clusters","verbs":["read"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","watch","logs","exec","proxy"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"alerts","verbs":["read","list"]}]', true)
) AS v(name, description, rules, is_builtin)
WHERE NOT EXISTS (
    SELECT 1
    FROM cluster_roles existing
    WHERE existing.name = v.name
);

INSERT INTO project_roles (name, description, rules, is_builtin)
SELECT v.name, v.description, v.rules::jsonb, v.is_builtin
FROM (
VALUES
('Project Operator', 'Operate workloads within a project without full project administration', '[{"resource":"projects","verbs":["read"]},{"resource":"workloads","verbs":["create","read","update","delete","list","scale","restart"]},{"resource":"pods","verbs":["read","list","watch","logs","exec","proxy"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"argocd","verbs":["read","list","sync"]}]', true),
('Project Troubleshooter', 'Inspect workloads and use pod-level diagnostics within a project', '[{"resource":"projects","verbs":["read"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","watch","logs","exec","proxy"]},{"resource":"monitoring","verbs":["read","list"]}]', true)
) AS v(name, description, rules, is_builtin)
WHERE NOT EXISTS (
    SELECT 1
    FROM project_roles existing
    WHERE existing.name = v.name
);
