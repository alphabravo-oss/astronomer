# Plan 030 — Make Astronomer efficient, observable, and operationally dependable

## Status and review boundary

- Status (2026-10-07): **INTEGRATED AND DEPLOYED LOCALLY**, combined with Plan 031 on `integrate/030-031-local-k3s`; k3s runs `1.2.0-local.1322c98e`. Core live checks pass. Performance, scale/soak and full recovery qualification remain open; the full plan is not complete. See the dated consolidation record in the implementation ledger.
- Current evidence: [implementation ledger](./030-efficiency-evidence/2026-10-06-implementation/README.md).
- Planned against Astronomer commit `f87b189f`, October 6, 2026.
- Priority: P1; effort: multi-week program, delivered as independently verifiable changes.
- Risk: medium overall; high for observation architecture, live rollout, and recovery exercises.
- Objective: responsive cluster operations with predictable resource cost, accurate state, working integrations, and demonstrated recovery.
- “Rancher-like efficiency” is an operational aspiration, not a measured comparison. No Rancher performance benchmark was conducted.
- The user subsequently authorized implementing this plan. Proceed with source work and its required verification. Infrastructure purchases and destructive live recovery exercises still require their concrete scope to be established; unavailable environment access must be recorded without inventing completion.
- Plan 016 retains release/GA qualification authority. Plans 026–027 own existing workflow improvements; 028–029 own offering qualification and its estate. Reuse their evidence and infrastructure rather than create competing qualification programs.

All paths below are relative to `astronomer/` unless explicitly prefixed with `../`. Read `AGENTS.md` before execution. Preserve the existing uncommitted Plan 029 and index edits. Implement in a dedicated branch/worktree once authorized; make focused commits, and do not push or deploy merely because a local gate passes.

## Approved development recovery target (October 6 update)

The user selected the existing RustFS service for development backup/restore testing. Use a separate Astronomer test bucket and isolated restore target; do not alter Charlie's bucket or data. Same-host RustFS is acceptable for this development implementation. E10/E11 must prove actual backup/restore behavior with the existing encryption/Object Lock safeguards, but off-host loss resilience and production RPO/RTO qualification are deferred production work, not a prerequisite to completing this dev exercise. Record that distinction in all reports. Current sandbox access to both k3s and Docker sockets is denied; endpoint/credentials and supported object-store behavior remain to be checked once access returns.

## 1. What we know, and what we do not

The October 6 live inspection used `/etc/rancher/k3s/k3s.yaml`, namespace `astronomer`, and `https://astronomer.dev.alphabravo.io`. This is a historical snapshot to recheck, not a continuously valid assertion.

| Area | Observed evidence | Consequence |
| --- | --- | --- |
| Core | Server, worker, frontend, Postgres, Redis ready; current pods had zero restarts over about nine days | Preserve this working baseline |
| Public API | Valid HTTPS; `/health` and `/readyz` pass | Health is dependency-aware but not a complete integration qualification |
| Source identity | Checkout `f87b189f`; deployed health version `1.2.0-local.14341d1b`; Helm revision 223 | Do not treat source findings as proof of exact deployed behavior without checking build provenance |
| Durable work | 2,756 task-outbox and 41,768 audit-outbox rows all delivered; schema 70 clean | No observed outbox backlog; Redis queue state still needs separate measurement |
| Estate | Only the local cluster active and registered; one connected tunnel | No present multi-cluster or scale proof |
| Charlie | Two agents blocked on registry HTTP 401 | Diagnose pull credential lifecycle before claiming integration readiness |
| Grafana | Main container runs; required proxy cannot pull an old `localhost` image digest; pod 2/3 ready | Fix artifact availability and desired image ownership |
| Prometheus | Twelve active targets up; no Astronomer server or worker scrape targets | Application performance and queue pressure are insufficiently observed |
| Recovery | Zero management-backup destinations, no backup CronJobs, no recorded restore drills | Existing manual backup evidence does not establish current off-host recovery |
| Utilization | Node about 0.7 CPU cores / 9 GiB RAM; disk 45% used, about 179 GiB free | No current host saturation |
| Reservations | Server requests 4 cores/4 GiB while using about 13m/109 MiB; worker requests 1 core/1 GiB while using about 7m/40 MiB | Measure load before reducing reservations; requests are scheduling reservations, not actual usage |
| Exporters | Separate and monitoring-stack copies of node-exporter and kube-state-metrics | Check ownership and consumers before consolidation |
| Flux | Existing two exporter HelmReleases ready; source-controller standby unready | Standby is intentional, not a failed reconciliation engine |

