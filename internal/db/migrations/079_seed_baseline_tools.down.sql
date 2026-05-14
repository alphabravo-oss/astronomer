-- Reverse sprint 079.
--
-- Removes the cluster_tools rows seeded for the platform-baseline
-- operators. The cert-manager row is owned by migration 033 and stays.
-- The helm_repositories row for fluent is removed too; bitnami/aqua/
-- jetstack/prometheus-community are owned by 075/077 and stay.

DELETE FROM cluster_tools WHERE slug IN (
    'trivy-operator',
    'kube-state-metrics',
    'prometheus-node-exporter',
    'fluent-bit'
);

DELETE FROM helm_repositories WHERE name = 'fluent';
