# Changelog

## Unreleased

## 2.1.0 - 2026-10-20

Technology-guide realignment (Phase 0 + Phase 1) and the A1 Operator Console UI.
**This release has breaking deploy changes — read "Upgrading to 2.1.0" first.**

### Security

- **eMudhra intermediate CA compromise (2026-10-20).** Go 1.27.2 and older
  accept certificate chains from the compromised eMudhra intermediates;
  Go 1.27.3 rejects them regardless of the OS trust store. Every Go binary in
  every 2.1.0 image is built with the pinned Go 1.27.3 builder, including the
  third-party ones that used to come prebuilt: `kubectl` in `astronomer-shell`
  is compiled from k8s.io/kubectl v0.35.9 (dl.k8s.io ships go1.26.8 builds) and
  the `age` CLI in `astronomer-dr` from filippo.io/age v1.3.2 (Alpine's package
  is go1.26.9), via the `tools/imagebins` module.
  `scripts/check-image-go.sh` fails `make docker-build` and the release
  workflow (amd64 + arm64) when any Go binary in an image is older than
  go.mod's `go` line. Verified against dv-g3b-emudhra.we-have-a-problem.com
  and dv-g3b-aaa-emudhra.we-have-a-problem.com: rejected from inside the 2.1.0
  server image; a go1.27.2 build accepts the first host inside the 2.0.1 image.
- Flux `source-controller` (upstream image, go1.26.8, its CA store still
  trusts emSign ECC Root CA - G3) now runs with the agent image's CA bundle:
  the agent writes ConfigMap `astronomer-trust-bundle` in
  `astronomer-delivery-system` and the system Kustomization patches
  source-controller to mount it over `/etc/ssl/certs` (whole directory — Go
  also reads per-certificate files, so `SSL_CERT_FILE` alone is not enough).
  New agent install manifests carry the exact-name Role for that ConfigMap;
  agents enrolled earlier skip the mount until the Role is applied.
- Base images refreshed: distroless `static-debian13:nonroot` (was
  debian12) for server/worker/agent, nginx `1.31.7-alpine3.24-slim` for the
  frontend; alpine 3.24.2, node 24.22.0-alpine and golang 1.27.3-alpine were
  already the newest digests.
- Release images are signed in the legacy `.sig`/`.att` layout
  (`--new-bundle-format=false`) because policy-controller 0.13.1 cannot
  verify Sigstore bundles; the chart is signed after a `cosign login`.

### Upgrading to 2.1.0 (breaking)

- **Migrations run inside the server at boot** (goose, embedded, advisory
  lock). The migrate image, the `migrate` Helm hook Job, the server init
  container and the `image.migrate.*`, `image.allowSchemaSkew`, `migrate.*`
  values are gone. Bookkeeping moves from golang-migrate `schema_migrations`
  to `goose_db_version`; an existing 2.0.x database (schema 81) needs a
  one-time baseline before the first 2.1.0 boot —
  `docs/runbooks/upgrade-2.1.md`. Preflight target schema is **84**.
- **Encryption key format changed to age.** `secrets.encryptionKey` /
  `ASTRONOMER_ENCRYPTION_KEY` must be an age keyring
  (`[label:]AGE-SECRET-KEY-1…`, current first); Fernet keys are rejected.
  Migration 00082 discards every stored ciphertext: re-enter Helm repository
  credentials, registry/git/cloud credentials, SSO client secrets, SMTP/SIEM/
  vault/webhook secrets and Dex connectors. SSO sessions and TOTP enrolments
  are deleted. Rotate with `astro admin rotate-keys`.
- API: one error envelope `{"error":{code,message,request_id,fields,details}}`
  for every error (429, quota and maintenance extras under `error.details`,
  lost DB → 503 `db_unavailable`); authorization is decided by one static
  route→permission table before feature/scope gates and denials write
  `auth.authorization_denied` audit rows.
- Logs are zerolog JSON: key `message` (not `msg`), lowercase levels,
  request summary fields `route`/`status`. New env `LOG_FORMAT`,
  `REGISTRY_LOCAL`, `REGISTRY_FALLBACKS`, `REGISTRY_PLAIN_HTTP`.