This was not an exhaustive security audit, browser acceptance run, disaster recovery exercise, or comparative Rancher benchmark. Live failures are evidence; proposed frontend/DB bottlenecks below are investigations until measured.

### Verified source anchors

1. `internal/agent/delivery/observer.go:372` starts `observedInventory`. Its loop currently does:

   ```go
   id, ok := raw.(string)
   if !ok { continue }
   parts := strings.Split(id, "_")
   ```

   It expects five fields. `deploy/flux/install.yaml:4051` and the live CRD instead define entries as objects with `id` and `v`; `id` is `<namespace>_<name>_<group>_<kind>`. `observer_test.go:28` supplies invented string entries. Actual resource identities are skipped although entry counts survive.
2. `internal/agent/delivery/runtime.go` uses 30-second desired-state and 15-second status ticks. Both `requestAndReconcile` and `sendStatus` call `probe.Inspect`. `system_inventory.go:38–73` lists cluster-wide Deployments, StatefulSets, DaemonSets, storage classes and PVCs. Assignment observation also performs direct reads before status digest suppression.
3. `internal/agent/state_subscriber.go` already uses shared informers, rate-limited event emission and a 30-minute resync setting. `state_subscriber_crd.go` handles dynamically available CRDs. `health_inventory_test.go` and `subscriber_disconnect_gate_test.go` protect existing cache/backpressure behavior. Do not introduce an independent duplicate watch system without first establishing a concrete capability gap.
4. `frontend/src/lib/live/status-store.ts` already implements:

   ```ts
   export function liveFallback(baseMs: number): () => number | false {
     return () => (status === "open" ? false : baseMs);
   }
   ```

   `frontend/src/lib/hooks/kubernetes-proxy.ts` uses canonical query keys, cancellation signals and this fallback. Frontend streaming and polling suppression already exist.
5. `deploy/chart/templates/service-monitor.yaml` gates both application ServiceMonitors on `metrics.serviceMonitor.enabled`, false in `values.yaml:1028`. Discovery labels, namespace selectors and NetworkPolicy must align with the actual Prometheus installation.
6. `deploy/flux/patches/source-controller.yaml:30` documents the intentionally unready warm standby and Recreate strategy. Do not change readiness to green or delete the standby simply to quiet alerts.
7. `deploy/chart/templates/management-plane-backup-cronjob.yaml` requires bucket, credentials reference, source identity, wrapping-key reference and encryption-key backup enablement. Setting a single enabled flag is insufficient.

## 2. Guardrails and scope

- Astronomer adopts clusters; provisioning remains with external IaC. Flux remains the sole downstream reconciliation engine.
- Postgres owns durable intent/history; Redis is dispatch infrastructure; observed Kubernetes state must not become an alternative authority for desired state.
- Preserve tenant, project, cluster, namespace, resource and verb authorization. A shared cache never grants access. Recheck authorization at response/subscription time and on revocation.
- Never cache secret bodies for inventory convenience; preserve metadata-only and privilege-profile restrictions. Keep credentials out of reports, process arguments, screenshots and metric labels.
- Retain audit guarantees, failure visibility, bounded queues, deadlines and cancellation. Do not trade away correctness to improve a benchmark.
- Prefer dependency injection and existing domain packages; do not expand the large handler package with a new observation subsystem.
- No public API or wire-protocol changes by accident. Update source OpenAPI/protocol contracts, generators and mixed-version tests if an explicit change is needed.
- No direct DB writes, manual status forcing, TLS bypass, or unmanaged Helm/kubectl repair of application-owned installations. Read-only inspection is fine. Repair desired configuration through its owning API/chart workflow.
- In-scope source: `internal/agent/`, `internal/agent/delivery/`, `internal/observability/`, relevant `internal/server/`, `internal/tunnel/`, `internal/handler/`, `internal/charlie/`, `internal/config/`, `scripts/loadtest/`, `deploy/chart/`, `deploy/dashboards/`, relevant frontend hooks/live/query/table modules, associated tests and current operator docs.
- Existing database/worker modules enter scope only after profiles identify a problem and the task records exact files and a regression test. No speculative SQL/index rewrite.
- Out of scope: Rancher forks, unrelated Charlie/Constellation source, cluster provisioning implementation, UI redesign, replacement of Flux, global dependency upgrades, new cache databases, production release claims.
- Use `../astronomer-full-test-iac` as an existing estate dependency. Changes there require its own instructions and explicit scope reconciliation.

