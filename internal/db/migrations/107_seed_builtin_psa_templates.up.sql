-- Sprint 107 — ship default Pod Security Admission (PSA) starter templates
-- "delivered but not enabled".
--
-- Pod Security Admission is the in-tree Kubernetes admission controller that
-- enforces the three Pod Security Standards — privileged / baseline /
-- restricted — per namespace, in enforce / audit / warn modes (it replaced
-- the removed PodSecurityPolicy). The app already models a PSA template as a
-- row in pod_security_templates; a template only *enforces* anything once it
-- is bound to a cluster via cluster_security_policies. Seeding template rows
-- here therefore delivers ready-to-use defaults WITHOUT changing the security
-- posture of any cluster — operators still have to explicitly assign + apply.
--
-- 1. Add an is_builtin flag so the three platform-owned starter templates can
--    be marked and protected from edit/delete (mirrors the platform-baseline
--    pattern on cluster_templates / the is_builtin flag on the RBAC catalogs).
-- 2. Seed the three starter templates. We deliberately do NOT insert any
--    cluster_security_policies rows: delivered, not enabled.

ALTER TABLE pod_security_templates
    ADD COLUMN IF NOT EXISTS is_builtin BOOLEAN NOT NULL DEFAULT false;

-- Privileged: entirely unrestricted, the open Pod Security Standard. Useful as
-- an explicit "PSA is off / opted out" template for system or trusted
-- namespaces. enforce=privileged is a no-op admission-wise.
INSERT INTO pod_security_templates (
    name, description, is_default, is_builtin,
    enforce_level, enforce_version,
    audit_level, audit_version,
    warn_level, warn_version,
    exempt_usernames, exempt_runtime_classes, exempt_namespaces
) VALUES (
    'Privileged (PSA off)',
    'Built-in starter template. The unrestricted Pod Security Standard — no restrictions on pod capabilities. Use for trusted/system namespaces or to explicitly opt a namespace out of PSA. Delivered but not applied to any cluster.',
    false, true,
    'privileged', 'latest',
    'privileged', 'latest',
    'privileged', 'latest',
    '[]', '[]', '["kube-system", "kube-node-lease"]'
)
ON CONFLICT (name) DO NOTHING;

-- Baseline: minimally restrictive standard that blocks known privilege
-- escalations while staying broadly compatible. Enforced; audit/warn surface
-- restricted-level gaps so operators can plan a tightening to restricted.
INSERT INTO pod_security_templates (
    name, description, is_default, is_builtin,
    enforce_level, enforce_version,
    audit_level, audit_version,
    warn_level, warn_version,
    exempt_usernames, exempt_runtime_classes, exempt_namespaces
) VALUES (
    'Baseline',
    'Built-in starter template. Enforces the Baseline Pod Security Standard (blocks known privilege escalations, broadly compatible) while auditing and warning against the stricter Restricted standard. A safe first step. Delivered but not applied to any cluster.',
    true, true,
    'baseline', 'latest',
    'restricted', 'latest',
    'restricted', 'latest',
    '[]', '[]', '["kube-system", "kube-node-lease"]'
)
ON CONFLICT (name) DO NOTHING;

-- Restricted: the hardened standard following current pod-hardening best
-- practices. Enforced/audited/warned at restricted across the board.
INSERT INTO pod_security_templates (
    name, description, is_default, is_builtin,
    enforce_level, enforce_version,
    audit_level, audit_version,
    warn_level, warn_version,
    exempt_usernames, exempt_runtime_classes, exempt_namespaces
) VALUES (
    'Restricted',
    'Built-in starter template. Enforces, audits, and warns at the Restricted Pod Security Standard — the hardened policy following current pod-hardening best practices. Recommended for production workloads. Delivered but not applied to any cluster.',
    false, true,
    'restricted', 'latest',
    'restricted', 'latest',
    'restricted', 'latest',
    '[]', '[]', '["kube-system", "kube-node-lease"]'
)
ON CONFLICT (name) DO NOTHING;
