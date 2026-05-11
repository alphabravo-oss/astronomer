-- Remove the role rows added in 036. is_builtin=true guard avoids deleting
-- operator-customized rows that happen to share a name.
DELETE FROM global_roles WHERE is_builtin = true AND name IN (
    'Catalog Maintainer', 'Backup Operator', 'Cluster Registrar', 'Alerts Manager'
);
DELETE FROM cluster_roles WHERE is_builtin = true AND name IN (
    'Workload Editor', 'Pod Incident Responder', 'Cluster Monitoring Admin'
);
DELETE FROM project_roles WHERE is_builtin = true AND name IN (
    'Project Auditor', 'Project Deployer'
);
