# Platform baseline (sprint 075)

After install / first boot the management plane seeds well-known
helm_repositories rows (aqua, jetstack, fluent, prometheus-community —
migrations 075/077/079; migration 083 removes the original bitnami
seed because Broadcom deprecated the public Bitnami catalog in Aug
2025 and `helm install bitnami/...` now pulls stale unpatched images)
and kicks a one-shot `catalog:sync` if `helm_charts` is empty. This
closes the "register cluster → auto-install platform baseline" gap:
the five slugs the platform-baseline cluster_template (sprint 074)
references —

- `trivy-operator`
- `kube-state-metrics`
- `node-exporter`
- `fluent-bit`
- `cert-manager`

— resolve against the seeded catalog without operator intervention.

## Verifying coverage

Hit the read-only superuser endpoint (also used by the dashboard banner
once sprint 074's UI lands):

```
GET /api/v1/admin/platform-settings/default-cluster-template/coverage/
```

Response shape:

```json
{
  "template_id": "",
  "expected_slugs": ["trivy-operator", "kube-state-metrics", "node-exporter", "fluent-bit", "cert-manager"],
  "resolved": [
    { "slug": "trivy-operator", "found": true, "chart_id": "<uuid>", "repository": "aqua" }
  ],
  "missing_slugs": []
}
```

`missing_slugs` is the operator's signal: empty means the baseline is
ready to fire; non-empty means either the first-boot `catalog:sync`
hasn't drained yet (wait ~30s after install) or the upstream chart name
differs from the slug the template carries. If a slug is permanently
missing, edit `defaultBaselineSlugs` in
`internal/handler/platform_baseline_coverage.go` and the matching
template spec to use the upstream chart name.

## Operator customization

The migration uses `ON CONFLICT (name) DO NOTHING`, so an operator who
re-points a seeded repo at a private mirror (or adds repos with the
same name before the migration runs) is never overridden on re-runs.
The `.down.sql` deletes only the named rows so a downgrade keeps
operator-added repos.

## Frontend banner

Deferred — the `/dashboard/settings/compliance/baselines` page that
ships with sprint 074 should call the coverage endpoint and render a
"5/5 slugs resolved" banner, linking missing slugs to
`/dashboard/catalog?search=<slug>`. Until then operators verify via
the API directly.