## 3. Delivery sequence and task ledger

Each task needs a PR/change summary, exact commit/image identities, test output, and sanitized evidence. Estimates are engineering estimates, excluding access/provider delays. All tasks start TODO.

| ID | Task | Depends on | Effort | Risk |
| --- | --- | --- | --- | --- |
| E00 | Reconcile source, live state, and existing plans | — | 0.5–1 day | Low |
| E01 | Application observability and baseline capture | E00 | 1–3 days | Low/medium |
| E02 | Correct Flux inventory decoder and fixtures | E00 | 0.5–1 day | Low |
| E03 | Repair image delivery for Grafana and Charlie | E00 | 1–3 days plus access | Medium |
| E04 | Repeatable performance/scale baseline | E01, E02; E03 for integration journeys | 2–4 days | Medium |
| E05 | Shared observation snapshot for delivery inventory | E04 | 3–6 days | High |
| E06 | Cached assignment observation and bounded reporting | E05 | 2–4 days | High |
| E07 | Measure and close frontend request/render hot spots | E04; E05/E06 for final comparison | 2–4 days | Medium |
| E08 | Measured DB/worker/tunnel hot-spot tuning | E04; repeat after E06 | 1–4 days if justified | Medium |
| E09 | Exporter consolidation and workload sizing | E03, E04, E06–E08 | 1–3 days | Medium |
| E10 | Configure backups and prove isolated restore | E00; required before risky live rollout | 2–4 days plus storage access | High |
| E11 | Failure isolation, rollout, and capacity report | All applicable tasks | 3–5 days plus estate access | High |

E02, E03 and E10 can progress independently of the cache work. Keep engineering baseline and optimized candidate separate. E08 may close as “no tuning justified” with evidence. E07 must preserve completed Plan 027 behavior. Provider-dependent work stays BLOCKED with named missing inputs; it must not prevent independent code work.

## 4. Detailed execution tasks

### E00 — Capture a reproducible starting point

- [ ] Run `git status --short`, `git rev-parse HEAD`, and `git diff --stat f87b189f..HEAD -- internal/agent internal/observability internal/handler internal/charlie frontend/src/lib deploy/chart scripts/loadtest`.
- [ ] Re-read the source anchors and relevant active-plan status. Record changes and remove tasks already completed; retain outstanding acceptance gaps.
- [ ] Read live pod/deployment state, image digests, release revision, readiness, connected cluster count, node utilization, volume usage, and protected config references. Never dump Secrets or unrestricted Helm values to a public log.
- [ ] Identify the actual source revision of each deployed component. Do not assume the frontend, agent, server and worker share one build merely because the chart version matches.
- [ ] Create sanitized evidence under `advisor-plans/030-efficiency-evidence/<run-id>/`; keep credential files outside tracked paths with restrictive permissions.
- [ ] Record target environment, expected ownership, approved mutations and recovery route for later live work. This plan's historical access does not guarantee access in a future sandbox.

Verify: `git diff --check` exits 0; baseline report names source/image identities and distinguishes observed facts from unresolved provenance. Source drift must be reconciled before implementing affected tasks.

### E01 — Make application behavior measurable

Primary scope: chart ServiceMonitor/rule/dashboard templates, `internal/observability/`, existing server/worker metrics, monitoring integration values.

- [ ] Inventory existing metrics before adding names: HTTP duration/errors, DB pool wait/saturation, queue depth/oldest age, audit/task outbox outcomes, tunnel connectivity, event relay lag, heap/goroutines and resource-search outcomes.
- [ ] Enable application ServiceMonitors in the intended environment; match Prometheus selectors and allowed network paths. Keep base chart usable without Prometheus CRDs.
- [ ] Add missing bounded-cardinality metrics for inventory refreshes, Kubernetes LIST/GET/WATCH traffic, cache freshness/sync state, reconnects and observation work duration. Reuse `internal/observability/resource_search_metrics.go` registration/label conventions. No resource names, URLs, tokens or unbounded user IDs in labels.
- [ ] Aggregate agent measurements through an existing supported telemetry path where possible. Never expose a new public unauthenticated metrics endpoint just to collect per-agent data.
- [ ] Build a dashboard covering API latency, queues/outboxes, DB waits, agent state freshness, downstream API load and resource consumption. Display scrape absence as missing data, not zero demand.
- [ ] Make alerts distinguish intentional Flux standby from loss of the active artifact-serving controller. Investigate k3s-specific missing scheduler/controller-manager targets; adjust rules to actual observable components rather than globally suppressing alerts.
- [ ] Capture 30 minutes of idle baseline plus representative interactive operations. Record scrape gaps and cold-start intervals separately.

