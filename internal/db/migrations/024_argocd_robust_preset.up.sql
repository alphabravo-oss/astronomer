-- 024_argocd_robust_preset.up.sql
--
-- Tightens the ArgoCD catalog preset to ship `accounts.astronomer` with
-- apiKey + login capability and bind it to upstream's admin role. Without
-- this, the only path to authenticate Astronomer's typed client and the UI
-- proxy is admin's session JWT, which expires every 24h and silently flips
-- the instance to "unhealthy" until someone manually re-mints it.
--
-- The preset also pins server.rootpath/basehref to /argocd so the SPA emits
-- correct asset paths under the Astronomer reverse-proxy mount.
--
-- This is an UPDATE rather than an INSERT so existing rows get the new
-- values; the underlying chart install still has to be re-run for the
-- changes to land on a live ArgoCD release.

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
        'configs:\n'
        '  params:\n'
        '    server.insecure: "true"\n'
        '    server.rootpath: "/argocd"\n'
        '    server.basehref: "/argocd"\n'
        '  cm:\n'
        '    accounts.astronomer: "apiKey, login"\n'
        '  rbac:\n'
        '    policy.default: "role:readonly"\n'
        '    policy.csv: |\n'
        '      g, astronomer, role:admin\n'
        ::text
    )
)
WHERE slug = 'argocd';
