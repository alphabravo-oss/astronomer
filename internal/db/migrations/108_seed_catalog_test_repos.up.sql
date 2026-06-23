-- Migration 108 — seed a handful of blessed, project-maintained Helm repos so
-- the catalog page has real cards/versions/values to exercise end to end.
--
-- These are official charts published by the projects themselves (no Bitnami),
-- chosen to cover BOTH install-UI paths:
--   • cert-manager / kyverno / external-secrets ship values.schema.json -> form
--   • longhorn ships only values.yaml                                   -> YAML editor
-- The two observability repos (prometheus-community, grafana) were already
-- seeded by migration 082.
--
-- is_default=true marks them as platform-shipped defaults; operators can
-- disable them (enabled=false) or add their own repos without losing these.
-- ON CONFLICT (name) DO NOTHING keeps re-runs and operator edits a no-op.
INSERT INTO helm_repositories (id, name, url, repo_type, description, is_default, enabled, auth_type, created_at, updated_at)
VALUES
    (gen_random_uuid(), 'jetstack', 'https://charts.jetstack.io',
     'helm', 'Jetstack — cert-manager, trust-manager. X.509 certificate management for Kubernetes.',
     true, true, 'none', now(), now()),
    (gen_random_uuid(), 'kyverno', 'https://kyverno.github.io/kyverno/',
     'helm', 'Kyverno — Kubernetes-native policy engine (admission control, mutation, generation).',
     true, true, 'none', now(), now()),
    (gen_random_uuid(), 'external-secrets', 'https://charts.external-secrets.io',
     'helm', 'External Secrets Operator — sync secrets from Vault, AWS/GCP/Azure secret managers.',
     true, true, 'none', now(), now()),
    (gen_random_uuid(), 'longhorn', 'https://charts.longhorn.io',
     'helm', 'Longhorn — cloud-native distributed block storage for Kubernetes.',
     true, true, 'none', now(), now())
ON CONFLICT (name) DO NOTHING;