Verify: `go test ./internal/observability/...` and `make verify-enterprise VERIFY_SCOPE=helm` pass. On the authorized deployment, Prometheus target JSON contains both server and worker with health `up`; metrics advance during requests/work; missing-target behavior is tested in an isolated fixture. Retain queries and returned series as evidence.

### E02 — Repair actual Flux resource identities

Primary scope: `internal/agent/delivery/observer.go`, `observer_test.go`, protocol validation tests and `internal/handler/delivery/inventory_test.go` if required by the consumer contract.

- [ ] Add regression fixtures matching the bundled Kustomization and HelmRelease CRDs: map entries with `id` and `v`, four identity fields, empty core group, and empty namespace for cluster-scoped resources.
- [ ] First demonstrate the real-schema regression fails on the current decoder.
- [ ] Decode fields into API version, kind, namespace and name. Validate types, malformed IDs, empty/invalid version, mixed valid/invalid entries and the existing maximum resource bound.
- [ ] Preserve documented total-count semantics, sanitization and readiness behavior; do not infer individual resource health from a count.
- [ ] Verify frontend/API inventory consumers receive correct identities. Remove the fabricated schema fixture rather than supporting it as an undocumented compatibility contract.

Verify: `go test ./internal/agent/delivery ./internal/handler/delivery` passes with the new regression. On a disposable member, use the supported delivery API to reconcile a minimal Kustomization, then compare reported identities against actual Flux inventory. Existing unit tests alone are not live proof.

### E03 — Repair integration artifact availability

Primary scope: `internal/handler/monitoring_stack_grafana.go` and its tests, related shared monitoring values, `internal/charlie/activation.go`, `activation_helm.go` and tests; deployment configuration through existing owners.

- [ ] Trace Grafana proxy image selection from intended candidate artifact through generated Helm values to deployed pod. Determine whether stale desired config, missing distribution, or both caused the unavailable digest.
- [ ] Use a digest-pinned image available to every target node. Local imports can remain a documented development mechanism, but must cover all scheduled nodes and survive the intended recreation test; production requires durable artifact distribution.
- [ ] Trace Charlie pull-secret references, registry audience/scope, credential expiry/refresh, and activation reconciliation. A 401 proves authorization failure, not its precise cause. Repair the supported credential/configuration lifecycle without weakening registry authentication.
- [ ] Add focused tests for image propagation and pull-secret references; keep credential material redacted in failures and audit output.
- [ ] Exercise the supported Grafana proxy route with authenticated users and denied users. Exercise a real Charlie request and reconnect; pod readiness alone is insufficient.
- [ ] Demonstrate safe pod recreation in the test environment can pull the intended images without relying on an accidental old node cache.

Verify: `go test ./internal/charlie/... ./internal/handler` passes; authorized live rollout reaches Grafana 3/3 and Charlie 2/2 with current images; functional access tests and negative authorization tests pass. Capture digests and HTTP outcomes, not credentials.

### E04 — Establish the benchmark before optimizing

Primary scope: existing `scripts/loadtest/`, its README/profiles, browser qualification fixtures and `docs/scale-baseline.md`. Preserve protected certification provenance.

- [ ] Reuse the existing harness; do not replace it with raw authenticated loops or direct DB fixture insertion. It already provisions synthetic agents, tracks audit outcomes and exports JSON/checksums.
- [ ] Run engineering tiers of 5 and 50 synthetic clusters first, then the existing estate-100 profile when infrastructure permits. Higher tiers are optional capacity exploration until lower tiers pass.
- [ ] Separately test real agents on at least two adopted members, with 1/10/100 delivery assignments and increasing actual Kubernetes resource counts. Synthetic tunnel responses cannot validate informer memory, LIST reduction or Flux behavior.
- [ ] Fix hardware, component replicas, image digests, chart values, dataset, workload mix and rate limits for each before/after comparison. Record cold start separately; use 5-minute warmup and at least 30-minute measured windows for optimization comparisons, three repetitions where practical.
- [ ] Include idle estate, active resource churn, inventory browsing, scoped search, delivery status, multiple panels, two browser tabs, and reconnection bursts.
- [ ] Record throughput achieved as well as requested; skipped work, stale data and HTTP errors cannot count as efficiency improvements.
- [ ] Add a report reducer/test for proposed cache metrics and relative before/after criteria if the current report cannot express them. Record all mandatory drills as NOT_RUN until genuinely executed.

