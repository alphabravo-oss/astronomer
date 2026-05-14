-- Sprint 082 — seed observability repos for the per-cluster Apps tab.
--
-- Migration 075 seeded the tools-tab basics (bitnami / aqua / jetstack)
-- for the Platform Baseline template. The Apps tab is the broader
-- catalog browse — its flagship installs are kube-prometheus-stack
-- (prometheus-community) and loki-stack (grafana). Neither was in the
-- 075 seed, so on a fresh install the Apps tab would land empty for
-- the two charts we ship as "blessed" Apps.
--
-- ON CONFLICT (name) DO NOTHING is the same idempotency guarantee
-- migration 075 uses: re-running on an existing DB is a no-op, and
-- operators who manually added these repos (or pointed them at a
-- private mirror) keep their rows untouched.
--
-- We deliberately don't seed:
--   • docker-hardened-images / dhi.io — paywalled DHI subscription,
--     OCI-only, no index.yaml. Surfaced via the "Suggested catalogs"
--     picker (sprint 082+) so paying users can opt in with one click
--     without making fresh installs look broken.
--   • rancher-apps — pulls in many curated charts but the URL surface
--     is large; deferred to the suggested-catalogs picker as well.

INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES
    (gen_random_uuid(), 'prometheus-community', 'https://prometheus-community.github.io/helm-charts',
     'helm', 'Prometheus community — kube-prometheus-stack, prometheus, alertmanager, exporters. Seeded by sprint 082 for the Apps tab.',
     true, 'none', now(), now()),
    (gen_random_uuid(), 'grafana', 'https://grafana.github.io/helm-charts',
     'helm', 'Grafana Labs — loki-stack, tempo, mimir, grafana itself. Seeded by sprint 082 for the Apps tab.',
     true, 'none', now(), now())
ON CONFLICT (name) DO NOTHING;