- The DR image ships PostgreSQL 17 client tools and the Go `dr-crypto`
  (envelope `ASTRODR2`); Python-format DR backups do not decrypt.
- `branding.primary_color` defaults to `""` (unconfigured); the untouched
  `#0066CC` seed is reset by migration 00084.
- Release candidates are tagged `vX.Y.Z-rc.N`; the policy-controller subject
  regex in `deploy/policy-controller/cip-astronomer.yaml` admits them.

### Fixed during the 2.1.0 deploy

- The management-plane (`local`) cluster's system-release assignment now
  follows the released release after a server upgrade
  (`RetargetLocalDeliverySystemAssignment` before each observation); it used
  to keep the release current at enrollment and sit in `applying` forever.
- `astronomer_agent_connections` no longer keeps a stale
  `astronomer_instance_id="unknown"` series at 0 when the reporter publishes
  before the instance id is resolved (it kept `AstronomerAgentDisconnected`
  firing for connected clusters after the 2.1.0 boot-order change).

- Management backup and restore drill run end to end on the chart's own
  CNPG 17: the wrapping passphrase and key Secret are dereferenced from their
  Secret-volume symlinks (dr-crypto accepts only regular files), only
  non-empty top-level keys are bundled, and the schema version is read from
  `goose_db_version` (first successful backup + drill on astronomer-dev,
  2026-10-20T23:59Z).

### Phase 1 — backend

- Testing: testcontainers + pgtestdb substrate; every DB test is a
  `//go:build integration` test run by `make test-integration`.
- goose embedded migrations (ADR 0009), age column encryption with a
  `(key_id, algorithm, ciphertext, nonce)` row shape and `astro admin
  rotate-keys` (ADR 0010), zerolog `pkg/logging` with a slog bridge (ADR 0011),
  `pkg/telemetry` tracer + meter provider on one `/metrics` (ADR 0012).
- `internal/api/apiutil` error contract (259 codes) and the static route
  authorizer (905 routes, deny-by-default) (ADR 0016).
- Operator split into `internal/operator/{wire,lifecycle,mirror}` with per-key
  locking and stage metrics (ADR 0013).
- Air-gap layered registry resolver with optional in-cluster zot and
  `astro airgap plan|mirror` (ADR 0014); Python release/mirror scripts removed.
- Delivery fixes: `max_unavailable 0` means serial rollout, observation no
  longer promotes `pending` assignments, version `v`-prefix normalisation,
  `astro delivery system-rollout start|approve|status`, decommission cascades
  to `cluster_deployments`.
- Chart: `kubeVersion >=1.33 <1.38`, CNPG/DR NetworkPolicies (kube-apiserver,
  Barman plugin, object-store egress), chart-managed Barman `ObjectStore`,
  `scheduledBackup.immediate`, chart-managed Sigstore delivery trust root,
  policy-controller values + `ClusterImagePolicy` with per-vendor authorities
  (ADR 0015), k3s monitoring overlay.
- gosec G115 resolved with `pkg/safeconv`; the rule exception is gone.
- Go builder image pinned by digest (`deploy/docker/go-image.lock`).

### UI — A1 Operator Console and the shared kit

- Shared AlphaBravo UI kit in `frontend/src/ui/` (tokens, shell, sidebar,
  header, palette, tables, forms, states) — `docs/ui/KIT.md`, ADR 0017.
- Global rail of seven pillars + cluster panel with six groups; counts moved
  from the sidebar to the cluster overview Inventory.
- Sentence case and one vocabulary (Tools, Catalog, Tool links, Fleet,
  Releases, Access control, Audit log, Agents, Onboarding templates).
- Branding is a theme-level accent only; Charlie is a header icon + drawer
  (`⌘J`), no FAB; one command palette (`/`, `⌘K`).
- Fleet-first overview from one clusters query (verdict, clusters by health,
  capacity, agents, delivery); full entity names in tables; humanised audit
  actions; settings two layers deep.
- Playwright live tier covers the shell, palette, Charlie, settings layers,
  overview count agreement and axe (serious+critical = 0) on ten pages.

### Phase 0 — realignment with the AlphaBravo Technology Selection Guide