Existing command shape (set these variables to reviewed test targets and private credential paths first):

```sh
go test ./scripts/loadtest
go run ./scripts/loadtest -server "$TEST_API" -metrics-server "$TEST_METRICS" -token "$TEST_TOKEN_FILE" -profile scripts/loadtest/profiles/small.yaml -out "$RUN_DIR/loadtest-small.md"
```

The second command mutates test fixtures; never run it against an unreviewed target. Follow `scripts/loadtest/README.md` for observer credentials and certification metadata. Default small-profile output is engineering evidence, not complete scale certification.

Verify: harness tests pass; report, JSON and checksum exist; achieved load and all fixture cleanup are accounted for. Existing gates include cluster-list p99 ≤500 ms, pod-list p99 ≤2000 ms, oldest pending task ≤60 seconds and event-relay lag ≤30 seconds; do not silently relax these to get a pass.

### E05 — Share observation state instead of repeated inventory scans

Primary scope: `internal/agent/state_subscriber*.go`, delivery `probe.go`, `system_inventory.go`, `runtime.go`, lifecycle composition and related tests.

- [ ] Map existing informer ownership, stores, typed fields, metadata-only sources, sync readiness and privilege restrictions. Reuse stores capable of answering the inventory question; identify exact missing fields before adding watchers.
- [ ] Define a narrow injected read-only snapshot interface in a package that avoids an agent/delivery import cycle. Follow the existing `ControllerProbe` injection pattern. Include per-kind availability, sync/freshness information and version/generation where required.
- [ ] Wire local embedded and remote agents to the same abstraction, scoped to their actual credentials. Do not broaden RBAC to make caching convenient.
- [ ] Replace repeated whole-cluster workload/PVC lists with snapshot reads. Add scoped watchers only for missing data; strip unused fields to bound memory. Do not maintain a second full-object copy if a safe immutable projection suffices.
- [ ] Coalesce concurrent refreshes. Give discovery and compatibility checks a bounded slower refresh policy, with explicit refresh on relevant CRD/controller changes.
- [ ] Distinguish “absent”, “not authorized”, “not synced”, “disconnected” and “healthy”. A cache read must not manufacture a new observation timestamp for old data.
- [ ] Handle watch expiration/410, relist, object deletion, absent-then-installed CRDs, privilege changes, startup cancellation and shutdown. Informer resync is not a guarantee of fresh server data; handle watch/relist health explicitly.
- [ ] Use existing transport cancellation/backpressure patterns. Never block informer callbacks on a disconnected tunnel or let pending changes grow without bound.

Verify: `go test -race ./internal/agent/...` passes, including informer-backed inventory and subscriber-disconnect regressions. Counting fake clients show no new LIST calls on unchanged delivery ticks after sync. Tests cover stale snapshots, restricted profiles, deletes and reconnect. Real-agent benchmark must show ≥80% fewer delivery-induced steady-state LISTs on the same workload, without freshness regression or unbounded memory growth. This is a proposed acceptance target, not an existing result.

### E06 — Observe assignments from bounded cached state

Primary scope: delivery `runtime.go`, `observer.go`, checkpoint handling and existing/new focused observation tests.

- [ ] Reuse or add narrowly scoped Flux source/reconciler observations keyed by GVR, namespace and name. Choose namespace/project coverage according to actual permissions and measured watcher count.
- [ ] Replace two direct GETs per assignment per status tick where a synchronized snapshot can answer correctly. Retain bounded, explicit fallback for missing capabilities; do not launch one informer per assignment.
- [ ] Track dirty assignments on relevant changes, coalesce rapid events, and retain periodic freshness/status resync. Preserve acknowledged checkpoint generation and removal/tombstone semantics.
- [ ] Bound queues and response work; reconnect storms use jitter and concurrency limits. An overloaded or disconnected cluster cannot monopolize management-plane workers.
- [ ] Preserve failure states, status normalization, agent mixed-version compatibility and continuous Flux ownership. No dropped mutation/audit intent is acceptable.

