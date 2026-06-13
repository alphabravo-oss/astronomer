DELETE FROM global_roles
WHERE is_builtin = true
  AND name IN (
    'Platform Operator',
    'Security Auditor',
    'Compliance Manager',
    'GitOps Admin',
    'GitOps Viewer',
    'Logging Viewer',
    'Monitoring Admin',
    'Monitoring Viewer',
    'Restore Operator',
    'Support Bundle Operator',
    'Audit Viewer',
    'Catalog Admin'
  );

DELETE FROM cluster_roles
WHERE is_builtin = true
  AND name IN (
    'Catalog Installer',
    'Cluster Backup Operator',
    'Node Operator',
    'Service Mesh Operator',
    'Storage Manager'
  );

DELETE FROM project_roles
WHERE is_builtin = true
  AND name IN (
    'Config Manager',
    'GitOps Deployer',
    'Namespace Operator',
    'Secret Manager',
    'Service and Ingress Manager',
    'Workload Deployer',
    'Workload Viewer'
  );
