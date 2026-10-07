# Changelog

## Unreleased

Target release: **1.2.0**.

### Added

- Saved table views, consistent operator page/table controls, observation freshness,
  and shared observation/assignment caches.
- Rancher-style, cluster-scoped Apps discovery with curated, Astronomer
  first-party, community, and operator-added catalog sources; enriched app
  details; schema-guided install/upgrade flows; immutable values review; and
  Flux-owned lifecycle operations.
- Multi-cluster Applications and System Components views with rollout cohorts,
  per-cluster history, resource/drift evidence, cluster groups, and normalized
  ownership, compatibility, storage, HA, and update posture.
- Catalog/delivery metrics, alerts, dashboards, support diagnostics, DR table
  validation, wrapped private-catalog CA backup, and a static HTTPS
  application-catalog air-gap export/import workflow.

### Release qualification

Publication of v1.2.0 requires automated builds, vulnerability/license scans,
SBOMs and signing, clean installs on Kubernetes 1.33, 1.34 and 1.35, and a signed
v1.1.0-to-v1.2.0 upgrade, backup/decryption and clean-restore rehearsal.
Cloud-provider acceptance, production-scale certification, the Rancher comparison
benchmark and human assistive-technology certification are deferred. These
certifications are not claimed for this release. The v1.2.0 publication policy
does not require a separately named release approver; this exception is scoped
to this tag and does not relax the policy for subsequent releases.

### Upgrade notes from 1.1.0

This release includes intentional API migration changes despite retaining the
1.2.0 product version. Existing integrations must be reviewed before upgrading;
it is not a fully backward-compatible API update.

- Browser login, refresh and TOTP completion use HttpOnly session cookies; token
  and refresh-token values are no longer returned in JSON. Preserve the cookie
  jar and send the CSRF header for unsafe session-authenticated requests.
  Automation should use a scoped API token from `/api/v1/auth/tokens/` rather
  than extracting credentials from browser login responses.
- Cluster lists and agent lifecycle lists expose rows under `data` with
  `pagination` metadata; cluster-agent inventory exposes `summary` alongside
  those fields. Replace old nested `data.items` and DRF `count/next/previous`
  assumptions. Agent inventory supports cursor pagination; legacy offsets are
  capped at 10,000.
- Use `/api/v1/clusters/{id}/generate-kubeconfig` for a proxy YAML download,
  `/generate-direct-kubeconfig` for an authorized short-lived direct download,
  and `/kubeconfig-preview` for JSON preview. The old underscore spelling and
  `/kubeconfig` alias are removed.
- Use the Flux delivery sources, bundles, targets and rollouts APIs in place of
  the retired delivery estate alias. Activity is documented at `/api/v1/activity`.
- Charlie trigger-rule clients must send `estate_threshold_percent`. Tool form
  renderers must support the new `multiline` widget or provide a text/YAML fallback.
- Catalog preview, README and values payloads are wrapped in `data`; the OpenAPI
  descriptions and generated clients now match the existing server behavior.
- Saved views add migration 071 after the existing 067–070 offering migrations.
  Back up the database and encryption key, use the supported upgrade path, and
  qualify rollback against the older release's schema guard before relying on it.

The exact reviewed API changes are recorded in
[the compatibility review](docs/api-breaking-change-review.md). Release
qualification and external acceptance remain required before publication.

### Changed

- Curated catalog synchronization can be disabled with `catalog.enabled=false`
  without disabling management of installed applications or custom Helm
  repositories.

### Fixed

- A fresh evaluation install now starts with delivery disabled until signed
  release artifacts are configured, avoiding an otherwise non-runnable default.
- The chart rejects an externally owned core Secret with bundled PostgreSQL,
  a combination Kubernetes cannot safely interpolate into the application DSN.
- Astronomer can attach its HTTPRoutes to an existing cross-namespace Gateway,
  allowing it to share one load balancer and externally terminated certificate.
- Production server, worker, frontend, and Dex workloads now enforce node-level
  topology spreading; Dex also has a disruption budget.
- Helm verification renders against the chart's supported Kubernetes 1.35 API
  target instead of Helm's older implicit default.
- Release retries refuse to reuse exact image tags unless signed provenance
  binds every image to the release commit, and refuse mismatched chart content.

## 1.1.0 - 2026-08-21

### Added

- **Fleet Grafana** as a shared stack family next to Thanos: ClusterIP install, optional 1Gi PVC, grafana-proxy ticket bounce, Explore-lock, Fleet/Management and per-cluster folders, PromQL/LogQL rewrite for cluster-scoped Explore.
- **Optional Astronomer Loki** (`feature.hosted_loki` default false): sizer-gated ClusterIP warehouse, ingest tokens, per-cluster system destinations, Fluent Bit Secret mounts, managementLogging overlay when Loki is healthy.
- **Management-cluster sizer API** (`GET /api/v1/settings/monitoring/sizer/`): leftover-floor Grafana vs fail-closed Loki.
- **OSS air-gap kit** on the GitHub Release: `astronomer-airgap-vX.Y.Z.tar.gz` plus complete digest-pinned `astronomer-images.txt`, with `astronomer-save-images.sh` / `astronomer-load-images.sh` (default linux/amd64, no image blobs on GitHub).
- Fleet vs cluster observability IA (Shared stacks / Fleet metrics / Alerting / Logging destinations vs per-cluster Metrics / Monitoring stack / Alerting rules / Logging pipelines).

### Changed

- **Cluster monitoring stack `enableGrafana` default.** When `enableGrafana` is omitted on a **new** (`not_configured`) cluster stack, Grafana is now **disabled** if the shared (fleet) Grafana family is healthy. Previously omitted `enableGrafana` always defaulted to `true` (kube-prometheus-stack chart default). Explicit `true` / `false` is unchanged. Already-configured stacks keep the historical `true` default on upgrade/replace so an omitted key does not strip an existing cluster Grafana.

  Cluster Grafana still talks to **this** cluster’s Prometheus (15d local retention) and survives an Astronomer outage. Fleet Grafana is the lobby (Thanos + logs) and is down when Astronomer is down. New cluster stacks no longer default Grafana on when the lobby exists.

- **OSS images:** Alpine `apk upgrade` on every final stage; migrate and frontend run as non-root in the image config; Go builds use `-trimpath`; migrate copies SQL only; shell fetches kubectl in a throwaway stage (no runtime curl); release CI runs the same Trivy HIGH/CRITICAL gate as PRs before signing.
- **Worker delivery verification** uses in-process sigstore-go instead of a bundled Cosign CLI. Cosign-key sources still mount `<key_ref>.pub`; keyless sources also need `trusted_root.json` in the same trust secret. Verify stays offline (no TUF, Rekor, or Fulcio network calls).
- **Release CI** rebuilds an exact image tag when the tag exists but is unsigned (failed scan-after-push leftover). Signed tags stay immutable.

### Notes

- v1.0.0 image digests and Flux `certificateIdentity` `@refs/tags/v1.0.0` remain that release’s identity. This tag publishes new first-party images and a new chart; it does not retag v1.0.0.
- Charlie remains the already-qualified `v1.0.63` artifact. Loki is not auto-installed.
