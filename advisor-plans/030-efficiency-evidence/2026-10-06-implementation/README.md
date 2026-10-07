# Plan 030 implementation evidence — 2026-10-06

Status as of 2026-10-07: **integrated and deployed to local k3s; full Plan 030 remains open**. See [the consolidation and deployment record](#2026-10-07-consolidation-and-local-deployment). The October 6 entries below are historical evidence from the restricted environment.

## Provenance and isolation

- Original planning baseline: `f87b189fa5ea94ad5e86392f90f810f6b6dbd07c`; implementation subsequently reconciled onto historical deployed source `14341d1b` (see chronological evidence below).
- Implementation worktree: `/root/astronomer-all/astronomer-efficiency-030`, branch `implement/030-efficiency`.
- Latest accepted source: `d1d4875abffb678d3019504a2a523e79efe59378`; observation adapter and refreshed inventories integrated; final available checks recorded below. Live qualification and failed/unavailable gates remain open.
- Original worktree/user branch and unrelated Plan 031 changes are preserved. Root writes only these advisor-plan artifacts; separate executors implement isolated changes reviewed before integration.
- Baseline measurement branch remains separate at `59b16ca4`; it is never merged into the candidate. Local baseline/candidate binary manifests are pinned to their recorded commits, not silently relabeled as latest source.
- No push, deployment or live infrastructure mutation has occurred in this implementation session.

## Current live-access boundary

Read-only command attempted:

```sh
kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes
```

Result: `Unable to connect to the server: dial tcp 127.0.0.1:6443: socket: operation not permitted`.

The current execution sandbox restricts network/socket access and permits no escalation. Earlier live results in Plan 030 remain historical evidence, not reverified current state. No deployment, registry credential repair, scrape activation, cluster provisioning, real-agent load test, backup or restore was performed during this implementation run. Code/tests that do not require inaccessible services can proceed. Live-dependent tasks remain open; this is not a reason to stop independent engineering.

## Recon decisions

- E01: application ServiceMonitors already exist and production values enable them. Do not introduce a second metrics discovery mechanism or force CRDs on base development installs. Missing-target alerting needs attention: the present PrometheusRule has no absent/all-down server/worker scrape alert.
- E03: Grafana proxy image already comes from configured server image; Charlie activation already propagates registry pull secret references. Historical image failures alone do not justify a speculative authentication or desired-state rewrite. Reinspect live configuration/credential lifecycle when access returns.
- E05: reuse existing StateSubscriber typed stores; its initial HasSynced predicate is not a continuing freshness guarantee. New consumers must account for watch loss and privilege changes. PVC/storage classes currently use metadata observation, which cannot satisfy full inventory detail by itself.
- E07: frontend liveFallback already disables polling while live transport is open. Investigate measured gaps instead of building another streaming layer.
- E08/E09: no database/concurrency or production sizing change justified by idle historical usage alone.

## Task status

| Task | State | Evidence/next action |
| --- | --- | --- |
| E00 | Partial | Historical source drift reconciled and isolated branch retained; current live image/schema/config provenance still unavailable |
| E01 | Source verified; live pending | Alerts/dashboard/observation panels and attributable request counters integrated; collector integrated; live scrape/PromQL/baseline proof pending |
| E02 | Source verified; live pending | Real Flux identity decoder regression fixed and tested; actual member inventory proof pending |
| E03 | Live blocked | Existing image/pull-secret wiring inspected; current registry/configuration evidence needed before justified repair and functional verification |
| E04 | Engineering tooling verified; runs pending | Real estate phases, conservative counters and offline comparisons integrated; reviewed real fixtures, lifecycle accounting, matched runs and repetitions unexecuted |
| E05–E06 | Source verified; live pending | Shared typed observation, assignment cache, lifecycle/checkpoint/freshness wiring and UI integrated and tested; actual LIST/GET reduction, memory and freshness/soak evidence pending |
| E07 | Instrumentation verified; live pending | Browser engineering artifacts/journeys integrated; actual browser measurements and evidence-driven hotspot decisions pending |
| E08 | Measurement-dependent | No DB/worker/tunnel tuning or no-change verdict justified without representative profiles |
| E09 | Existing source ownership retained; live pending | Deployed-source exporter reuse preserved; intended scrape ownership, sizing and live regression evidence pending |
| E10 | Live blocked | Dedicated RustFS dev recovery worksheet prepared; bucket/endpoint/Secrets/backup/isolated restore unexecuted; off-host qualification deferred |
| E11 | Partial | Pinned local binaries and durable guarded drill cleanup verified; observation adapter integrated; final combined gates, live faults/canary/soak/rollback evidence pending |

## Required remaining evidence

All Plan 030 gates, including real Flux identity reporting, live targets, authenticated Grafana/Charlie behavior, cache LIST/GET reductions and freshness, scale/soak results, backup and isolated restore, remain open until individually proven. Do not convert unit-test success into live or scale qualification.

## Deployed-source reconciliation

`git merge-base f87b189f 14341d1b` confirms the deployed source is a descendant of the original plan baseline. The local `feat/offering-functional-runner` worktree is clean at `14341d1b`. Its history changes 207 files relative to main, including offering functional execution and monitoring baseline ownership. It does not fix `observedInventory`'s string decoding. Relevant commits include `00d6f604` (reuse baseline exporters), `0eefc8f3` (baseline ownership documentation), and `304e63af` (conflicting Prometheus operators).

Decision: preserve those existing changes by moving the isolated implementation branch onto `14341d1b` after the first isolated decoder fix is committed. Do not alter the existing offering-runner worktree. Do not reimplement exporter ownership already present there; retain live migration/consolidation validation under E09.

## Reviewer checks completed

- Selected existing load-report conservation/error/leak-window tests: PASS (`go test ./scripts/loadtest -run 'Test(EngineeringRunFails|CertificationFailsClosedOnTraffic|LeakWindowAverages)' -count=1`). This checks the harness, not system capacity.
- Existing chart Charlie-expiry and runbook-reference tests: PASS (`go test ./deploy -run 'Test(CharlieExpiryAlertsAreContentFreeAndDeterministic|PrometheusRunbookURLsResolve)' -count=1`).
- Ad-hoc Helm render attempts without a complete fixture were rejected by production schema and secret-preflight guards. No values were applied and no guards bypassed. Use repository render helpers for subsequent chart tests.
- `promtool` is not installed. An expression-render test must not be represented as PromQL execution.

## E02 implementation and review

- E02 commit: `49aecd85` (`fix(delivery): decode real Flux inventory resource references`), rebased cleanly onto `14341d1b`.
- Changed decoder uses real `{id, v}` references and four identity fields; supports empty core group and cluster-scoped namespace. Invalid entries are skipped without changing total count. Existing protocol bounds retained.
- Added real-schema fixtures, malformed/mixed values, bounded-resource cases, protocol acceptance and scoped API JSON passthrough coverage.
- Executor demonstrated regression failure before implementation and reports delivery/handler/protocol race tests plus vet/complexity/dependency checks passing on the original base.
- Reviewer read all five changed files and reran `go test ./internal/agent/delivery ./internal/handler/delivery ./pkg/protocol -count=1` on the reconciled baseline: all pass.
- Full backend gate previously stopped on missing isolated frontend dependencies. Existing matching dependency tree is now linked into the isolated worktree; no dependency install was performed. Full gate still needs rerun and may encounter sandbox/service restrictions.
- E02 remains **source verified / live verification pending**, not fully complete: no disposable-member Flux reconciliation was possible.

## E07 existing-behavior baseline

`npm --prefix frontend test -- src/lib/live/stream.test.ts src/lib/live/hooks.test.tsx src/lib/live/paced-invalidate.test.ts src/lib/hooks/kubernetes-proxy.test.tsx`: 4 files, 18 tests PASS. These cover existing stream/polling/query behavior; no browser performance or capacity claim follows.

## E04/E05 sequencing clarification

Live comparison runs are blocked, but source optimization may proceed after a local counted-client regression records the pre-change API work and preserves a before/after test. This does not satisfy the real-agent benchmark gate. Real load, latency, memory, freshness and soak acceptance remain required before rollout/completion. Avoid stalling all source work on network access, and avoid presenting a fake-client benchmark as production capacity.

## E01 scrape-absence protection

- Commit `b433ba32`: per-release namespace/service alerts for missing or all-down server/worker metrics, custom fullname/cross-namespace render contracts, opt-out coverage, operational runbook, ten promtool scenarios including independent component failure.
- Reviewer inspected complete changes and reran `go test ./deploy -run 'Test(ApplicationMetrics|PrometheusRunbookURLsResolve)' -count=1 -v`: render/opt-out/runbook checks PASS; PromQL evaluator explicitly SKIPPED (promtool unavailable).
- Full Helm enterprise gate PASS with stable source tree: `/tmp/astronomer-verify-enterprise/20261006T043517Z-3-49aecd85b2b4/evidence.json` (pre-commit tree containing E01; final fixture-only amendment adds independent-component cases and passed narrow rerun).
- Offline attempt to build cached Prometheus's promtool failed because required dependency versions are absent and module cache is read-only. No evaluator result claimed.
- Live ServiceMonitor activation, target health, application metrics advance and agent instrumentation remain open. This is a bounded part of E01, not completion of all observability work.

## Broader gate status after dependency recovery

Reviewer backend gate run with the existing dependency tree reached formatting, ShellCheck (65 scripts), migration safety (70 migrations) and data-governance checks: PASS. It then failed at pinned `sqlc@v1.31.1` acquisition: proxy.golang.org DNS/socket access denied. Log: `/tmp/astronomer-030-backend-gate.log`. Full backend gate remains incomplete.

Reviewer reran complexity/docs checks on the untouched `14341d1b` offering-runner worktree. All nine complexity violations and comparison-document semantic failure reproduce there; these are inherited, not introduced by E02/E01. No thresholds or baselines were relaxed. Violations: tunnel/handler.go, worker/tasks/project_reconcile.go, frontend/lib/api/catalog.ts, frontend/lib/query-keys.ts, handler/monitoring_stack_cluster.go, AppInstallModal, InstallChartForm, ClusterDetailPage, ToolInstallModalEditor. Documentation failure: docs/rancher-astronomer-comparison.md semantic contract.

Reviewer E02 race rerun on deployed-source baseline: `go test -race ./internal/agent/delivery ./internal/handler/delivery ./pkg/protocol -count=1`: PASS.

## E05 prerequisite discovered

`StateSubscriber.Run` previously passed a stop channel to typed `WaitForCacheSync` that was closed only by a defer after Run returned. A forbidden typed LIST could therefore prevent bootstrap and ignore cancellation forever. E05a is a focused bounded/cancellable lifecycle fix with regression tests before shared-inventory work. It does not establish cache freshness or implement E05 by itself.

## User-selected development backup destination

User reply: use an existing RustFS for testing; this is development. Prior live snapshot showed `charlie-dev-rustfs`. `../charlie/docker-compose.yml` confirms a RustFS service pinned at `rustfs/rustfs:1.0.0-beta.12`, internal endpoint `http://rustfs:9000`, persistent volume and a Charlie-owned bucket initializer. This is configuration evidence, not proof the service is reachable now.

Recheck command `docker ps --filter name=rustfs --format ...` failed with permission denied on `/var/run/docker.sock`. No credentials were read or displayed. Use a dedicated Astronomer bucket, retain encryption/Object Lock checks and prove isolated restore when access is available. Off-host host-loss qualification is deferred production scope per the user's dev instruction; don't call same-host testing off-host DR.

## E05a lifecycle prerequisite implementation and review

Commit `ccf15808` fixes bounded/cancellable informer bootstrap. Before the fix, the executor reproduced pre-cancelled startup issuing API requests and cancellation during a Forbidden typed LIST hanging. Both typed and metadata factories now share a bounded synchronization window, follow caller cancellation, and are joined on exit. Per-kind unsynced stores remain unavailable; healthy watches continue after partial bootstrap.

Reviewer inspected the complete diff and independently ran:

```sh
GOCACHE=/tmp/astronomer-030-go-cache go test -race ./internal/agent -run 'Test(StateSubscriber(CanceledBeforeStart|CancelDuringDenied|PartialTyped|BoundsTyped)|HeartbeatUsesInformer|HeartbeatWorkIsBounded|InformerEventsDoNotBlock|ReconnectReplayIsComplete)' -count=1
```

PASS. Tests cover cancellation, denied typed/metadata lists, healthy-kind updates and repeated owned-watch shutdown. They do not prove all auxiliary CRD/gatekeeper goroutines are joined. The sole complexity baseline change lowers the touched file ceiling from 1055 to 1037 lines; no inherited ceiling was raised.

## E05b in progress — explicit freshness contract

Cached inventory needs observation times/states rather than new receipt timestamps. The selected design adds negotiated optional observation metadata to existing delivery inventory/component JSON; uses the existing SQL observed_at argument (no speculative migration); acknowledges capability through the tolerant CONNECT_ACK contract; and suppresses new delivery fields for strict legacy delivery peers. Legacy direct observation remains explicit and tested until both peers negotiate support. Cached producers are not yet enabled. Queued extended observation frames across reconnect require final-write protection so a new agent cannot replay unsupported fields to an old server. No mutation/audit frames may be dropped by that protection.

## Additional independent checks and RustFS preparation

- Added [RustFS development recovery worksheet](rustfs-dev-recovery.md), with proposed dedicated bucket/Secret names, existing chart configuration shape, Pod-network endpoint checks, Object Lock checks and actual backup/isolated restore acceptance steps. This is prepared configuration, not applied live evidence. E10 text and checklist now consistently reflect the user's accepted development scope.
- Reviewer ran `GOCACHE=/tmp/astronomer-030-go-cache go test ./internal/observability/... ./internal/charlie/...` in the implementation worktree. Observability passed (0.757s). Charlie, contract and fakebridge suites could not complete: `httptest` TCP listener creation failed with `socket: operation not permitted`. Do not count the combined command as passed or infer Charlie runtime health. This adds local network-test restrictions to the known live-access blocker.

### E05b reviewer checks during implementation

Candidate is not yet committed or accepted as complete. Reviewer inspected optional protocol observation fields, capability ACK intersection/reset, final queued-write downgrade guard, retry wake/suppression reset, digest immutability, source-time ingestion, API source/generated shape and both agent composition roots. Requested explicit distinction between unknown and incompatible identity, unavailable source reads, and coverage for system-assignment source timestamps.

Independent race checks passed on the in-progress candidate:

- `go test -race ./pkg/protocol ./internal/delivery/status ./internal/delivery/compatibility ./internal/agent/delivery` (with `/tmp/astronomer-030-go-cache`): all four packages passed; retained log `/tmp/astronomer-030-observation-review.log`.
- `go test -race ./internal/agent -run 'Test(ObservationNegotiation|QueuedExtendedObservations)' -count=1`: passed; `/tmp/astronomer-030-observation-tunnel-review.log`.

These checks cover wire/digest/ingestion and queued observation compatibility, not live mixed-version servers. Cached producers remain unimplemented; consumer aging and heartbeat margin must be addressed before enabling them (current heartbeat floor and fleet stale threshold are both five minutes).

### E05b accepted source prerequisite — `428cc20e`

Executor committed `feat(delivery): negotiate source observation freshness`; reviewer confirmed clean worktree and reviewed all source paths plus final API passthrough regression and system-assignment source/null timestamp test. The latter independently passed under race (1.033s). Accepted this scoped prerequisite; E05 shared cache work remains incomplete.

Executor terminal checks: protocol/status/compatibility/delivery and focused tunnel race tests passed; API/capability tests passed; frontend type-check passed; `go vet ./internal/... ./cmd/agent` passed; OpenAPI generation, frontend generated drift checks, dependency boundaries and whitespace passed. SDK generated with an offline build from exact cached `oapi-codegen/v2@v2.5.0`, using its version override to preserve the pinned header; no hand-edited generated output.

Full `go vet ./internal/... ./cmd/...` cannot pass the deployed baseline: `cmd/astro/catalog.go:881` calls `DeleteCatalogInstalledByIdWithResponse` without the required request body. Reviewer inspected both unchanged call and required-body generated signature at `14341d1b`, confirming this predates E05b. Nine prior complexity violations and the comparison-document check remain; no ceilings were raised (only touched tunnel file ceiling lowered).

E05c dispatched to existing executor: shared typed observation source and ongoing watch-health tracking, immutable projections, bounded metrics and counting-fake verification. Delivery cache activation, UI aging and heartbeat headroom remain subsequent E05 integration work. No Kubernetes-read reduction or live runtime improvement is claimed yet.

Reviewer independently reran `go test ./internal/handler/delivery -run TestInventoryAPIExposesSourceTimeAndComponentFreshness -count=1` at `428cc20e`: passed (0.015s). A second isolated executor is implementing the frontend freshness consumers from this commit while the existing executor implements E05c; no cache producers will activate before both are reviewed. Newly observed unrelated original-tree Plan031/UI/dependency changes are preserved and outside Plan030 edits.

## CLI build prerequisite repaired

Reviewed isolated executor commit `c2f9d68c` (one source line): catalog uninstall supplies an empty typed request body to the generated SDK. Both data-deletion and failed-release-cleanup confirmations remain omitted; no new flags or generated edits. Reviewer independently ran `go vet ./cmd/astro`: passed. Executor's existing narrow validation/confirmation tests passed; socket-dependent full CLI tests were not run.

Main executor integrated the reviewed change as `22e2cfd8`. This resolves the specific inherited compile mismatch recorded above; it does not by itself establish the full backend gate. Full internal/cmd vet is being rerun against the integrated source.

Additional parallel E01 work is scoped to the existing management dashboard: actual rendered namespace/fullname selectors, explicit scrape absence and existing process CPU/heap/goroutine metrics. Observation-metric panels remain dependent on the E05c producer contract. No new telemetry endpoint or global monitor enablement is planned.

Reviewer full `GOCACHE=/tmp/astronomer-030-go-cache go vet ./internal/... ./cmd/...` at integrated `22e2cfd8` completed with exit 0. Log: `/tmp/astronomer-030-full-vet-after-cli.log` (empty success output). The previous CLI-related vet blocker is resolved; this does not waive other gates or socket-dependent tests.

## E01 management dashboard source accepted — `786bf729`

Reviewed dashboard/template/render tests/runbook. Added server/worker scrape availability and per-replica process CPU, heap and goroutine panels using existing metrics. Helm injects actual release namespace/fullname into dashboard constants; standalone configuration is documented. Disconnected count now gates individual target samples by successful scrape, uses an instant query, and preserves missing data instead of falling back to zero. Review caught and corrected a range-query/last-value issue that could otherwise retain historical green status.

Reviewer `go test ./deploy -run TestApplicationDashboard -count=1` passed (0.694s); `/tmp/astronomer-030-dashboard-review.log`. Tests cover custom scope, panel selectors, missing-data configuration, instant disconnected query and monitor/dashboard opt-outs. Executor's final Helm gate passed with stable source tree, as independently inspected in `/tmp/astronomer-verify-enterprise/20261006T050340Z-3-0e23e8d4a03b/evidence.json`. It records the pre-amend dirty source commit `0e23e8d4`, not immutable final commit provenance; final commit contains those tested changes. No real PromQL evaluator or live Grafana/Prometheus acceptance is claimed. Main executor has been asked to integrate the reviewed commit.

## E05 component UI review in progress

Reviewed new source-freshness helper, list counts/columns, detail and nested evidence badges. Independently ran both focused frontend suites: 17 tests passed, including age transition on unchanged query data, zero API calls with open live stream, consistent summary and badge state, legacy omission behavior and one cleaned-up clock per page. Log: `/tmp/astronomer-030-ui-freshness-review.log`. Awaiting final broad suite results/commit before integration. Heartbeat margin and cache producer wiring remain incomplete.

## UI freshness accepted and integrated

Reviewed `3efa968c`, integrated by main executor as `7232c2d3`. Independently inspected terminal full-suite log `/tmp/astronomer-030-ui-tests.log`: 298 files / 1,865 tests passed in 180.48s. Focused reviewer tests already passed. Executor typecheck, scoped lint and direct Prettier passed; staged formatter wrapper encountered sandbox `spawnSync git EPERM`. Frontend enterprise gate stops at stale generated code-health inventory, independently reproduced on deployed-source baseline. Do not treat those exceptions as a passed full gate.

The inherited modal hook-dependency warning is separately repaired in candidate `9cd24965`: one missing dependency, redundant alias removed, same function size. Reviewer is rerunning full zero-warning lint and seven affected tests before accepting integration.

Dashboard accepted commit integrated as `7ea86f4e`. E05c review found disconnected-state recovery could ignore valid resumed watch events; executor is fixing this before acceptance. Direct-probe counting baseline has been executed independently; it remains a local fake-client measurement, not a live efficiency result.

## Frontend lint prerequisite accepted — `9cd24965` / integrated `a72cb8e7`

Reviewed modal source and preservation tests. Added missing chart-name effect dependency while removing redundant selected-version alias; function size remains unchanged, no baseline relaxation. Reviewer full `npm --prefix frontend run lint` passed with `--max-warnings=0` (`/tmp/astronomer-030-lint-review.log`). Reviewer targeted modal suite passed four tests (`/tmp/astronomer-030-hydration-review.log`). Executor reported seven affected tests across its selected suite; four is the independently rerun modal-file count. Full typecheck passed per executor. The prior hook-dependency lint blocker is resolved.

## E05c in-progress review evidence

- Reviewer direct baseline `TestDirectProbeReadBaseline`: ten platform probes issue 60 typed LISTs and 30 controller GETs, excluding discovery and optional dynamic inventory; `/tmp/astronomer-030-direct-baseline-review.log`.
- Reviewer selected observation/informer race tests passed (observation 1.032s; agent 3.289s), `/tmp/astronomer-030-shared-source-review.log`. Full observation lifecycle package subsequently passed under race (1.054s), `/tmp/astronomer-030-observation-lifecycle-review.log`.
- These results precede final proactive-repair policy and metric accounting refinements. E05c is not yet accepted or committed; final tests must cover the final candidate.
- Review required recovery from ordinary watch EOF via applied resumed events/bookmarks, while retaining denial/expired relist barriers; regression added by executor.
- Review required distinct actual API-call counters versus watch lifecycle events so synthetic repairs/closures do not inflate reported API request load.
- Quiet-source repair is being moved before source freshness expiry. Planned refresh may retain an already verified, bounded-age matching snapshot; genuine 410/403/API failures must remain explicit noncurrent states. No read-time timestamp renewal is allowed.
- No real delivery cache reduction is claimed yet: typed source read-count tests are prerequisites, not measurements of an activated cache producer or live agents.

## E05c source accepted — `2569bccd`

Final source prerequisite is committed and reviewed. Six tracked typed stores share the existing informer factory; PVC/StorageClass metadata duplicates are replaced. Store fingerprints gate relist application, projections are immutable, watch recovery/cancellation are explicit, and quiet sources repair proactively before freshness expiry. Review additionally found/fixed revival through a raw-current state after local-deadline grace expiry. Request counters now count actual LIST/WATCH calls separately from watch lifecycle events and synthetic repairs.

Independent final race checks on the accepted candidate:

- `go test -race ./internal/agent/observation ./internal/agent/delivery -count=1`: passed (1.055s / 2.964s); `/tmp/astronomer-030-e05c-final-packages.log`.
- Selected shared-observation tests plus existing cancellation/partial-bootstrap/heartbeat/backpressure/replay tests in `internal/agent`: passed (4.313s); `/tmp/astronomer-030-e05c-final-agent.log`.
- Executor full internal/cmd vet and dependency checks passed. Nine inherited complexity failures and comparison-doc failure remain; only touched subscriber ratchet lowered from 1037 to 1036.

Local fake-client evidence: ten direct platform probes use 60 typed LISTs + 30 controller GETs; six shared typed sources use six bootstrap LISTs + six WATCHs, then 600 snapshot reads issue zero extra API requests. These are different source-level exercises, not a before/after claim for activated delivery or live capacity. E05d must compare the same workload through the real delivery consumer.

E05d dispatched: consume sources in embedded/remote delivery, coalesce discovery/dynamic refresh, explicit partial/stale states, heartbeat margin and consumer instrumentation/tests. E06a concurrently prepares a standalone scoped Flux assignment cache on a separate branch; runtime wiring is reserved for later integration to avoid overlapping edits. Full plan remains open, including live performance, integrations and RustFS recovery.

## E06a assignment source accepted — `8d288c8f`

Reviewed the standalone cache, projection, shared snapshot extension, tests and architecture note in `astronomer-assignment-cache-030`. Five ownership-filtered Flux informers serve all accepted assignments; subscriptions and dirty IDs are bounded, unchanged subscription replacements preserve pending work without creating more, and immutable batch reads preserve source timestamps. No runtime consumer, RBAC expansion, mutation fallback or checkpoint change is included yet.

Reviewer `go test -race ./internal/agent/delivery ./internal/agent/observation -count=1` passed on the clean committed candidate (12.686s / 1.059s), log `/tmp/astronomer-030-assignment-root-review.log`. Executor package vet passed, and reconnect/projection race tests passed five repetitions. A preliminary in-progress reconnect test failed; the final committed package run passed after the test explicitly modeled unavailable reconnects. Review also required unchanged-subscription dirty-work preservation and exact condition-discriminator normalization; regression tests cover both.

Counting fakes cover 1/10/100 assignments: direct baseline 2N GETs per batch; ten cached batches use five initial LISTs/five WATCHs and zero GETs. This is source-level evidence, not activated-runtime or live-cluster qualification. E06b must integrate this source with negotiation, status scheduling and checkpoint/removal fences after E05d stabilizes.

## E01 telemetry follow-up in progress

Read-only exposure audit confirms embedded-agent metrics use the existing server registry and conditional server ServiceMonitor. Standalone agents expose `/metrics`, but scrape annotations and the agent Service do not prove downstream Prometheus collection; no agent ServiceMonitor/PodMonitor is present. Dashboard additions are being limited to correctly scoped embedded source freshness, tracked LIST/WATCH load, refresh duration/frequency and existing DB acquisition duration. Remote collection/aggregation and live scrape evidence remain unverified.

## E01 embedded observation dashboard accepted — `571d72dc` + `fc46913c`

Reviewed six new panels, render tests and runbook; main integration waits for E05d producer acceptance. New queries retain target identity and release scope. Tracked LIST/WATCH counts exclude lifecycle events and uninstrumented clients; DB duration measures all acquisitions, not just blocked waits. Review caught that sampled age/availability gauges could freeze while the server remained scrapeable: the revised producer contract adds sample and source timestamps, and panels expire producer samples after 60 seconds and current source evidence after four minutes. Source age grows from the preserved observation timestamp; unknown or missing observations remain missing data. These exact metric names are present in the in-progress E05d producer.

Independent `go test ./deploy -run 'TestApplicationDashboard|TestApplicationMetrics' -count=1` passed (1.591s), `/tmp/astronomer-030-telemetry-root-review.log`. Executor final Helm enterprise evidence reports all subgates passed and `source_tree_stable: true`: `/tmp/astronomer-verify-enterprise/20261006T053505Z-3037522-571d72dc3992/evidence.json`. That run used the stable pre-commit worktree subsequently committed as `fc46913c`; it is not a clean-commit run. An earlier run invalidated by concurrent edits is not acceptance evidence. Actual PromQL evaluation remains unverified because promtool is unavailable; live target discovery and remote agent collection remain pending.

## E06b integration design approved; implementation pending E05d base

The existing deployment status requires a nonzero legacy `observed_at` and lacks source metadata. Extend the unreleased negotiated observation contract with an optional per-deployment `observation`, preserving a separate assessment timestamp. Modern persistence uses source time or SQL NULL, never assessment time as a substitute for an unread source. Existing nullable `last_observed_at` and inventory JSON support this without a migration or generated SQL edits; publish metadata as `inventory.observation` through the documented API. Coalesced higher-sequence heartbeats must update source evidence without replaying transition, rollout or outbox effects. Retain all session, generation, digest and sequence fences.

Use the inventory marker from the same status pass to select modern cached versus legacy direct observations. Modern unavailable/denied/unsynced sources have no GET fallback. Synchronize subscriptions from accepted checkpoints, including partial reconciliation and removals, and verify cached identity/generation/digest against current accepted state before normalization. One source lifecycle belongs to the runtime context and survives management reconnects. Dirty notifications schedule bounded status-only work, including while waiting for a state response; they never request desired-state reconciliation per event. Periodic status passes remain necessary for expiry and application barriers.

Backend/runtime/protocol/API artifact work precedes a separate deployment UI patch. UI must distinguish source freshness from reported phase and must not imply existing server-side reported-phase counts/filters are freshness-aware. Live timing, concurrency and checkpoint qualification remain required after source integration.

## E05d shared consumer accepted — `99dccdf6`

Reviewed the final shared probe, discovery/dynamic refresh, CRD metadata invalidation, production composition roots, metrics, protocol bounding and tests. Modern remote and embedded delivery now use the shared typed source; legacy sessions retain explicit direct probes. Ten consumer inspections after informer sync produce zero recurring typed LIST/GET calls in local counting tests. Concurrent inspections coalesce one discovery refresh and five initial optional dynamic LISTs. This is activated source-path evidence with real informer machinery over fake clients, not a live k3s result.

Final review fixes include optional-component observation validation, sampling typed state after refresh I/O, certificate expiry across slow requests, Longhorn snapshot-count availability, bounded retained operator detail slices with exact-sized backing arrays, and deep-copying returned dynamic observation metadata. The heartbeat floor is one minute; cached source timestamps are not restamped. Timestamp metrics support dashboard detection of a stopped producer. Only the subscriber complexity ratchet decreases, from 1036 to 1017.

Independent final checks on clean `99dccdf6`:

- Full delivery and observation package race tests passed (2.708s / 1.068s), `/tmp/astronomer-030-e05d-root-packages.log`.
- Selected real-informer, composition, CRD metadata, bootstrap/cancellation, heartbeat, replay and protocol-negotiation agent race tests passed (4.792s), `/tmp/astronomer-030-e05d-root-agent.log`.
- Executor full internal/cmd vet and dependency checks passed. Full `go test -race ./internal/agent/...` cannot complete because an existing audit HTTP test attempts a sandbox-forbidden TCP listener; its delivery/observation packages pass. Nine inherited complexity violations and comparison-document failure remain.

Executor is integrating accepted E06a and telemetry commits into the isolated implementation branch before E06b begins. No live deployment, production efficiency, soak, or recovery acceptance is implied.

Combined clean base is now `ac8c4e6d`: E06a integrated as `345fb0ff`, dashboard as `a7086cc8`, dashboard expiry revision as `ac8c4e6d`. Executor combined race checks passed (delivery 12.162s, observation 1.061s), deploy render/metrics checks passed (1.807s; PromQL evaluation explicitly skipped), and full internal/cmd vet passed. Logs: `/tmp/astronomer-030-combined-{race,deploy,vet}.log`. E06b backend work is dispatched from this base in a new isolated assignment-runtime worktree. Deployment UI design is approved but waits for the generated schema milestone.

Reviewer independently reran `go vet ./internal/... ./cmd/...` on the combined base: exit 0, `/tmp/astronomer-030-combined-root-vet.log`.

## Offline generator prerequisite verified

Found exact pinned `sqlc v1.31.1` at `/root/go/bin/sqlc` and exact `oapi-codegen v2.5.0` in the Go build cache. E06b normal OpenAPI generation can use the supported Make `OAPI_CODEGEN` override; no generated files are hand-patched.

Reviewer copied the SQL generator inputs and outputs from clean `ac8c4e6d` into `/tmp/astronomer-030-sqlc-verify-m0a9qgfq`, ran pinned SQLC generation plus extension write/check, and compared every output file by SHA-256. Outputs match exactly; all 28 extensions are current, with no handwritten generated-package production files or GOOS-suffixed queries. Evidence: `/tmp/astronomer-030-sqlc-offline-verification.json`. This clears the uncertainty about SQL output drift on that base. It does not claim the full backend enterprise gate passed: its script still invokes the network-dependent `go run` path, and other gate/environment limitations remain.

## E06 API milestone and parallel follow-ups

Reviewed committed schema milestone `6d2561dc` from `ac8c4e6d`: source OpenAPI, embedded spec, generated frontend types and generated SDK add optional `DeliveryResourceInventory.observation`. Normal generation used `make openapi-generate OAPI_CODEGEN=/tmp/astronomer-030-oapi-codegen` with exact pinned v2.5.0. Omission covers legacy reports and local mutation decisions; it never proves fresh source evidence. Runtime/protocol/ingestion changes remain separate uncommitted implementation work at this milestone.

Deployment UI implementation is active in `astronomer-deployment-ui-030` from this schema, preserving reported-phase filter semantics and adding source-age presentation without new polling. The backend status scheduler is also being bounded by the remaining desired-response wait deadline so status collection cannot extend that wait indefinitely. Final committed review and integration are pending.

E04 audit found the existing loadtest and delivery scale commands synthetic/in-process, with no real assignment-tier collector. An isolated extension in `astronomer-real-estate-030` is implementing engineering-only collection of pre-provisioned member/assignment fixtures, bounded per-member metrics and explicit measured windows. It must not claim fixture creation, cold-start, churn, browser, reconnect, end-to-end freshness, or an 80% reduction from cache-only counters. Those unexecuted scenarios remain `NOT_RUN`; qualification remains false until full real-agent requirements are met. Standalone metrics exposure already exists; verified per-member collection and equivalent baseline attribution are still required.

## Deployment freshness UI accepted — `c278eaa0`

Reviewed nine scoped frontend files against schema milestone `6d2561dc`. Shared pure source-age logic preserves component behavior; deployment list/detail status, conditions and drift age on one existing clock per page without extra polling. Missing metadata is labeled source freshness unknown, including local mutation decisions. Modern missing source timestamps never fall back to receipt time. Filters and totals remain explicitly reported-phase based; historical events, actions and permissions are unchanged.

Reviewer independently ran the four observation/deployment/component/presentation suites: 51 tests passed in 4.45s. Executor full frontend suite passed 301 files / 1,905 tests in 201.52s (`/tmp/astronomer-030-deployment-ui-tests.log`), with zero-warning lint, typecheck, formatting and diff checks passing. Nine inherited complexity violations remain unchanged. Candidate is committed and clean; integration waits for backend acceptance. No live-wire or browser performance qualification is implied.

## E06b backend accepted — `90334c48`

Reviewed the immutable 22-file backend candidate on schema `6d2561dc`. Both composition roots now use the shared constructor; one assignment cache belongs to the runtime lifecycle, independent of tunnel reconnects. Modern status passes batch cached assignments without direct GET fallback, verify accepted assignment identity, preserve oldest dependency source time, and report noncurrent observations without cached health. Status-only dirty scheduling is bounded and serviced during desired-response waits under the remaining wait deadline. Subscription synchronization includes partial applies and deletion outcomes.

Protocol validation, digest stripping and the final-write negotiation guard include deployment metadata. Ingestion persists source time or NULL and exposes metadata through inventory JSON. Coalesced newer-sequence reports refresh source evidence under existing fences without replaying transition, rollout, deletion, outbox or ack effects; the existing readiness-repair callback remains intact. No SQL migration or query change was needed. Only the touched runConnect complexity ceiling decreases, 366 to 363.

Independent full race checks passed: delivery 14.254s, observation 1.084s, status 1.077s, delivery handler 1.334s, protocol 1.051s (`/tmp/astronomer-030-assignment-runtime-root-race.log`). Executor full internal/cmd vet and focused real-informer/composition/negotiation agent tests passed. Exact pinned cached SDK generation reproduces the checked-in output. The official backend enterprise gate still stops at its hardcoded network-dependent SQLC invocation; this is not a full-gate pass. Integration of schema, backend and accepted UI is authorized on the isolated implementation branch.

Review confirmed a pre-existing checkpoint acknowledgement issue: runtime advances snapshot generation/ETag/credential epoch before Save, so a failed Save can leak an undurable acknowledgement into subsequent status or state requests. A separate bounded follow-up is authorized to stage headers for Save and publish them only on success, preserving actual in-memory assignment/deletion state and partial retries. It requires failed-save/status/request/successful-retry regressions; the cache integration does not silently change this behavior.

E04 draft review caught two collector issues before acceptance: normal measurement expiry canceled its own in-flight requests/scrapes, and metric endpoint identity was not independently bound to the declared member. Both require fixes and regression tests before the collector can be accepted. Engineering-only qualification restrictions remain unchanged.

Accepted schema/backend/UI integrated cleanly on `implement/030-efficiency` as `acea1ac3`, `6aa9a1ee`, and `9babb64a` respectively. The original user branch remains untouched.

## Durable checkpoint acknowledgement follow-up accepted — `861c00fd`

Two-file follow-up stages snapshot generation, ETag and credential epoch in the checkpoint passed to Save and promotes them only after success. Actual applied assignment identities remain available in memory for safe retries. Reviewer read the full patch and independently ran the failed-save/status/request/successful-retry regression under the race detector: passed, 1.122s. Executor full delivery race passed (15.099s, `/tmp/astronomer-030-checkpoint-ack-race.log`), full internal/cmd vet passed (`/tmp/astronomer-030-checkpoint-ack-vet.log`), and whitespace checks passed. Nine inherited complexity findings remain unchanged. Integration is authorized on the isolated implementation branch.

The durable-header fix integrated as `4ba2df65`.

## E04 engineering collector accepted — `6b44a726`

Reviewed the 20-file increment and its existing loadtest integration. Strict manifests identify 2–10 adopted remote members, assignment tiers of 1/10/100 per member, scoped deployment IDs/generations/digests, resource census and frozen environment hashes. The read-only collector verifies public API identities and scoped assignment sets; complete source-fresh rendered inventories can establish namespace membership through independent resource GETs. Legacy/truncated/missing evidence stays unavailable. Check-only validation performs no network or credential access.

Per-member metrics retain bounded series, resets, missing samples, source ages, resource data and coverage. Every metrics origin must independently identify the declared cluster through the existing health endpoint, with target-specific credentials and no redirects. Measurement scheduling and bounded draining are separate; completed samples outside the measurement window do not become fabricated errors or in-window evidence. Root and independent reviewer confirmed both draft findings were fixed with regressions.

Reviewer `go test -race ./scripts/loadtest -run TestRealEstate -count=1` passed (8.224s, `/tmp/astronomer-030-estate-root-review.log`). Executor selected new/existing socket-free race tests passed (13.308s, `/tmp/astronomer-030-estate-final-race.log`), full loadtest/internal/cmd vet passed, and real CLI check-only passed. Full package race still encounters an inherited forbidden httptest listener. Documentation check retains the inherited comparison-contract failure; complexity retains nine inherited findings with only the touched main.go ceiling lowered 831→813.

Integrated only the collector commit as `d27cf165` on the implementation branch; its schema prerequisite was already present. The working tree is clean. Reports deliberately remain unqualified: this increment does not implement fixture lifecycle, churn, browser/multiple-panel/two-tab journeys, reconnect bursts, end-to-end freshness, or matched historical request attribution. Those requirements retain NOT_RUN status and E04 remains incomplete.

Further source-gap review identified attributable Kubernetes request measurement and browser performance evidence as remaining offline work, distinct from the blocked live runs. Existing tracked informer counters cannot prove total delivery-induced LIST reduction or assignment GET absence; exact instrumentation and baseline comparability must precede those claims. No speculative DB, worker or sizing changes are authorized by absent measurements.

## Browser fixture observation parity accepted — `04fef9f7`

The existing browser fixture constructed a direct delivery runtime, bypassing the optimized shared-source and negotiated-freshness path. It now constructs one metadata-backed StateSubscriber, injects that source and the dynamic client into its probe, uses the shared observed-runtime constructor, and supplies the tunnel negotiation/retry callbacks. A fixture-local lifetime helper cancels and joins the public subscriber, mirror and delivery Run boundaries on tunnel exit or runtime failure. The delivery runtime owns its assignment cache; existing mirror internals retain their cancellation contract. Synthetic CIS/Loki edges and seeding are unchanged, so this is still not an actual production estate.

Root reviewed the complete three-file patch and independently ran scoped race tests: passed, 1.208s. Executor scoped race (1.188s), vet and whitespace checks passed. Integrated cleanly as `af3b9e98` on the implementation branch.

## Further measurement implementation in progress

E01 request-attribution design is approved in a separate worktree. Instrument the existing agent REST-config/client family with bounded consumer/operation/resource/outcome labels. Tag actual typed/Flux informer callbacks because client-go's stop-channel factory start loses context values; tag direct inventory and assignment reads at their consumer boundary. Legacy discovery uses context.TODO internally and therefore needs a dedicated discovery-only client with a fixed inventory classification. Preserve existing clients, watchers, credentials and RBAC. A tiny schema sentinel can establish installation of this instrumentation family, not complete process-wide coverage. Wrapper/retry/deduplication tests are required; request counts mean observable RoundTrip attempts, not watch events or unobservable transport internals. Matching historical baseline instrumentation and live coverage remain necessary for reduction/zero-GET claims.

E07 engineering browser measurements are approved as a separate test-only artifact alongside the existing benchmark recorder. The closed Rancher schema and product behavior remain unchanged. Required evidence includes sanitized fixed-group request counts, completed/failed/unfinished window accounting, resource byte coverage, bounded action-to-DOM latency samples, supported rendering/heap proxies with explicit missing reasons, and existing live journeys including scope changes, two tabs, fallback and denial. New thresholds, React-render claims, total browser-memory claims and qualification by source tests are prohibited. Actual matched live/browser runs remain pending.

E04 measured workload phases are also approved in a separate source increment: explicit idle, namespaced browsing, scoped delivery inventory/list/detail, and the actual namespace/type resource-search API. Reuse the existing scheduler with deterministic member/assignment coverage, independent per-phase warmup/measurement/drain accounting and metrics. Preserve the historical synthetic/header-latency contract; add estate-only full-response/body-validation accounting so a truncated HTTP 200 cannot count as success. Each request must have one terminal outcome even when multiple diagnostic categories apply.

Search has no cluster/project filter and fans out to all authorized active clusters. Initial search support must independently prove self/superuser credentials and exactly census a separately declared bounded search target set, including local only if declared. Restricted-user cluster-list permissions cannot be equated with search permissions. Repeat census/permission checks, reject target changes, validate partial/error/truncation results, and retain the documented non-atomic snapshot limitation. No API enhancement, secret-resource search, fixture mutation, real-agent reconnect, or live benchmark is included in this increment; the full remaining requirements stay open.

## E01 attributable request instrumentation accepted — `99c6734f`

Reviewed the 22-file transport helper, classifier, production/fixture composition, consumer tags, tests and documentation. Copied configs preserve caller state, prior wrapper order, token-file authentication and rate configuration; nested wrappers count only the innermost observable attempt. Discovery-specific wiring preserves the old discovery limiter and HTTP timeout policy. Review caught both generated-limiter and discovery-default-timeout differences before acceptance; regression tests cover them. Callback-local tags survive client-go's stop-channel informer startup. Runtime observation GETs are distinguished from other executor reads.

The bounded counter and v1 instrumentation sentinel are exposed through existing metrics paths. No additional typed/dynamic clientsets, informers, privileges or endpoints were introduced. Unknown paths and unclassified traffic remain explicit; the sentinel proves installation of the stated transport family only. Collector/dashboard adoption and a matching historical instrumentation backport are still required. These counters alone do not establish live LIST reduction or GET absence.

Root independently ran full race suites for kuberequests (1.158s), delivery (15.934s), observation (1.773s), and the browser fixture (1.888s), plus selected actual-client production wiring/informer/composition races in agent (1.410s) and server (1.277s). Logs: `/tmp/astronomer-030-kube-metrics-root-packages.log` and `/tmp/astronomer-030-kube-metrics-root-composition.log`. Executor final race suites, full internal/cmd/fixture vet and dependency-boundary checks passed; nine inherited complexity findings and the comparison-document failure remain. Integrated only this accepted commit as `c8428f76`; implementation tree clean, original user branch untouched.

Historical baseline preparation is under read-only review. It must identify a real pre-cache source revision, apply the same measurement layer without importing optimization behavior, and retain metadata/coverage limitations. Forcing a candidate into legacy negotiation is not a historical before/after baseline.


## E04/E07 reviewed measurement increments integrated — 2026-10-06

Implementation branch `implement/030-efficiency` is clean at `f5095fc65f87052340ee397b56db8e6daf496c83`. Original user branch remains untouched.

- Browser engineering candidate `9aa03137` integrated as `ecde43e9`. Root inspected the final completed-body/fixture-row readiness assertions and independently ran both instrumentation suites: 20 tests pass (`/tmp/astronomer-030-browser-root-review.log`). Executor full suite before final readiness changes: 303 files / 1,924 tests passed; final focused tests/typecheck/scoped lint/format and eight-test discovery passed. Browser artifacts are opt-in, bounded, sanitized and explicitly not qualification. Chromium/live execution remains unavailable.
- Estate phase candidate `358e7fa5` integrated as `f5095fc6`. Root reviewed full-window histogram correction; independent reviewer found no blocking lifecycle, search-scope, accounting or synthetic-mode regression. `go test -race ./scripts/loadtest -run 'TestRealEstate|TestWorkloadTarget' -count=1` passed in 15.345s. Executor scoped race/vet also passed. Idle/resource/delivery/search phases now preserve warmup and measurement boundaries, validated response completion and achieved-work counts. Histograms count all completions and report conservative p99 bucket upper bounds; overflow is unavailable, never clamped. Search membership remains snapshot evidence, not atomic fencing. Full listener-dependent suite remains blocked. No live measurements.

## Historical measurement baseline prepared — 59b16ca4

Separate branch `implement/030-baseline-metrics`, historical parent `a72cb8e7f8a41cc11b2cdbde703479d9543e8335`, immutable measurement-only patch `59b16ca4dd953fb2f1366201a5be92885942e973`. This baseline must never be integrated into the candidate branch. Root reviewed production adaptations and independently passed helper/delivery race tests (1.109s / 3.100s, `/tmp/astronomer-030-baseline-root-race.log`). Existing direct reads and timing contracts are preserved; absent modern observation/freshness evidence remains absent.

Agent/server artifacts in `/tmp/astronomer-030-baseline-build/` have a manifest, source patch and SHA256SUMS. Root independently checked both hashes and `go version -m`: exact clean patch revision, Go 1.26.6, standard linker flags, version 1.2.0. Agent SHA256 `26c2b9845353a3606fbd957cd4de3d17e2c924aff8f42c1aa360fbe70425b091`; server SHA256 `9419b38cb30a88759eb94300a778cae756fd3a1745efe82f545c0ac798d075ee`. Manifest initially says built_pending_review; this ledger records completed source/artifact review. These are local binaries, not OCI images or deployed provenance.

Next source increment: adopt actual Kubernetes transport counters into the existing collector with strict labels, bounded series, reset/missing-series accounting and separate descriptive transport evidence. Preserve all existing freshness requirements and overall unqualified status until required workloads/drills execute. E00–E11 remain open wherever live evidence or further implementation is missing.


## E11 pinned candidate local builds reviewed — f5095fc6

Both agent and server compiled from clean detached `f5095fc65f87052340ee397b56db8e6daf496c83`. Root independently ran `sha256sum -c SHA256SUMS` and inspected `go version -m` for both binaries: exact source revision, `vcs.modified=false`, Go 1.26.6 and standard linker flags. Candidate uses the same platform, version 1.2.0 and deliberately frozen build timestamp as the historical baseline. Manifest and binaries: `/tmp/astronomer-030-candidate-build/`.

Agent SHA256 `1e3d5440b73d9801901dffad9495c0adc82d661218c44711e02608c1e076a758`; server SHA256 `b38f98b48e684ac83f0ed2c58da7576f2b49b9af5fed07a1cd8010af30308d38`. Manifest initially says built_pending_review; this ledger records root artifact review. This is a pinned intermediate candidate, not the final plan result. OCI artifacts, deployment and live qualification remain NOT_RUN.

## E04/E11 mutation adapter gap verified

Root independently read `scripts/run-delivery-resilience-drill.py`. Existing cleanup is process-local callbacks; scale restoration lacks UID guards and persisted original state, and created policy/job cleanup is not crash recoverable. A separate executor is extending this existing runner with durable intent/receipts, exact resource and namespace identity checks, server-enforced mutation/delete preconditions, reverse-order resumable cleanup and hermetic crash/conflict tests. No live mutation is authorized by this source-only task.

After that prerequisite, controlled annotation changes can correlate exact opaque resourceVersion with `cluster.k8s_changed` SSE, measuring mutation-to-server observation (not UI render or delivery convergence). Real reconnect requires a genuinely disposable owned agent and independent restoration access; an existing installed agent must not be relabeled to manufacture ownership. Source inspection identifies these paths, but no event correlation/reconnect implementation or execution is yet accepted. Full E04/E11 scope remains outstanding.


## E01/E04 transport collector accepted — cca49b67

Candidate `cca49b67` integrated alone as `fc818e96daacfb73db4d350de5dbef359af8016b`; duplicate local phase parent excluded. Root reviewed reducer, strict parser, source wiring, tests and documented semantics, then independently passed `GOCACHE=/tmp/astronomer-030-go-cache go test -race ./scripts/loadtest -run 'TestRealEstate|TestWorkloadTarget' -count=1` in 15.601s (`/tmp/astronomer-030-estate-transport-root-race.log`). Executor focused race 16.565s and scoped vet passed. Nine inherited complexity findings unchanged; listener-dependent full suite remains unavailable.

Collector now accepts the exact instrumented request family and v1 installation marker with fixed bounded labels (2,520 transport series plus two markers, independent of legacy 256-series bound). Per-series, per-consumer/operation and total-family evidence distinguishes observed adjacent increments from complete first-to-last sampled deltas. Late-born/disappearing/reset series, scrape gaps, invalid markers and observed process restarts cannot produce complete deltas. Process-start timestamps remain sampled evidence, not collision-proof identities; family installation is not all-client coverage. All-failed and failed-final-scrape paths retain unavailable/null deltas. Missing historical source freshness still fails existing freshness checks. Manifest reads now bound allocation before decoding, and schema diagnostic correctly names v2.

No comparison or ≥80%/GET-zero acceptance is claimed. Offline comparison design is next; live matched windows, repeated execution, all-member behavior, freshness, churn/reconnect and soak remain required. Core agent/server binaries previously built from f5095fc6 remain pinned intermediate artifacts; this collector-only commit is not silently substituted into their provenance.


## Live access rechecked during adapter review

`kubectl --kubeconfig=/etc/rancher/k3s/k3s.yaml --request-timeout=5s get --raw=/readyz` again fails with `dial tcp 127.0.0.1:6443: socket: operation not permitted`. `docker ps --format '{{.Names}}'` again fails with permission denied on `/var/run/docker.sock`. These current checks confirm the environment restriction, not cluster or RustFS health. Do not substitute default kubectl localhost:8080 behavior for the configured k3s endpoint.

Source work continues independently: offline comparison implementation and durable drill cleanup. Root review identified and requested corrections for read/write restoration races and destructive replacement of failed-run history during cleanup resume. Successful cleanup must remain separate from scenario success. A stable same-host lock location must not change with TMPDIR. All changes remain unaccepted until final immutable review and tests.


## E04/E11 durable mutation recovery accepted — 464ba4ee + 6311b8dc

Paired ledger commits integrated as `5a862ea6` and `2ad627deb55575e248c8855c569ef9f54904d68c`; implementation worktree clean. Root inspected the complete runner/ledger and final UID-receipt correction, then independently passed all 25 hermetic tests (`/tmp/astronomer-030-drill-ledger-root-tests.log`). Executor also passed three existing scale-evidence tests, Python compilation, example validation and docs checks; nine inherited unrelated complexity failures remain.

Existing resilience runner now persists private narrow mutation intent before writes, uses server-enforced UID/resourceVersion preconditions, records successful create response UID before verification, refuses pre-existing or replaced objects, and restores in reverse order with explicit unresolved outcomes. Same-host recovery uses a fixed `/tmp` private namespace-identity flock plus owned create-only ConfigMap; no distributed lease or cross-host recovery is claimed. Cluster identity uses kube-system namespace UID, with explicit required read permission. Extra ConfigMap permissions are documented. No full resource bodies, Job templates, credentials or arbitrary error text are retained.

Root-review corrections: do not overwrite a field changed between initial and guarded read; retain original failed/interrupted scenario history on cleanup resume; make lock identity independent of TMPDIR; capture returned UID rather than adopting a later same-name replacement. Ambiguous timeout/no-response creation can reconcile exact persisted nonce/ownership, but cannot rule out a replacement that copied that nonce before any UID receipt; docs state this limitation. Cleanup success is independent of scenario success and release_eligible remains false. Restored spec fields do not prove application/session recovery; deleting a Pod and process restart history are irreversible. No live drill ran.

Next bounded adapter design is a controlled metadata annotation change correlated by exact opaque resourceVersion with authorized `cluster.k8s_changed` SSE. It must prove context-to-cluster identity, subscribe before mutation, preserve returned receipts and restore via this ledger. It can measure mutation-to-server observation only; UI rendering and delivery convergence remain separate missing evidence.


## Real fixture lifecycle audit and scope decision

The existing offering runner creates then cleans one compiled lifecycle case; it has no retained tier/export mode. The delivery CLI exposes supported target create/apply, rollout, detail and fenced delete operations, while the read-only estate collector requires explicitly pre-provisioned assignments. Existing W11 validation has a different complete-matrix contract and must not be relabeled as E04.

Public target creation has only process-local 60-second idempotency caching, not a durable creation receipt. A lost create response or crash before local receipt cannot safely establish ownership by name. Any future automated bridge must record UNKNOWN and refuse same-name adoption/deletion; rollout and target-delete durable replay are different contracts. A preview proves placement, not rendered confinement. Pre-reviewed immutable bundles, renderer namespaces, expected object identities and collision checks remain necessary before live fixture preparation.

No generic provisioning subsystem or production idempotency rewrite is authorized by this audit. Plan E04 can use reviewed fixtures prepared through existing public CLI/API owners, with explicit created-ID and cleanup accounting during actual execution. The source-only audit is not lifecycle evidence; fixture preparation/cleanup, real member adoption, immutable artifacts and retained readback remain unexecuted. Source priorities are finishing the comparator/observation adapter and applicable final verification; live inputs must be resolved on the actual estate.


## E04 offline comparison accepted — 95ea74e0 + 0762a5ff

Integrated as `fc5d6f18` then `b46b561dff64a95485736b2c7faeab3c2d0547a3`; implementation tree clean. Root reviewed final input validation, reconstruction, workload/provenance fences, CLI dispatch/relocation, artifacts and docs. Independent reviewer passed estate/scheduler race tests 15.035s and new comparison tests 17.612s (`/tmp/astronomer-030-compare-independent-race.log`, `/tmp/astronomer-030-compare-independent-new-race.log`). Root then independently passed final follow-up comparison race tests 18.173s (`/tmp/astronomer-030-compare-root-followup.log`). Executor vet passed. Nine inherited complexity failures remain; main.go's obsolete exception was removed after flag parsing moved to existing config.go, without raising limits. Inherited Rancher comparison documentation semantic failure remains.

Existing loadtest entrypoint now has offline-only baseline/candidate comparison, before any online authentication/profile/provisioning setup. Explicit empty comparison flags are rejected rather than falling through online. Reports strictly bound/validate input, reconstruct complete transport deltas and aggregates, show phase versus member work, include shared observation in combined cost and retain all-family cost including other. Exact catalog, VERIFIED member source evidence, matching workload, sampled-window boundaries, producer/count consistency, attribution and reviewed-image provenance control eligibility. Descriptive numbers are distinct from eligibility; qualified is always false and no 80% acceptance/GA award occurs automatically.

Review corrections included shared-cache cost omission, repeated fleet work mislabeled as member work, incomplete member/catalog/source proof, short sampling windows, mismatched producer/sample accounting, unusable logical checksum names, and the explicit-empty-flags online fallthrough. Checksums now resolve actual output basenames directly. Historical callback relabeling and missing freshness remain blockers, never smoothed into savings. Actual before/after files, repeated live measurement and all required drills remain NOT_RUN.


## E04/E11 exact annotation-to-SSE observer accepted — f9bd4b26

Integrated as `754ac3d760023506e27a5aa46bfdbe598360bbd0`. Root reviewed final transport/deadline handling, guarded mutation receipts and restoration, exact resourceVersion correlation, public error sanitization, source fingerprint and runbook. The source digest is a canonical filename-to-file-SHA256 map, preventing ambiguous concatenation; cleanup resume preserves original run provenance.

Root independently passed all 25 ledger tests and 25 of 26 observer tests. The remaining observer test fails at AF_UNIX socketpair `sendall` with `PermissionError: Operation not permitted` under root's restricted execution profile, before exercising the assertion. Executor reported all 51 tests passing in its default broader environment; this is separate evidence, not a root full-suite pass. No retry through broader permissions was requested. Executor also reports three scale-evidence tests, example validation, compilation and docs checks passing.

Observer checks direct/proxy namespace UIDs, requires a settled owned Deployment, subscribes before a top-level metadata patch, persists the authoritative returned resourceVersion and matches that exact opaque version in SSE. Guarded restoration uses the existing durable ledger even after missing receipts. Trials include failures and unattempted repetitions; p95 requires at least 20 requested trials all succeeding. Transport limits and cancellation retain socket ownership across TLS/Connection-close; uncancellable OS DNS workers are explicitly documented and cannot create late connections. This measures dispatch-to-SSE including kubectl overhead, not UI rendering, delivery convergence or complete scale/churn qualification. No live observation occurred.


## Final combined regression and inventory refresh

Code-health and operation-task inventories refreshed in isolated candidate `3a04f137`, reviewed in full by root and integrated as `d1d4875abffb678d3019504a2a523e79efe59378`. Only two generated documents changed: current counts/references, duplicate candidates 37→38 and dead candidates 5→4. Both documented checks passed with zero hard failures; no budgets or ratchets changed. Tree frozen clean before authoritative gates.

Combined regressions at parent `754ac3d7` (only generated documentation changed afterward):

- Race PASS: agent delivery (17.502s), Kubernetes request instrumentation (1.374s), shared observation (1.165s), delivery status (1.084s), compatibility (1.132s), delivery handler (2.351s), protocol (1.178s). Full agent package fails at `TestHTTPAuditSenderPostsBatchWithBearer` because TCP listeners are forbidden. Log `/tmp/astronomer-030-final-core.log`; overall command exit 1, not a full pass.
- Browser fixture race PASS (1.479s), `/tmp/astronomer-030-final-fixture.log`.
- Observability PASS (7.645s); Charlie, contract and fakebridge suites fail at httptest listener creation. `/tmp/astronomer-030-final-observability-charlie.log`, overall exit 1.
- Full loadtest suite fails at `TestScrapeOnceCapturesProcessLeakMetrics`, TCP listener permission denied. `/tmp/astronomer-030-final-loadtest.log`, exit 1. Previously recorded scoped comparison/collector race successes remain separate.
- `go vet ./internal/... ./cmd/... ./scripts/loadtest ./scripts/testdata/live-browser-fixture` PASS, exit 0, `/tmp/astronomer-030-final-vet.log`.

No socket-dependent failures were suppressed or rerun through a less restricted agent. No live/scale/recovery qualification follows from these results.


## Final authoritative gates — frozen d1d4875a

- Helm scope PASS, source clean and tree stable: `/tmp/astronomer-030-final-gates/helm/20261006T072937Z-3-d1d4875abffb/evidence.json`. Chart input/dependency contract, lint, development/production renders and complete deploy tests (43.273s) passed. This does not execute PromQL without promtool.
- Backend scope FAIL at pinned SQLC download because DNS sockets/network are forbidden. Formatting, 65-script shell checks, 70 migrations, migration policy fixtures and destructive-table governance passed before that stop. Evidence: `/tmp/astronomer-030-final-gates/backend/20261006T072936Z-3-d1d4875abffb/evidence.json`, clean/stable source. Previously recorded cached exact generator comparisons are separate evidence; authoritative backend scope did not pass.
- Direct `go build ./...` initially failed VCS discovery in the nested worktree. With explicit `GIT_DIR=/root/astronomer-all/astronomer/.git/worktrees/astronomer-efficiency-030` and `GIT_WORK_TREE=/root/astronomer-all/astronomer-efficiency-030`, full build PASS (no disabling VCS stamping). Log `/tmp/astronomer-030-final-go-build-explicit-worktree.log`.
- Both `make local-ci-pr-representative` and `make local-ci-pr` FAIL during runner installation, before any workflow execution. Used npm offline mode and a writable `/tmp` cache under the existing network restriction; required zod tarball unavailable (`ENOTCACHED`). Logs `/tmp/astronomer-030-final-local-ci-representative.log` and `/tmp/astronomer-030-final-local-ci-full.log`. No workflow matrix pass is claimed.
- Initial simultaneous enterprise attempts shared a timestamp/PID-derived output directory because each sandbox PID was 3. Their interleaved tool rows produced false prerequisite failures; those artifacts are invalid. Reran with distinct `VERIFY_ARTIFACT_DIR` roots per scope, preserving the actual commands and correct source binding above.
- Captured runtime is Node v22.22.1/npm9.2.0; repository expects Node24.21.0. The manifest-alignment check verifies declared versions, not the running executable. Any frontend results here must retain this runtime mismatch limitation. No dependency upgrade/install or gate bypass was used to hide it.


Frontend authoritative scope at frozen `d1d4875a` FAILS after code-health (including generated client/inventories), lint and type-check all PASS. Formatter test harness fails `spawnSync git EPERM`; root reproduced the precise cause by invoking the test file directly, without changing permissions. Evidence `/tmp/astronomer-030-final-gates/frontend/20261006T072937Z-3-d1d4875abffb/evidence.json` records clean/stable source. Diagnostic `/tmp/astronomer-030-final-formatter-diagnostic.log`. Unit/build follow-ups are separate checks, not a successful enterprise scope.


Frontend follow-up production build and CSP checks PASS; bundle-budget self-test and all three eager closure budgets PASS (466 chunks; bootstrap 326,314 gzip bytes, login 354,410, app 425,197). Build output is in the tool transcript; bundle output `/tmp/astronomer-030-final-frontend-build.log`. Generated route tree/source remains unchanged, verified clean by git afterward.

Full `npm test` did not complete: it reported failures in paged-selection and Charlie shell plus skipped Charlie route tests, then ceased producing progress. Root interrupted it (exit130), retaining `/tmp/astronomer-030-final-frontend-unit.log`; no full-suite pass or blanket environment-only failure attribution is claimed. A bounded, isolated one-worker rerun of paged-selection passed all11 tests in14.78s (`/tmp/astronomer-030-final-frontend-failure-isolation.log`). This distinguishes a nonreproduced failure under reduced concurrency from a confirmed source regression; it does not erase the incomplete full run.


Bounded Charlie one-worker rerun completed normally (exit1) in65.20s: 27/28 tests passed; the closed-drawer Charlie shell test timed out at its existing5000ms limit. Charlie route file passed. Log `/tmp/astronomer-030-final-frontend-charlie-isolation.log`. These Charlie source/test paths and Vitest config have no diff from deployed-source baseline14341d1b to current HEAD. This is an unresolved final verification failure, not proof of a new Plan030 regression or permission-only failure. Do not increase timeouts, weaken isolation, or change unrelated product behavior to manufacture a pass. Recheck under the supported Node24.21 runtime and representative runner resources; diagnose any reproducible failure there.

## Handoff boundary — implementation paused, plan not complete

Final implementation HEAD `d1d4875abffb678d3019504a2a523e79efe59378`, branch `implement/030-efficiency`, clean. Original user branch `feat/031-ui-refinement` preserved. No push, deployment, bucket creation, Secret creation, backup or restore occurred. Available source work/review and combined checks above are complete for this handoff, but the entire E00–E11 plan is not complete.

The same k3s/Docker/network restriction has persisted across multiple goal turns; repeated retries cannot produce live evidence. Resume requires an execution environment authorized to reach the existing k3s/Docker estate and required verification dependencies. First re-establish live deployed provenance, RustFS reachability/version/ownership and telemetry; execute the approved separate-bucket dev worksheet. Then perform the reviewed real-member baseline/candidate windows, integration repairs supported by current evidence, freshness/fault/reconnect/browser measurements, isolated recovery, canary/soak and rollback proof. Retain Plan016 release authority. Off-host production recovery remains explicitly deferred, not an extra prerequisite imposed on the user's dev choice.

Verification also remains open for authoritative backend/frontend and LocalCI, socket-dependent suites, supported Node runtime/full frontend unit suite (including the recorded Charlie timeout), PromQL execution and actual browsers. Helm, source compilation and the scoped successes above do not replace those checks. This status is BLOCKED rather than complete; no efficiency percentage, live health, recovery success or rollout readiness is awarded.


## 2026-10-07 consolidation and local deployment

The user authorized worktree cleanup, consolidation, local deployment and functional validation, with PR decisions deferred. The network restriction from October 6 is no longer present. No branch was pushed and no PR was created, updated or merged during this work.

### Source and cleanup

- Active checkout: `/root/astronomer-all/astronomer`, branch `integrate/030-031-local-k3s`.
- Combined Plan 031 tip `6099df97`, Plan 030 tip `d1d4875a`, and the previously deployed offering-runner lineage `14341d1b`. Merge commit `94bac627`; integration fixes `be148222`; test fixes `2619df28` and `b341ac35`; contract fix `1322c98e`; optional browser-runner correction `66653597`.
- Archived all **43 secondary Astronomer worktrees**, including dirty/untracked/ignored contents, before removing them. Only the primary checkout remains. Removed **65 integrated or patch-equivalent local branches**. Other repositories were left alone.
- Recovery archive: `/root/astronomer-all/.worktree-archives/20261007-consolidation` (private permissions). Verified Git bundles retain original refs; individual compressed worktree archives have recorded SHA-256s. `removed-worktrees.json`, `branch-cleanup.json` and `preserved-branches.json` map every action and original tip.
- Eight tips remain under `archive/20261007/`: `feat/031-6b-a`, `feat/031-6b-c`, `feat/031-6b-d`, `feat/031-cc-gap-report`, `feat/031-p4`, `implement/027-api-workflows`, `implement/027-cr-scope`, and `implement/030-baseline-metrics`.
- The 031 alternatives overlap later integrated P4/P6b implementations; the gap report predates the final 031 measurements. The 027 branches overlap later catalog/workflow fixes; the 030 baseline is a deliberately separate measurement control. They have non-equivalent commits, so their original tips were retained rather than silently discarded or indiscriminately merged. Archived screenshots and scratch tests are not claimed as newly integrated product changes.
- Active branches are now `main`, the existing `feat/offering-functional-runner` PR branch, and the integration branch. Main remains `f87b189f`. Existing PR #46 remains draft/open; this integration has no PR.

### Integration corrections

The UI branch used migration 067 for saved views, while the deployed offering branch already used migrations 067–070. Saved views now use **071**, and chart/compatibility/schema checks agree. No existing migration was rewritten on the live database.

Merge conflict resolution preserved both the refined UI and offering form/preview/review behavior. Oversized units were extracted without raising complexity ceilings. Generated SQL/OpenAPI artifacts were refreshed. Verification also found and corrected an invalid queued-operation fixture, an accessibility header mismatch, audit ownership assertions after extraction, a concurrent test-fixture map race, and an OpenAPI positive-number constraint incompatible with the schema validator. The constraint retains strict positivity and works with the pinned generator.

The optional browser engineering suite is now excluded during discovery unless `LIVE_BROWSER_ENGINEERING=1`; the live runner records an explicit `not_run` artifact otherwise. Enabled discovery contains eight journeys. Its delivery-inventory assertion now follows the actual `controller_inventory.system_components` envelope. These are test-only corrections, not evidence of a completed performance run.

### Deployed identity and preservation

- Endpoint: `https://astronomer.dev.alphabravo.io`.
- k3s node: `astronomer-dev-1-mj`, Kubernetes `v1.35.7+k3s1`.
- Astronomer Helm revision **225**; monitoring Helm revision **6**. Both report `deployed`.
- Seven images built from clean commit **`1322c98e7914427fb094f33cdc156ae285ed0f11`**, version **`1.2.0-local.1322c98e`**, imported into local k3s and pinned by digest. Subsequent changes are test-runner/documentation only. Exact digests and source-tree hash are in the private evidence below.
- Builds used Go 1.26.6, Node 24.21.0, the repository runtime Dockerfile stages for Go binaries, and the original frontend/shell/DR Dockerfiles. Host Go builds were CGO-disabled and stamped with source identity; this is local deployment evidence, not a release build attestation.
- Database schema **71**, dirty **false**. `/health` and `/readyz` pass. Frontend, server and worker are 1/1; the local agent and delivery inventory report the new version.
- Six pre-existing non-Helm Secret data maps, both PVC identities, and the Grafana proxy key were verified unchanged. PostgreSQL and Redis were preserved.
- A pre-upgrade PostgreSQL custom-format backup was restored into a temporary verification database; schema and core record counts matched. Only that verification database was dropped. This is a local database recovery check, not a management-plane/off-host DR qualification.
- Grafana's unavailable proxy image was replaced through the existing Helm release; its pod is 3/3 and authenticated proxy health reports `database: ok`.
- Existing ServiceMonitor/rule/dashboard wiring was enabled for this deployment. Prometheus reports the new server and worker targets `up`; PromQL queries return 1 for each. Observation timestamps and request/cache metrics are emitted.

### Verification completed

| Check | Result and scope |
| --- | --- |
| Frontend enterprise gate | PASS at `be148222`: 2,307 unit tests, lint, types, production build, budgets, zero npm audit vulnerabilities. Product frontend source is unchanged in the final image apart from build stamps. |
| Full Go tests and full race suite | PASS at `b341ac35`, including the fixed concurrent fixture. The backend invocation subsequently stopped on the OpenAPI schema issue; that failed invocation is retained as failed evidence. |
| API-contract enterprise gate | PASS at `66653597` after the schema and optional-test fixes: build/vet, API package tests, generated contracts, documentation/complexity/dependency checks, route/error/security contracts and zero-unowned-quarantine policy. This completes the failed contract portion; it is not relabeled as a fresh full backend invocation. |
| Helm enterprise gate | PASS at `b341ac35`: lint, renders and chart contracts. Chart source is unchanged afterward. |
| Real PostgreSQL integration | PASS: 21/21 tests, including saved-view concurrency on schema 71. |
| Focused fixture race regression | PASS: 25 consecutive concurrent-replay executions under the race detector. |
| Browser-runner selection | Disabled: no engineering tests collected; enabled: eight collected. Type-check, ShellCheck and quarantine policy pass. No engineering performance run claimed. |
| Live saved views | API create/list/update/default/delete round trip passed; test records removed. UI save, page reload, reapply and confirmed delete also passed. |
| Live cluster and delivery | Authenticated Kubernetes node read succeeds. Two existing Flux assignments are ready; inventory reports 3 and 5 resource identities, with current source observations. |
| Live UI | T3 browser checked login/session, overview, cluster list/detail, workloads, Tools, delivery estate/system components, project-scoped catalog, schema-backed install form and Grafana. The form was canceled without installing an add-on. Cluster list checked at 390px and desktop with no document overflow. Final overview shows the deployed version. |

These checks establish a functioning local rollout, not completion of the whole efficiency program or release qualification. No full Local CI matrix, real-member scale run, sustained soak, or destructive rollback drill was performed in this session.

### Remaining work and observed limitations

1. **Plan 030 performance/recovery qualification remains open:** matched real-member baseline/candidate measurements, LIST/GET reduction and memory evidence, fault/freshness/reconnect drills, scale/soak/canary/rollback proof, and the dedicated RustFS/management-plane recovery worksheet. Do not claim an efficiency percentage from this rollout.
2. **Plan 031 features remain incomplete:** backend image search/presence support and server-side audit/alerting sort/filter parameters. This session performed a focused live UI walkthrough, not the entire keyboard/accessibility/visual matrix.
3. **Charlie is disabled** in its authoritative/requested mode. Its two old pods still fail registry pulls with 401. Fresh authorized onboarding/credential recovery is required before testing an enabled integration; this rollout did not enable it.
4. **Flux source-controller reports 1/2 ready.** The second pod is waiting for the leader lease and its artifact-port readiness probe fails; the leader reconciles both assignments. This existed for 11 days before the rollout. Assess the intended standby-readiness behavior before changing its probe or replica policy.
5. **Known workload overview defect:** healthy DaemonSets appear `0/1`/`Unknown`. The backend summary reads replica fields rather than DaemonSet counters; the mapping is byte-identical to main. The detailed system inventory reports its own observations. This pre-existing display bug was identified, not repaired here.
6. **Tool/ownership reconciliation needs follow-up:** Tools reports no installed add-ons while system inventory and Flux show existing baseline exporters and cluster-owned cert-manager/ingress. Both old monitoring-stack exporters and baseline exporters remain present. No duplicate stack was installed to force green status.
7. Grafana's home page loads through the authenticated proxy; its external RSS/news panel reports a feed error. This does not invalidate the Grafana database/proxy health check.
8. Before PR submission: decide the scope of the existing offering draft versus this combined branch, resolve the chosen remaining defects, and run the repository's lockfile-pinned full Local CI PR matrix. No push/merge decision is implied by local deployment success.

### Evidence and recovery locations

Private evidence directory: `/var/tmp/astro-consolidation-20261007` (contains credentials/backups; do not publish wholesale).

- `final-state.json`, `final-images.tsv`, `build-provenance-final.json`, `source-final-before.json` identify the final runtime and images.
- `database-before.pgcustom`, `backup.log`, `values-before.json`, `manifest-before.yaml`, `helm-history-before.json` preserve pre-upgrade recovery state.
- `deploy-final.log`, `deploy-grafana-final.log`, `render-contract-review.log` record the final upgrades and preservation checks; `rollout-be148222/` retains the first rollout's provenance.
- `api-validation.json`, `api-validation-final.log`, `delivery-inventory-final.json`, `prometheus-targets-final.json`, `prometheus-up-query.json`, `postgres-integration.log` contain live/test evidence.
- Frontend gate: `gates/frontend-final/20261007T142545Z-1511351-be14822227f3/evidence.json`.
- Full Go/race and the original contract failure: `gates/backend-complete/20261007T144115Z-1553578-b341ac35e633/evidence.json`.
- Completed API contract gate: `gates/api-complete/20261007T145432Z-1584680-66653597785a/evidence.json`.
- Helm gate: `gates/helm-final/20261007T144808Z-1572105-b341ac35e633/evidence.json`.

Rollback must account for schema 71. The migration is additive, but the older image's schema guard must not be bypassed casually. Keep the tested database backup and old release values/images; no rollback or data deletion is part of this handoff.


## 2026-10-07 consolidated PR preparation

The user subsequently authorized PR publication. The integration extends the entire
head of existing draft [PR #46](https://github.com/alphabravo-oss/astronomer/pull/46),
so that PR is used for the combined offering, efficiency and UI scope. No overlapping
PR, main-branch push or merge is required. The local rollout above remains at
`1322c98e`; subsequent PR-preparation changes affect tests, scanning and documentation.

The lockfile-pinned full `make local-ci-pr` matrix was run with two jobs against
`ff5302b40e5f`: **20 jobs passed and 5 failed** in 76m 30s. This run is **not green** and must not be represented as same-commit
qualification of the final PR head. Private logs and retained runner workspaces are
under `/var/tmp/astronomer-pr-20261007` and Local CI run 64 in
`/root/.local/state/local-ci/logs/`.

| Matrix check | Result |
| --- | --- |
| Backend enterprise gate | PASS, including the complete Go/race, migration and generated-contract gates in one invocation. |
| PostgreSQL integrations | PASS, 21/21 tests both normally and under the race detector. |
| Worker runtime, process restart, Redis outage | PASS in both ordinary and race lanes. |
| Tunnel owner HA | PASS. |
| PostgreSQL streaming failover | PASS. |
| Frontend enterprise gate | PASS, 2,307 tests, type/lint/build/budgets and zero npm audit vulnerabilities. |
| Helm enterprise gate | PASS. |
| Offering static qualification | FAIL during Go setup, before tests: `/bin/sh: 1: version: not found`. Direct host Go qualification tests and all six Python harness tests pass. A clean runner result remains required. |
| PostgreSQL outage, both modes | FAIL because the production transaction inventory omitted `handler.TableViewsMutationTx`. Fixed by `b56175fa`; direct dedicated-container reruns pass in both modes, including real outage/recovery. Original matrix results remain failed. |
| Playwright E2E | FAIL: 23 failed test IDs from 414 scheduled cases. Nine functional/assertion failures and fourteen mobile visual baselines; no retries or baseline acceptance applied. Subsequent route-smoke and blocking visual-matrix steps did not run. |
| Disposable live browser | FAIL before stack launch: the static inventory counted optional engineering declarations as core journeys. Fixed by `88327ea9`; static contract, ShellCheck and actual Playwright discovery pass with 16 core journeys including Trivy. The actual stack still requires a rerun. |
| Image builds, scans and SBOMs | PASS for all seven components: server, worker, agent, migrator, shell, DR and frontend. |
| Qualification aggregate | Not executed by Local CI after first-wave failures; no same-commit aggregate qualification claimed. |

The nine functional/assertion E2E failures cover catalog-project 503, CIS-detail
503 and logging-operation error states on desktop/mobile; app-install default
YAML on desktop/mobile; and a mobile resource action bar extending beyond the
viewport. The fourteen visual failures cover both themes for clusters, cluster
overview, resource explorer, logging, delivery, RBAC and management backup on mobile.
These require diagnosis/review; passing unit tests and the focused live walkthrough
do not supersede them.

Commit `466b11e5` adds an exact path-and-line Gitleaks exception for the synthetic
offering-report idempotency fixture assignment. Pinned Gitleaks
v8.27.2 passes across all local Git refs; it does not exempt arbitrary credentials.
PR readiness also requires the outstanding program/provider/performance work to be
explicitly scoped, and appropriate review of the combined change. The PR remains
a draft while required checks are outstanding.


## 2026-10-07 release-readiness corrections

Follow-up release preparation fixed the remaining deterministic PR failures:
management project/CIS/logging queries now expose errors promptly instead of
waiting through the adopted-agent reconnect retry budget; mobile header actions
wrap within the viewport; the install lifecycle fixture uses the real `data`
envelope and normalized YAML. Fourteen mobile visual baselines were inspected
against the intentional navigation/table/empty-state changes and updated without
relaxing thresholds, including the corrected cluster action wrapping.

The real live journey assertions now target the current permission explanation,
the exact YAML Edit action, and the accessible direct-kubeconfig tooltip. A fresh
host-owned disposable stack passed all 16 live journeys, including real Flux,
Trivy ingestion, direct/proxy kubeconfig validation, and Velero backup/restore.
Private evidence is under `/var/tmp/astronomer-release-20261007/live-local`.

The worker race fixture previously used a once-per-minute snapshot schedule and
could legitimately create a second snapshot when its replay crossed a minute.
It now seeds a daily schedule that is due once immediately and next due six hours
away. The real PostgreSQL/Redis worker race qualification passed (103 seconds).
The production scheduler and task retry behavior are unchanged.

The first host full E2E rerun passed 410/414 cases; three failures coincided with
the disposable cluster changing host networking, and one was the newly fixed
mobile header baseline. Focused functional tests and the corrected mobile header
snapshots passed. The full matrix on the committed candidate must establish
final qualification; these partial runs are not relabeled as a green matrix.

The release owner chose **1.2.0**, reserving 2.0.0 for a planned refactor, after
being informed that the v1.1.0 tag comparison contains real API incompatibilities.
CHANGELOG and API policy documents disclose that transition and the exact
compatibility-review entries distinguish wire-preserving catalog documentation
corrections from intentional authentication/endpoint/pagination migrations.

Release-history inspection found v1.1.0 was published by GitHub Actions run
32448999389. Signed cloud/scale/Rancher/accessibility evidence requirements were
introduced afterward; repository environments were empty at inspection and no
release approval variable was configured. No external certification or human
approval evidence is fabricated by local tests.

### v1.2.0 publication policy and final local fixes (2026-10-07)

The owner explicitly selected automated publication for v1.2.0, with cloud,
scale, Rancher benchmark and human accessibility certifications deferred and
no separately named release approver required. A subsequent real SBOM check
found the newly introduced strict license policy rejects existing dependencies
(BusyBox lacks asserted license metadata; PostgreSQL includes GPL and other
licenses outside that policy). The owner separately authorized deferring license
qualification for this tag while retaining findings and SBOMs. The signed runtime
report now explicitly distinguishes passed vulnerability checks from deferred
license qualification; unresolved findings are preserved, not zeroed or waived.
Other versions retain mandatory license and external approval policies.

Release and recovery publication now require an automatic v1.1.0 upgrade,
backup/decryption and clean-restore rehearsal. Promotion verifies exact signing
identities, original and producer run IDs, commit/tag and matching manifest
hashes. Evidence and a qualification disclosure are attached to the release.
The rehearsal review fixed actual PostgreSQL variable substitution (stdin rather
than `psql -c`), readiness-port ownership, disposable API-token lifetime,
canonical webhook routes, and the seventh DR image. Actual SQL extracted from
the script passed against an owned PostgreSQL 16 database, including proof-row
verification and live-database replacement. Release contracts, all deployment
package tests, 17 Python qualification-verifier tests, the executable rehearsal
harness, ShellCheck and actionlint passed.

Full lockfile-pinned Local CI run 66 completed in 101m 10s: **22 passed, 3 failed**.
Backend, offering-static, all eleven stateful entries, PostgreSQL streaming
failover, the real live-browser stack and all seven image build/scan/SBOM entries
passed. The live stack passed all 16 browser journeys and verified real Flux,
Trivy, direct read-only member credentials, and completed Velero backup/restore.
The E2E job passed all 414 cases and all 310 route-smoke cases before its tablet
visual matrix failed. The other failures were a stale generated frontend source
inventory and a restore-ordering assertion tied to the old SQL spelling.
The aggregate did not run after those first-wave failures; this is not a green
full-matrix claim.

Corrected frontend enterprise verification passed with a stable source tree at
`f5e4632e45add1c4292193b35333c76d2168abad`: 2,307 tests, lint/types/build/bundle
checks and zero npm audit vulnerabilities. Corrected Helm enterprise verification
passed with a stable source tree at
`7f9bb5b1d794c9ca432c4de0a505158ed4f621a4`. Tablet screenshot review found a real
header defect: scope filters compressed action buttons into a 228px column.
The action group now wraps as a unit into an 88px two-row header. A viewport-height
regression assertion and 20 individually reviewed light/dark tablet baselines
cover the fix and intended prior UI changes. The final complete visual matrix
passed **80/80**; focused lint and TypeScript checks passed. Intermediate host
browser runs caught `ERR_NETWORK_CHANGED` during concurrent Docker network
teardown, and one overlapping run lost its shared preview server. Those are
recorded failures, not retries hidden by the test configuration; the final run
was isolated after container jobs ended and passed without relaxed thresholds.

The local k3s deployment remains the previously verified `1322c98e` runtime until
the released artifacts are deployed. Final GitHub checks on the pushed PR head,
merge, tagged release qualification and publication are still pending at this
record's creation. Private raw logs, SBOM preflight output and test evidence are
retained under `/var/tmp/astronomer-release-20261007`; no private credentials or
backup archives are committed.

### Follow-up: RustFS, scanner freshness, and runtime-image preflight

The owner requested RustFS in place of the disposable MinIO fixture and clarified
that vulnerability database freshness matters independently of scanner version.
The live fixture now uses RustFS 1.0.1 pinned by digest, plus Astronomer's DR
image as an AWS S3 client. Its readiness probe runs from the client Pod before
mutations; backup verification checks that the actual S3 object is nonempty.
Platform-specific Docker export avoids importing references to undownloaded
architectures into k3d. An initial complete live run passed all 16 journeys with
RustFS, Flux and Trivy, including Velero backup and restore; that diagnostic run
needed a manual image import. The corrected automatic runner is being verified
separately before push.

The built-in catalog now uses chart 0.37.0, Trivy Operator 0.35.0 and scanner
0.75.0. The operator and scanner images passed the local HIGH/CRITICAL scan with
fixed vulnerabilities selected. Database updates remain automatic, including
the Java database, while reports expire after six hours. Software images remain
pinned; database content is not frozen to those software versions. The signed
upstream Flux distribution was regenerated and verified at v2.9.6.

Local backend and Helm enterprise gates passed with stable source trees after
these dependency changes. The Charlie lifecycle tests now wait for the watcher
to complete shutdown before asserting final counters; 100 race-enabled repeats
passed. The previous PR revision `f016e145` passed all 30 GitHub checks. That is
not qualification of these subsequent changes.

Installing Trivy through the actual local delivery API exposed an empty-array
bug: the bundle handler encoded an empty dependency list as JSON null, rejected
by the durable JSON database contract. The handler now preserves empty arrays
for dependencies and capability requirements. Focused handler race tests cover
both omitted and explicitly empty request fields; local API verification follows
the updated deployment. No database constraint was weakened.

Publication remains blocked by third-party vulnerability findings. The
checksum-verified Trivy 0.75.0 preflight used the 2026-10-07 database and found
remaining findings even in current upstream Flux, Dex and Node Exporter images.
PostgreSQL 16-alpine findings were in its bundled gosu helper, requiring separate
reachability review. These are scanner findings, not assertions that every CVE
is reachable. No vulnerability waivers were added. The owner's v1.2.0 license
qualification deferral remains unchanged and does not waive this vulnerability
gate. Private raw reports and the candidate-image summary are retained under
`/var/tmp/astronomer-release-20261007/trivy-preflight/`.
