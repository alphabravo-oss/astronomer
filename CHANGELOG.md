# Changelog

## Unreleased

### Added

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

### Changed

- Curated catalog synchronization can be disabled with `catalog.enabled=false`
  without disabling management of installed applications or custom Helm
  repositories.

## 1.2.0 - 2026-08-25

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
