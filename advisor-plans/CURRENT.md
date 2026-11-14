# Current working plan — Astronomer

Updated 2026-11-14. **This is the only active implementation and qualification
plan.** All numbered plans, parallel ledgers and previous execution instructions
are archived. Do not execute an archived checklist independently or treat an old
pass as qualification of a new candidate.

## Outcome and non-negotiable boundaries

Bring the current implementation and documentation onto GitHub main, then finish
the remaining product integration and release qualification below. Merging source
is not a deployment, a completed feature, or production/GA approval.

- Local password login, MFA, recovery and API tokens always remain available.
  Optional SSO uses shared AlphaBravo Identity or customer OIDC. No SSO-only mode.
- Dex is retired. Do not resume the cancelled connector, renderer, registration
  or worker-hardening work. Existing broker workloads need an owned live cleanup
  after the verified cutover; source deletion alone is not that proof.
- Astronomer adopts clusters. Flux is the delivery engine; CNPG/PostgreSQL owns
  durable intent. Preserve databases, keys, volumes and unrelated workloads.
- Authorization, tenant scope, mandatory audit, signature verification and
  secret protection fail closed. Never weaken capacity or qualification guards.
- Keep the old ui-demo cluster until its supported replacement passes acceptance.
  Physical interoperability, hardware purchases and destructive recovery campaigns
  need a concrete authorized scope; this source merge does not authorize them.
- Required missing evidence stays OPEN/BLOCKED. Historical synthetic/component
  proofs cannot close actual product, independent-estate or protected release gates.

## Current implementation baseline

The consolidation starts at primary `origin/main`
`040870752fc1635ecbb08d64f9e4b71538a061f4`, repository
`alphabravo-oss/astronomer`. `company` is a legacy remote, not the publication
target. The older planning checkout is documentation input only; its old source
must not overwrite current main. Source and test changes come from the reconciled
Identity worktree, including the retained runtime fixes.

Implementation committed as `12fcad7f9d`: optional shared RP/browser login and
explicit account bindings; Dex source/chart/CLI/worker removal; migrations 86–88;
encrypted scoped provisioning storage, registration locking and an audited
durable recovery journal; authentication/idempotency/backup/standby-health fixes;
catalog status projection, configuration path handling and editor sanitization.
These source implementations are not claims of deployed acceptance.

Candidate schema: **88**. Migration 87 is the destructive broker-retirement
boundary; 88 adds the provisioning journal. Shared library pin: Identity v0.1.0.
Do not deploy before reviewing the [cutover runbook](../docs/runbooks/shared-identity-cutover.md).

