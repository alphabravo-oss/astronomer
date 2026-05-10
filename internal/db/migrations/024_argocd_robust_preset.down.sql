-- Roll back to the minimal Day-1 preset. Existing live ArgoCD installs are
-- not reverted; the preset only affects future install-from-catalog flows.
UPDATE cluster_tools
SET presets = jsonb_set(
    presets,
    '{default}',
    to_jsonb(
        E'server:\n'
        '  service:\n'
        '    type: ClusterIP\n'
        '  ingress:\n'
        '    enabled: false\n'
        'controller:\n'
        '  replicas: 1\n'
        'redis-ha:\n'
        '  enabled: false\n'
        'applicationSet:\n'
        '  enabled: true\n'
        'dex:\n'
        '  enabled: false\n'
        ::text
    )
)
WHERE slug = 'argocd';