Verify: delivery race tests and real-agent assignment tiers pass. Unchanged assignments issue zero recurring direct GETs after initial sync, except documented bounded repair paths. A test modifies and deletes a source/reconciler and observes the correct new status. Watch interruption yields explicit stale/unknown state and recovery, not indefinitely green state. Record end-to-end freshness p95; proposed connected-state target ≤30 seconds.

### E07 — Optimize only measured frontend hot spots

Primary scope: `frontend/src/lib/live/`, `lib/hooks/kubernetes-proxy.ts`, canonical query keys/API adapters and affected tables; preserve Plan 027 acceptance.

- [ ] Capture request counts, payload bytes, rendering time and memory for cluster list, resource list, delivery inventory and scoped search, using the same data as E04.
- [ ] Audit existing `liveFallback`, stream reconnection invalidation and paced dispatch before editing. Fix demonstrated duplicate requests or missing invalidations; do not replace live transport wholesale.
- [ ] Share canonical query keys across panels; retain tenant/cluster/namespace/filter dimensions. Ensure cancellation on scope change and no cache leakage after logout, permission revocation or cluster switching.
- [ ] Verify inactive views and background tabs do not perform unnecessary polling while healthy streaming is available. If cross-tab coordination is justified by measured cost, specify auth/session isolation and leader failover before implementing it; otherwise keep it deferred.
- [ ] Preserve server-side paging, totals and search semantics. Apply existing TanStack virtualization only to measured large-table render bottlenecks; retain keyboard accessibility, selection and scroll restoration.
- [ ] Test stale/reconnecting indicators, disconnected stream fallback, rapid route changes, two panels sharing one query and denied users.

Verify: `cd frontend && npm run type-check && npm run lint && npm test` passes; targeted Playwright journeys compare before/after request counts and p95 interaction time. Identical mounted queries share requests; a healthy stream suppresses fallback polling; loss of stream resumes it. No acceptance by screenshots alone. Large-table optimizations must preserve existing responsive/accessibility behavior.

### E08 — Tune DB, workers and tunnels only where profiles justify it

- [ ] Review DB pool wait, slow-query evidence, queue age, handler duration, tunnel fan-out, goroutine/FD growth and audit/task delivery before altering settings.
- [ ] For each hot spot, record exact files/symbols, measured cause, bounded change and test. Prefer batched reads, bounded fan-out, existing cache invalidation and suitable query plans over extra replicas by default.
- [ ] Evaluate worker concurrency against database capacity and downstream limits. Increasing concurrency must not amplify overload or starve latency-sensitive tasks.
- [ ] Preserve periodic-task leadership, idempotency, transactional outboxes and terminal failure semantics. Separate interactive and background budgets using existing mechanisms where available.
- [ ] Any SQL change goes through query sources/sqlc generation and migration safety gates. Do not add speculative indexes or a second database/cache.

Verify: narrow changed-package tests plus E04 rerun pass. Record either an improvement without regression or a justified no-change decision. A changed query requires plan/timing evidence on representative data; changed concurrency requires queue drain and DB saturation evidence.

### E09 — Consolidate exporters and size from representative demand

- [ ] Map exporter release ownership, ServiceMonitors, dashboard labels, baseline dependencies and consumers. Pick one supported owner per intended exporter role.
- [ ] Update owner intent before removing redundant releases so Flux/product reconciliation does not recreate them. Confirm metric names/labels and queries remain valid; retain reinstall/rollback details.
- [ ] Measure CPU distributions, RSS/working-set peaks, throttling, startup, reconciliation bursts, connection pools and memory after soak. Separate management components from adopted-cluster agents and observability overhead.
- [ ] Propose dev/small and production profiles with explicit dataset/cluster/RPS envelope and headroom. Do not globally replace HA production defaults with single-node dev measurements.
- [ ] Reduce requests incrementally in the test deployment and rerun load. Choose limits with burst behavior in mind; CPU limits can cause throttling despite spare node capacity.
- [ ] Treat source-controller standby as intentional. Multiple replicas on one host do not establish host-loss resilience.

Verify: Helm gate passes, intended scrape coverage remains complete, exporter duplicates are removed only where proven redundant, and E04/E11 show no new Pending/OOMKilled/throttling or latency/queue regression. Publish measured sizing ranges with hardware and replica counts, not a universal magic number.

