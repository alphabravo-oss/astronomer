-- Curated tools for adopted clusters. Fresh installs run this forward migration too.
-- Official charts verified 2026-09-10: charts.longhorn.io and neuvector.github.io/neuvector-helm.
INSERT INTO public.cluster_tools
    (id, slug, name, description, icon, category, charts, version_constraint, default_namespace, presets)
VALUES
    ('e8c42de5-d35b-4994-b3f5-b30fac0b9b01', 'longhorn', 'Longhorn',
     'Distributed block storage. Requires Linux nodes with open-iscsi and mount propagation configured. Preserves the existing default StorageClass; new volumes retain data when claims are deleted.',
     'hard-drive', 'storage',
     '[{"order":0,"repo_url":"https://charts.longhorn.io","namespace":"longhorn-system","chart_name":"longhorn"}]',
     '1.12.1', 'longhorn-system',
     '{"default":"persistence:\n  defaultClass: false\n  defaultClassReplicaCount: 3\n  reclaimPolicy: Retain\n"}'),
    ('e8c42de5-d35b-4994-b3f5-b30fac0b9b02', 'neuvector', 'NeuVector',
     'Runtime security and image scanning. Enforcers require privileged Linux workloads and access to the container runtime. Manager remains internal (ClusterIP); configure authentication before exposing it.',
     'shield-check', 'security',
     '[{"order":0,"repo_url":"https://neuvector.github.io/neuvector-helm","namespace":"neuvector","chart_name":"core"}]',
     '2.11.1', 'neuvector',
     '{"default":"controller:\n  replicas: 3\nmanager:\n  svc:\n    type: ClusterIP\n  ingress:\n    enabled: false\n  route:\n    enabled: false\ncve:\n  scanner:\n    replicas: 1\n"}')
ON CONFLICT (slug) DO NOTHING;
