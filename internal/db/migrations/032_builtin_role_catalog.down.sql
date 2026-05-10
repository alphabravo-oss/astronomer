DELETE FROM project_roles
WHERE is_builtin = true
  AND name IN ('Project Operator', 'Project Troubleshooter');

DELETE FROM cluster_roles
WHERE is_builtin = true
  AND name IN ('Cluster Operator', 'Cluster Troubleshooter');

DELETE FROM global_roles
WHERE is_builtin = true
  AND name IN ('User Administrator', 'RBAC Administrator', 'Auditor');