The catalog assurance branch is already included in catalog `origin/main`
`856f3356e811f63dd3514cb3ac950cbe36189891` (PR #1, branch tip `d02c4e1`).
Signed catalog publication, ingestion and live qualification remain open.
The application builder is now pinned to Go **1.27.4** with the matching
multi-platform image digest; fresh vulnerability results belong to this source
candidate and do not qualify existing deployed images.

## Execution order and completion ledger

Every checkbox needs an exact source/artifact identity, check result and retained
evidence before closure. Where code already exists, verify and close the residual;
do not rebuild it merely because an old document has unchecked boxes.

### S0 — Consolidate and publish the current source (current request)

- [x] Inventory worktrees/remotes and privately snapshot the three dirty checkouts.
  All non-evidence changes in the older fixes checkout are present in the Identity
  tree; differences are Identity adoption, generated dependencies or formatting.
- [x] Preserve old plans and evidence under `archive/2026-11-14/`, carry remaining
  requirements into this document and record source dispositions in the archive.
- [x] Validate the consolidated tree: Go lint/vet/format/build/race suite,
  real Identity/PostgreSQL/Redis tests, chart checks, frontend checks/build and
  applicable security/generated-contract checks. The [verification receipt](evidence/2026-11-14-consolidation/README.md) records 15,568 Go tests (7 explicit skips), 2,408 frontend tests, real integration results and limitations.
- [ ] Commit implementation plus planning consolidation, fast-forward primary
  GitHub main without force, and verify the exact remote commit and contents.
- [ ] Bring the primary local checkout onto published main while preserving old
  branch/worktree snapshots. No live release is implied by this source operation.

### S1 — Finish optional Identity and catalog provisioning (P0)

- [x] Shared RP/PKCE/state and strict token verification; explicit issuer/subject
  bindings to existing local users; immediate local revocation checks.
- [x] Local login remains independent; old Dex source and shipped assets removed.
- [x] Provisioning foundations: encrypted immutable scoped Secrets, cross-process
  operation locking, and migration-88 journal with atomic audit and revision fencing.
- [ ] Extend and publish the shared library's scoped lookup/recovery, rotation
  and deletion APIs. Keep engine SDK coupling in the shared component. Preserve
  project grants and treat a lost create-only credential as recovery-required.
- [ ] Wire the worker to durable intent and shared provisioning. Commit claim
  before upstream mutation; persist returned secrets before readiness; handle
  timeouts, worker death, lost locks and ambiguous results without blind creation.
- [ ] Add authorized project/cluster APIs, bounded configuration and private
  administration credentials. Recheck authority at execution; audit all mutations.
- [ ] Wire supported catalog products to exact issuer/audience/callback contracts
  and scoped per-product Secret delivery. Keep credentials out of Helm values,
  logs, ordinary API responses and audit detail. Customer OIDC remains RP-only
  when it has no shared management capabilities.
- [ ] Implement explicit rotation and retirement: stage generation, verify
  consumer rollout, revoke old upstream client, then clean owned credentials with
  UID/version guards. Preserve another installation's clients, projects and roles.
- [ ] Complete settings/capability integration and accessible admin/browser flows.
  Prove password+TOTP/recovery, API-token independence, SSO/unlink/revocation,
  wrong issuer/audience/tenant denial, outage fallback with audit/metrics/alerts,
  private CA and disconnected operation. Keep unsupported federation/group/global
  logout capabilities explicitly unsupported until actually implemented and tested.
- [ ] Qualify provider artifact signatures, HA, network policy, backup/key recovery,
  air-gap assets, branding/privacy and measured latency/RSS. Prove a second real
  supported product's audience and roles; component demo proof is insufficient.

### S2 — Qualify a candidate and perform the owned live cutover (P0)

- [ ] Produce one immutable signed candidate: source, image/chart/catalog digests,
  compatibility manifest, toolchain and transitive assets. Recheck current support
  versions and advisories; October version snapshots are historical.
- [ ] Prove backup/restore and local administrator access first; inventory old
  links and explicitly approve new subject mappings. Do not infer by email.
- [ ] Drain/stop old server/worker processes and retired broker queues before
  schema 87; apply schema 88 and the coherent candidate. Preserve a coordinated
  database/release rollback unit. No old binary may run against dropped tables.
- [ ] Verify and remove only retained broker resources whose ownership is proven.
  Prove routes, UI, worker tasks, workloads, Secrets/ConfigMaps and shipped images
  no longer expose Dex. Preserve unrelated identity deployments.
- [ ] Verify R01–R14 fixes on the deployed candidate: credential boundaries,
  scope-aware idempotency, encrypted optional trust restore, warm standby and
  negative leader-loss recovery, widget cancellation, authoritative application
  status, literal configuration keys, editor/CSP and reproducible test timing.
- [ ] Resolve Charlie enrollment/suspension and verify effective agent authority
  against the selected compatibility contract before unlatching execution.
- [ ] Retain local/SSO/outage/browser/API results. No full cutover is currently
  recorded; keep the original demo cluster until these acceptance gates pass.

### S3 — Tools, applications and independent-estate assurance (P1)

- [ ] Re-enumerate the complete supported offering/API/lifecycle matrix from the
  merged candidate. Retire Dex cases and replace them with Identity cases. The
  old 174-case denominator is historical, not a current remaining count.
- [ ] Resolve estate prerequisites: supported Kubernetes, owned disposable targets,
  immutable candidate, transport and independent-host evidence. ui-demo-v211 and
  site-demo previously reported the same physical system UUID; do not claim an
  independent two-member estate until that is resolved and measured again.
- [ ] Execute discovery, install, functional access, configuration, upgrade,
  rollback, restart/recovery and uninstall for every advertised offering. Test
  RBAC, denied operations and cluster/project scope; capture failures and cleanup.
- [ ] Finish monitoring/logging qualification and genuine hashed assurance reports;
  preserve distinction between Platform Tools, Applications, automatic components
  and external ownership. Installed does not mean functionally qualified.
- [ ] Complete catalog-driven version/default/removed-path UX, server-backed
  storage/Secret choices, validators, privileges/CRDs/capacity/image preflights and
  links to functional/qualification surfaces. Preserve explicit review of changes.
- [ ] Reconcile supporting catalog branches and signed publication; activate daily
  upstream discovery on its canonical default branch; publish operator/support
  documentation. Integrate testbed/IaC responsibilities without discarding harnesses.

### S4 — Security, supply chain and disconnected delivery (P0/P1)

- [ ] Finish W01/W02 current dependency/toolchain, strict security gate and waiver
  provenance review. Merge checks must fail on applicable findings; do not convert
  a missing signing/cloud/approval proof to a waiver or a synthetic pass.
- [ ] Finish W03/W04 runtime-consumed signed mirrored catalog and asset closure,
  offline vulnerability DB authenticity/freshness/rollback, updater authority,
  private registries/CA/proxies and a cold no-public-egress installation.
- [ ] Complete image-admission support claims for actual signature authorities,
  digest pins and upstream exceptions; test denial and recovery with the candidate.
- [ ] Prove encrypted backup/key rotation/history and clean restore/decrypt on
  the exact candidate, including optional catalog CA/signature trust and bounded
  ownership. Recovery sizing and free-space checks remain enforced.

### S5 — Data, observability, performance and architecture (P1/P2)

- [ ] Finish W07 repository-wide cursor/selector pagination and count certainty;
  preserve scope/filter binding, cancellation, progress and empty-later-page recovery.
- [ ] Complete W10 observability: supported collectors, traces/logs/metrics,
  telemetry health, missing-data/denied states, dashboards and actionable alerts.
- [ ] Run engineering 5/50-cluster then estate-100 measurements and real-agent
  assignment/resource-count tests. Record fixed hardware/digests, warmup,
  achieved throughput, errors, audit conservation, memory and reconnect behavior.
  Complete cache/informer/coalescing, notification and soak/recovery acceptance.
- [ ] Finish W11/W12 Helm/Flux/CNPG install-upgrade-rollback, current/previous
  database support, pooling/failover/physical historical-key restore, browser and
  report qualification against the current candidate.
- [ ] Continue W13 domain extraction with characterized boundaries and W14 typed
  upgrade/DR CLI production adapters/rehearsals. Do not replace Flux or introduce
  a second cluster-provisioning platform. Keep v2 work separate from v1 guarantees.
- [ ] Finish W15 catalog Python and supporting-repository packaging, provenance,
  supply-chain and public documentation consistency. Ordinary crypto is the
  selected scope; FIPS artifacts require an explicit product/support decision.

### S6 — UX and workflow residuals (P1/P2)

- [ ] Extend loading/403/404/offline/stale, partial/cancelled bulk operations,
  denied relationship continuation and desktop/mobile tests across workflows.
  Preserve already-completed navigation/design-system work; no wholesale redesign.
- [ ] Audit remaining capped selectors and truthful empty/count states. Add a
  backend image-filter contract before claiming cross-cluster image presence.
- [ ] Decide authorized entity lookup without project context, effective-privilege
  probing/freshness/refusal semantics, and questions.yaml/chart ingestion before
  advertising these capabilities. Exhaustive Kubernetes relationship graphs and
  demand-only Charlie P2 packs are optional scope, not implied release blockers.
- [ ] Revalidate regression coverage for plans 018–025/027/031 against the merged
  product; historical un-ticked acceptance lists do not authorize rebuilding
  those already-completed primitives, navigation or form migrations.

### S7 — External release/GA evidence (release-blocking)

- [ ] Credentialed signed adopted-cloud acceptance: EKS, GKE, AKS and DOKS.
- [ ] Certified mandatory-audit throughput, conservation, outage/drain and backlog.
- [ ] Comparable retained measurements for production sizing recommendations.
- [ ] Paired cold/warm Rancher comparison plus preregistered counterbalanced human
  clarity/error-recovery study; no unmeasured performance/clarity claims.
- [ ] Exact-candidate accessibility: NVDA/Chrome, Narrator/Edge,
  VoiceOver/Safari and critical iOS VoiceOver workflows.
- [ ] Retained scale certification for every advertised rung, including estate-100.
- [ ] Exact-candidate backup, clean restore, upgrade, rollback and application decrypt.
- [ ] Protected release-engineering approval bound to the exact tag, commit,
  source run, artifacts and all cloud/scale/audit/DR/benchmark/accessibility proofs.
- [ ] Only if physical interoperability is claimed: obtain signed six-host inventory
  and concrete destructive authorization, then three repetitions of each single-
  and three-server K3s/RKE2 topology (12 runs), with independent Constellation
  observations, signed evidence and owned cleanup/reboot proof. Otherwise keep
  the claim explicitly out of scope.

## Historical source disposition and verification rules

All previous plan files are under [the archive](archive/2026-11-14/). Their
relative links have been relocated; original-source digests and private snapshots
preserve provenance. The previous plan index and 000–015 closeout ledger are
historical records, not additional current plans.

Completed scopes remain closed: Vite/TanStack migration, Flux-only architecture,
audit tombstone/backfill invariants, approved Charlie P1, navigation/workflow
primitives, and scoped W00/W05/W06/W08/W09/W16 implementation. Their current
candidate acceptance is covered above, not assumed from older passes.

The detailed carry-forward ledger below retains open acceptance language from
the most recent source plans. It is part of this plan, not a second backlog.
Overlapping statements are acceptance detail for S1–S7, not independent counts.
An unchecked historical task that may already be implemented means **verify the
current implementation and retain evidence**, not automatically implement again.
Cancelled Dex-building tasks and old publication instructions are superseded by
the boundaries and S0 above. Future status updates belong here only.
Source identifiers below preserve traceability to archived specifications;
they do not reactivate those plans. Current corrections replace obsolete counts,
paths and publication instructions. The inventory retains each original wording
and maps it to its unique current identifier.

## Detailed carry-forward acceptance ledger

### From 016-production-ga-closure-master-plan

[Archived specification](archive/2026-11-14/016-production-ga-closure-master-plan.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **016-CF001** — The intended source is committed and reproducible from a clean checkout; the exact verified commit produced the deployed digest-pinned artifacts.
- [ ] **016-CF002** — Static, race, PostgreSQL, browser, Helm, generated-contract, and applicable stateful gates pass on that exact commit with zero hidden skips.
- [ ] **016-CF003** — Default-deny networking, DR authority, authenticated immutable backups, safe air-gap input handling, immutable image inputs, and supply-chain gates pass.
- [ ] **016-CF004** — Estate-scale UI truth, action search, cancellation, accessibility, Playwright, Node/toolchain, and ADR criteria pass.
- [ ] **016-CF005** — Estate-100 and every advertised higher scale rung have retained evidence; telemetry correlates API through agent.
- [ ] **016-CF006** — All eight Phase 6 external qualifications pass and protected release approval binds the exact artifacts.
- [ ] **016-CF007** — Documentation makes only evidence-backed production, scale, accessibility, Rancher-comparison, cloud, DR, and compliance claims.
- [ ] **016-CF008** — If physical interoperability is claimed, all 12 Phase 7 runs and cleanup gates pass on exact Astronomer/Constellation candidates.
- [ ] **016-CF009** — Record S7 completion and its retained release evidence here only after all applicable release criteria pass. Keep the archive historical and README pointing to this plan.

### From 028-all-offerings-api-qualification

[Archived specification](archive/2026-11-14/028-all-offerings-api-qualification.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **028-CF001** — Inventory matches the frozen deployed APIs, UI offers, catalog and static registries with no omitted names, variants or lifecycle flags.
- [ ] **028-CF002** — Every required case has a named functional oracle, exact API sequence, owner and fresh PASS evidence; unsupported claims have verified explicit contracts.
- [ ] **028-CF003** — All supported install/update/upgrade/rollback/adopt/remove/recovery paths pass without workaround, and each offering performs its promised job.
- [ ] **028-CF004** — Real provider and tenant-isolation cases, persistent operation readback and cleanup pass; environment-blocked cases remain visibly unqualified until completed.
- [ ] **028-CF005** — Prior known failure paths have been rerun, including pipeline PUT, interrupted upgrades, native backend routing and restore tracking.
- [ ] **028-CF006** — Resolve UI/API disagreement and advertised-but-unsupported actions under S3/S6; retain them in the inventory until resolved.
- [ ] **028-CF007** — Machine coverage verification fails on any newly offered item without a test. CI retains per-build results, and S7 release evidence references them.
- [ ] **028-CF008** — Close the S3 offering qualification only after all above are satisfied. Until then publish the full current denominator and PASS/FAIL/BLOCKED/NOT_RUN counts, not “all extensions work.”

### From 029-two-cluster-offering-install-and-functional-validation

[Archived specification](archive/2026-11-14/029-two-cluster-offering-install-and-functional-validation.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **029-CF001** — The frozen runtime inventory contains every exposed application, Tool, provider, preset, transport, extension and Charlie capability with no unmatched entry.
- [ ] **029-CF002** — Two independent disposable member clusters pass API estate preflight.
- [ ] **029-CF003** — Every base row in the regenerated candidate inventory and every runtime-expanded variant has a terminal result. The former 174-row inventory is historical; remove Dex cases and add supported Identity cases before freezing the current denominator.
- [ ] **029-CF004** — Every required result is PASS; no FAIL, BLOCKED or NOT_RUN remains.
- [ ] **029-CF005** — NOT_SUPPORTED appears only where an explicit product contract says so and its truthful rejection was tested.
- [ ] **029-CF006** — Lifecycle, canary, authorization, recovery, durable readback and cleanup dimensions pass independently for each applicable entrypoint.
- [ ] **029-CF007** — Both disposable member clusters are clean after each ownership-conflicting wave; remove campaign-owned disposable resources after acceptance. Keep the original ui-demo until separately approved migration/retirement, and preserve management and unrelated resources.
- [ ] **029-CF008** — Evidence contains immutable candidate/catalog/chart/image identities and no secrets.
- [ ] **029-CF009** — `go test ./scripts/qualify-offerings/... -count=1`, offline false-green tests, real browser qualification and the candidate's required source/release gates pass on the final source. The historical `verify-enterprise` target is absent; map its required acceptance to current gates without inventing a successful run.

### From 030-efficiency-and-operational-readiness

[Archived specification](archive/2026-11-14/030-efficiency-and-operational-readiness.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **030-CF001** — Record a clean source revision and compare agent, telemetry, handler, Charlie, frontend, chart and load-test changes against the actual deployed baseline. `f87b189f` is a historical comparison, not the assumed current deployment.
- [ ] **030-CF002** — Re-read the source anchors and relevant active-plan status. Record changes and remove tasks already completed; retain outstanding acceptance gaps.
- [ ] **030-CF003** — Read live pod/deployment state, image digests, release revision, readiness, connected cluster count, node utilization, volume usage, and protected config references. Never dump Secrets or unrestricted Helm values to a public log.
- [ ] **030-CF004** — Identify the actual source revision of each deployed component. Do not assume the frontend, agent, server and worker share one build merely because the chart version matches.
- [ ] **030-CF005** — Create sanitized evidence under `advisor-plans/evidence/<run-id>/` and link it here; keep credential files outside tracked paths with restrictive permissions.
- [ ] **030-CF006** — Record target environment, expected ownership, approved mutations and recovery route for later live work. This plan's historical access does not guarantee access in a future sandbox.
- [ ] **030-CF007** — Inventory existing metrics before adding names: HTTP duration/errors, DB pool wait/saturation, queue depth/oldest age, audit/task outbox outcomes, tunnel connectivity, event relay lag, heap/goroutines and resource-search outcomes.
- [ ] **030-CF008** — Enable application ServiceMonitors in the intended environment; match Prometheus selectors and allowed network paths. Keep base chart usable without Prometheus CRDs.
- [ ] **030-CF009** — Add missing bounded-cardinality metrics for inventory refreshes, Kubernetes LIST/GET/WATCH traffic, cache freshness/sync state, reconnects and observation work duration. Reuse `internal/observability/resource_search_metrics.go` registration/label conventions. No resource names, URLs, tokens or unbounded user IDs in labels.
- [ ] **030-CF010** — Aggregate agent measurements through an existing supported telemetry path where possible. Never expose a new public unauthenticated metrics endpoint just to collect per-agent data.
- [ ] **030-CF011** — Build a dashboard covering API latency, queues/outboxes, DB waits, agent state freshness, downstream API load and resource consumption. Display scrape absence as missing data, not zero demand.
- [ ] **030-CF012** — Make alerts distinguish intentional Flux standby from loss of the active artifact-serving controller. Investigate k3s-specific missing scheduler/controller-manager targets; adjust rules to actual observable components rather than globally suppressing alerts.
- [ ] **030-CF013** — Capture 30 minutes of idle baseline plus representative interactive operations. Record scrape gaps and cold-start intervals separately.
- [ ] **030-CF014** — Add regression fixtures matching the bundled Kustomization and HelmRelease CRDs: map entries with `id` and `v`, four identity fields, empty core group, and empty namespace for cluster-scoped resources.
- [ ] **030-CF015** — First demonstrate the real-schema regression fails on the current decoder.
- [ ] **030-CF016** — Decode fields into API version, kind, namespace and name. Validate types, malformed IDs, empty/invalid version, mixed valid/invalid entries and the existing maximum resource bound.
- [ ] **030-CF017** — Preserve documented total-count semantics, sanitization and readiness behavior; do not infer individual resource health from a count.
- [ ] **030-CF018** — Verify frontend/API inventory consumers receive correct identities. Remove the fabricated schema fixture rather than supporting it as an undocumented compatibility contract.
- [ ] **030-CF019** — Trace Grafana proxy image selection from intended candidate artifact through generated Helm values to deployed pod. Determine whether stale desired config, missing distribution, or both caused the unavailable digest.
- [ ] **030-CF020** — Use a digest-pinned image available to every target node. Local imports can remain a documented development mechanism, but must cover all scheduled nodes and survive the intended recreation test; production requires durable artifact distribution.
- [ ] **030-CF021** — Trace Charlie pull-secret references, registry audience/scope, credential expiry/refresh, and activation reconciliation. A 401 proves authorization failure, not its precise cause. Repair the supported credential/configuration lifecycle without weakening registry authentication.
- [ ] **030-CF022** — Add focused tests for image propagation and pull-secret references; keep credential material redacted in failures and audit output.
- [ ] **030-CF023** — Exercise the supported Grafana proxy route with authenticated users and denied users. Exercise a real Charlie request and reconnect; pod readiness alone is insufficient.
- [ ] **030-CF024** — Demonstrate safe pod recreation in the test environment can pull the intended images without relying on an accidental old node cache.
- [ ] **030-CF025** — Reuse the existing harness; do not replace it with raw authenticated loops or direct DB fixture insertion. It already provisions synthetic agents, tracks audit outcomes and exports JSON/checksums.
- [ ] **030-CF026** — Run engineering tiers of 5 and 50 synthetic clusters first, then the existing estate-100 profile when infrastructure permits. Higher tiers are optional capacity exploration until lower tiers pass.
- [ ] **030-CF027** — Separately test real agents on at least two adopted members, with 1/10/100 delivery assignments and increasing actual Kubernetes resource counts. Synthetic tunnel responses cannot validate informer memory, LIST reduction or Flux behavior.
- [ ] **030-CF028** — Fix hardware, component replicas, image digests, chart values, dataset, workload mix and rate limits for each before/after comparison. Record cold start separately; use 5-minute warmup and at least 30-minute measured windows for optimization comparisons, three repetitions where practical.
- [ ] **030-CF029** — Include idle estate, active resource churn, inventory browsing, scoped search, delivery status, multiple panels, two browser tabs, and reconnection bursts.
- [ ] **030-CF030** — Record throughput achieved as well as requested; skipped work, stale data and HTTP errors cannot count as efficiency improvements.
- [ ] **030-CF031** — Add a report reducer/test for proposed cache metrics and relative before/after criteria if the current report cannot express them. Record all mandatory drills as NOT_RUN until genuinely executed.
- [ ] **030-CF032** — Map existing informer ownership, stores, typed fields, metadata-only sources, sync readiness and privilege restrictions. Reuse stores capable of answering the inventory question; identify exact missing fields before adding watchers.
- [ ] **030-CF033** — Define a narrow injected read-only snapshot interface in a package that avoids an agent/delivery import cycle. Follow the existing `ControllerProbe` injection pattern. Include per-kind availability, sync/freshness information and version/generation where required.
- [ ] **030-CF034** — Wire local embedded and remote agents to the same abstraction, scoped to their actual credentials. Do not broaden RBAC to make caching convenient.
- [ ] **030-CF035** — Replace repeated whole-cluster workload/PVC lists with snapshot reads. Add scoped watchers only for missing data; strip unused fields to bound memory. Do not maintain a second full-object copy if a safe immutable projection suffices.
- [ ] **030-CF036** — Coalesce concurrent refreshes. Give discovery and compatibility checks a bounded slower refresh policy, with explicit refresh on relevant CRD/controller changes.
- [ ] **030-CF037** — Distinguish “absent”, “not authorized”, “not synced”, “disconnected” and “healthy”. A cache read must not manufacture a new observation timestamp for old data.
- [ ] **030-CF038** — Handle watch expiration/410, relist, object deletion, absent-then-installed CRDs, privilege changes, startup cancellation and shutdown. Informer resync is not a guarantee of fresh server data; handle watch/relist health explicitly.
- [ ] **030-CF039** — Use existing transport cancellation/backpressure patterns. Never block informer callbacks on a disconnected tunnel or let pending changes grow without bound.
- [ ] **030-CF040** — Reuse or add narrowly scoped Flux source/reconciler observations keyed by GVR, namespace and name. Choose namespace/project coverage according to actual permissions and measured watcher count.
- [ ] **030-CF041** — Replace two direct GETs per assignment per status tick where a synchronized snapshot can answer correctly. Retain bounded, explicit fallback for missing capabilities; do not launch one informer per assignment.
- [ ] **030-CF042** — Track dirty assignments on relevant changes, coalesce rapid events, and retain periodic freshness/status resync. Preserve acknowledged checkpoint generation and removal/tombstone semantics.
- [ ] **030-CF043** — Bound queues and response work; reconnect storms use jitter and concurrency limits. An overloaded or disconnected cluster cannot monopolize management-plane workers.
- [ ] **030-CF044** — Preserve failure states, status normalization, agent mixed-version compatibility and continuous Flux ownership. No dropped mutation/audit intent is acceptable.
- [ ] **030-CF045** — Capture request counts, payload bytes, rendering time and memory for cluster list, resource list, delivery inventory and scoped search, using the same data as E04.
- [ ] **030-CF046** — Audit existing `liveFallback`, stream reconnection invalidation and paced dispatch before editing. Fix demonstrated duplicate requests or missing invalidations; do not replace live transport wholesale.
- [ ] **030-CF047** — Share canonical query keys across panels; retain tenant/cluster/namespace/filter dimensions. Ensure cancellation on scope change and no cache leakage after logout, permission revocation or cluster switching.
- [ ] **030-CF048** — Verify inactive views and background tabs do not perform unnecessary polling while healthy streaming is available. If cross-tab coordination is justified by measured cost, specify auth/session isolation and leader failover before implementing it; otherwise keep it deferred.
- [ ] **030-CF049** — Preserve server-side paging, totals and search semantics. Apply existing TanStack virtualization only to measured large-table render bottlenecks; retain keyboard accessibility, selection and scroll restoration.
- [ ] **030-CF050** — Test stale/reconnecting indicators, disconnected stream fallback, rapid route changes, two panels sharing one query and denied users.
- [ ] **030-CF051** — Review DB pool wait, slow-query evidence, queue age, handler duration, tunnel fan-out, goroutine/FD growth and audit/task delivery before altering settings.
- [ ] **030-CF052** — For each hot spot, record exact files/symbols, measured cause, bounded change and test. Prefer batched reads, bounded fan-out, existing cache invalidation and suitable query plans over extra replicas by default.
- [ ] **030-CF053** — Evaluate worker concurrency against database capacity and downstream limits. Increasing concurrency must not amplify overload or starve latency-sensitive tasks.
- [ ] **030-CF054** — Preserve periodic-task leadership, idempotency, transactional outboxes and terminal failure semantics. Separate interactive and background budgets using existing mechanisms where available.
- [ ] **030-CF055** — Any SQL change goes through query sources/sqlc generation and migration safety gates. Do not add speculative indexes or a second database/cache.
- [ ] **030-CF056** — Map exporter release ownership, ServiceMonitors, dashboard labels, baseline dependencies and consumers. Pick one supported owner per intended exporter role.
- [ ] **030-CF057** — Update owner intent before removing redundant releases so Flux/product reconciliation does not recreate them. Confirm metric names/labels and queries remain valid; retain reinstall/rollback details.
- [ ] **030-CF058** — Measure CPU distributions, RSS/working-set peaks, throttling, startup, reconciliation bursts, connection pools and memory after soak. Separate management components from adopted-cluster agents and observability overhead.
- [ ] **030-CF059** — Propose dev/small and production profiles with explicit dataset/cluster/RPS envelope and headroom. Do not globally replace HA production defaults with single-node dev measurements.
- [ ] **030-CF060** — Reduce requests incrementally in the test deployment and rerun load. Choose limits with burst behavior in mind; CPU limits can cause throttling despite spare node capacity.
- [ ] **030-CF061** — Treat source-controller standby as intentional. Multiple replicas on one host do not establish host-loss resilience.
- [ ] **030-CF062** — Recheck the existing `charlie-dev-rustfs` service and determine an endpoint reachable from backup/drill Pods; the Compose hostname `rustfs` is not assumed resolvable from k3s. Use a dedicated Astronomer test bucket. Verify required S3, versioning and Object Lock behavior against the actual deployed version before configuring backups. Preserve Charlie buckets and data.
- [ ] **030-CF063** — Establish dedicated credential Secret references, retention, encryption/wrapping-key custody and source identity using the existing supported configuration. Record achieved backup age and restore duration; proposed dev targets remain RPO ≤24 hours and RTO ≤2 hours. Off-host storage and production RPO/RTO qualification are deferred.
- [ ] **030-CF064** — Configure through the supported management/chart owner. Keep writer and retention/delete credentials separate; include encryption/signing material recovery as required by the existing design.
- [ ] **030-CF065** — Run a backup and verify authenticated manifest, objects, encryption and completion evidence. Alert on missing/old backup success and failed restore drills.
- [ ] **030-CF066** — Restore into an isolated database/environment with no production endpoints or external notification side effects. Verify schema, expected fixture records, credential decryptability, authentication and representative reads.
- [ ] **030-CF067** — Record elapsed restore time and backup age. Verify the drill cannot overwrite the source database, and clean up only its own resources.
- [ ] **030-CF068** — Document the development restore procedure and its same-host limitation. Track independent storage and recovery when the management host is lost as deferred production work.
- [ ] **030-CF069** — Build an internally consistent candidate and retain source/image/schema/config identities. Use the existing release/deployment workflow; inspect rendered changes before applying.
- [ ] **030-CF070** — Deploy to a test estate, then a bounded canary. Preserve previous artifacts and configuration for rollback; account separately for any irreversible schema compatibility change.
- [ ] **030-CF071** — Exercise agent disconnect/reconnect, watch expiry, event bursts, slow/unreachable member, server and worker restarts, DB/Redis interruption in isolated fixtures, denied RBAC, stale reads and token rotation/revocation.
- [ ] **030-CF072** — Prove one failing member does not block unrelated cluster reads. Check explicit partial results, deadlines, event recovery and conserved audit/operation intent.
- [ ] **030-CF073** — Run 30-minute comparison windows and a four-hour steady/churn soak. Run real host/etcd/storage failover only on the native multi-node estate and under its existing authorization; K3d on one host is not independent failure-domain proof.
- [ ] **030-CF074** — Verify the user journeys: login, cluster list, resource browsing, workload status, Flux resource inventory, Grafana proxy, Charlie request, backup status and restore evidence.
- [ ] **030-CF075** — Promote only within authorized scope. Roll back on new sustained 5xx, missing audit intent, growing queues, freshness breach, OOM, lost authorization isolation or failed critical integration.
- [ ] **030-CF076** — Publish supported capacity, remaining unknowns, before/after measurements and exact evidence. Feed qualifying artifacts into S7; engineering measurements alone do not establish GA.
- [ ] **030-CF077** — E00 source/live/plan reconciliation complete
- [ ] **030-CF078** — E01 application telemetry and baseline complete
- [ ] **030-CF079** — E02 real Flux schema regression fixed
- [ ] **030-CF080** — E03 image lifecycle and functional integrations proven
- [ ] **030-CF081** — E04 reproducible before benchmark retained
- [ ] **030-CF082** — E05 inventory shared with existing observation infrastructure
- [ ] **030-CF083** — E06 assignment reads and status reporting bounded
- [ ] **030-CF084** — E07 measured frontend issues resolved or explicitly ruled out
- [ ] **030-CF085** — E08 DB/worker tuning justified or closed with no-change evidence
- [ ] **030-CF086** — E09 exporter ownership and sizing validated
- [ ] **030-CF087** — E10 dedicated RustFS development backup and isolated restore proven; off-host production qualification tracked separately
- [ ] **030-CF088** — E11 comparative run, soak, canary and authorized rollout complete
- [ ] **030-CF089** — Applicable source, race, frontend, Helm and Local CI gates pass
- [ ] **030-CF090** — Reconcile S3/S5/S6/S7 evidence and status here without duplicate claims or reopening archived execution ledgers.

### From 030-rancher-tool-configuration-ux

[Archived specification](archive/2026-11-14/030-rancher-tool-configuration-ux.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **030-UX-CF001** — Populate cluster-backed choices such as StorageClass and existing Secret through read-only public APIs, with free text retained for GitOps-created resources.
- [ ] **030-UX-CF002** — Add dedicated validators for Kubernetes quantities, durations, cron, hostname, CIDR and secret references.
- [ ] **030-UX-CF003** — Show additions/removals caused by the chart version separately from operator edits.
- [ ] **030-UX-CF004** — Warn when an old value path is absent from the target chart while preserving the user's ability to review and remove it.
- [ ] **030-UX-CF005** — Show a target-version default diff and removed-path warnings for catalog App upgrades.
- [ ] **030-UX-CF006** — Add remaining privilege, CRD ownership, node/storage capacity, image reachability and external-fixture preflight checks.
- [ ] **030-UX-CF007** — Link a completed install to its functional surface and last qualification/canary state.

### From 031-ui-refinement-and-design-system-depth

[Archived specification](archive/2026-11-14/031-ui-refinement-and-design-system-depth.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **031-CF001** — P10.3 Image presence across clusters (blocked: backend has no image filter; see after.md)

### From 032-technology-guide-alignment-and-latest-stable-master-plan

[Archived specification](archive/2026-11-14/032-technology-guide-alignment-and-latest-stable-master-plan.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **032-CF001** — New release candidates use the latest stable selected runtimes and tools, with upstream release discovery and qualification recorded.
- [ ] **032-CF002** — CNPG remains the production PostgreSQL manager; supported external PostgreSQL remains an explicit operator choice.
- [ ] **032-CF003** — Existing CNPG databases, PVCs, keys, credentials, and backup histories survive ordinary application upgrades.
- [ ] **032-CF004** — A fresh, uncached disconnected installation can adopt a cluster, install built-ins, scan workloads, and complete selected catalog operations without public egress.
- [ ] **032-CF005** — Authentication, tenant authorization, audit, durable operations, and assignment fencing retain their current security guarantees.
- [ ] **032-CF006** — Delivery enum values and generated consumers agree with database constraints.
- [ ] **032-CF007** — Every supported list makes progress or reports an explicit bounded compatibility error.
- [ ] **032-CF008** — Security findings have enforced gates, review ownership, and scoped risk acceptance.
- [ ] **032-CF009** — Supported-browser and database-version claims have retained real evidence.
- [ ] **032-CF010** — Domain extraction and orchestration consolidation preserve behavior and have characterization coverage.
- [ ] **032-CF011** — Release qualification scope is honest and machine checked; existing release exceptions remain accurately recorded.
- [ ] **032-CF012** — Every work package has a source-bound evidence record and a reviewed completion verdict.
- [ ] **032-CF013** — Create one isolated branch/worktree per bounded package or compatible PR slice.
- [ ] **032-CF014** — Record base commit, owning repository, source hash, dependencies, owner, and evidence location.
- [ ] **032-CF015** — Reconcile overlapping acceptance requirements here before implementation; do not duplicate completed changes or reopen archived plans.
- [ ] **032-CF016** — Re-run applicable verification after rebases or dependency merges.
- [ ] **032-CF017** — Keep generated output changes paired with canonical source changes.
- [ ] **032-CF018** — Merge dependent slices in dependency order.
- [ ] **032-CF019** — Remove worktrees only after preserving needed uncommitted work and verifying merged state.
- [ ] **032-CF020** — Avoid accumulating abandoned validation branches or silently replacing another executor's changes.
- [ ] **032-CF021** — Map each new task to an existing residual, an independently completed outcome, or genuinely new work.
- [ ] **032-CF022** — Mark an already-satisfied task complete only with current code and applicable evidence.
- [ ] **032-CF023** — Link historical evidence as historical; never attribute it to a changed candidate.
- [ ] **032-CF024** — Update this plan with current execution status and evidence; keep README as the single-plan pointer and preserve historical results.
- [ ] **032-CF025** — Retain `postgres.mode: cloudNativePG` in the production profile.
- [ ] **032-CF026** — Retain `deploy/chart/templates/postgres-cnpg.yaml` and scheduled-backup semantics.
- [ ] **032-CF027** — Keep the CNPG operator ownership boundary explicit; do not install, upgrade, or uninstall an existing shared operator as an incidental application action.
- [ ] **032-CF028** — Retain externally managed Barman Cloud plugin/ObjectStore credentials and configuration.
- [ ] **032-CF029** — Preserve cluster name, storage class, PVC identity, WAL storage, instance count, synchronous-replication policy, service names, and application DSNs unless a reviewed migration explicitly changes them.
- [ ] **032-CF030** — Preserve database users, Secret references, certificate trust, connection limits, pool sizing, monitoring, backups, and recovery configuration.
- [ ] **032-CF031** — Keep CNPG operator upgrades distinct from PostgreSQL server-major upgrades and Astronomer schema migrations.
- [ ] **032-CF032** — Keep logical management backups and encryption-key recovery; they are not replaced by an untested CNPG/PITR configuration.
- [ ] **032-CF033** — Keep clean-restore and decryption rehearsals after database/image/migration changes.
- [ ] **032-CF034** — Refuse major PostgreSQL upgrades by changing `imageName` on an existing data directory without the supported CNPG procedure.
- [ ] **032-CF035** — Treat a restored CNPG cluster as a new controlled recovery target; never enable recovery bootstrap against an initialized live cluster.
- [ ] **032-CF036** — Rehearse primary failover and application reconnection with real CNPG when updating its operator, plugins, server major, or connection model.
- [ ] **032-CF037** — Record qualified CNPG/PostgreSQL/plugin/extension combinations, including airgap images.
- [ ] **032-CF038** — W01.5 Build all first-party Go tools/binaries, including migrator/key rotation/DR/shell helpers omitted by a simplistic server-only inventory. Test reachable vulnerabilities using the actual build toolchain. Build portion PASS on clean `54763ab3`: all 28 discovered command packages, 58 actual steps and 223 default capacity checks; exact source and VCS metadata independently verified. Historical build record is absent at its original path; retain fresh build evidence for this candidate. Fresh reachable-vulnerability checking remains OPEN, so this combined task stays unchecked.
- [ ] **032-CF039** — W01.10 Expand automatic update discovery to Helm, manual CLIs, CNPG, Flux, Trivy, built-in apps, Charlie compatibility, dev stack, and supporting repositories. Use custom manager/resolver where Dependabot does not cover an ecosystem.
- [ ] **032-CF040** — W01.13 For each lagging component, either qualify the current stable target or record the specific compatibility blocker. Do not silently preserve the old major as policy.
- [ ] **032-CF041** — W01.14 Add freshness dashboard/report data: selected/upstream version, discovery age, exception age, owner, deadline, and evidence. Fail release preflight if the review is missing/stale; offline release preflight validates the signed resolution record instead of contacting upstream.
- [ ] **032-CF042** — W02.1 Capture current gosec results using the selected toolchain. Triage HIGH/MED findings by code path and rule; separate true defects, inaccessible/test only code, and documented standard platform behavior.
- [ ] **032-CF043** — W02.4 Keep full-history redacted Gitleaks, safe checkout credentials, action digest pins, and reviewable allowlists. Test reverted-secret detection with synthetic nonsecret fixture values.
- [ ] **032-CF044** — W02.10 Validate ruleset/check identity against actual emitted workflow checks, avoiding merge queues or skipped jobs that make required checks impossible. Retain a machine-readable readback of applied settings.
- [ ] **032-CF045** — W03.4 Build a closed asset inventory from declared supported chart values/presets and actual renders. Include init containers, sidecars, hooks, operators, jobs, enabled optional profiles, and generated downstream workloads.
- [ ] **032-CF046** — W03.11 Include chart values-profile and image inventory digests in the signed catalog BOM. Require complete export inventory; preserve operator overrides through an explicit unqualified/needs-requalification state.
- [ ] **032-CF047** — W03.12 Reuse hardened release kit extraction and offline Sigstore verification patterns. Do not invent a second weaker verifier or fetch trust keys during verification.
- [ ] **032-CF048** — W03.14 Keep S3 configuration endpoint overrides and discovery explicit; distinguish internal data endpoint from externally reachable presigning endpoint where browser links are used. Qualify RustFS operations, not just an HTTP 200 health check.
- [ ] **032-CF049** — W04.1 Define DB assets: main Trivy DB, Java DB when required, media version/schema, upstream revision/digest, downloaded/updated times, expected next update, authenticity metadata, and allowed maximum age.
- [ ] **032-CF050** — W04.2 Configure internal DB repository locations through canonical typed configuration and signed profiles; preserve public GHCR only for explicitly connected mode.
- [ ] **032-CF051** — W04.3 Implement connected mirror refresh and disconnected export/import using latest published stable DB formats compatible with the selected scanner. Use verification/attestation controls appropriate to upstream DB artifacts; do not invent nonexistent upstream signatures.
- [ ] **032-CF052** — W04.4 Record which DB the scan actually used. Prefer upstream report/metadata or a measured scan job integration; report unknown when unavailable rather than derive a fictitious freshness age from scan completion time.
- [ ] **032-CF053** — W04.5 Keep automatic updates enabled against configured internal mirrors. A read-only immutable offline bundle supplies an initial snapshot plus an operator update procedure.
- [ ] **032-CF054** — W04.6 Define initial freshness defaults: report TTL remains 6h unless measured reason changes it; DB refresh follows upstream publishing; choose and document a maximum DB age after studying feed cadence. Unknown/expired DB must not produce a clean qualified security posture.
- [ ] **032-CF055** — W04.7 Distinguish no findings, stale DB, scan pending, scan failed, database unavailable, Java DB not needed, and upstream mirror unreachable in API/UI.
- [ ] **032-CF056** — W04.8 Verify an updated DB trigger causes selected workloads to rescan; prevent indefinite use of cached clean reports.
- [ ] **032-CF057** — W04.9 Create unique owned test management/member clusters with fresh runtime/image/DB caches. Load only the signed qualified local asset set; network controls deny public egress and public DNS calls while allowing declared local control services.
- [ ] **032-CF058** — W04.10 Verify the egress deny setup with negative control attempts from relevant namespaces/hosts. CNI policies alone do not prove hosts/container-runtime pulls are blocked; instrument those paths too.
- [ ] **032-CF059** — W04.11 Install Astronomer using local registry, retain CNPG production profile in at least one qualification case, and adopt a fresh outbound agent cluster through supported Astronomer API.
- [ ] **032-CF060** — W04.12 Exercise built-ins, Flux source acquisition, chart installation, workload scan/ingest, application catalog, local identity, local observability, management backup/clean restore, and Charlie offline behavior within its qualified scope.
- [ ] **032-CF061** — W04.13 Test missing chart, missing image, stale DB, wrong DB schema, bad signature, local registry outage, DB mirror outage, management disconnect, and reconnect. Required operations fail/degrade explicitly; previously released local Flux continues reconciling.
- [ ] **032-CF062** — W04.14 Declare optional external cloud/SSO/AI/provider features unavailable or separately qualified offline equivalents. Disabling a feature does not qualify that feature as offline.
- [ ] **032-CF063** — W04.15 Publish only redacted closed-schema evidence: source commit/release digest, all asset digests, profiles, exact versions, network deny proof, observed outbound attempts, DB identity/age, test outcomes, cleanup ownership, and timestamps.
- [ ] **032-CF064** — W07.1 Inventory all list operations and classify cursor/offset/in-memory/dynamic K8s continuation. Record authorized filters, sort keys, limits/count semantics, and existing clients.
- [ ] **032-CF065** — W07.4 Implement stable keyset ordering with unique tie-breaker; bind cursor to tenant/project/cluster/authorization scope, filters, ordering, and any required snapshot identity.
- [ ] **032-CF066** — W07.5 Scope/filter before pagination and counts. Exact total is optional; never fabricate it from page length or global count.
- [ ] **032-CF067** — W07.6 Add indexes after reviewing actual queries/plans; use expand-safe DDL and migration checks. Prove bounded query cost with representative data.
- [ ] **032-CF068** — W07.7 Convert catalog/controlplane and remaining critical collections; generated browser/SDK/CLI/Charlie consumers follow opaque continuation.
- [ ] **032-CF069** — W07.8 Retain K8s upstream continuation semantics without pretending arbitrary resources use SQL keyset pagination.
- [ ] **032-CF070** — W07.9 Handle dataset mutation: deleted anchor, inserts at boundary, equal timestamps, null sort fields, filter change, authorization revocation, stale cursor, and empty final page.
- [ ] **032-CF071** — W07.10 Check tables/search/exports/bulk selection don't assume that total or arbitrary offset navigation is always available.
- [ ] **032-CF072** — W10.2 Update Grafana, Loki, Tempo, Prometheus, and collector versions through W01. Replace Promtail with supported Alloy or an existing Fluent Bit pipeline, selected by configuration ownership and deployment fit.
- [ ] **032-CF073** — W10.3 Capture current log labels, tenant-safe fields, trace attributes, and dashboard queries before migrating. Verify collector changes against Loki/Tempo migration notes.
- [ ] **032-CF074** — W10.4 Specify context propagation across API request, durable operation, queued task, database call, agent tunnel, Kubernetes operation, and Flux reconciliation observation. Use stable operation correlation when asynchronous work cannot be represented as one synchronous span.
- [ ] **032-CF075** — W10.7 Define cardinality budgets: user IDs, raw URLs, resource names, SQL parameter values, and operation IDs must not become unlimited metric labels.
- [ ] **032-CF076** — W10.8 Redact bearer tokens, cookies, database credentials, private keys, secret manifests, and sensitive request data before logging/export. Add fixtures asserting their absence.
- [ ] **032-CF077** — W10.9 Publish health distinctions: process readiness, dependency readiness, queue ownership, stale observed state, downstream reconciliation, exporter failure, and scan database freshness.
- [ ] **032-CF078** — W10.10 Connect actionable alerts to runbooks: CNPG unavailable, replication/backup failure, worker ownership loss, delivery generation stalled, refresh failures, scan DB stale, and catalog trust failure.
- [ ] **032-CF079** — W10.11 Measure overhead with representative task throughput and API traffic. Establish an agreed baseline and acceptable delta rather than inventing a universal percentage.
- [ ] **032-CF080** — W11.1 Inventory Helm CLI invocations, approximately 28 SDK imports at the baseline, release storage assumptions, chart APIs, hook handling, error mapping, and Flux helm-controller dependencies.
- [ ] **032-CF081** — W11.2 Write an ADR covering each consumer separately. Select latest stable standalone Helm; migrate embedded SDK only after its supported APIs and behavior are qualified; let upstream Flux own its tested dependency graph.
- [ ] **032-CF082** — W11.3 Add characterization tests for install, upgrade, uninstall, dry-run/rendering, values precedence, release history, timeouts, cancellation, hooks, and interrupted operations.
- [ ] **032-CF083** — W11.4 Port one adapter at a time. Preserve existing public operation status and error envelopes; avoid a CLI subprocess replacement that bypasses authorization or durable operation recording.
- [ ] **032-CF084** — W11.5 Render the chart across standalone PostgreSQL, CNPG, external PostgreSQL, standalone Valkey, Sentinel, external Valkey, ingress/Gateway, network-policy, and disconnected configurations.
- [ ] **032-CF085** — W11.6 Preserve CNPG CR identity and ownership, instance count, PostgreSQL major selection, Barman plugin/ObjectStore configuration, secret references, monitoring, WAL/replication settings, and ScheduledBackup behavior.
- [ ] **032-CF086** — W11.7 Review CRD lifecycle and hooks explicitly. Application Helm rollback must not delete a CNPG cluster, PVC, ObjectStore, or a shared operator installation.
- [ ] **032-CF087** — W11.8 Test Helm/Flux coexistence using actual supported Flux releases. An upstream Helm incompatibility becomes a scoped component exception with evidence, not a forced downstream dependency override.
- [ ] **032-CF088** — W11.9 Regenerate chart/schema/docs/contracts after intentional changes. Check signatures and exact chart digest in a real install.
- [ ] **032-CF089** — W11.10 Prove interruption recovery, upgrade resume, release history, and compatible rollback with preserved application data.
- [ ] **032-CF090** — W12.5 Use real databases for transactions, advisory locks, idempotency, JSON constraints, key rotation, outbox ordering, row counts, tenant scope, query plans, and migration compatibility. Keep mocks where they test adapter behavior efficiently.
- [ ] **032-CF091** — W12.7 Qualify actual CNPG: three production-mode instances, failover under writes, application reconnect, replicated state, operator/Barman plugin compatibility, physical backup, restore to a fresh cluster, and PITR when claimed.
- [ ] **032-CF092** — W12.8 Verify recovery of encryption master keys, key IDs, agent/Charlie credentials, tenant identity, audit chains, and delivery generations. A PostgreSQL restore without the ability to decrypt stored credentials is incomplete.
- [ ] **032-CF093** — W12.10 Add Firefox and WebKit projects for critical authenticated workflows and responsive layouts; install matching browser binaries in CI. Keep Chromium route/visual breadth where appropriate.
- [ ] **032-CF094** — W12.11 Exercise login, refresh rotation, logout/revocation, mutations, validation, recovery, keyboard focus, dialogs, tables, uploads/downloads, streaming and terminal cancellation in the cross-browser lane.
- [ ] **032-CF095** — W12.13 Expand Kubernetes qualification to current stable 1.37 and maintain the declared older range only where actually tested. Include 1.36 as a transition lane and verify kubectl/client-go/Flux/CNPG version-skew support from upstream.
- [ ] **032-CF096** — W12.14 Reuse two-cluster and offerings inventories from Plans 028–029 instead of inventing another incomplete list. Verify optional Charlie/provider features only when their services are available.
- [ ] **032-CF097** — W14.1 Classify scripts as small wrapper, fixture generator, developer workflow, release state machine, or runtime operation. Prioritize scripts owning destructive cleanup, secrets, or upgrade rollback.
- [ ] **032-CF098** — W14.2 Document inputs, outputs, command compatibility, exit codes, preflight, timeouts, process ownership, evidence files, cleanup and resume behavior before rewriting.
- [ ] **032-CF099** — W14.3 Add failure-injection characterization: dependency unavailable, child crash, timeout, signal, partial install, lost lock, cancelled run, interrupted upgrade, and failed rollback.
- [ ] **032-CF100** — W14.4 Implement typed plans and bounded subprocess execution with explicit argv/env handling, cancellation, captured exit status, redaction, and resource ownership.
- [ ] **032-CF101** — W14.5 Use durable checkpoints for multi-stage operations that need resume. Separate validation, preview, execution, observation, rollback assessment, and cleanup.
- [ ] **032-CF102** — W14.6 Keep secrets in protected temporary files or scoped secret references; never interpolate them into logs, shell command substitution, or retained evidence.
- [ ] **032-CF103** — W14.7 Preserve shell entrypoints as short compatibility wrappers while old and new runners are compared on disposable environments.
- [ ] **032-CF104** — W14.8 Retain existing checks for PDBs, state recovery, readiness, immutable images, schema compatibility, backup prerequisites, and seven-image manifest validation.
- [ ] **032-CF105** — W14.9 Preserve machine-readable evidence contracts consumed by CI and release qualification; version schemas when intentionally changed.
- [ ] **032-CF106** — W14.10 Remove duplicate implementation only after parity tests pass. Keep simpler fixture scripts if conversion would add complexity without safety benefit.
- [ ] **032-CF107** — W15.1 Record catalog build inputs, schema validators, generated outputs, signing/publication ownership, tests, Python scripts, and transitive dependencies.
- [ ] **032-CF108** — W15.2 Select current stable Python using W01; validate wheels and platform support before changing CI. A release published today may need a recorded short-lived compatibility exception for unavailable dependencies.
- [ ] **032-CF109** — W15.3 Introduce a project manifest and reproducible full dependency lock, preferably `pyproject.toml` plus `uv.lock` after documenting the choice. Direct requirements pins alone do not capture all resolved dependencies.
- [ ] **032-CF110** — W15.4 Define separate runtime/build/test dependency groups; use locked installs and verify regeneration is deterministic.
- [ ] **032-CF111** — W15.5 Add Ruff and appropriate type checks, progressing from known interfaces to strict owned modules. Do not hide all errors behind permanent blanket ignores.
- [ ] **032-CF112** — W15.6 Preserve existing meaningful unittest coverage. Use pytest where it adds parametrization/fixtures; test runner branding is not an acceptance criterion.
- [ ] **032-CF113** — W15.7 Audit dependencies, generate an SBOM, scan generated catalog artifacts, and retain findings/waivers according to the shared policy. Pin GitHub Actions to reviewed commit SHAs with automated update discovery.
- [ ] **032-CF114** — W15.8 Verify generated catalog signatures, source metadata, archive membership, image dependencies, and schema versions before Astronomer imports them.
- [ ] **032-CF115** — W15.9 Inventory supporting examples and testbed/IaC configurations for Go/Node/Python/Helm/Kubernetes/Trivy/CNPG/Valkey/RustFS references; route maintained consumers to generated version inputs.
- [ ] **032-CF116** — W15.10 Mark inactive prototypes/reference repositories explicitly. Do not report a dormant UI as a production dependency, or silently omit a repository that builds a shipped artifact.
- [ ] **032-CF117** — W15.11 Coordinate cross-repository changes with recorded source SHAs and compatible catalog schema versions; test producer/consumer pairs before either side publishes.
- [ ] **032-CF118** — W17.1 Freeze candidate source SHAs across application/catalog/contracts; record chosen versions and scoped exceptions.
- [ ] **032-CF119** — W17.2 Run appropriate focused checks during implementation, then the complete required PR and candidate gates once the integrated candidate is stable.
- [ ] **032-CF120** — W17.3 Build all first-party artifacts with the selected toolchain. Verify SBOMs, vulnerability results, signatures/provenance, declared source SHA, and exact image digests.
- [ ] **032-CF121** — W17.4 Validate the manifest's complete application-image set. The baseline has seven roles: server, worker, frontend, agent, migrate, shell, and DR. Derive required roles from the contract; never hard-code the previous six-image assumption.
- [ ] **032-CF122** — W17.5 Prepare a disposable fresh-install and upgrade rehearsal. Inspect the target context, namespace, current release, CNPG resources, shared operators, PVC ownership, schema, and backup recoverability using sanitized evidence.
- [ ] **032-CF123** — W17.6 Restore a backup to prove database and key recovery before any production-mode major migration. Do not treat an upload confirmation as a restore test.
- [ ] **032-CF124** — W17.7 Deploy the exact signed candidate into local k3s using the supported install/upgrade path. Preserve database CR identity, PVCs, secrets, backup histories, and explicit operator ownership.
- [ ] **032-CF125** — W17.8 Verify browser login/refresh/logout, tenant authorization, durable operation completion, worker recovery, cluster adoption/agent connectivity, Flux generation fencing, built-ins, selected catalog operations, scans/freshness, audit, and telemetry.
- [ ] **032-CF126** — W17.9 Execute the candidate's fresh disconnected qualification with empty caches and blocked public egress. Keep external provider/Charlie requirements explicit.
- [ ] **032-CF127** — W17.10 Compare pre/post deployment IDs, schema, resource ownership, readiness, selected workload outcomes, and customer data invariants without retaining secret values.
- [ ] **032-CF128** — W17.11 Rehearse interrupted upgrade and recovery against candidate schema 88 and the destructive migration-87 boundary. Reread the actual deployment/rollback contract before execution; the old 71/66 baseline is historical. Helm rollback must not pretend to reverse an incompatible database migration.
- [ ] **032-CF129** — W17.12 Aggregate machine-readable qualification evidence, exact upstream-CVE waivers, license findings, remaining limitations, and migration notes into the candidate review packet.
- [ ] **032-CF130** — W17.13 Resolve the future tag's actual policy before publication. The approved v1.2.0/v1.2.1 deferrals and absence of a separate named approver apply to those tags; do not manufacture artifacts or silently extend tag-specific exceptions.
- [ ] **032-CF131** — W17.14 Select a future version from the integrated public contract changes. Keep v1 compatibility for ordinary modernization; reserve breaking refactor publication for the separately scoped v2 program.
- [ ] **032-CF132** — W17.15 Publish only after the candidate and applicable approval conditions are satisfied. Preserve v1.2.0/v1.2.1 artifact immutability; fixing a released artifact requires a new version, not overwriting its digest.
- [ ] **032-CF133** — W17.16 Recheck installed digests and health after publication, preserve the evidence packet, and update completion status only for the scope actually validated.
- [ ] **032-CF134** — Include fixes to authentication recovery, malformed durable data, pagination, state parity, catalog trust, freshness reporting, dependency updates, and enforced first-party security gates when their v1 compatibility is proved.
- [ ] **032-CF135** — Use expand/migrate/contract for compatibility-preserving changes. The explicitly requested Dex retirement uses the documented hard cutover at migration 87: verified backup, old-process drain, local recovery and coordinated database/release rollback are mandatory before deployment.
- [ ] **032-CF136** — Treat raising minimum Kubernetes/browser versions as a customer-facing compatibility decision even if application APIs are unchanged.
- [ ] **032-CF137** — Keep PostgreSQL major migration optional and separately rehearsed for existing installations while a fresh-install default is qualified.
- [ ] **032-CF138** — Do not block a v1 milestone on optional v2 architecture extraction or unclaimed FIPS capability.
- [ ] **032-CF139** — Include accurate migration notes and supported exceptions in each milestone packet.
- [ ] **032-CF140** — Validate source/artifact binding, timestamp ordering, expected/actual case counts, result values, required environment fields, and evidence digests.
- [ ] **032-CF141** — Distinguish `pass`, `fail`, `not-run`, `deferred`, and `not-applicable`. “Deferred” needs authority/scope; “not applicable” needs a reason.
- [ ] **032-CF142** — Sign or attest qualifying evidence when the release contract requires it; merely storing this JSON does not make it signed.
- [ ] **032-CF143** — Retain sanitized public summaries separately from restricted raw logs, cluster identifiers, and credentials.
- [ ] **032-CF144** — Record reviewer identity and approval separately from machine outcome. Automation cannot invent a human sign-off.
- [ ] **032-CF145** — Store manifests and SBOM references by digest so a different rebuild cannot reuse the previous result.
- [ ] **032-CF146** — Inventory current Cluster CR UID/name/namespace, operator version and ownership, instance count, PostgreSQL image/digest, extensions, StorageClass/PVCs, replicas, connection routing, ObjectStore/ScheduledBackup resources, key references, and backup retention.
- [ ] **032-CF147** — Identify shared resources. Astronomer must not uninstall a shared CNPG operator or Barman plugin during application cleanup.
- [ ] **032-CF148** — Confirm current standby/backup readiness and restore actual application state, including decryptable secrets, to an isolated cluster before changing the production path.
- [ ] **032-CF149** — Select the upgrade method supported by the chosen stable CNPG release. Use that release's documentation; the moving/development documentation is a research lead, not an executable guarantee.
- [ ] **032-CF150** — Separate same-major patching from a major-format upgrade. Do not let a generic image-update bot perform a PostgreSQL major migration.
- [ ] **032-CF151** — Rehearse extension compatibility, storage requirements, downtime, application pool reconnect, queued operations, and replica recovery on a copy of representative data.
- [ ] **032-CF152** — Prefer an explicitly planned new-cluster/cutover path where it gives clearer recovery. An operator-supported in-place major path needs its own downtime and backup approval; replica PVC recreation may be part of that method, so ordinary-upgrade “preserve every PVC” checks cannot be applied blindly.
- [ ] **032-CF153** — Design recovery for post-cutover writes. The old cluster is not a complete rollback source once new writes are acknowledged; define quiescence/replay/restore or accept a measured loss boundary explicitly.
- [ ] **032-CF154** — Preserve separate backup/WAL recovery histories across PostgreSQL majors and create a new post-upgrade recovery baseline. Verify selected Barman behavior rather than assuming pre-upgrade archives can recover across the format boundary.
- [ ] **032-CF155** — Keep old backups and keys according to retention; verify a chosen restore timestamp lies within the correct major's recovery history.
- [ ] **032-CF156** — Compare row counts and representative tenant objects, operation/audit continuity, ciphertext readability, constraints, extensions, indexes/query plans, and worker health after cutover.
- [ ] **032-CF157** — Record measured downtime, RPO/RTO, recovery steps and limitations; authorize production migration only after a concrete reviewed rehearsal packet.
- [ ] **032-CF158** — Read the candidate's actual schema/rollback floor rather than hard-coding the baseline's 71/66.
- [ ] **032-CF159** — Add schema/reader changes first; migrate bounded batches; switch writers only after old/new reader coexistence is proved.
- [ ] **032-CF160** — Never run down migrations simply because a Helm release was rolled back. Assess database/application compatibility and recover using the documented safe path.
- [ ] **032-CF161** — Back up encrypted data and necessary keys through the supported protected process; retain only references and digests in public evidence.
- [ ] **032-CF162** — Restore historical ciphertext with old keys before retiring any reader/algorithm/key.
- [ ] **032-CF163** — Reconcile work ownership and outbox state after interruption; avoid replaying a user operation as a new mutation without idempotency protection.
- [ ] **032-CF164** — Verify audit linkage and agent/Charlie credential usability after restoration.
- [ ] **032-CF165** — Preserve Valkey standalone/Sentinel/external modes and the existing queue/cache semantics. Test Sentinel discovery, authentication/TLS, failover, and worker reconnect where claimed.
- [ ] **032-CF166** — Verify supported Valkey upgrades and persisted data compatibility; an ephemeral cache test cannot prove durable queue recovery.
- [ ] **032-CF167** — Keep RustFS as the supported in-cluster S3-compatible implementation when configured; keep external S3-compatible endpoints as explicit options. Do not reintroduce a mandatory MinIO dependency.
- [ ] **032-CF168** — Verify bucket/object metadata, access control, TLS/CA trust, multipart uploads, retention and recovery semantics used by Barman, catalog kits, scans, and audit/export.
- [ ] **032-CF169** — Avoid sharing credentials across tenants or unrelated backup/catalog/scan roles without an explicit threat-model decision.
- [ ] **032-CF170** — Check the actual installed RustFS digest and storage layout before changing its version; preserve PVC/data ownership and exercise a recovery test.
- [ ] **032-CF171** — Verify every mirrored chart/image/DB asset from its signed inventory and declared trust policy. Rewriting a repository URL does not itself make the rewritten bytes trusted.
- [ ] **032-CF172** — Preserve disconnected local Flux reconciliation and last accepted intent even when the management service or upstream registry is unreachable.
- [ ] **032-CF173** — Reconcile the operator/agent/Flux/client-go/kubectl/CNPG compatibility matrix before raising cluster minimums.
- [ ] **032-CF174** — Use a separate disposable current-stable cluster first; preserve the existing local k3s as a known comparison until the candidate passes.
- [ ] **032-CF175** — Treat a Kubernetes minor/major platform upgrade as a platform operation, with its own backup and control-plane recovery; it is not an application Helm update.
- [ ] **032-CF176** — Maintain namespace/resource ownership labels and explicit shared-operator boundaries.
- [ ] **032-CF177** — Ensure an old agent cannot report stale success or overwrite a new generation after upgrade.
- [ ] **032-CF178** — Validate Flux signed-image trust according to its upstream policy; do not replace upstream Flux images with custom forks merely to hide accepted upstream CVEs.
- [ ] **032-CF179** — Rehearse cancelled Helm operations and failover during a rollout; compare desired/observed state after recovery.
- [ ] **032-CF180** — W00 reconciled source/claim baseline and documented architecture invariants.
- [ ] **032-CF181** — W01 selected current stable inventory and exercised discovery/alignment automation.
- [ ] **032-CF182** — W02 enforced first-party security gates and transparent scoped exceptions.
- [ ] **032-CF183** — W03 verified source-bound catalog/runtime artifacts used by provisioning.
- [ ] **032-CF184** — W04 fresh disconnected scans with truthful DB age and imported metadata.
- [ ] **032-CF185** — W05 bounded browser refresh, settled queues, cancellation, and form error behavior.
- [ ] **032-CF186** — W06 delivery/API/database enums and generated consumers agree.
- [ ] **032-CF187** — W07 pagination progresses and stays within authorized scope.
- [ ] **032-CF188** — W08 selected fixed SQL is generated with preserved transaction behavior.
- [ ] **032-CF189** — W09 durable JSON/request validation and malformed-reader behavior are explicit.
- [ ] **032-CF190** — W10 supported observability stack with correlated, redacted signals.
- [ ] **032-CF191** — W11 qualified Helm consumer migrations preserving Flux/CNPG ownership.
- [ ] **032-CF192** — W12 support matrix has actual current evidence and honest limitations.
- [ ] **032-CF193** — W13 v2 preparation has enforced domain boundaries without accidental v1 breaks.
- [ ] **032-CF194** — W14 selected operational runners preserve safety and evidence under failure.
- [ ] **032-CF195** — W15 catalog tools and supporting version consumers are reproducible and tested.
- [ ] **032-CF196** — W17 exact milestone candidate is built, installed, exercised, and recoverable within approved release scope.
- [ ] **032-CF197** — All unresolved upstream CVEs/licenses/qualification limitations are disclosed according to the actual applicable policy.
- [ ] **032-CF198** — Advisor index and evidence ledger reflect observed status and exact scope.
- [ ] **032-CF199** — Fresh supported-cluster run must retain system Kustomization Ready=True beyond the deployment progress deadline, healthy source fetch, application lifecycle, and a negative leader-loss check with recovery. Keep the original demo cluster until R16 passes.
- [ ] **032-CF200** — `go test ./internal/handler ./internal/delivery/... -count=1` and applicable real-DB tests pass. R16 browser verifies install→installed and uninstall→removed using the enriched Apps endpoint only.
- [ ] **032-CF201** — `npm run build && npm run bundle:check` exits 0 with retained fresh byte counts; browser login/configuration/error-path behavior still passes.
- [ ] **032-CF202** — Select a supported patched dependency or documented source-bound disposition under current policy. Preserve lockfile determinism and editor rendering/sanitization behavior; no global package update.
- [ ] **032-CF203** — Run high-severity audit, sanitizer negative tests, editor tests, type/lint/build/budgets. A waived gate is not a vulnerability fix; record remaining risk explicitly.
- [ ] **032-CF204** — Account for the current seven external-tool/chart prerequisite skips in the retained source-gate inventory and qualify required fixtures for release. The old 32-skip count and deleted Dex contracts are historical; retirement is not a passing qualification case. Retain replacement Identity coverage and distinguish source checks from deployed acceptance.
- [ ] **032-CF205** — Diagnose the two operations-pagination full-suite failures using controlled timing/isolation; retain failing runs, root cause and regression. An isolated pass is insufficient closure. Run the full frontend suite once after the fix.
- [ ] **032-CF206** — If enabling, obtain fresh enrollment through the supported private workflow and prove readiness plus an authorized action. If retaining disabled, use supported suspend/uninstall semantics with identity retention and no unrelated workload deletion.
- [ ] **032-CF207** — Verify bridge endpoints and enrollment readiness match the declared state; never log or retain credential-bearing enrollment bodies in evidence. Provider access remains an explicit dependency.
- [ ] **032-CF208** — Freeze exact source, images/chart, catalog and supported Kubernetes identities. Run required source gates on that candidate: `make check`, `make security`, changed frontend gates, Helm/distribution and generated-contract checks; retain original outputs and unresolved audit results.
- [ ] **032-CF209** — Deploy fixes only under a new immutable candidate identity through the supported workflow; preserve rollback material. Do not overwrite released v2.1.1 artifacts or re-label old browser evidence.
- [ ] **032-CF210** — On the owned supported replacement, require system rollout Ready beyond ten minutes, registration/reconnect, inventory, UI install/configuration/health/uninstall, final durable operation completion, truthful Apps status and desktop/mobile accessibility. Use actual login and supported APIs; no DB status writes.
- [ ] **032-CF211** — Rehearse optional-trust backup→isolated restore including decryptability and Goose version checks, with success/failure evidence and cleanup. Keep management data untouched.
- [ ] **032-CF212** — Finish reconciliation of the restored `scripts/qualify-offerings` runner with current Identity APIs, regenerate the denominator, and run the actual supported-estate preflight. Its source tests cover false-green and compatibility guards; live provider and matrix cases remain NOT_RUN/BLOCKED until exercised. Existing site-demo is not blanket authorization to destroy its workloads.
- [ ] **032-CF213** — Decide old-demo migration/retirement only after replacement qualification and an inventory/data migration plan. Preserve the original cluster now. Removing it or its data is not part of this source implementation slice.
- [ ] **032-CF214** — Update this ledger's S1–S7 scope-specific claims; preserve the archived plans. Do not mark all programs DONE based on one local application.
- [ ] **032-CF215** — **6.4 Published shared API lifecycle.** v0.1.0 provides `Ensure`, whose `Put` key is the upstream project name and whose create-only result is returned on persistence failure; it does not provide application rotation/deletion APIs. Extend and publish the shared layer's explicit scoped lookup/recovery/revocation contract, with real engine tests and immutable release evidence. Keep engine SDK imports inside the shared library, not Astronomer handlers. Preserve project grants during rotation; record recovery when a create-only secret was lost.
- [ ] **032-CF216** — **6.5 Crash-safe registration worker.** Use 6.2 and 6.3 around the shared client; verify stored credentials against returned upstream IDs. Retry `Put` with the original returned Result on persistence failure. Prove timeout after successful write, worker restart, issuer failure, lost database lock and missing secret cases. A process crash before persistence must stop for recovery.
- [ ] **032-CF217** — **6.6 Authorized API and configuration.** Add bounded typed provisioning configuration and private administration credentials, explicit project/cluster authorization, transactional mutation audit, operation status and RBAC-safe retries. No provisioning dependency in local login or startup; customer OIDC without shared management capabilities must remain supported as an RP only.
- [ ] **032-CF218** — **6.7 Catalog and delivery integration.** Declare supported products and callback contracts, inject issuer/audience/callback plus a scoped Secret reference through the existing delivery path, materialize only to the approved target namespace, and fail closed on incomplete credentials. Keep secret values out of install requests, Helm values, operation responses and audit payloads.
- [ ] **032-CF219** — **6.8 Rotation and retirement orchestration.** Stage a new immutable generation, verify consumer rollout before revoking the previous upstream credential, then perform guarded owned Secret cleanup. Track failures durably; uninstall must not delete another installation's app/project or roles.
- [ ] **032-CF220** — **6.9 Real product acceptance.** Prove a second supported product's SSO, distinct audience and least-privilege roles; denial for wrong issuer/project; recovery during storage/issuer outages; credential rotation and uninstall. Use deployed candidate artifacts and real browser/API evidence.
- [ ] **032-CF221** — **6.10 Release integration.** Qualify private transport/CA, least-privilege provisioning credentials, backup/key recovery, air-gap dependencies, full source/security gates and offering-matrix replacements. Do not mark package 6 complete from adapter tests alone.

### From 033-platform-tools-applications-assurance

[Archived specification](archive/2026-11-14/033-platform-tools-applications-assurance.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **033-CF001** — Choose and own the monitoring/logging qualification estate and exact supported configuration/environment matrix; run all live lifecycle, functional, upgrade/data/recovery, isolation, workload security and air-gap checks above.
- [ ] **033-CF002** — Retain and review actual reports; create genuine hashed qualification receipts; only then promote those exact profiles. **No entries are Assured.**
- [ ] **033-CF003** — Application source publication is tracked in S0; catalog branch source is already merged into catalog main at `856f3356e811f63dd3514cb3ac950cbe36189891`. Still run applicable hosted release gates, publish a signed catalog, and verify ingestion plus installation on staging.
- [ ] **033-CF004** — Activate daily upstream discovery through the merged default-branch workflow; assign a review owner. Local execution does not activate a schedule.
- [ ] **033-CF005** — Publish the corresponding operator/support documentation to `astronomer-docs` and consolidate test-estate ownership in `astronomer-testbed` as separate repository operations.

### From 026-platform-robustness-and-workflow-closure

[Archived specification](archive/2026-11-14/026-platform-robustness-and-workflow-closure.md). Source checkboxes are not proof of current failure; reconcile against current code before changing it.

- [ ] **026-CF001** — P1 / 12 — RBAC users/roles/bindings, binding role/project choices, visible-binding names, monitoring storage selection, project Members and SSO mapping roles now use bounded pagination or exact-ID lookup. Project counts use canonical metadata; SSO choices match the mapping scope and reset on scope changes. Audit names use exact visible-ID lookups; Audit cluster/project filters page remotely with known-ID entry, and CIS history uses canonical 25-row pages. Audit other legacy selectors rather than claiming the entire repository is uncapped. Continuation verification is recorded below.
- [ ] **026-CF002** — POLICY / 19 — Clone/download now covers built-in table families and custom-resource instances using exact group/version/plural/scope paths. Nodes, events, PVs and CRD definitions deliberately cannot be cloned; PVC cloning creates a claim, not a data copy.
- [ ] **026-CF003** — FOLLOW-UP / 20 — Common direct ownership chains and both Pod-to-Service and Service-to-Pod navigation are bounded and paged; discovery-denial browser coverage is present. An exhaustive Kubernetes relationship graph remains outside this bounded implementation.
- [ ] **026-CF004** — DECISION / 22 — Links emitted by delivery preserve project context. A bare entity ID without a project prompts project selection. Automatic project-agnostic resolution needs a documented server-side authorization-safe lookup, not scanning every project.
- [ ] **026-CF005** — DECISION / 25 — Declared registration profile is explicitly not measured authority. A live effective-privilege probe and mutation-refusal policy require a bounded protocol, caller/probe authorization, freshness rules, upgrade compatibility and operator recovery semantics before enforcement. No new live privilege claim is made.
- [ ] **026-CF006** — P1 / 26, 28 — Native grant create/delete denial and recovery, bulk partial/unconfirmed/cancelled outcomes, and denied relationship continuation have desktop/mobile workflow coverage. Virtualized grouping now has a separate three-viewport component suite plus a real CIS filtering regression. Extend the remaining loading/403/404/offline/stale cross-product; assistive-technology qualification remains separate. Canonical shared-table callers distinguish count certainty, preserve continuation for short pages and retain Previous on empty later pages. CVE detail preserves pagination/count certainty, context resets and denied-page recovery.
- [ ] **026-CF007** — EXTERNAL / 29 — Real agent identity and Charlie compatibility against an explicitly selected environment/credentials; do not use mocked browser success as evidence.
- [ ] **026-CF008** — EXTERNAL / 30 — All eight Plan 016 protected/live/human qualifications remain separate from this local implementation.
- [ ] **026-CF009** — DESIGN ONLY — Plan 025 questions.yaml/chart ingest remains an ADR/spike, not a shipped runtime capability.