### E10 — Prove development backups and isolated recovery

Primary scope: existing management-backup API/worker pathways, chart backup/retention/restore templates and runbooks. Reuse current encryption and Object Lock design.

- [ ] Recheck the existing `charlie-dev-rustfs` service and determine an endpoint reachable from backup/drill Pods; the Compose hostname `rustfs` is not assumed resolvable from k3s. Use a dedicated Astronomer test bucket. Verify required S3, versioning and Object Lock behavior against the actual deployed version before configuring backups. Preserve Charlie buckets and data.
- [ ] Establish dedicated credential Secret references, retention, encryption/wrapping-key custody and source identity using the existing supported configuration. Record achieved backup age and restore duration; proposed dev targets remain RPO ≤24 hours and RTO ≤2 hours. Off-host storage and production RPO/RTO qualification are deferred.
- [ ] Configure through the supported management/chart owner. Keep writer and retention/delete credentials separate; include encryption/signing material recovery as required by the existing design.
- [ ] Run a backup and verify authenticated manifest, objects, encryption and completion evidence. Alert on missing/old backup success and failed restore drills.
- [ ] Restore into an isolated database/environment with no production endpoints or external notification side effects. Verify schema, expected fixture records, credential decryptability, authentication and representative reads.
- [ ] Record elapsed restore time and backup age. Verify the drill cannot overwrite the source database, and clean up only its own resources.
- [ ] Document the development restore procedure and its same-host limitation. Track independent storage and recovery when the management host is lost as deferred production work.

Verify: `go test ./internal/handler` and Helm gate pass for changed paths; a real backup to the dedicated RustFS test bucket and isolated restore produce sanitized retained evidence. Same-host testing does not demonstrate host-loss recovery. No backup/restore success claim based solely on CronJob existence or a mocked upload. Live destructive rollout does not proceed without a usable recovery checkpoint.

### E11 — Failure qualification and staged rollout

- [ ] Build an internally consistent candidate and retain source/image/schema/config identities. Use the existing release/deployment workflow; inspect rendered changes before applying.
- [ ] Deploy to a test estate, then a bounded canary. Preserve previous artifacts and configuration for rollback; account separately for any irreversible schema compatibility change.
- [ ] Exercise agent disconnect/reconnect, watch expiry, event bursts, slow/unreachable member, server and worker restarts, DB/Redis interruption in isolated fixtures, denied RBAC, stale reads and token rotation/revocation.
- [ ] Prove one failing member does not block unrelated cluster reads. Check explicit partial results, deadlines, event recovery and conserved audit/operation intent.
- [ ] Run 30-minute comparison windows and a four-hour steady/churn soak. Run real host/etcd/storage failover only on the native multi-node estate and under its existing authorization; K3d on one host is not independent failure-domain proof.
- [ ] Verify the user journeys: login, cluster list, resource browsing, workload status, Flux resource inventory, Grafana proxy, Charlie request, backup status and restore evidence.
- [ ] Promote only within authorized scope. Roll back on new sustained 5xx, missing audit intent, growing queues, freshness breach, OOM, lost authorization isolation or failed critical integration.
- [ ] Publish supported capacity, remaining unknowns, before/after measurements and exact evidence. Feed qualifying artifacts into Plan 016 rather than declaring GA here.

Verify: existing harness thresholds remain green, new cache/freshness criteria pass, all mandatory tests have retained results, and live checks demonstrate the intended candidate. If external certification is unavailable, mark that portion BLOCKED; do not substitute unit tests.

## 5. Verification commands and evidence rules

Run commands from the repository root unless stated otherwise. These are existing repository entry points, not claims that the entire suite was run while authoring this plan.

```sh
go test ./internal/agent/delivery ./internal/handler/delivery
go test -race ./internal/agent/...
go test ./internal/observability/... ./internal/charlie/...
go test ./scripts/loadtest
go vet ./internal/... ./cmd/...
make verify-enterprise VERIFY_SCOPE=backend
make verify-enterprise VERIFY_SCOPE=helm
# In frontend/:
npm run type-check
npm run lint
npm test
# Back at repo root for phase/PR boundary:
make verify-enterprise VERIFY_SCOPE=frontend
make local-ci-pr-representative
make local-ci-pr
```

Use narrow tests during iteration; full applicable gates at phase/PR boundaries. Do not install or run the whole matrix solely to validate this planning document. If a sandbox, missing service or dependency blocks a gate, record the command/error and environment requirement. Never call it passed.