- Removed committed CI evidence, plan trees, generated inventories,
  compatibility ledgers, qualification-only scripts, binaries, packages and 20
  workflows (git ls-files 10,408 → ~5,100). Evidence lives in CI artifacts, not
  git. Previous history is intact.
- Toolchain: Go 1.27.3, Node 24.22.0 LTS, Helm 4.3.1, golangci-lint 2.15,
  gotestsum 1.13, Air 1.67. Dependencies refreshed (`go get -u`, npm bumps:
  React 19.3.1, TypeScript 6.0.3 (7.0.2 is blocked by typescript-eslint 8.72, whose peer range is <6.1.0 and which needs the JS compiler API the native TS 7 package does not ship), Vite 8.3.4, TanStack Query 5.104.5, vitest
  5.0.5, Playwright 1.64.1, lucide-react 1.59, @tanstack/react-table 9.3.1, radix-ui 1.8.1; eslint stays on 9.39 because eslint-plugin-jsx-a11y 6.10 declares no eslint 10 peer support; jsdom is held at 30.1.2 and @tanstack/react-virtual at 3.14.14 because 30.1.3 / 3.14.21 break the DataTable behaviour tests — tracked for Phase 1).
- Canonical `Makefile` (`dev build test test-integration lint vet fmt generate
  migrate check security fips-build updates docker-build helm-lint e2e`);
  `GO_VERSION` derived from `go.mod` for every image build.
- Distroless multi-stage Dockerfiles (`USER 65532`), digest-pinned bases;
  `database-cache-helper` no longer baked into the server image.
- Air live reload per binary, `scripts/dev.sh` one-command stack, single
  `deploy/docker-compose.yml` with Postgres 17, Redis 8, Tempo 3.1 (OTLP gRPC),
  Loki 3.7, Grafana Alloy, Prometheus 3.15, Grafana 13.2.
- Security gates as `make security` (gosec fail-on-HIGH SARIF, govulncheck
  SARIF, gitleaks full history); `make fips-build` stamps `pkg/version.FIPSMode`.
- Conformance: middleware order (recovery → request-id/tracing → logging),
  `service_build_info` gauge, `astronomer_request_errors_total{route,reason}`,
  OTLP gRPC exporter by default (`OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf`
  opt-in), session cookie takes precedence over bearer, `SERVER_ADDR`
  configurable, standalone proxy env reads moved into `internal/config`.
- Charlie: the artifact reconciler now persists the applied chart/image
  digests to `charlie_connections` after a successful Helm apply and caps Helm
  history at 5, so the agent release is no longer re-applied every interval.
- ADRs 0001–0008 under `docs/decisions/`; `CLAUDE.md` principles with a
  derived `AGENTS.md`.
- Playwright live tier (`frontend/playwright.live.config.ts`) is the default
  `npm run test:e2e`: real login persisted as storageState, cluster list,
  settings, metrics and refresh/revocation journeys against the Air-served
  binary. The OpenAPI-stub tier remains as `test:e2e:stubbed`.

## 2.0.1 - 2026-10-11

- Fix offline toolkit downloads by allowing the exact GitHub release-asset
  redirect while preserving public destination checks, TLS, digest
  verification and bounds. The incomplete v2.0.0 publication retains its
  immutable tag and signed images; v2.0.1 rebuilds and qualifies the complete
  artifact set from its own source.
- The release owner renewed the v1.2.1 qualification deferrals and upstream
  vulnerability risk acceptance for v2.0.1 on 2026-10-12.

## 2.0.0 - 2026-10-11

- Integrate the technology-guide implementation: updated toolchain and dependency
  authority, API and generated-client contracts, pagination, typed database and
  JSON boundaries, observability, distribution verification and upgrade tooling.
- Add a compact single-node Helm profile using CloudNativePG with one database
  instance. Production retains three CNPG instances and synchronous replication.
  Reduce application resource reservations, separate Sentinel resource budgets,
  consolidate monitoring exporters and adjust Flux placement on single-node sites.
- Monitor the PostgreSQL session holding server reconciler leadership and stop
  guarded loops when that session is lost. Replica scaling no longer depends on
  a static replica-count environment variable.
- Validate CNPG upgrade schema state and reject empty upgrade targets. Switching
  an existing bundled database to CNPG still requires an explicit dump/restore;
  changing Helm values alone does not migrate data.

