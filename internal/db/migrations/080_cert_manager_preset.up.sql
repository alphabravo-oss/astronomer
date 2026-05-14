-- Sprint 080 — make cert-manager's default preset survive slow webhook starts.
--
-- During live verification of the platform-baseline apply on a k3d cluster
-- (sprint 079) the cert-manager install succeeded — all 4 cert-manager
-- pods reached Ready — but the chart's post-install startupAPICheck Job
-- hit BackoffLimitExceeded because the webhook needs ~30s to register
-- after pod-Ready and the Job's 60s timeout fires sooner on a
-- resource-constrained k3d node. The helm install therefore returns
-- failure and the platform-baseline apply marks cert-manager failed
-- even though cert-manager itself is healthy.
--
-- The deterministic fix is the chart's documented escape hatch:
-- startupapicheck.enabled=false. Astronomer's webhook-readiness probe
-- (separate from the helm post-install Job) is the real source of truth
-- for "cert-manager is up" inside the platform anyway — operators don't
-- need the helm post-install Job to gate the install.
--
-- Idempotent: replaces the existing `default` preset key on the
-- cert-manager row only; other presets are untouched.

UPDATE cluster_tools
SET presets = jsonb_set(
    presets,
    '{default}',
    to_jsonb($preset$crds:
  enabled: true
prometheus:
  enabled: true
startupapicheck:
  enabled: false
$preset$::text)
)
WHERE slug = 'cert-manager';