Each evidence directory must contain `README.md` with task IDs, candidate identities, environment, timestamp, exact commands, outcomes and limitations; retain machine-readable performance results and sanitized charts where useful. Never commit raw Secrets, cookies, private DSNs or unrestricted logs.

## 6. Proposed acceptance matrix

These are reviewable targets. Existing stricter release gates win; any change to a target needs an explicit documented rationale before the qualifying run.

| Property | Acceptance |
| --- | --- |
| Integration truth | Grafana and Charlie function through supported authenticated paths; failed states remain visible |
| Inventory correctness | Actual Flux schemas decode correctly, including core and cluster-scoped identities |
| Idle observation cost | ≥80% reduction in delivery-induced repeated LISTs; no recurring per-assignment GETs after cache sync except bounded repair |
| Freshness | Connected-state observation-to-UI p95 ≤30 seconds; stale/disconnected information never relabeled as fresh |
| API speed | Retain existing cluster-list p99 ≤500 ms and pod-list p99 ≤2000 ms on declared benchmark hardware/workload |
| Work completion | Existing queue-age, audit conservation, achieved-load and error gates pass; no hidden dropped work |
| Memory/lifecycle | Existing harness heap/goroutine/FD bounds pass; no monotonic retained-state growth during four-hour soak |
| UI | No duplicate same-key fetches within a shared query client; polling suppressed with healthy stream; reconnect fallback proven |
| Sizing | Requests supported by measured peaks/headroom; no new OOM, throttling-driven breach or scheduling failure |
| Development recovery | Real backup to the dedicated RustFS bucket and isolated restore within reviewed dev RPO/RTO; matching keys successfully recovered. Off-host storage and key recovery after host loss remain deferred production qualification |
| Claims | Synthetic, real-agent, single-host and multi-host results explicitly distinguished |

## 7. Stop conditions, rollback, and maintenance

Stop the affected task and report when:

- Source or runtime provenance contradicts a prerequisite and cannot be reconciled.
- A change requires broader cluster privileges, secret-body caching, unaudited mutation or weakened TLS/authentication.
- A cache cannot distinguish unsynced/stale/denied from absent, or a queue cannot remain bounded.
- Required storage/registry credentials, estate access, or deployment authorization are unavailable.
- Verification fails repeatedly without an understood cause, or success depends on removing a failing assertion.
- Recovery would overwrite live data, a migration prevents safe rollback, or fixture ownership is uncertain.

For reversible runtime/cache changes, retain the previous deployable image/config and verify rollback in the test estate. If a temporary fallback switch is essential, make it typed, documented and time-bounded with a removal task; do not leave two permanent observation architectures. A fallback must preserve API load bounds and truthful stale-state behavior.

Future reviewers must inspect watch scope, cache memory/cardinality, authorization revocation, agent-version compatibility, event deletion/replay and metric-label cardinality whenever adding resource kinds. Update performance baselines after material workload or topology changes.

## 8. Review checklist and completion ledger

Before implementation, review the task order, proposed freshness/performance targets, target estate, backup destination/RPO/RTO and allowed live rollout scope. Credentials and infrastructure inputs can arrive later without blocking independent source work.

- [ ] E00 source/live/plan reconciliation complete
- [ ] E01 application telemetry and baseline complete
- [ ] E02 real Flux schema regression fixed
- [ ] E03 image lifecycle and functional integrations proven
- [ ] E04 reproducible before benchmark retained
- [ ] E05 inventory shared with existing observation infrastructure
- [ ] E06 assignment reads and status reporting bounded
- [ ] E07 measured frontend issues resolved or explicitly ruled out
- [ ] E08 DB/worker tuning justified or closed with no-change evidence
- [ ] E09 exporter ownership and sizing validated
- [ ] E10 dedicated RustFS development backup and isolated restore proven; off-host production qualification tracked separately
- [ ] E11 comparative run, soak, canary and authorized rollout complete
- [ ] Applicable source, race, frontend, Helm and Local CI gates pass
- [ ] Plan 016/026/028/029 evidence and status reconciled without duplicate claims

Considered and rejected: treating Flux standby NotReady as a controller failure; replacing existing frontend streaming wholesale; introducing a new central cache before measuring existing caches; reducing resources directly to idle usage; claiming Rancher parity or production HA from this one-node deployment.