### Migration and acceptance

This is a major release. Read [the 2.0 migration notes](docs/runbooks/upgrade-2.0.md)
before upgrading, preserve database and encryption-key backups, and update API
clients for the documented contract changes. The integrated schema is version 81.

The release owner explicitly approved carrying the v1.2.1 qualification deferrals
and upstream vulnerability risk acceptance forward to **v2.0.0**, and explicitly renewed the same scope for **v2.0.1**
on 2026-10-12. CVE identities, image digests and waiver expiries remain unchanged. Builds, fresh
vulnerability scans, exact-image waivers, SBOMs, signatures, Kubernetes clean
installs and signed upgrade/backup/restore checks remain mandatory. Cloud, scale,
Rancher benchmarking, human accessibility and license qualification remain
deferred; findings are retained and those certifications are not claimed.
Other future tags retain the strict publication policy.

Local single-node acceptance passed before release preparation; this is not a
multi-host HA or disaster-recovery certification. Technology-guide Plan 032
remains incomplete: development telemetry still uses Promtail, six public API
schemas remain permissive, a selectable agent audit stub remains, and repository
consolidation is outstanding. This release does not claim complete guide alignment
or introduce an embedded-k3s Docker appliance.

## 1.2.1 - 2026-10-08

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

### Release preparation fixes

- Replace the unpublished v1.2.0 candidate with new immutable v1.2.1 artifacts.
  Fix server/worker startup to accept the seven-image release manifest, including
  disaster recovery, and qualify Charlie from its verified signed OCI archive.
  The release owner explicitly carried the same CVE waivers and qualification
  deferrals forward to v1.2.1; waiver digests and expiry remain unchanged.

- Update upstream runtime images: Fluent Bit 5.1.3, Dex 2.45.1,
  kube-state-metrics 2.20.0 (chart 8.6.0), and digest-pinned CloudNativePG
  PostgreSQL 17.11 on the maintained standard Trixie image. The CNPG image
  continues to use the separate Barman Cloud plugin; the bundled development
  database stays on PostgreSQL 16. Existing CNPG deployments using non-C
  collations must check OS collation-version changes and rebuild affected
  indexes before refreshing collation versions.
- Update the signature-verified upstream kubectl binary to 1.35.9 and scan
  the complete shell image without the former kubectl exclusion. Fresh frontend
  builds refresh Alpine packages, including the corrected pcre2 package.
- Retain unresolved upstream vulnerability findings with explicitly approved,
  expiring, exact-digest waivers for v1.2.1. Continue to prefer upstream fixes;
  no custom downstream builds are introduced. The approval covers 115
  image/finding pairs across eight images and expires on 2026-11-06; raw scan
  findings remain disclosed, and newly detected findings remain blocking.

- Refresh the authenticated Flux distribution to v2.9.6 and Trivy Operator to
  0.35.0 with scanner 0.75.0. Vulnerability database updates remain automatic;
  scanner reports refresh every six hours independently of software version pins.
- Use RustFS for disposable Velero backup/restore validation and the first-party
  S3 client for bucket creation and backup object verification.
- Accept delivery bundles with no dependencies or capability requirements by
  persisting empty JSON arrays, matching the database contract.

### Release qualification

Publication of v1.2.1 requires automated builds, vulnerability scans, retained license findings,
SBOMs and signing, clean installs on Kubernetes 1.33, 1.34 and 1.35, and a signed
v1.1.0-to-v1.2.1 upgrade, backup/decryption and clean-restore rehearsal.
Cloud-provider acceptance, production-scale certification, the Rancher comparison
benchmark and human assistive-technology certification are deferred. These
certifications are not claimed for this release. License qualification is also
deferred: unresolved license findings remain in the signed runtime-image report
and retained SBOMs for review. No clean license qualification is claimed.
Unwaived vulnerability findings remain blocking. The v1.2.1 publication policy
does not require a separately named release approver; this exception is scoped
to this tag and does not relax the policy for subsequent releases.

### Upgrade notes from 1.1.0

This release includes intentional API migration changes despite retaining the
1.2.1 product version. Existing integrations must be reviewed before upgrading;
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
