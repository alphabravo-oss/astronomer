# Astronomer Enterprise-Grade API and Rancher-Parity Implementation Plan

**Status:** LOCAL IMPLEMENTATION COMPLETE — the corrected exact-tree static and locally runnable integration sign-off is green; eight credentialed, production-scale, release-candidate, protected-approval, or human qualification executions remain before GA release approval
**Created:** 2026-08-23
**Baseline:** `c8534cc` (`main`)
**Scope:** All 20 findings from the 2026-08-23 deep enterprise/Rancher-parity audit
**Delivery engine:** Flux only. Fleet and Argo are out of scope.
**Cluster lifecycle:** Adopted-cluster management only. Cluster provisioning, machine drivers, and node-pool provisioning are out of scope.
**Checklist ledger:** 352 of 360 granular items are implemented and evidenced; eight are open. All 34 local defects found by the completion audit and reopened Finding 7 review are closed. The remaining eight require credentialed, production-scale, release-candidate, protected-approval, or human execution through the now fail-closed producers.

## 1. Objective

Make Astronomer a defensible enterprise day-2 Kubernetes management product with:

- authorization and tenant isolation that hold for every read and write path;
- durable, observable reconciliation whose accepted work cannot silently disappear;
- a release and upgrade contract that is executable and proven on supported versions;
- an OpenAPI-first management surface usable independently of the first-party UI;
- operator UX at or above the checked-out Rancher management/explorer baseline, while intentionally using Flux and excluding cluster provisioning;
- measured HA, performance, recovery, accessibility, and browser evidence;
- code ownership boundaries that make future regressions difficult to introduce.

This plan is the implementation ledger. A checkbox may be marked complete only when its named evidence exists and passes. Passing a narrower unit test does not complete a broader acceptance item.

## 1.1 Completion-audit reopening delta — 2026-08-25

The first full final-tree run disproved the prior local-signoff claim. A green
normal/race/API/Helm result was necessary but not sufficient: generated
inventories had drifted, several integration qualifications silently skipped,
and multiple external-evidence validators accepted self-asserted or
statistically invalid evidence. These tasks are release blockers, not optional
hardening and not substitutes for the eight real external executions.

### Generated ownership and dead-code truth

- [x] Regenerate the code-health and operation/task inventories with the supported writers after the backend/frontend decomposition; verify 101 current documents, 591 links, 809 changed production files under seven hotspot ceilings, and current task/operation counts.
- [x] Classify the remaining `SetClusterDirectAccess` sqlc dead-code candidate through runtime/compatibility evidence. Exhaustive runtime/handler/worker/CLI/migration/compatibility search proved it was redundant with the active transactional `CreateCluster`/`UpdateCluster` paths and had no consumer; the SQL declaration was removed through pinned sqlc regeneration. The current inventory has 1,145 sqlc declarations and zero dead-code candidates, with full Go and code-health gates green.

### Scale, mandatory audit, and drill provenance

- [x] Sustain mandatory-audit mutations for the certified duration or an explicitly bounded representative window instead of stopping after the first 50–100 seconds; gate observed active duration, achieved rate, backlog, drain latency, drops, and conservation.
- [x] Derive `IntentsObserved` from independently queried durable outbox rows correlated to the qualification requests; never increment accepted and observed-intent counters in the same local function.
- [x] Replace the optimistic maximum per-replica sizing calibration with a conservative comparable-sample lower bound or approved quantile, utilization headroom, monotonicity checks, and rejection of heterogeneous/nonlinear samples.
- [x] Require every day-2 drill producer to emit a closed signed manifest and verify exact source run, ref, conclusion, workflow identity, content digest, freshness, profile, and commit before scale certification incorporates it.

### Protected release evidence and cloud acceptance

- [x] Replace self-asserted RC/cloud/accessibility digest strings with downloaded, closed-schema-validated, digest-recomputed, Sigstore-workflow-identity-verified artifacts bound to the exact tag, commit, source run, result, and freshness.
- [x] Add the signed aggregate scale/audit/sizing evidence and signed Rancher automated/human benchmark evidence to the protected release-approval schema, release workflow, and resume workflow.
- [x] Add a closed accessibility evidence artifact covering every required OS/browser/AT/version, viewport, zoom, critical workflow, defect/waiver, tester/reviewer, timestamp, and retained-artifact digest, including iOS VoiceOver.
- [x] Prove cloud acceptance idempotency from matching durable operation/result identities and exactly one effective provider mutation; do not infer it merely from final convergence after replaying a key.

### Rancher comparative qualification

- [x] Require fixed cold/warm repetitions, fixture and injected-failure identifiers, independent probes, trace digests, and preregistered median/non-inferiority thresholds for every product/task pair; reject missing, zero, or implausible metrics.
- [x] Replace the one-participant/URI human pass with a closed counterbalanced study schema containing participant/order assignments, per-task answers, completion/recovery outcomes, rating scale, exclusions, minimum sample, defects, and preregistered thresholds.
- [x] Measure epoch-based operator-active intervals across reload/navigation and separate controller/backend wait and idle time; add navigation, idle, and recovery characterization.
- [x] Add a protected end-to-end benchmark producer that pins both subjects, provisions the declared fixtures/injections, runs all repetitions/probes, retains traces/screenshots, imports the human study, validates the closed aggregate, and keyless-signs it.

### Exact release-candidate rehearsal

- [x] Install the previous chart with signed/versioned previous-release baseline values or a minimal compatibility values contract validated against both chart schemas; apply target values only during the actual upgrade.
- [x] Hash and attestation-verify the downloaded previous chart against its signed previous-release manifest before installation.
- [x] Create a disposable encrypted record through the production application path before backup and read/decrypt it through the upgraded restored application, comparing only a non-secret semantic digest.
- [x] Select exactly one expected inner evidence file, validate its closed schema/result/mode/source/target/manifest/backup bindings, and derive outer RC status from those fields rather than fixed pass literals.
- [x] Replace marker-only RC tests with an executable stubbed shell integration harness covering artifact identity, previous/target values compatibility, clean restore, evidence selection, product decrypt proof, cleanup fences, and negative paths.

### Authoritative verification and retained sign-off

- [x] Add required same-commit CI jobs for worker-runtime, Redis-outage, process-restart, PostgreSQL-outage/failover, and tunnel-owner qualifications, including their intended race variants and a single aggregating sign-off check.
- [x] Stop presenting backend/frontend/Helm `all` as complete release sign-off: either make every safe locally runnable lane part of it or name it static verification and add an exact-commit aggregator for database, browser, live, fresh-cluster, cloud, scale, benchmark, accessibility, and RC evidence.
- [x] Add a shared disposable PostgreSQL integration runner for the Dex lifecycle, agent-upgrade match, built-in provisioner, and Delivery rollout suites; fail if any expected integration test skips.
- [x] Add deterministic check-mode gates for the Go SDK and Charlie generated client and include release-manifest, image-inventory, air-gap, and Python contract checks in the authoritative gate.
- [x] Resolve existing accessibility warnings, promote the six core jsx-a11y rule families to errors, and run ESLint with `--max-warnings=0` in local and CI verification.
- [x] Run tablet visual snapshots, the flake/quarantine policy, and a named Trivy-enabled live lane in required or explicitly scheduled retained evidence rather than leaving them outside continuous sign-off.
- [x] Add non-mutating `gofmt` verification, whole-tree whitespace validation, `bash -n`, ShellCheck, and the migration verifier's negative-fixture self-tests.
- [x] Use a fresh evidence directory and emit a closed commit/timestamp/tool-version/subgate/result/hash manifest plus CI step summary; retain successful as well as failed sign-off evidence.
- [x] Replace the no-op Helm “input contract” log step with an executable dependency-free chart assertion or remove the phantom claim and correct workflow documentation.
- [x] Rerun the complete corrected static and locally runnable integration sign-off on one exact tree, retain the manifest, and reconcile every checked claim before restoring local-complete status.

### Completion-audit closure evidence

- Exact-tree static sign-off passed 48/48 subgates with a stable 3,226-file source hash `1a3e9566d72c395aab085ccb579fe7e55fdedbe06fcb909b9a90e19d7eb01dad`. It includes complete normal and race Go suites, generated/API/release contracts, frontend lint/type-check/160 files and 1,045 tests/build/bundle/audit, and Helm lint/development/production/contracts. Evidence: `/tmp/astronomer-enterprise-wave16-final-rerun-20260825/20260825T060423Z-4068501-c8534ccef974/evidence.json`.
- The disposable PostgreSQL runner applied migrations 1–26 and executed all 12 expected PostgreSQL-gated tests with zero skips. The executable RC black-box harness passed its positive lifecycle plus wrong identity, pre-existing cluster, duplicate artifact/evidence/backup, missing restored secret, and destructive-fence negative paths.
- Release/cloud/scale/Rancher/accessibility evidence contracts passed 14 Python tests, five Node benchmark tests, all ten protected producer wiring checks, and the 62-script Bash/ShellCheck gate. The retained retry-disabled live Flux/Trivy/Velero/direct-access suite remains 16/16 at `/tmp/astronomer-live-browser-trivy-signoff-20260825-final`.

## 2. Baseline evidence and known failures

The following observations were reproduced at `c8534cc` before implementation:

| Area | Baseline result |
|---|---|
| Go lint | Failed with 8 findings, including unchecked closes, deprecated `ReverseProxy.Director`, and staticcheck simplifications. |
| Go tests | All packages passed except 3 Charlie Helm/network-isolation contract tests under `deploy`. |
| Frontend architecture | Failed: stale code-health inventory and 21 generated-schema shadows in `frontend/src/lib/api/delivery.ts`. |
| Frontend lint | Failed: 7 errors and 271 warnings. |
| Frontend type check | Failed with 2 TypeScript errors. |
| Frontend unit tests | 3 failures in `ExtensionProvider.test.tsx`; 868 tests passed. |
| Frontend build | Passed. Largest emitted chunk was approximately 873 kB uncompressed. |
| Dependency audit | `npm audit --audit-level=moderate` passed with zero findings. |
| Helm verification | Lint and renders passed; 3 Charlie chart contract tests failed. |
| Migration round trip | Failed before PostgreSQL startup because the script requires exactly migration `001`, while migrations `001`–`004` exist. |
| OpenAPI | 500 of 686 router operations documented (72.9%); 186 routes missing; 388 operations lacked `operationId`; 82 lacked a typed 2xx schema. |
| Scale | No production-like passing baseline is recorded. |

The final implementation evidence and the remaining release-candidate qualification work are recorded in sections 28 and 29. The baseline table is retained so the improvement is auditable.

## 3. Non-negotiable engineering invariants

1. **Authentication is not authorization.** Every protected object is authorized at the correct global, project, cluster, and namespace scope.
2. **Accepted intent is durable.** If an API returns success for asynchronous work, durable intent exists before the response and survives Redis, worker, server, and tunnel-owner failure.
3. **Missing dependencies fail loudly.** A production process must not register a task it cannot execute. A handler must never return success because its runtime dependencies are nil.
4. **One task, one owner.** Every task type declares its queue, owning process, dependencies, idempotency key, retry policy, terminal state, and recovery sweep.
5. **The API is the product contract.** First-party UI behavior must be possible through documented APIs; the UI may not rely on phantom fields or undocumented side channels.
6. **No lossy compliance writes.** Security-relevant mutations and their audit evidence must commit atomically or fail the mutation.
7. **Upgrade before scale.** Schema compatibility, rolling server/worker/agent upgrades, and rollback boundaries must be proven before GA scale claims.
8. **Safe expert escape hatches.** YAML, shell, direct access, and arbitrary proxy operations are explicit, authorized, audited, time-bounded where possible, and clearly distinguished from normal workflows.
9. **UI states are truthful.** Loading, empty, partial, stale, unauthorized, unavailable, failed, retrying, and successful states must be visually and semantically distinct.
10. **Evidence is reproducible.** No enterprise claim is accepted from prose alone; a command, automated test, rendered artifact, or retained live-run report must prove it.

## 4. Program waves and dependency order

| Wave | Scope | Blocking dependency | Exit condition |
|---|---|---|---|
| 0 | Plan, baseline, test harness repair | None | This plan is committed to the tree and baseline failures are reproducible. |
| 1 | P0 authorization and release integrity | Wave 0 | Findings 1, 4, 5, 6, and 7 pass focused and enterprise gates. |
| 2 | Worker/task ownership and allow-list enforcement | Wave 1 | Findings 2 and 3 pass process-topology and crash/retry tests. |
| 3 | Durable audit, CIS, and agent compatibility | Wave 2 | Findings 8–10 pass restart, rolling-upgrade, and failure-injection tests. |
| 4 | API-first contract and pagination | Waves 1–3 | Findings 11–13 pass 100% supported-route coverage and API client conformance. |
| 5 | Explorer, accessibility, browser journeys, logging | Wave 4 | Findings 15–18 pass desktop/mobile, axe, visual, and live workflow gates. |
| 6 | Scale, modularity, and documentation | Waves 1–5 | Findings 14, 19, and 20 pass measured HA/scale and documentation drift gates. |
| 7 | GA evidence and release rehearsal | All prior waves | Full evidence matrix is green on a release candidate with no waiver hiding a required invariant. |

### 4.1 Completed external-effect saga inventory

The finite local audit-boundary inventory identified during the review is now
implemented. Direct `recordAudit` calls are not automatically defects: sampled
reads, blocked decisions, remote-effect observations, and executor
compatibility fallbacks remain valid. The completed families and their durable
boundaries are:

| Priority | Implemented family | Durable boundary and evidence |
|---:|---|---|
| 1 | Managed Kubernetes resource, node, and pod mutations | Resource CRUD, pod deletion, image rescan, and every node mutation persist authorized intent, identifier-only task metadata, and mandatory audit before the member-cluster effect. Active-target indexes and generation/attempt CAS prevent out-of-order or stale completion; drain remains retrying until accepted evictions disappear and reports blockers/partial progress truthfully. Resource, workload, and node receipts recheck current scoped capability after loading and bind every route identifier. |
| 2 | Mutating raw Kubernetes proxy requests | The synchronous expert escape hatch remains, but mutating requests require fail-closed durable pre-effect audit before tunnel dispatch and record only a sanitized best-effort outcome after the response. |
| 3 | Asynq DLQ retry/discard | Migration 020 provides a durable administrative operation and `effect_started_at` crash phase. The identifier-only worker converges replay after a consumed target, distinguishes initial missing targets, and exposes only sanitized error categories. |
| 4 | Management-backup destination CRUD/test/run | Migration 021 adds generation-fenced desired state and durable operations. Reconciliation preserves Kubernetes resource versions, rejects mutation during an applying generation, fences scheduled/manual Job concurrency, and marks a run complete only after the Job succeeds. Credentials remain encrypted PostgreSQL state and never enter task/audit payloads. |
| 5 | Cluster image rescan | The request commits a workload operation, task intent, and audit before execution; attempt-fenced terminal/retry writes suppress stale workers, and the UI polls the generated receipt to a truthful terminal state. |

The first-party generic resource editor still intentionally uses the audited
raw Kubernetes proxy for resource kinds outside the managed durable-resource
API. It is an API-backed expert boundary, not a claim that every arbitrary CRD
mutation has a bespoke saga. Other retained local tracks are the cross-family
API-semantics promotion in Finding 11, residual generated-client migration and
generic camelization removal in Finding 12, and the broader decomposition
targets in Finding 19.

Release-environment-only evidence remains: real Redis/PostgreSQL process-kill
and topology tests, credentialed cloud conformance, mandatory-audit outage/load
tests, Rancher side-by-side human benchmark, live browser/agent journeys,
production-sized soak reports, HA/failover/RPO/RTO exercises, and release-
candidate backup/restore/upgrade rehearsal. These items may not be checked off
from mocks, local prose, or narrow unit suites.

## 5. Finding 1 — Security read authorization and object scoping

### Problem and reasoning

Security templates, policies, fleet scan listings, cluster scan listings, scan details, and CSV exports are authenticated but incompletely authorized. Scan-by-ID queries do not bind the object to the route cluster or the caller's visible-cluster set. The generated route inventory claims a route-specific read gate that is not present in code.

### Required invariants

- Global security reads require `security:read`.
- Cluster security reads require both `clusters:read` and visibility of the named cluster.
- A scan detail under `/clusters/{cluster_id}/.../{id}` must match both identifiers.
- A fleet scan detail must verify that the caller can read the scan's cluster unless a documented global-security permission grants fleet visibility.
- Unauthorized and cross-tenant object lookup returns the repository's non-disclosing response policy consistently.
- Pagination totals count only authorized rows.

### Implementation tasks

- [x] Add `secRead := requirePermission(... ResourceSecurity, VerbRead)` to the global security route group.
- [x] Add the standard cluster-read gate to policy and scan routes under `/clusters/{cluster_id}`.
- [x] Decide and document whether `security:read` grants estate-wide scan visibility or still intersects visible clusters; the implementation uses the stricter authorized-cluster intersection.
- [x] Add SQL queries `GetSecurityScanResultByClusterAndID` and `CountSecurityScanResultsByCluster`.
- [x] Add an authorized estate-list query that joins the caller-visible cluster IDs in SQL rather than filtering after pagination.
- [x] Make `GetScan`, `GetScanFull`, and `ExportScanCSV` use the scoped authorization service and scoped query.
- [x] Apply the same read gate to templates, policies, controller status, and profiles.
- [x] Correct `docs/generated-route-inventory.json` generation so RBAC descriptions come from executable route metadata, not generic prose.
- [x] Add read routes to the security-sensitive route registry.
- [x] Add structured audit events for successful compliance-report exports without recording finding content.

### Tests and validation

- [x] Route tests: unauthenticated, authenticated/no grant, global security reader, cluster reader, wrong-cluster reader, superuser. `security_read_scope_route_test.go` pins all seven personas and proves global `security:read` still intersects cluster visibility.
- [x] Handler tests: scan ID belongs to another cluster; route cluster ID and scan cluster ID disagree; deleted/tombstoned cluster behavior. Scoped object reads return the same not-found contract for cross-cluster and decommissioned/tombstoned rows.
- [x] Pagination tests: first page contains unauthorized rows before authorized rows; returned page and total remain correct. The handler test proves authorization is passed into SQL before `LIMIT`, and generated-query shape tests pin the predicate/order/count symmetry.
- [x] CSV tests: authorization and mandatory audit persistence are checked before headers/body are written.
- [x] Route-inventory test verifies the real middleware chain for every security read route.
- [x] Run `go test ./internal/server ./internal/handler ./internal/db/queries -count=1`.
- [x] Run `./scripts/verify-enterprise.sh api-contract`.

### Rollout and rollback

- No data migration is required.
- Expect formerly over-permissive callers to receive 403/404. Emit authorization-denial metrics by route without object contents.
- Rollback must not restore unauthenticated object lookup; if compatibility is needed, grant an explicit role rather than removing gates.

## 6. Finding 2 — Task ownership, process composition, and silent no-ops

### Problem and reasoning

The standalone worker registers task handlers whose package-global dependencies are configured only inside the server process. The server process drains only the tunnel queue and does not register those default-queue handlers. Multiple handlers treat absent dependencies as a successful no-op, so Asynq deletes the task. This affects at least webhook, SIEM, GitOps, cluster registry, cloud credential, NetworkPolicy, snapshot, kubectl-session, and related sweeps.

### Required invariants

- Every registered production task can execute in its owning process.
- Every task requiring the WebSocket tunnel is consumed only by a server/tunnel owner.
- DB/network-only tasks are consumed by the standalone worker and configured there.
- Dependency validation occurs before queue consumption begins.
- A configuration defect returns a terminal configuration error and readiness failure; it never acknowledges the task.
- Periodic recovery exists for every critical intent, but recovery is not a substitute for correct immediate delivery.

### Implementation tasks

- [x] Introduce a typed task descriptor registry containing task type, queue, owner, required capabilities, retry class, idempotency class, and scheduler entry.
- [x] Replace process-global `ConfigureX` variables with explicit handler structs or immutable runtime containers passed during mux construction. All 31 former task-package configurators are removed. Family-specific immutable runtimes own Flux delivery, outbound and outbox dispatch, Charlie alerts and triggers, maintenance, API-server allow-lists, GitOps, tool drift, templates, NetworkPolicy, mesh, cloud credentials, registries, projects, cluster and control-plane snapshots, decommission, deferred dispatch, kubectl reaping, and security ingestion. Cross-cutting dependencies now live in `tasks.CoreRuntime`, which normalizes a caller-owned value and scopes it to each invocation context instead of mutating package state. `worker.StandaloneRuntime` and `worker.TunnelRuntime` bind all 76 task descriptors explicitly; construction rejects missing and typed-nil always-on dependencies before Redis subscription, and optional families remain bound while feature-aware production validation decides whether their dependencies are required.
- [x] Split `RegisterHandlers` into worker-owned, tunnel-owner, and optional feature handler sets derived from the registry.
- [x] Add startup validation that every registered handler has all required dependencies.
- [x] Configure webhook and SIEM dispatchers in `cmd/worker` using DB, encryption, HTTP transport, metrics, and settings providers.
- [x] Configure GitOps sync in `cmd/worker`, including credential decryptor, bounded clone root, task outbox, and decommission enqueuer. Manual and webhook source sync now commit a source-ID-only `gitops:sync` task-outbox row plus audit intent before returning a typed `202 queued` receipt; the request path performs no clone or reconciliation inline.
- [x] Move cluster-registry, cloud-credential, NetworkPolicy, cluster-snapshot, and other tunnel-requester tasks to the tunnel queue and register them on the server-owned worker.
- [x] Route kubectl session reaping according to its actual dependency set; split DB-only expiry from tunnel pod cleanup where required.
- [x] Configure deferred dispatch, token rotation, control-plane snapshot, and project reconciliation in their real owner.
- [x] Replace production task paths that acknowledged absent runtime dependencies with construction-time rejection or a retryable/terminal error.
- [x] Remove runtime reset backdoors entirely. Runtime-family tests construct isolated immutable values, broad-runtime direct-call tests use `CoreRuntime.Context`, and production task code exports no `Configure*`, `Reset*`, or `*ForTest` runtime mutation functions.
- [x] Add task-owner metrics and a readiness diagnostic listing configured/registered/scheduled task counts.
- [x] Add a generated task inventory document and drift test.

### Tests and validation

- [x] Table test proves every descriptor has exactly one owner and queue.
- [x] Composition tests construct both complete worker dependency graphs, reject incomplete and typed-nil core and family dependencies, verify every one of the current 83 descriptors is runtime-bound, resolve every descriptor through its owning runtime, and invoke the converted family handlers to prove the unbound fallback is unreachable. Core-runtime tests also prove normalization does not mutate caller-owned input and independent invocation contexts cannot cross-wire values.
- [x] Handler test asserts absent dependencies never return nil.
- [x] Enqueue/consume integration test for each critical task family with real Redis and PostgreSQL. The disposable PostgreSQL 16 plus Redis 7/Asynq harness executes all 83/83 production descriptors twice through their registered owners with `MaxRetry(0)`. The final tranches cover snapshot and control-plane-snapshot families, maintenance defer replay, cloud credential materialization/drift, kubectl reap, group metrics, Gatekeeper policy/reconcile-all, tool drift, fail-closed Charlie triggering, and decommission single/all in addition to the earlier project, template, registry, NetworkPolicy, delivery, image, and rescan families. Active paths use real PostgreSQL rows/SQLC queries, Redis/Asynq owners, leader/outbox, production deferred replayers, encryption, and authenticated controllable tunnel runtimes only at external Kubernetes boundaries. Replay assertions prove terminal lease/error convergence, exact fan-out, idempotent state/audit effects, and recovery enqueueing. The completed harness passes normal in 100.506s and race in 97.739s, with focused tests, vet, sqlc drift, and diff checks green. It also exposed and fixed a real group-metrics Prometheus label-cardinality panic.
- [x] Kill worker after intent commit and before execution; replacement worker completes exactly once. The real PostgreSQL 16/Redis 7 subprocess harness captures the original Asynq task ID, proves it is active before killing the first worker, starts the replacement without any recovery enqueue, observes that same task automatically enter retry with `retried=1` and `lease expired`, and then proves completion after at least two attempts with exactly one durable effect, one effect audit, and one completion audit. A subsequent deliberate duplicate delivery cannot duplicate durability. Normal and race executions pass with production-parity retry delay and real lease expiry.
- [x] Kill tunnel-owning server during execution; task retries on another eligible server without double mutation. `./scripts/test-tunnel-queue-ha.sh` passed on disposable PostgreSQL 17 and Redis 7 after all 23 migrations: owner A was terminated after the synthetic agent committed the DELETE effect but before its response, owner B retried with a fresh stream, and the harness observed two requests but exactly one converged effect.
- [x] Disable all server replicas; tunnel tasks remain pending rather than being consumed by standalone workers. The same production-constructor integration run stopped both tunnel consumers, started a standalone worker, and asserted the tunnel-queue task and durable operation both remained pending.
- [x] Run full Go race tests for worker/tasks and tunnel ownership.

### Rollout and rollback

- Deploy producers capable of stamping the new queue before consumers stop accepting old queue entries.
- Drain or migrate existing default-queue tunnel tasks during upgrade.
- Retain temporary dual-consume compatibility only with an idempotency fence and remove it after the queue is empty.

## 7. Finding 3 — API-server allow-list enforcement and provider completion

### Problem and reasoning

The allow-list reconciler is not configured by any runtime. Its handlers acknowledge tasks when dependencies are absent. AKS, DOKS, and self-managed providers are scaffolds. The API can therefore persist an enforcement promise without a functioning reconciler.

### Required invariants

- An enabled allow-list has an executable provider or the API rejects enforcement mode.
- Reconciliation has durable status: desired, observed, provider, last attempt, next attempt, applied generation, and last error.
- Unsupported providers never silently downgrade enforcement to monitoring.
- Cloud credentials are least-privilege, encrypted, redacted, and never stored in task payloads.
- Apply is idempotent and drift is periodically detected.

### Implementation tasks

- [x] Construct the provider registry in the owning process with EKS and GKE clients.
- [x] Route reconciliation according to whether provider calls require only cloud APIs or also a cluster tunnel.
- [x] Wire API-server allow-list reconciliation through the task runtime registry.
- [x] Implement AKS authorized IP range read/apply using the supported Azure SDK and credential materializer.
- [x] Implement DOKS firewall/cluster endpoint restriction using the supported DigitalOcean API.
- [x] Define self-managed enforcement as an explicit integration contract; expose monitor-only and reject enforce when no safe generic implementation exists.
- [x] Add capability discovery to the create/update API and UI.
- [x] Validate and canonicalize CIDRs, reject overlaps/invalid ranges as appropriate, and enforce bounded list size.
- [x] Add optimistic generation/CAS so stale tasks cannot overwrite newer desired state.
- [x] Add provider-specific error taxonomy and retry/backoff policy.
- [x] Surface drift, unsupported capability, credential expiry, and last successful enforcement in API/UI.
- [x] Add alerts for prolonged drift and repeated provider authorization failures. The worker exports a dedicated HTTP-401/403 counter, the chart alerts on 30-minute drift and repeated 15-minute authorization failures, and the metrics contract plus remediation runbook are linked and validated.

### Tests and validation

- [x] Provider unit tests for no restriction, exact match, drift, apply, permission denied, throttling, and transient failure.
- [x] Contract tests using official provider API fakes or recorded sanitized fixtures.
- [x] Task restart/idempotency tests and stale-generation tests.
- [x] API tests reject enforce mode for unsupported providers.
- [x] UI tests clearly distinguish monitor-only from enforce.
- [x] Add a protected EKS/GKE/AKS/DOKS cloud-acceptance runner with exact pre-existing-target binding, public `/32` or `/128` restriction, credential validation, snapshot/enforce/same-key replay/convergence/finally-restore semantics, closed sanitized evidence schemas, Sigstore signing, and positive/negative contract tests. AWS credentials support session tokens plus real bounded STS AssumeRole through one shared resolver with redacted typed failures and race-safe refresh.
- [ ] Execute the protected runner against credentialed adopted-cloud sandboxes for EKS, GKE, AKS, and DOKS and retain verified signed evidence.

## 8. Finding 4 — Authoritative verification baseline

### Problem and reasoning

The declared enterprise verification command is red in backend, frontend, and chart areas. A red baseline normalizes regressions and prevents release evidence from being meaningful.

### Implementation tasks

- [x] Fix all current Go lint errors without suppressing rules globally.
- [x] Replace deprecated `ReverseProxy.Director` usage with `Rewrite` and explicitly preserve trusted forwarding semantics.
- [x] Fix all frontend lint errors.
- [x] Triage warnings by category; repair shared-component accessibility warnings before leaf warnings.
- [x] Fix current TypeScript errors.
- [x] Repair `ExtensionProvider` tests so they install deterministic API mocks and never issue accidental network requests.
- [x] Resolve code-health inventory drift and schema-shadow failures through actual API type migration, not allowlisting.
- [x] Resolve chart-contract failures according to the final Charlie isolation design.
- [x] Ensure every piped CI command preserves the failing exit code (`pipefail` or native reporter).
- [x] Add a CI summary that enumerates every enterprise subgate and artifact.
- [x] Prohibit required-gate `continue-on-error` and non-expiring blanket waivers.

### Validation

- [x] `./scripts/verify-enterprise.sh backend`
- [x] `./scripts/verify-enterprise.sh frontend`
- [x] `./scripts/verify-enterprise.sh helm`
- [x] `./scripts/verify-enterprise.sh api-contract`
- [x] All constituent `verify-enterprise.sh` scopes completed independently in this work session (the same backend, frontend, and Helm gates invoked by `all`).
- [x] `git diff --check` is clean and `git status --short` is fully enumerated; the intentional implementation diff remains uncommitted for user review.

## 9. Finding 5 — Database migrations and supported upgrades

### Problem and reasoning

The repository has four migrations and expects schema version 4, while the live round-trip harness, chart preflight, release compatibility file, and runbooks still encode a single migration/version-1 fresh-install world.

### Required invariants

- A fresh install reaches exactly `ExpectedSchemaVersion` on PostgreSQL 16 and 17.
- Every supported prior Astronomer release can upgrade to the current release.
- Down migrations are tested for development correctness but production rollback policy explicitly accounts for irreversible/expand-contract changes.
- Old servers never run against schemas they cannot understand; new servers do not become ready before required migrations.
- Migrations are retry-safe and do not expose plaintext secret material.

### Implementation tasks

- [x] Rewrite the round-trip harness to discover ordered migrations and assert the maximum version dynamically.
- [x] Test full up, one-step down/up for every reversible migration, and full down only in disposable databases.
- [x] Add fixtures for the last supported 1.0.x and 1.1.x schema snapshots.
- [x] Add upgrade tests from each fixture to current on PostgreSQL 16 and 17.
- [x] Update chart preflight schema bounds to use release metadata rather than literal version 1.
- [x] Update compatibility schema to represent `minimum_upgrade_schema`, `target_schema`, reversible boundary, and install modes.
- [x] Replace `single_initial` with the real migration policy.
- [x] Update release manifest generation and upgrade scripts.
- [x] Add migration lock/concurrent installer tests.
- [x] Add interruption tests: terminate migration between statements where transactional semantics permit and verify clean retry.
- [x] Add expand/migrate/contract checks for destructive DDL and require explicit compatibility windows.
- [x] Update backup-before-upgrade and restore validation steps.

### Validation

- [x] `./scripts/check-migrations.sh`
- [x] `./scripts/migrate-roundtrip-smoke.sh` on PostgreSQL 16 and 17
- [x] release-upgrade matrix workflow from every supported fixture
- [x] chart preflight tests for below-minimum, exact, current, dirty, and future schema states

## 10. Finding 6 — Charlie private listener and network isolation

### Required invariants

- Charlie private listeners are disabled until an owner-bound installation provides all required identity and key material.
- No public Ingress/Gateway/Service exposes MCP or bridge private endpoints.
- Base server/worker/frontend policies cannot reach private Charlie agent ports before activation.
- Activated access is exact by pod identity, namespace, port, protocol, and mTLS identity.
- Chart version and Charlie protocol versions are distinct and not conflated in tests.

### Implementation tasks

- [x] Decide whether the server should omit the listener entirely or bind only after atomic credential validation.
- [x] Gate listener environment, volumes, and egress rules on the same explicit feature/install state.
- [x] Remove unconditional private port 7443 from the base NetworkPolicy.
- [x] Ensure 7444 is not declared as a container/service/ingress port in the dormant state.
- [x] Generate an activation-specific NetworkPolicy owned by the Charlie installation lifecycle.
- [x] Update chart tests to expect chart 1.1.0 metadata while keeping protocol 1.0.0 assertions separate.
- [x] Add restricted-render and activated-render golden tests.
- [x] Add mTLS negative tests for untrusted client CA, expired certificate, wrong URI SAN, and missing signing key.
- [x] Add uninstall tests proving policies/listeners return to dormant state.

## 11. Finding 7 — Truthful direct-access and kubeconfig workflow

### Product decision and required invariants

Astronomer supports two explicit access modes for adopted clusters. Proxy
kubeconfig is the default because requests remain inside Astronomer's
authorization, revocation, and audit boundary. Rancher-parity direct kubeconfig
is an opt-in expert workflow using a short-lived, read-only Kubernetes
TokenRequest credential pinned to the member-cluster CA. Astronomer does not
issue static or broadly privileged portable credentials, and documentation must
state that a minted Kubernetes token cannot be revoked through the Astronomer
session boundary before its bounded expiry.

- Proxy credentials are scoped, time-bounded, revocable through the Astronomer token boundary, and contain no static placeholder token.
- Direct credentials require exact cluster authorization and an explicit enabled policy; they are read-only, CA-pinned, and expire within the documented maximum.
- Proxy token state and mandatory audit intent commit atomically before the credential is returned.
- Direct issuance persists a mandatory pre-effect intent before the remote TokenRequest and a sanitized outcome afterward.
- Cluster updates are true partial updates: omitted scalar, label, and annotation fields survive unchanged.

### Implementation tasks

- [x] Expose one unambiguous `downloadProxyKubeconfig` frontend operation backed by the documented proxy endpoint.
- [x] Scope proxy credentials to short-lived, read-only access and retain issuance identity/expiry through the existing token boundary.
- [x] Avoid static placeholder tokens in downloadable kubeconfigs; refuse issuance when a safe proxy credential cannot be minted.
- [x] Expose direct kubeconfig as an explicit permission-gated opt-in action rather than a disabled-looking or implicit field.
- [x] Enforce the live-qualified short-lived, read-only, exact-cluster, CA-pinned direct credential boundary.
- [x] Commit proxy token creation and mandatory audit intent in one locked transaction; audit failure leaves no token row and returns no credential. `mintKubeconfigToken` now inserts the API-token row and sanitized outbox intent through the same `runTx` callback, with rollback tests proving neither becomes usable alone.
- [x] Persist a durable direct-issuance intent before the remote TokenRequest and a sanitized outcome afterward; pre-intent failure makes zero remote call and post-effect errors are truthful. Focused handler tests pin zero remote calls on audit failure and the bounded may-have-minted diagnostic after a post-effect audit outage.
- [x] Replace generic `Partial<ClusterRegistration>` and non-pointer handler fields with the generated exact optional `UpdateClusterRequest`. The frontend hook/modal now use `UpdateClusterInput`, while the Go request uses presence-aware pointers and preserves the accepted explicit-JSON-null JSONB behavior.
- [x] Merge cluster updates under the existing row lock so omitted display name, description, environment, region, labels, annotations, and access configuration survive. `GetClusterByIDForUpdate` and one-field/concurrent merge tests pin the lossless behavior; explicit empty values remain clearing operations.
- [x] Rewrite current OpenAPI/UI/operator/security documentation around the supported proxy-default and bounded direct expert modes, including the direct-token revocation limitation. The generated TypeScript client/types, Go SDK, embedded specification, comparison documentation, and security review are synchronized.

### Tests and validation

- [x] API tests cover proxy permission, bounded lifetime, and unavailable issuance dependencies.
- [x] Browser/API contract tests prove the proxy download hits the documented endpoint and contains the expected proxy server URL.
- [x] Contract and PostgreSQL tests prove every submitted update field is documented/consumed and every omitted field survives a one-field update. The OpenAPI request-field gate covers all 151 bound request shapes with zero drift, and the locked SQL/handler tests cover omission, explicit clear, malformed URL, and HTTPS-only validation.
- [x] Security tests prove a cluster reader cannot mint direct credentials, pre-effect audit failure makes no remote call, scope is read-only, expiry is bounded, CA data is exact, and neither audit nor errors contain credential material. Route denial, exact agent TokenRequest capability, dedicated read-only manifest RBAC, sanitized audit/error, and kubeconfig construction tests are green; the retained 16/16 live suite remains the end-to-end member-cluster proof.

## 12. Finding 8 — Durable audit evidence

### Required invariants

- A successful security-relevant mutation and its audit intent commit in one PostgreSQL transaction.
- Dispatch to archive/SIEM is retried durably and idempotently.
- Audit storage failure fails closed for classified high-risk mutations.
- Read-audit sampling/drop behavior is explicitly separate and observable.
- Shutdown drains only best-effort telemetry; it is not the durability mechanism.

### Implementation tasks

- [x] Classify audit actions as mandatory mutation, mandatory sensitive read/export, or sampled read.
- [x] Add transactional audit-outbox tables and sqlc queries, partitioned/retained consistently with audit logs. Migration 015 adds the bounded sanitized intent envelope, leases/retries/dead state, stable event identity, delivered-receipt retention, and SIEM dedupe column.
- [x] Introduce transaction-aware mutation services so domain write and audit intent share a transaction. Migrated families now include project namespace membership; security templates/policies/scans/cancellation; all coarse and native Kubernetes/CRD RBAC role, binding, and grant changes; identity group-mapping CRUD and administrative multi-binding resync; administrative user lifecycle/password/session invalidation; self-service password/API-token/logout and race-safe failed-login lockout transitions; cluster lifecycle/ownership/registration options-confirm-retry-cancel and agent-token/upgrade operations; cluster-group lifecycle and bulk membership moves; cluster-template CRUD/apply/reapply/detach; network-policy template and application lifecycle; Gatekeeper constraint create/delete; extension install/enable/disable/bundle verification; platform-default-template update/reapply; chart-rating create/upsert/update/delete; general settings and SSO-provider create/delete; dashboard-widget and Prometheus-datasource CRUD; GitOps registration-source CRUD/manual/webhook sync requests; platform-setting single/batch/reset mutations; read-audit-policy CRUD; notification-template override/reset; quota-plan CRUD; maintenance-window CRUD and deferred-operation enqueue/cancellation; control-plane policy/alert-acknowledgement/silence mutations; legacy and multi-registry configuration; Dex connector/settings/SSO stage transitions; SMTP settings; SIEM forwarders and test intents; webhook subscriptions/test/retry intents; Vault connections/default selection/health results; cloud credentials; backup storage/backups/schedules/triggers/restores; catalog and project-catalog repository/subscription/installed-chart lifecycles; logging output/pipeline/saved-search/apply-test/retry/hosted-Loki-token/attach lifecycles; tool install/upgrade/uninstall/adopt/retry lifecycles; workload scale/restart/delete/retry lifecycles; monitoring backend/cluster configuration, shared and per-cluster stack lifecycle, and operation retry; alerting notification-channel/rule/event/silence/inhibition lifecycles; and the Flux delivery source/bundle/version/target/rollout/approval/deployment/system-rollout lifecycle. Gatekeeper create/delete now commits desired state, an identifier-only tunnel task, and mandatory audit intent together; request handling performs no member-cluster effect, while generation-fenced reconciliation records sanitized outcomes and retries failed rows after a five-minute cooldown. Cluster-template, network-policy, and platform-default reapply desired state commits with unique identifier-only task intents and audit; network-policy bulk apply is idempotent and atomic across namespaces; cluster-group subtree deletion commits the parent and per-affected-cluster audit intents together; alerting rule-channel association writes share the parent transaction. GitOps sync, multi-registry, SIEM/webhook/alert-channel test operations, cloud-credential materialization, catalog sync requests, bundle-version resolution, and rollout scheduling also commit their task-outbox intent in the same transaction. Extension configuration and verification omit manifests/bundle configuration from audit; chart-rating aggregates update in the same locked transaction and omit review text; settings cache changes and SSO runtime registration occur only after commit, with audited compensation for failed provider registration. The final raw-Kubernetes, DLQ, management-backup, image-rescan, managed-resource, pod-delete, and node-operation families now have explicit synchronous fail-closed or durable external-effect saga boundaries, so every classified local mutation family has a named pre-effect durability decision.
- [x] Add an idempotent outbox dispatcher with lease, attempt, next-attempt, terminal error, and dead-letter state. `audit:outbox_dispatch` is leader-elected, runtime-validated, scheduled in both supported process compositions, and observable through database metrics/alerts.
- [x] Remove the lossy global writer from the mandatory compliance-export path and expose a fail-closed synchronous persistence API for other high-risk callers.
- [x] Retain bounded async batching only for sampled reads and expose dropped counts by policy/action.
- [x] Add fail-closed behavior for an unhealthy mandatory audit path.
- [x] Add archive/SIEM delivery dedupe keys. Migration 017 adds the immutable event-filter glob primitive, and `DeliverAuditOutbox` now inserts a stable-UUID receipt into every matching enabled SIEM queue in the same PostgreSQL statement that persists `audit_log` and acknowledges the outbox. PostgreSQL 16/17 integration coverage proves wildcard/comma filtering, disabled/nonmatching exclusion, exact envelopes, and crash replay without duplicate receipts.
- [x] Extend support bundles with redacted audit-pipeline health. `audit-pipeline-health.json` contains lifecycle counts/timestamps only and deliberately excludes action, resource, detail, and error content.

### Tests and validation

- [x] Transaction rollback leaves neither mutation nor audit intent. Project, security, coarse/native RBAC, identity group mappings/resync, administrative-user, self-service auth, cluster-management/groups/templates/network-policies/Gatekeeper constraints, extensions/platform-default templates, chart ratings, general settings/SSO, dashboards/Prometheus datasources, GitOps registration, platform settings, read-audit policies, notification templates, quota plans, maintenance windows/deferred enqueue/cancellation, control-plane policy/alert/silence state, registry, Dex, cloud-credential, backup/restore, catalog/project-catalog, logging, tools, workloads, monitoring, alerting, and Flux delivery transaction tests exercise audit-write failure rollback; GitOps, cluster-template, network-policy, Gatekeeper, platform-default reapply, and alerting coverage prove desired state/task intents plus audit commit or roll back together, cluster-group and identity-resync coverage prove multi-audit membership/binding decisions cannot partially commit, while registry, cloud-credential, catalog sync, bundle-version, and rollout-planner proofs cover domain + task intent + audit as one decision, and the PostgreSQL 16/17 smoke executes a real insert/rollback assertion. Gatekeeper coverage additionally proves the request path performs no pre-commit remote effect, delete locks and generation-fences its row, task/audit payloads exclude authored YAML, Kubernetes 404 deletion converges, stale tasks cannot overwrite newer intent, remote failures persist only sanitized errors, and the bounded sweep repairs pending plus cooldown-eligible failed rows. Extension/platform-default coverage proves every mutation entry reaches the exact production transaction runner, locked decisions roll back cleanly, and manifests/bundle configuration never enter audit or task payloads. Chart-rating coverage proves chart/natural-key locking, aggregate rollback, truthful cross-chart/not-found behavior, all-entry wiring, and review-text redaction. General-settings/SSO coverage proves cache and runtime changes occur only after commit, provider-key and delete locking, secret/client-ID/issuer/query/org redaction, audited compensation after registration failure, and truthful repair-required behavior if compensation also fails. Platform-setting coverage additionally proves cache invalidation occurs only after commit, Charlie runtime transitions compensate on rollback, and audit detail omits configuration values; read-audit-policy and maintenance-window coverage likewise prove evaluator invalidation is post-commit; quota, maintenance, and native-RBAC state-dependent updates lock target rows before decisions. Native-RBAC coverage proves authorization-cache invalidation is post-commit; control-plane coverage proves silence reasons are excluded from audit evidence and delete-not-found semantics are truthful. Catalog and logging tests additionally prove durable idempotency replay cannot commit newly staged state behind an older operation, while logging saved-search coverage proves query state/audit rollback and query-text omission; tool/workload/monitoring tests reject cross-operation key aliasing, and monitoring proves metadata + operation + audit rollback as one unit. The external-effect tests additionally prove pre-effect audit ordering for raw proxy writes; DLQ phase/replay convergence; management-backup generation, resource-version, active-Job, and truthful Job-outcome behavior; resource/node active-target serialization; node eviction observation; workload attempt CAS; receipt scope/revocation checks; sanitized errors; and key-rotation coverage for every new encrypted intent column.
- [x] Commit succeeds, process dies before dispatch, replacement dispatches once. Dispatcher lease/retry tests plus the PostgreSQL replay smoke reset a delivered lease and prove the stable event key leaves exactly one canonical row.
- [x] PostgreSQL outage causes high-risk mutation failure, not unaudited success. A drift-checked inventory now instantiates all 54 production shared transaction interfaces across auth, authorization, cluster lifecycle, security, catalog, logging, monitoring, backup, settings, delivery resources, and related mutations. Healthy execution commits a real state marker and sanitized audit outbox atomically; with PostgreSQL stopped, every `BeginTx` fails before its callback/effect and maps through `audit.ErrOutboxUnavailable`; recovery proves exactly the healthy markers/intents and zero outage markers, intents, or secret canaries. Real HTTP boundaries additionally prove RBAC returns standard `503 audit_unavailable`, login returns no token/cookie, SMTP test makes no remote send, and raw Kubernetes forwarding makes no remote dispatch during outage. The same guarded PostgreSQL 16 drill exercises real compliance apply/revert plus delivery planner, rollout action/approval, deployment control, and system-rollout services over a seeded graph, proving healthy state+audit, pre-effect outage failure, and no recovery leakage. Normal and race modes pass via `make test-postgres-outage-qualification`. The qualification exposed and fixed generic transaction begin/commit error mapping plus two rollout SQL parameter-inference defects.
- [x] Redis outage does not lose committed audit intent. Canonical audit intent and every matching SIEM receipt are PostgreSQL-only; Redis schedules delivery but is not the source of truth. The PG16/17 migration matrix replays a delivered outbox row and proves one durable receipt per destination after recovery.
- [x] Partition rotation and retention preserve outbox foreign-key behavior. The outbox intentionally has no partition FK; PostgreSQL 16/17 real delivery lands through the partitioned parent, every down/up edge passes, and retention deletes only old delivered receipts.
- [ ] Load test proves no mandatory audit loss at certified throughput.

## 13. Finding 9 — Resumable CIS scan ingestion

### Required invariants

- Every running scan has a durable owner lease and next poll time.
- Server restart, leader change, or tunnel-owner loss resumes the scan.
- Exactly one terminal transition wins.
- Cancellation and timeout are represented explicitly.
- Polling uses the correct tunnel-owner queue and does not spawn untracked goroutines.

### Implementation tasks

- [x] Persist scan phase, generation, owner lease, attempt, next poll, deadline, and upstream report identity.
- [x] Replace `go pollScanReport` with a tunnel-queue task and durable outbox enqueue in the create transaction.
- [x] Add a periodic orphan/running-scan recovery sweep.
- [x] Add CAS updates for running→complete/failed/cancelled.
- [x] Separate transient tunnel/provider errors from terminal report errors.
- [x] Add cancellation API and UI.
- [x] Add progress events derived from durable state.
- [x] Bound report size and findings ingestion transaction size.

### Tests and validation

- [x] Restart before first poll, during poll, and after report read/before DB commit. Lifecycle tests now exercise recovery-outbox reconstruction followed by replacement-owner finalization, transient tunnel-owner loss after claim followed by durable reschedule and replacement-owner completion, and a failed finalization commit after the report is read followed by a successful lease-scoped retry.
- [x] Duplicate task delivery produces one final result.
- [x] Disconnected cluster resumes before deadline and times out after deadline. A tunnel disconnect before `poll_deadline` persists `next_poll_at` plus an outbox retry without failing the scan; the recovery sweep at the deadline performs the durable terminal failure instead of enqueueing another poll.
- [x] Two server replicas cannot both finalize conflicting results.

## 14. Finding 10 — Agent and protocol compatibility

### Required invariants

- App version, heartbeat schema, tunnel protocol, and delivery capability versions are independently negotiated.
- Current server accepts only documented supported/deprecated ranges.
- Unknown future majors and missing required capabilities fail before work dispatch.
- N/N-1 rolling server and agent upgrades are tested in both orders.
- Offline clusters continue the last accepted generation and reconcile safely after reconnect.

### Implementation tasks

- [x] Move compatibility ranges into the release compatibility artifact and generate Go/Helm/docs views.
- [x] Replace hard-coded v0.1/v0.2 constants.
- [x] Add upper bounds and explicit future-version behavior.
- [x] Include protocol and capability sets in CONNECT admission.
- [x] Return machine-readable incompatibility reasons and upgrade recommendations.
- [x] Gate operations by required capability, not app version alone.
- [x] Add agent canary, pause, rollback, stuck detection, and max-unavailable acceptance tests. Signed system-release validation requires canary, pause/resume, controller-restart/rollback, explicit rollback, and max-unavailable proof keys in the live-delivery evidence artifact. Deterministic service/scheduler tests cover stable explicit canary ordering, closed pause/resume/rollback transitions, progress/assignment deadlines, stuck agent-operation failure, rollback fencing, and hard count/percentage unavailable budgets. Implementation validation also found and fixed two safety defects: large canary cohorts are now released in bounded slices instead of bypassing `max_concurrent`/`max_unavailable`, and partial-canary rollback completion counts only assignments that actually left the previous release. Failed and not-yet-min-ready assignments continue reserving availability before another slice can release.
- [x] Document support lifetime and deprecation windows.

## 15. Finding 11 — Complete OpenAPI coverage

### Required invariants

- Every supported HTTP operation is in OpenAPI with stable `operationId`, auth, permissions, parameters, request schema, success schema, and standard errors.
- Internal/diagnostic/proxy operations are explicitly classified rather than silently omitted.
- Route/spec drift fails CI in both directions.
- Streaming, SSE, websocket, download, and raw proxy semantics are documented with appropriate extensions or companion contracts.

### Implementation tasks

- [x] Inventory all router operations and classify public, internal, proxy, stream, compatibility alias, or deprecated; the current fully wired test router contains 754 mounted operations, all 754 are covered, and 22 spec operations are explicitly nil-gated.
- [x] Document all supported operations, including security, settings, monitoring, logging, tools, native RBAC, streams, and SCIM.
- [x] Add `operationId` to every supported operation.
- [x] Add typed 2xx responses and reusable request schemas. All 793 quality-checked operations have named success contracts and all 151 Go-bound request shapes match with zero field drift, provisional schemas, or propertyless placeholders. The 104 actionable `202` operations have generated receipt/status semantics; five exact exceptions are documented. Kubernetes API proxy responses remain explicitly unwrapped and service-proxy responses remain typed binary passthroughs. `RouteResponseEnvelope` is retained only as an unused compatibility component; zero operations reference it. The GitOps webhook documents its real shared-secret authentication, quota reads fail closed and exhaust the authorized fleet, and platform settings expose an exact atomic batch-update operation.
- [x] Standardize pagination, error envelopes, idempotency, conditional requests, and async-operation responses. Alerting and audit filters/pagination, SCIM envelopes, conditional requests, bounded request bodies, and standard request-ID-bearing errors are explicitly typed. The final ratchet classifies exactly 104 actionable documented `202` mutations; every one requires a bounded 1–128-character `Idempotency-Key`, persists actor/route/target/canonical request digest and the exact receipt atomically with state, task/event, and mandatory audit intent, returns typed `202` with truthful `Location` and `Retry-After`, replays the original receipt without duplicate effects, rejects changed key reuse with `409`, and exposes an exact status resource. The final 36-route tranche covers Delivery source/target/rollout/deployment controls; cluster, node, pod, template, allow-list, NetworkPolicy, and Gatekeeper controls; DLQ/task-outbox/backup/catalog/GitOps/monitoring/webhook administration; Charlie retry; SIEM tests; and Dex apply/register. Exactly five locked exceptions remain because their semantics are intentionally ingest, privacy acknowledgement, or interactive message exchange: GitOps webhook ingest, API-server audit ingest, password-reset acknowledgement, Charlie thread message, and Charlie session message. `scripts/openapi-quality.mjs` locks both the 104-operation contract and the exact five-route exception inventory. Final generation reports 793 documented operations, 754/754 mounted-route coverage, 776 covered spec operations including 22 nil-gated operations, 151/151 request bindings, and zero route, request-shape, generated-client, SDK, embedded-spec, or SQLC drift.
- [x] Add permission/scope vendor extensions consumed by route tests and generated route inventory.
- [x] Add deprecation/sunset metadata to aliases.
- [x] Change `--check` to fail on missing supported routes and enforce a zero unclassified-route budget.
- [x] Generate embedded spec and client artifacts in one deterministic command.
- [x] Add spectral/schema linting and breaking-change comparison against the last release.

### Validation

- [x] Coverage reports 100% for supported routes and zero unclassified routes.
- [x] Zero supported operations lack `operationId` or typed success schemas.
- [x] Generated types/client/embed are clean after regeneration.
- [x] API compatibility diff is reviewed and stored for releases.

## 16. Finding 12 — Generated frontend API client and wire/view separation

### Required invariants

- HTTP method, URL, parameters, request body, and response wire type are generated from OpenAPI.
- Domain/view models may enrich generated DTOs only through explicit mapper functions.
- No handwritten interface shadows a generated schema.
- Request casing is generated at the boundary; response casing is deterministic and type-safe.
- TanStack Query keys include every variable used by the request.

### Implementation tasks

- [x] Select and pin an OpenAPI TypeScript client generator that supports fetch, binary, and cancellation requirements.
- [x] Require `operationId` before generating an operation.
- [x] Generate operation functions and wire DTOs under a clearly generated directory.
- [x] Build small domain API modules and mappers around generated operations.
- [x] Migrate `delivery.ts` first to eliminate all 21 schema shadows.
- [x] Migrate cluster CRUD and remove phantom Cluster fields.
- [x] Migrate security, logging, monitoring, RBAC, catalog, tools, backup, settings, and remaining modules. All ordinary control-plane JSON requests now use generated OpenAPI operations with exact wire boundaries, cancellation propagation, and feature-owned view mapping where required. This includes security, logging, monitoring, RBAC, catalog, tools, backup, settings, Charlie, Delivery, cluster detail, projects, registration, extensions, Vault, workloads, webhooks, cluster groups, compliance, SIEM, kubectl shell, nodes, audit policy, administration, snapshots, metrics, resource search, and both kubeconfig modes. Typed challenge, pagination, idempotency, ETag, asynchronous-receipt, text, and Blob semantics remain explicit rather than hidden by global transport behavior. The final raw-transport ratchet is zero ordinary compatibility calls and 25 intentional opaque Kubernetes proxy/resource-adapter calls, with separately marker-checked SSE, WebSocket, and non-JSON download transports. Generic resource CRUD correctly remains on the audited Kubernetes proxy because the durable managed-resource API intentionally covers a finite native-kind set.
  - The final ratchet is zero ordinary compatibility calls plus 25 intentional opaque Kubernetes adapters. Every ordinary control-plane request uses a generated operation; shell lifecycle status, full node pagination/raw Kubernetes-key mapping, read-audit CRUD, snapshot controls, settings, and both kubeconfig modes preserve exact wire and non-JSON boundaries. The public contract has 793 quality-checked operations and 151/151 Go-bound request shapes with zero field drift.
- [x] Split `api.ts`, `hooks.ts`, and `types/index.ts` as consumers migrate. Domain API modules now own cluster agents/registration, nodes, workloads/log streaming, metrics, Kubernetes resources/proxy calls, projects, audit/activity, Dex, search, and every settings family; React Query hooks own the matching feature boundaries; and shared types are grouped by domain. Compatibility is retained through thin export facades: `api.ts` is 179 lines, `api/settings.ts` 42, `hooks.ts` 70, and `types/index.ts` 61. CI no-growth ceilings of 220/100/100/100 lines lock those reductions in.
- [x] Generate or centrally define query-key factories from operation parameters. `frontend/src/lib/query-keys.ts` is the single typed factory for reads and invalidations, parameter-bearing operations include those parameters in their cache identity, prefix factories support family invalidation, TanStack exhaustive-dependency lint is blocking, and ESLint rejects inline `queryKey: [...]` arrays everywhere outside the factory.
- [x] Remove generic response camelization after all endpoints use generated wire mapping, retaining raw Kubernetes-object exemptions. The Axios success interceptor is now identity-only; the `astronomerRawWire` and preview-bypass mechanisms are deleted; exact wire casing is universal. Remaining conversions are explicit feature-owned cluster-agent and Delivery view mappers plus the live SSE envelope normalizer, while opaque Kubernetes objects remain untouched.
- [x] Add a generated-API boundary check that bans new product-code raw transport calls except approved proxy/stream adapters.

### Tests and validation

- [x] Generated-code drift check.
- [x] Request-field verification against real Go decoder shapes.
- [x] Type assertions for generated and mapped view models in the migrated domain boundary.
- [x] No `generated schema shadow` code-health findings.
- [x] No known phantom wire keys or accidental unknown request fields; the request-field gate covers 141 concrete Go-bound shapes with zero provisional or propertyless request schemas and zero field drift. The platform settings pass removed nonexistent `banners.*`/`tokens.*` keys, corrected token TTL units from seconds to minutes, and constrained banner severity to the backend's exact enum.

## 17. Finding 13 — Authorization-aware pagination and scale-safe lists

### Required invariants

- Authorization is applied before limit/cursor.
- Totals represent the full authorized filtered result, or the API explicitly omits expensive totals.
- Cursor ordering is stable and unique.
- No endpoint loads an unbounded fleet-wide collection merely to filter in Go.
- Filter names/casing are consistent across API and UI.

### Implementation tasks

- [x] Inventory and classify all 30 `TODO(total)` sites.
- [x] Define one pagination contract: cursor preferred for operational streams; offset permitted for small administrative sets.
- [x] Add SQL authorization scopes using visible cluster/project/namespace CTEs or joins.
- [x] Add paired count queries only where the UI needs exact totals.
- [x] Use limit+1 `has_more` where totals are unnecessary.
- [x] Fix logging, tools, catalog, RBAC bindings, resources, dashboards, cluster conditions, remediation, and security scans.
- [x] Add indexes matching authorization/filter/order predicates.
- [x] Add maximum limits and query timeouts.
- [x] Update generated API/client pagination types and DataTable behavior.

### Tests and validation

- [x] Unauthorized rows preceding authorized rows cannot create an empty first page.
- [x] Concurrent inserts do not duplicate/skip cursor results beyond documented consistency.
- [x] Explain plans at representative 100k/1m-row fixtures use intended indexes.
- [x] API contract tests assert total/has-more semantics consistently.

## 18. Finding 14 — Measured scale, HA, and recovery certification

### Required invariants

- Published fleet sizes and SLOs have a retained passing report from a production-like topology.
- Tests include steady state, reconnect storm, rollout fan-out, tunnel-owner failure, worker failure, Redis failover, PostgreSQL failover, and UI large-list behavior.
- Reports identify commit, images, chart values, Kubernetes/PostgreSQL/Redis versions, hardware, duration, and raw artifacts.

### Implementation tasks

- [x] Remove stale Argo terminology from load-test profiles and scenarios.
- [x] Add Flux assignment/status/rollout scenarios.
- [x] Add 100, 500, 1,000, and lab-only 2,000-cluster profiles.
- [x] Add realistic resource/cardinality distributions and slow/offline clusters.
- [x] Provision every synthetic tunnel agent through the public cluster/register APIs with an ordered, cluster-matching short-lived credential; adopt the durable credential returned by `CONNECT_ACK`; reserve the admin bearer for HTTP/provisioning; fail fast on incomplete mappings; and clean fixture clusters on pass/error unless an explicit debug-only retain flag is set.
- [x] Add reporting for API p50/p95/p99, tunnel latency, queue age, DB pool saturation, Redis latency, SSE lag, rollout convergence, and browser interaction budgets.
- [x] Add HA fault injectors and post-fault convergence assertions.
- [x] Add soak test and memory/goroutine/file-descriptor leak criteria.
- [ ] Size production chart defaults only from retained measured results and publish the evidence-derived recommendations.
- [x] Keyless-sign and verify each provenance-bound scale report and the same-release four-rung aggregate; retain Sigstore bundles for 90 days and reject unsigned, stale, malformed, wrong-identity, wrong-source-run, or mixed-release evidence. The fail-closed report now binds real fixture IDs and cardinality, scenario traffic, mandatory-audit mutations and conservation, HA drills, component replicas/rates, raw scrapes, source identity, commit, images, environment, and timestamps. A deterministic reducer requires multiple same-release samples before emitting unpublished server/worker/tunnel/audit sizing inputs; it cannot manufacture production defaults without real passing samples.
- [x] Add release-candidate scale workflow with environment authorization.

## 19. Finding 15 — Schema-driven Kubernetes resource explorer

### Product goal

Match or exceed Rancher's day-2 explorer for adopted clusters without implementing provisioning. Common tasks should be safe guided workflows; YAML remains an expert mode.

### Implementation tasks

- [x] Define the supported common-resource matrix: Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, Ingress/Gateway, ConfigMap, Secret, PVC, Namespace, ServiceAccount, Role, RoleBinding, NetworkPolicy, HPA, PDB, and discovered CRDs.
- [x] Build a schema/discovery API that combines Kubernetes OpenAPI, API discovery, printer columns, and Astronomer policy metadata.
- [x] Generate create/edit forms with validation, defaults, documentation, and advanced-field disclosure. The resource-schema endpoint now returns a bounded transitive `$ref` closure; generated frontend operations preserve that raw contract; create and edit surfaces offer guided/YAML modes over one Kubernetes object; live schema descriptions augment validated template defaults; edit locks immutable identity and retains the mandatory dry-run/diff/apply sequence.
- [x] Add workload image, ports, probes, resources, env/config/secret, volumes, scheduling, security context, autoscaling, and rollout controls. Guided workload editing covers the primary container, resource requests/limits, environment and external sources, PVC mounts, node selection/service accounts, non-root/read-only/privilege-escalation posture, health checks, HPA bounds/CPU targets, and Deployment rolling-update controls; PVC, PDB, NetworkPolicy, RBAC, Secret, Ingress, Service, CronJob, and Gateway forms use the same object projection with YAML escape hatch.
- [x] Add server-side dry-run, diff, conflict/field-manager display, and explicit force-conflict permission.
- [x] Add destructive-action impact previews and typed confirmation. The shared destructive dialog now presents an accessible scope/effects/recovery panel before its exact, case-sensitive confirmation input. Every generic Kubernetes delete workflow and node drain supplies conservative resource-aware effects for namespace cascades, controller-owned pods/workloads, traffic resources, NetworkPolicy/RBAC security posture, Gateway API references, secrets/configuration, CRDs, autoscaling/disruption controls, and reclaim-policy-dependent storage; pure policy tests and dialog interaction tests cover wording, ordering, reset, and disabled-button enforcement.
- [x] Add events, conditions, related resources, logs, exec, rollout history, and owner-reference navigation. The unified resource detail already exposes object-scoped Events and Conditions, owner links, workload-owned pod links, and permission-gated pod logs/exec. Explorer workload rows now route into that unified surface. A new Rollout history tab resolves Kubernetes-native ReplicaSets, ControllerRevisions, or CronJob Jobs, filters by owner UID (with kind/name fallback), sorts newest-first, displays readiness/job status and recorded change cause, and links inspectable revision resources.
- [x] Add namespace and permission-aware actions.
- [x] Preserve exact raw object keys in YAML mode and warn about managed fields/secrets.
- [x] Split the 2,837-line route into resource adapters, column definitions, action policy, forms, and detail panels. The filesystem route is now a seven-line composition wrapper; immutable route metadata, reusable drill-down primitives, generic-resource behavior, 30-plus family-specific column schemas, mutation policy, guided creation forms, deletion-impact previews, rollout history, and unified detail panels live in independently owned modules. TypeScript, quiet ESLint, formatting/whitespace validation, and 13 focused resource-policy/configuration tests pass.

### Tests and validation

- [x] Schema fixture tests across Kubernetes 1.33–1.35. Pinned fixture metadata records the official Kubernetes v1.33.0, v1.34.0, and v1.35.0 OpenAPI source URLs and their Deployment root/spec/status property contracts; handler tests replay v3 discovery/index/documents for each version and verify discovery, root preservation, and transitive definition closure.
- [x] Browser workflows for every common resource family. A generated Playwright matrix now opens the real generic detail route for Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, Ingress, Gateway, ConfigMap, Secret, PVC, Namespace, ServiceAccount, Role, RoleBinding, NetworkPolicy, HPA, and PDB; each journey verifies metadata/labels, live Conditions, and the editable YAML surface. Existing dedicated journeys cover Pods (containers/logs/exec), Services (events/owner navigation), and discovered CRDs. The 18-case matrix passes on both desktop Chromium and Pixel 7 mobile emulation (36/36).
- [x] Dry-run/conflict/forbidden/offline/partial-discovery states.
- [x] Mobile and keyboard operation for create, edit, scale, restart, delete, and YAML diff. A retry-free bundled-browser journey now completes guided resource creation, keyboard row drill-down, replica editing/scale, restart, ARIA-tab navigation, YAML edit, dry-run, accessible diff review, apply, exact typed confirmation, and delete using only focus and keyboard activation. The same test passes in Desktop Chrome and Pixel 7 emulation, asserts action bounds remain inside the mobile viewport, and records every expected mutation. The exercise also fixed a superseded asynchronous Guided/YAML tab transition, added roving tab semantics, named scale controls, and made the apply preview an announced region.
- [x] Add a pinned Rancher/Astronomer benchmark-v1 contract with neutral task definitions, Playwright instrumentation, closed evidence schema, fail-closed validator, semantic documentation drift checks, and exact Rancher backend/Dashboard revisions.
- [ ] Execute paired cold/warm automated Rancher and Astronomer runs plus the counterbalanced human clarity/error-recovery study, then retain sanitized benchmark evidence.

## 20. Finding 16 — Accessibility completion

### Required invariants

- WCAG 2.2 AA for supported workflows.
- All controls have programmatic names and labels.
- All pointer actions have keyboard equivalents and visible focus.
- Dialog focus is trapped, restored, and announced correctly.
- Tables support semantic navigation without fake interactive cells.
- Reduced motion, high zoom, and mobile reflow remain usable.

### Implementation tasks

- [x] Repair shared `Label`/form-field primitives and migrate raw label patterns.
- [x] Replace clickable `div`/`td` elements with buttons/links or complete keyboard semantics.
- [x] Repair ActionMenu and DataTable focus/selection/sorting behavior.
- [x] Remove harmful autofocus; use deliberate initial focus in modal infrastructure.
- [x] Fix heading hierarchy and empty heading primitives.
- [x] Add accessible descriptions for status-only icons, charts, terminals, and live updates.
- [x] Add skip links, landmarks, live-region policy, and consistent focus rings.
- [x] Promote core jsx-a11y warnings to errors after cleanup.
- [x] Run axe on the generated route manifest in desktop and mobile projects.
- [x] Add manual screen-reader and keyboard acceptance checklist for release candidates. `docs/accessibility-release-checklist.md` defines the supported AT/browser matrix, global focus and announcement checks, responsive/zoom/contrast checks, critical workflow scripts, evidence hygiene, defect expiry, and blocking exit criteria.
- [ ] Execute the release-candidate NVDA/Chrome, Narrator/Edge, macOS VoiceOver/Safari, and critical iOS VoiceOver matrix and retain the signed result digest.

### Validation

- [x] Zero ESLint accessibility errors/warnings in product code under the enforced policy.
- [x] Zero serious/critical axe violations on every manifest route in desktop and mobile projects.
- [x] Keyboard-only completion of registration, resource edit, RBAC binding, backup restore, and security scan workflows. `dashboard-smoke.spec.ts` drives adopted-cluster registration with focused typing and Enter; `resource-actions-keyboard.spec.ts` covers resource create, tab navigation, scale, restart, dry-run/apply, and destructive confirmation; and `critical-workflows-keyboard.spec.ts` drives native-select RBAC binding, the three-step CIS wizard, and Velero snapshot restore entirely through focus, arrow keys, typing, and Enter while asserting exact mutation bodies. The focused Chromium matrix passes 5/5 without retries.

## 21. Finding 17 — Live browser, visual, and failure-state regression coverage

### Implementation tasks

- [x] Create live browser journeys for bootstrap/login, cluster registration, reconnect, Flux deployment, rollout pause/resume/rollback, RBAC denial, resource dry-run/apply, backup/restore, CIS scan, logging query, direct/proxy kubeconfig, optional Trivy delivery/ingestion, and decommission. The final disposable production-stack suite passes 16/16 in 3.7 minutes with Playwright retries disabled. It proves real HTTPS/CA-pinned Flux reconciliation, generation-current Delivery state, real Trivy Helm installation and VulnerabilityReport ingestion, UI backup/delete/restore through the production Velero worker and MinIO, and permission-aware direct access with a parsed X.509 CA, 15-minute read-only identity, allowed ConfigMap read, denied write and Secret read, and zero token leakage. The suite also retains bootstrap/session/SSE/reconnect, RBAC, CIS, Loki, rollback, Monaco YAML dry-run/preview/apply/refetch, structurally validated proxy kubeconfig, and decommission evidence. Evidence: `/tmp/astronomer-live-browser-trivy-signoff-20260825-final`; all disposable infrastructure was removed and only the untouched user-owned `member-a`/`member-b` clusters remain.
- [x] Run the Go server, worker, PostgreSQL, Redis, frontend, and at least one controllable agent/tunnel fixture. The explicit `MANAGEMENT_BACKUP_ENABLED` contract is implemented and propagated through Helm: disabled mutations return `503` before transaction/outbox persistence and the worker owns neither backup descriptor; enabled server/worker startup requires a ready executor and the worker owns both descriptors; tunnel-core validation is independent. The matching `CRD_ENABLED` contract likewise keeps disabled CRD handlers, bindings, descriptors, and scheduler work out of both compositions and fails enabled startup without its dynamic client. A corrected disposable PostgreSQL 16/Redis 7 fixture applied all 23 migrations, built and ran the real server, worker, and frontend, verified every process/container before and after Playwright, passed 4/4 bootstrap/session/SSE/reconnect specs, and deterministically cleaned all resources. The owned 2026-08-24 k3d qualification then ran the chart-managed server, worker, PostgreSQL, Redis, and frontend with a separately adopted k3s cluster; applied the 6,517-line generated manifest; and proved a real agent heartbeat through the authenticated tunnel. The later baseline stage remained independently blocked and is not credited here. Harden the runner before shared-host use by constraining the server listener once the server exposes a bind-address option and by replacing mutable PostgreSQL/Redis tags with reviewed immutable digests.
- [x] Add deterministic visual snapshots for top operator routes at desktop, tablet, and mobile widths.
- [x] Add dark/light theme cases to the blocking visual matrix.
- [x] Add loading, empty, partial, permission-denied, offline, stale, retrying, and terminal-failure visual states. The shared state system now exposes semantically distinct, icon/tone/role-correct variants with optional recovery actions and focused interaction tests. Generic resource tables wire retryable errors into the DataTable state contract; unified resource detail uses the shared loading, authorization-denied, and retryable-error panels; existing data surfaces retain explicit empty states. Type-check, quiet ESLint, and focused state/resource tests pass.
- [x] Make screenshot diffs blocking with reviewed baselines and bounded masking for dynamic values.
- [x] Add console/page-error/network-failure assertions to the route crawl.
- [x] Add flake ownership, retry budget, quarantine expiry, and weekly trend reporting. The checked `docs/test-quarantine.json` registry requires an owner, issue, exact failure signature, maximum one retry, and a 14-day expiry; `scripts/test-flake-report.mjs` rejects unregistered skips, expired/orphaned entries, unexpected flakes, and retry-budget violations; the scheduled `test-flake-trend` workflow runs five retry-free attempts and retains its machine-readable and Markdown evidence for 90 days.
- [x] Retain traces, videos, screenshots, server/worker logs, and DB state on failure. Playwright now records traces even in retry-free runs; both browser tiers retain raw `test-results`; the live tier starts the real worker and uploads 90-day failure evidence containing server/worker logs, trace/video/screenshots, a schema-only PostgreSQL dump, safe per-table state counters, and Redis diagnostics without exporting application row contents.
- [x] Emit and always retain a sanitized fresh-cluster smoke evidence manifest. The atomic `astronomer-fresh-cluster-smoke/v1` document binds pass/fail status to commit, workflow/run identity, timestamps, adopted cluster/Kubernetes version, Flux distribution/controller image identities, management-plane/agent/shell/k3s image identities, completed checks, failed stage, and exit code; the workflow uploads it for 90 days on both success and failure without including credentials.

### Validation

- [x] Critical live suite passes repeatedly without retry. Two consecutive four-journey runs passed against the real Go server, worker, PostgreSQL, Redis, preview frontend, and browser with Playwright `retries: 0`; the second run reused control-plane state and exercised the login limiter rather than receiving a clean-room shortcut.
- [x] Visual suite has reviewed baselines and zero unapproved diffs.
- [x] Route crawl includes blocking axe checks and is paired with the visual snapshot suite rather than reviewer-only artifacts.

## 22. Finding 18 — Unified logging query experience

### Required invariants

- Every output declares capabilities: ship, test, query, tail, aggregate, link-out, retention visibility.
- Unsupported query operations are rejected during configuration or clearly labeled shipping-only.
- Query authorization and cluster/namespace scoping are enforced centrally.
- Secrets never return to the browser.

### Implementation tasks

- [x] Define a logging query-provider interface and capability discovery schema.
- [x] Implement Astronomer system Loki querying through the management API with tenant-safe LogQL rewriting.
- [x] Implement Elasticsearch/OpenSearch query adapter.
- [x] Implement Splunk query adapter where credentials/permissions permit.
- [x] Implement Datadog and CloudWatch adapters or explicit secure deep links when native querying is unsuitable. The API derives HTTPS console links from an exact official Datadog-site allowlist or validated AWS region/partition tuple, never reflects arbitrary hosts or credentials, and the UI opens them with an isolated `noopener noreferrer` browser target.
- [x] Define Syslog as shipping-only unless backed by a query store.
- [x] Normalize timestamps, pagination/cursors, fields, severity, source, and partial errors.
- [x] Add saved searches, shareable filters, context expansion, and live tail where supported. Migration 016 and owner/output-scoped CRUD APIs persist bounded private searches; the explorer round-trips filters through shareable URLs, expands result windows by five minutes, and polls a bounded five-minute tail only when the provider advertises `tail`.
- [x] Add per-provider limits/timeouts and explicit capability errors at the provider boundary.
- [x] Update UI capability badges and remove generic 501 surprises. The monitoring-endpoint DELETE route now performs a guarded, audited deletion instead of advertising `204` while always returning `501`; SQL permits cascading only terminal `uninstalled`/`not_configured` per-cluster records and refuses deletion until active cluster and managed Thanos, Alertmanager, Grafana, and Loki stacks are uninstalled, returning a typed `409` instead of orphaning releases.
- [x] Make pipeline destination selection durable and operational. The generated API round-trips `output_ids`/`output_names`; a single transaction commits the pipeline, same-cluster associations, reconcile operation, and audit intent; Fluent Bit rewrites selected namespace tags into pipeline-specific routes; unlinked outputs fail safe; and PostgreSQL returns a resource-in-use conflict rather than cascading away active routing policy. PostgreSQL 16/17 migration qualification covers rollback, batch reads, and deletion restriction.

## 23. Finding 19 — Codebase decomposition and ownership boundaries

### Required invariants

- Composition roots wire domain services but contain no domain behavior.
- Route files declare routes and middleware; authorization policy is testable metadata.
- Frontend domain modules own operations, hooks, view models, and screens without global god files.
- Refactors preserve behavior through characterization tests before movement.

### Implementation tasks

- [x] Characterize `internal/server/server.go` construction and shutdown behavior.
- [x] Introduce domain modules for security, delivery, observability, cluster operations, identity, audit, and extensions.
- [x] Move worker composition into the typed task registry/runtime.
- [x] Replace `routes.go` residual registrations with domain route modules and generated inventory metadata. Concrete endpoint registrations now live in focused API-entry, public, long-lived connection, security, delivery, resource/workload, RBAC/audit/agent, cluster, and tool/control-plane modules; `routes.go` retains only the `/api/v1` composition group and authenticated-router mount. Route-golden tests prove the refactor preserved the method/path/middleware surface, while `docs/routes.json` and `docs/generated-route-inventory.json` remain generated drift-gated evidence.
- [x] Split frontend API, hooks, and types by domain as generated operations land. Generated-operation domains include delivery, cluster CRUD, resource discovery, security policies/scans, logging, monitoring, RBAC, catalog, tools, backup, alerting, dashboards, GitOps, quotas, and settings. The remaining compatibility implementations were decomposed into feature-owned API, hook, and type modules; `api.ts`, `api/settings.ts`, `hooks.ts`, and `types/index.ts` are now 179/42/70/61-line export facades with blocking no-growth budgets.
- [x] Split the generic resource route into adapters and shared primitives. `generic-resource-table.tsx` now owns generated-hook-backed generic CRUD behavior and defense-in-depth authorization gates, `resource-list-columns.tsx` owns presentation schemas, `resource-route-config.ts` owns immutable supported-resource metadata, and `resource-table-primitives.tsx` owns accessible name links and authorization-aware row navigation. The route controller supplies only the selected adapter and family columns.
- [x] Establish dependency-direction tests and package import boundaries. `docs/architecture/dependency-boundaries.json` now defines nine owned, rationale-backed Go and frontend direction rules; `scripts/check-dependency-boundaries.mjs` parses actual source imports and the docs/release verification gates reject violations.
- [x] Add file/function complexity budgets focused on changed code rather than arbitrary blanket limits and complete the named hotspot decomposition. `server.go` is 611 lines and `app.go` is a 35-line orchestrator; persistence, core/tenant/cluster/identity handlers, integrations, Charlie, Delivery, router dependencies/policies/streams, runtime foundation/tasks/services, and Kubernetes discovery live in typed cohesive composition modules, with every new production file at or below 238 lines and every named function below the 240-line ceiling. `resources.go` is 933 lines and presentation/flattening lives in the 710-line `resource_presenters.go`. Exact characterization tests follow the owning files without weakening wiring, ordering, authorization, or transaction assertions. The changed-code and seven-hotspot gate passes across 809 changed production files.
- [x] Remove dead code only after generated inventory and runtime references prove it unreachable. Repository-wide Go reference classification reduced the generated backend and frontend dead-code inventories to zero. Thirty-seven unused sqlc queries and the legacy split audit archive/delete pair were removed only after confirming their active CAS, outbox, lease/fencing, or atomic archive-and-purge replacements; sqlc regeneration and all affected consumer tests pass.
- [x] Document owners and review requirements for security, migrations, worker topology, OpenAPI, and design-system changes.

## 24. Finding 20 — Release-bound architecture and parity documentation

### Implementation tasks

- [x] Rewrite the Rancher comparison around Flux, adopted-cluster scope, and the final implemented evidence.
- [x] Replace Argo-era control-plane-state contracts with Flux source, assignment, inventory, status, and ownership contracts.
- [x] Correct README frontend/tooling descriptions.
- [x] Generate compatibility documentation from `deploy/release/compatibility.yaml`.
- [x] Generate route, task, permission, feature, and supported-resource inventories.
- [x] Add documentation link/check validation and prohibited stale-term checks outside archives.
- [x] Separate historical/archive documents from current operator guidance visibly.
- [x] Add an enterprise capability scorecard with evidence links and honest unsupported items.
- [x] Update installation, upgrade, backup/restore, DR, air-gap, support-bundle, and troubleshooting runbooks. The operator index now links production install, management backup/restore, redacted support collection, and layered incident triage; existing upgrade, detailed DR, and disconnected install procedures cross-link the same immutable-release, key-custody, canary, proof, and acceptance contracts.
- [x] Require documentation drift checks in the release gate.

### 24.1 Live-discovered platform-baseline contract divergence — release blocker

The 2026-08-24 fresh adopted-cluster run proved that three independent
contracts had diverged. The current v1.1 runtime embeds and provisions exactly
the default-enabled components in `deploy/bundles/catalog.json` through normal
immutable Flux delivery. The fresh database still seeded and selected a dead
five-tool imperative `Platform baseline` template, while the coverage API and
current documentation hard-coded seven legacy slugs. The smoke combined both
worlds: it waited for three legacy tool-operation rows that the runtime
deliberately does not create, then expected the two real Flux releases, and
finally required a Trivy report although Trivy is not in the signed built-in
catalog. Waiting longer cannot make those contracts converge.

The product decision for v1.1 is conservative: the immutable built-in catalog
is the sole baseline membership, version, chart-digest, image-digest, namespace,
and release-name contract. It currently contains kube-state-metrics and
prometheus-node-exporter. Optional catalog tools do not become baseline members
through prose, a database seed, or a smoke-test constant. Promoting Trivy or
another component requires the same reviewed, signed, reproducible release
process as every existing built-in; no test may fabricate artifact identities
or bypass Flux through the retired template path.

#### Reconciliation tasks

- [x] Remove the fresh-database default reference to the dead imperative `Platform baseline` template and remove the unused seed row without deleting optional `cluster_tools` catalog entries. Current fresh schema 26, with this cleanup introduced by migration 023, seeds no legacy template and a `NULL` default; existing upgraded databases may retain the inert row until a separately reviewed cleanup migration.
- [x] Make the platform-baseline coverage API derive its expected component set from the embedded built-in catalog rather than a hard-coded seven-slug slice; the supported response shape is preserved.
- [x] Rewrite current platform-baseline and registration API documentation around the opt-in registration choice, compatible Ready Flux inventory, immutable rollout creation, and the two current signed built-ins.
- [x] Remove current non-archive comments and OpenAPI descriptions that still promise automatic legacy cluster-template attachment.
- [x] Make fresh-cluster smoke derive the exact default-enabled release-name and target-namespace set from the catalog, require every matching HelmRelease to be generation-current and Ready, and reject missing or unexpected Astronomer-managed releases. The live assertion uses Flux `spec.targetNamespace` and `spec.releaseName`, not UUID-derived CR names.
- [x] Remove the unconditional legacy `trivy-operator fluent-bit cert-manager` ToolOperation wait from baseline smoke.
- [x] Gate vulnerability-report acceptance on Trivy actually being a default-enabled signed catalog member; until then classify the scan as an explicit skipped optional-tool qualification, never as a passing baseline check.
- [x] Add drift tests that reject hard-coded baseline membership outside `deploy/bundles/catalog.json` and prove the fresh database does not select the retired template.
- [x] Add an explicit optional-tool live lane that installs Trivy through the supported Flux-native delivery model, waits for its durable operation and generation-current reconciliation, and then proves real `VulnerabilityReport` ingestion. `LIVE_BROWSER_TRIVY_ENABLED=1` uses a pinned Trivy chart plus verified chart SHA-256 and pinned operator/scanner/target image digests; creates the delivery through the production SourceHelmHTTP, RendererHelm, ScopePlatform planner/runtime; runs the production MirrorSubscriber; waits for succeeded/Ready state with desired and observed generation/spec digest equality; creates a real report; proves PostgreSQL ingestion; and validates the generated vulnerability API and UI. Live execution exposed and fixed three real integration defects: Helm releases now set `spec.install.createNamespace: true`; reversible migration 026 widens digest-qualified scanner versions to 128 characters with schema/release contracts aligned; and the scan target uses its pinned non-root UID/GID. Focused migration/scanner/delivery tests pass, the exact corrected-tree backend/frontend/Helm gates pass, and the final retry-disabled live suite passes 16/16. Evidence: `/tmp/astronomer-live-browser-trivy-signoff-20260825-final`.
- [x] Before promoting any non-Prometheus component, generalize built-in provisioning and validation from one hard-coded Helm source to per-component immutable sources with deterministic identities and conflict checks. Catalog parsing now accepts only canonical bounded public HTTPS repository URLs, detects conflicting immutable artifact identities, and plans one URL-sorted deterministic source per distinct repository while preserving the exact existing Prometheus source ID, name, source-spec digest inputs, and flat air-gap archive path. Each component is bound to its own resolved source URL; new source identities use deterministic URL-derived IDs/names; stored credentials, trust, status, and source fields are conflict-checked. The release builder validates arbitrary catalogs before network access, deduplicates identical artifacts, and separates same-named archives by source without changing the legacy layout. Disposable PostgreSQL 16 tests prove same-source retry stability, multi-source binding, transactional conflict rollback, and six-way concurrent convergence. That concurrency test exposed a stale serializable-snapshot race; the provisioner now acquires the per-cluster session advisory lock before opening the transaction and safely unlocks or discards the connection. Focused normal/race tests, a real bundle build plus `--check`, migration safety, sqlc drift, and diff checks pass.
- [x] Add the transitive release-image qualification and approval boundary. The gate enumerates every exact first-party, runtime, Flux-controller, built-in-bundle, and Charlie image reachable from the signed manifest; rejects mutable references; requires amd64 and arm64; evaluates fixed HIGH/CRITICAL Trivy findings; produces per-image SPDX SBOMs; enforces a closed license allowlist; and permits only exact-reference, finding-ID, named-approver, reason, and future-expiry waivers. Closed reports bind manifest/policy/waiver and per-image report hashes, are keyless-signed, and are re-verified by protected promotion. Release and resume also execute the exact Kubernetes 1.33–1.35 matrix.
- [ ] Obtain protected release-engineering approval bound to the exact tag, commit, source run, transitive runtime-image evidence, cloud evidence, RC evidence, and signed accessibility result before catalog promotion.
- [x] Re-run the complete owned fresh-cluster smoke from a clean management/adopted pair and retain a passing sanitized artifact; the manually interrupted diagnostic run remains separate non-signoff evidence. The settled-tree run passed 11 checks with one explicit Trivy-not-in-baseline skip, then removed only its owned clusters.

#### Required validation

- Catalog load/validation and provisioner tests prove membership, source,
  target namespace, release name, chart digest, and image digests are exact.
- Migration policy, fresh schema signature, and PostgreSQL 16/17 round trips
  prove the dead template cleanup is deterministic and upgrade-safe.
- OpenAPI generation, request-shape, Spectral, compatibility, embedded-spec,
  documentation-link, and prohibited-stale-term gates pass together.
- Shell syntax, ShellCheck, evidence-writer contracts, and deploy tests prove
  smoke derives rather than duplicates the catalog contract.
- Live evidence must show authenticated agent heartbeat, exact signed Flux
  controllers, exact Ready/current built-in HelmReleases and workloads,
  shell/proxy/API paths, registration `ready`, cleanup, and no credential
  material in retained artifacts.

## 25. Cross-cutting data and API migration policy

- Prefer expand/migrate/contract changes.
- Every new durable intent table includes ownership/tenant keys, generation, timestamps, status, attempts, next attempt, last error, and bounded payloads where applicable.
- Encrypt secrets before persistence; task payloads carry references, never clear credentials.
- Add indexes with migrations and demonstrate query plans on representative cardinality.
- API removals require deprecation metadata, release notes, and a supported compatibility period.
- UI migrations consume both old and new shapes only when a rolling deployment requires it; compatibility branches receive explicit removal tasks.

## 26. Security review checklist for every wave

The machine-checked `docs/security-wave-review.json` records a disposition for
all ten controls in waves 0–7 and pins each control to multiple source/test
anchors. Review completion is not a GA waiver: wave 7 deliberately retains the
live resource-bound, replay, audit-durability, and rollback qualifications that
also remain open in section 27.

- [x] Authentication and token scope
- [x] Global/project/cluster/namespace authorization
- [x] Object-level ownership and cross-tenant negative case
- [x] CSRF for cookie-authenticated mutation
- [x] SSRF and outbound destination policy
- [x] Secret redaction in errors, logs, traces, metrics, audit, support bundles, and task payloads
- [x] Request size, response size, concurrency, rate, and timeout bounds
- [x] Idempotency and replay behavior
- [x] Audit durability and content classification
- [x] Safe failure and rollback behavior

## 27. Required final validation matrix

### Static and unit gates

- [x] `gofmt`/generated drift clean
- [x] `go vet ./...`
- [x] pinned Go lint clean
- [x] `go test ./... -count=1`
- [x] `go test -race -count=1 ./...`
- [x] frontend code-health clean
- [x] frontend ESLint clean with accessibility policy
- [x] frontend TypeScript clean
- [x] frontend unit tests clean
- [x] production build and bundle budgets clean
- [x] npm audit threshold clean
- [x] Helm lint, dev render, production render, and chart contracts clean

### Contract and upgrade gates

- [x] OpenAPI supported-route coverage 100%
- [x] zero unclassified routes
- [x] zero missing operation IDs or typed supported success responses
- [x] generated TypeScript/client/embed drift clean
- [x] route permission inventory matches executable middleware
- [x] task ownership inventory matches registered/scheduled handlers
- [x] PostgreSQL 16/17 fresh and upgrade matrices pass
- [x] chart preflight version/skew matrix passes
- [x] agent N/N-1 compatibility matrix passes

### Live and failure-injection gates

- [x] critical Playwright live journeys pass without retries
- [x] route-wide axe and visual gates pass
- [x] fresh adopted-cluster smoke passes with Flux baseline
- [x] Server rolling restart preserves tunnel/task/CIS progress. The guarded PostgreSQL 16/Redis 7 process-restart qualification commits a real CIS scan plus task/audit intent, dispatches it through the production outbox and `NewTunnelWorker` on server A, persists the no-report poll/backoff, gracefully replaces A with B, reconnects an authenticated capability-correct protocol agent, and completes the same CIS row/task generations with one report and one audit. Normal and race executions pass. This is production-constructor/process evidence with a synthetic tunnel agent, not a Kubernetes Deployment rollout.
- [x] Worker restart preserves committed tasks and audit events. The same qualification commits the original notification task and audit in one PostgreSQL transaction, exits real `NewWorker` A before its scheduled Redis wakeups, starts B, and proves B drains the original rows to exactly one webhook effect and one audit; repeat dispatch is a no-op. Normal and race executions pass with complete disposable cleanup.
- [x] Redis outage/recovery converges without lost intent. A dedicated guarded drill stops its disposable Redis 7 container before the real alert-channel test transaction, while PostgreSQL 16 remains available. The successful API decision commits exactly one pending task intent and one mandatory audit intent with zero receiver effects. The production task-outbox dispatcher records one bounded/redacted failed delivery while Redis is down; the PostgreSQL-only audit dispatcher persists exactly one canonical audit event. Restarting the same Redis instance and rerunning the production dispatcher delivers the original row through the registered `notification:send` handler to a real HTTP receiver exactly once, without recreating business intent; repeat task/audit dispatch is a no-op. Normal and race executions pass, generated credentials/body/payload are absent from stored dispatcher errors, cleanup removes every owned container, and the run is exposed as `make test-redis-outage-recovery` plus the task-outbox-stalled runbook.
- [x] PostgreSQL failover/recovery meets recorded RPO/RTO. The opt-in certification lane uses a real PostgreSQL 16 primary plus physical synchronous standby, commits 24 canaries through the production Astronomer pool, sends `SIGKILL` to the primary, proves `/readyz` fails closed, promotes the standby, switches the stable writer endpoint, and proves the existing pool recovers for a durable write/read. The final evidence records 24/24 recovered canaries, RPO 0 rows/0 seconds against a zero-row threshold, and RTO 526 ms against 30,000 ms. It retains logs, topology, timestamps, provenance, thresholds, exact checks, and failed-bootstrap evidence for 90 days in CI. Managed-provider control-plane and DNS endpoint detection remain explicitly external to this local RTO.
- [x] tunnel-owner failover does not double-apply
- [ ] certified scale profiles pass and reports are retained
- [x] Add the protected pre-promotion RC producer with exact source/target artifact binding, unique owned-cluster destructive fences, previous-release install, encrypted proof seeding, full PostgreSQL backup, clean temporary-database restore and Fernet proof, exact signed-manifest upgrade, rollback/health verification, and keyless-signed digest-only evidence. Private recovery material and the uniquely owned disposable cluster are destroyed in the finally path; pre-existing clusters and ambient contexts are refused.
- [ ] backup, restore, and upgrade rehearsal pass on the release candidate

## 28. Completion evidence matrix

No implementation commit was created because the user did not request a commit. “Implemented” below means the named code and reproducible local evidence exist in the current intended working-tree diff. “Qualification pending” is not a euphemism for complete: it identifies evidence that requires a release-candidate environment, supported cloud credentials, multiple live clusters, controlled failure injection, or human assistive-technology review.

| Finding | Implementation evidence | Focused/reproducible evidence | Status |
|---:|---|---|---|
| 1 | Active-cluster/scoped security SQL, handler-side cluster-visibility intersection, routes, and mandatory export audit | Seven-persona route matrix, cross-cluster/tombstone handler tests, pre-pagination authorization test, SQL-shape tests, and API-contract gate | Implemented locally |
| 2 | `internal/worker/task_registry.go`, `tasks.CoreRuntime`, runtime validation, task inventory, immutable family runtimes, explicit `worker.StandaloneRuntime` and `worker.TunnelRuntime`, `internal/server/deferred_replay.go` | All 83 registered descriptors runtime-bound; zero task `Configure*` globals/reset paths; complete worker/tunnel construction; missing/typed-nil rejection; scoped-core isolation and normalization tests; family runtime tests; full worker/task race gate; local multi-process PostgreSQL/Redis restart and tunnel-owner failover qualifications | Implemented and locally qualified |
| 3 | Provider registry plus EKS/GKE/AKS/DOKS materializers, shared AWS session-token/AssumeRole resolver, typed bounded redacted error taxonomy, fail-closed unsupported-provider handling, and protected reversible cloud-acceptance producer | Sanitized API fixtures verify methods, paths, queries, request bodies, ETags, provider failure classification, AWS refresh/race behavior, exact target binding, restoration, convergence, closed evidence, and signing contracts | Implemented locally; credentialed four-provider execution pending |
| 4 | Repaired enterprise verification scripts, pinned lint, deterministic generated checks | Go, frontend, Helm, API, docs, dependency, accessibility, and visual gates | Implemented locally |
| 5 | Migrations 005–026, schema v26 binary/readiness/release contract, generated Helm preflight metadata, repository-owned session-locked migrator, release-line fixtures, destructive-DDL policy, signed-manifest upgrade helper, and protected clean-restore RC producer | Current local migration/generated/release/chart gates bind the binary and chart to schema 26. The dated 2026-08-24 PostgreSQL 16/17 release-upgrade and every-edge round-trip evidence qualified schema 23; subsequent migration 026 is covered by current migration/scanner/release contracts. The RC producer verifies a full dump in a clean temporary database and decrypts an original-key Fernet proof before exact-artifact upgrade. | Implemented locally through schema 26; execution on the exact release candidate remains external |
| 6 | Charlie listener/network isolation, exact installation-bound SPIFFE client verification during TLS handshake, dormant chart contract, and activation-owned resource teardown | Real TLS-socket negative tests for untrusted CA, expiry, wrong URI, and removed signing key; idempotent uninstall tests for agent namespace, product Secrets, private Service, both NetworkPolicies, and listener shutdown; deploy/chart contracts | Implemented locally |
| 7 | Proxy-default access plus explicit 15-minute read-only, CA-pinned direct TokenRequest credentials for adopted clusters; lossless cluster-update API; atomic proxy token/audit and pre-effect direct audit | Handler transaction/rollback tests, route denial, exact agent capability and manifest RBAC tests, generated OpenAPI/TS/Go clients, retained live direct/proxy journey | Implemented and locally qualified |
| 8 | Transactional mandatory-audit outbox, stable delivery identity, dispatcher, SIEM dedupe, health metrics/alerts/runbook/support evidence, and explicit sagas for raw Kubernetes writes, DLQ administration, management backup, image rescan, and managed resource/pod/node mutations | Per-family rollback and entry-point guards; pre-effect ordering; identifier-only tasks; active-target/generation/attempt fences; crash replay; receipt scope/revocation; truthful Job/drain observation; redaction/key rotation; local PostgreSQL/Redis outage and multi-process failure injection; load harness conservation checks | Implemented locally; certified production-like mandatory-audit throughput remains external |
| 9 | Durable CIS lifecycle schema, generation/status recovery, restart-safe ingestion | CIS lifecycle/query/handler tests plus the local rolling-server recovery qualification | Implemented and locally qualified |
| 10 | Generated agent/protocol compatibility contract and capability negotiation | Compatibility generator/check, protocol tests, and the N/N-1 compatibility matrix | Implemented and locally qualified |
| 11 | 793 quality-checked operations; 754/754 mounted routes; 22 nil-gated operations; 151/151 Go-bound request shapes; 104 actionable `202` contracts plus five exact exceptions; stable IDs, typed envelopes, pagination/idempotency/conditional semantics, Spectral validation, pinned oasdiff comparison, and sunsetted aliases | API coverage/quality/security/request-binding/async-semantics gates; zero route, request, provisional-schema, generic-envelope, or generated drift; focused normal/race tests | Implemented locally |
| 12 | Generated TypeScript operations/types, generated Go SDK, transport boundary, domain clients, and the shared truthful asynchronous-operation poller | Zero ordinary compatibility transports and 25 intentional Kubernetes adapters; generated drift; frontend type/lint/build; 159 unit files and 1,042 tests; abort/retry/blocked/partial/terminal/duplicate-submit/accessibility coverage; Go SDK/CLI build | Implemented locally |
| 13 | Authorization-aware SQL pagination queries, maximum limits, supporting indexes 006–014, and planner-visible scoped predicates | Query contract/handler tests plus live PostgreSQL certification with 100,000 mixed-status clusters, 1,000,000 scoped operations, intended-index assertions, and concurrent keyset insert boundaries | Implemented locally |
| 14 | Real-ID/cardinality estate profiles for 100/500/1,000/2,000-lab, four-hour soak, fail-closed traffic/audit/HA/leak certification, component sizing reducer, keyless-signed per-rung evidence, and a verified same-release aggregate workflow | Loadtest race tests, evidence/reducer tests, malformed/missing/zero/error/RPS/cardinality/conservation negative cases, workflow identity and mixed-release rejection | Producer implemented locally; production-like measured profiles, published sizing, and retained passing rows remain external |
| 15 | Kubernetes discovery/schema API, guided forms for the common-resource matrix, dry-run/SSA/conflict recovery, impact previews, unified detail/rollout/log/exec surfaces, and benchmark-v1 instrumentation | Discovery/form/conflict/authorization tests; 36/36 desktop/mobile resource-family journeys; 24 focused decomposition tests; 1/1 keyboard/YAML characterization | Product and benchmark harness implemented; paired Rancher automated and human benchmark execution remains external |
| 16 | Semantic primitives, strict accessibility lint, route-wide Axe crawl, keyboard/focus repairs, manual RC checklist, and protected approval digest binding | 264 desktop/mobile route checks; zero serious/critical Axe violations; five keyboard-only critical workflows | Automated boundary implemented; NVDA/Narrator/VoiceOver RC execution remains external |
| 17 | Deterministic visual baselines, route/Axe crawl, real server/worker/PostgreSQL/Redis/agent journeys, and local outage/restart/failover fixtures | 24 visual snapshots, 264 route/Axe checks, and retained retry-disabled 16/16 live Flux/Trivy/Velero/direct-access suite | Implemented and locally qualified; cloud, scale, accessibility, and exact-RC gates are tracked separately |
| 18 | Logging capability discovery; Loki/OpenSearch/Splunk adapters; allowlisted Datadog/CloudWatch console links; redaction and provider limits; private API-backed saved searches; shared filter URLs; context expansion; capability-gated live tail | Owner/cross-user handler tests, strict request/OpenAPI field contract, provider/open-redirect tests, PostgreSQL 16/17 migration round trip, frontend wire/share/UI tests | Implemented locally; native provider-account conformance remains release evidence |
| 19 | Domain route modules, typed task runtime, generated API boundary, dependency/complexity/ownership budgets, zero classified dead-code candidates, cohesive server production-composition phases, extracted resource presenters, settings/SSO ownership, and node receipt authorization | `server.go` 611 lines, `app.go` 35, every new server composition file ≤238, `resources.go` 933, `resource_presenters.go` 710; exact wiring/order characterization; handler/server normal and race; full build; scoped vet/lint; whole-tree complexity and full Go gate | Implemented locally |
| 20 | Flux/adopted-cluster parity docs, archived Argo-era material, generated compatibility/inventories, ownership guide | Documentation/link/terminology checker (101 current documents, 591 relative links) | Implemented locally |

### Historical reproducible execution record — 2026-08-24

The rows below are retained dated snapshots, not current inventory counts. Current schema, API, frontend, documentation, and final-tree results are recorded in the completion matrix and the 2026-08-25 snapshot that follows.

| Gate | Result retained in this work session |
|---|---|
| Final enterprise gates | Pass on the stopped-editing 2026-08-24 tree. The broad `all` run passed all 23 migration checks, sqlc drift, build, vet, zero-issue lint, complete normal/race Go suites, 99-document/564-link validation, release compatibility, 722/722 routed OpenAPI coverage, 747 operation-quality checks, 141 exact request shapes, reviewed breaking changes, generated/embed drift, and security metadata. After its only failures identified two stale generated inventory documents and then missing typed frontend idempotency callers, those were fixed and the settled scopes passed independently: API contract, frontend health/lint/type-check, 142 Vitest files/986 tests, production build, bundle budgets, zero-vulnerability audit, and Helm lint/development/production/contracts. A final post-fix static pass recorded 738 changed production files under seven hotspot ceilings and nine dependency rules across 1,176 files/8,624 imports. The live agent-identity acceptance correctly remained skipped because `AGENT_IDENTITY_TEST_CONTEXT` was not supplied. Artifacts: `/tmp/astronomer-verify-enterprise-20260824-final-rerun`, `/tmp/astronomer-verify-enterprise-20260824-final-api`, `/tmp/astronomer-verify-enterprise-20260824-final-frontend-rerun`, and `/tmp/astronomer-verify-enterprise-20260824-final-helm`. |
| `go test ./... -count=1` | Pass across every package. |
| `go test -race -count=1 ./...` | Pass across every package. |
| Explicit worker composition follow-up | Pass: all 83 registered descriptors resolve through immutable standalone/tunnel graphs; all 31 former task configurators and test reset paths are gone; scoped `CoreRuntime` context tests cover normalization, typed-nil rejection, and invocation isolation; family tests cover every converted runtime; generated operation/task and code-health inventories are current. |
| `go vet ./...` and pinned `golangci-lint` 2.12.2 | Pass; zero lint issues. |
| Alerting and cluster-group transaction follow-up | Pass: all 16 alerting write paths use the transaction-bound audit executor; alert-channel tests persist `notification:send` task-outbox and audit intents atomically; rule-channel associations share the rule transaction; cluster-group CRUD, subtree deletion, and bulk moves commit all affected audit intents with the state change. Static coverage guards every mutation entry point, rollback tests cover domain/task/multi-audit failure, and the stale direct-enqueue exception was removed. |
| Cluster-template transaction follow-up | Pass: CRUD and all apply/reapply/detach paths use the transaction-bound audit executor; application desired state, a unique durable apply task, and audit intent commit together; detach includes registration-policy cleanup. Static entry-point coverage, rollback proof, full handler/server tests, and lint pass. |
| Network-policy transaction follow-up | Pass: template CRUD, atomic idempotent multi-namespace apply, reapply, successful revoke, and failed-revoke state all use the transaction-bound executor. Application rows, one unique task intent per binding, and audit commit together; generated sqlc, rollback/static coverage, database/handler/server tests, and lint pass. |
| Dashboard transaction follow-up | Pass: dashboard-widget and Prometheus-datasource create/update/delete all use the transaction-bound executor, including delete prerequisite reads and datasource auth-preserving read-modify-write. Audit failure rollback and six-entry-point static guards pass; datasource PUT now persists name, preserves ciphertext when auth is omitted, rejects hostless/credential-bearing URLs, and exposes only endpoint origin in audit detail. Generated sqlc, full handler/server tests, lint, and the subsequent combined enterprise gate pass. |
| GitOps registration transaction follow-up | Pass: source create/update/delete plus manual/webhook sync use the transaction-bound executor. Sync commits a source-ID-only task intent and audit together, returns typed `202 queued` plus task ID, and never clones inline; task/audit failure rollback and five-entry-point guards pass. Credentialed writes fail closed without Fernet, repository URLs reject embedded credentials/query data, audit omits repository locations, and undecryptable Fernet fails distinctly while legacy plaintext remains readable. Full handler/worker/server, generated frontend, focused UI, and API-contract gates pass. |
| Platform-settings transaction follow-up | Pass: single update, atomic batch update, and reset use one transaction-bound setting/audit executor; audit-outbox failure rolls back every mutation and returns fail-closed `503 audit_unavailable`; local cache invalidation occurs only after commit. Charlie enable/disable remains ordered around its runtime lifecycle and compensates the runtime transition when the database decision fails. Audit detail contains keys and change metadata, never raw configuration values. Static all-entry-point coverage and the complete backend enterprise gate pass, including full normal/race Go suites, vet, zero lint issues, documentation, architecture/complexity budgets, and API/generated-contract checks. |
| Read-audit-policy transaction follow-up | Pass: create/update/delete and prerequisite reads use one transaction-bound policy/audit executor. Audit-outbox failure rolls back the policy, returns fail-closed `503 audit_unavailable`, and leaves the evaluator cache untouched; successful writes invalidate only after commit. Three-entry-point static coverage and focused handler/server suites pass. |
| Notification-template transaction follow-up | Pass: override upsert and reset use one transaction-bound state/audit executor; audit-outbox failure rolls back either mutation and returns fail-closed `503 audit_unavailable`. Audit detail carries format/size flags but no subject/body content; preview remains read-only. Two-entry-point static coverage and focused handler/server suites pass. |
| Quota-plan transaction follow-up | Pass: create/update/delete use one transaction-bound state/audit executor. Update and delete acquire a plan-row lock before existence and project/user reference decisions, preventing concurrent delete/recreate gaps. Audit failure rolls back all three mutation shapes with fail-closed `503 audit_unavailable`; reserved/in-use/not-found contracts remain distinct. Generated sqlc, three-entry-point static coverage, and focused handler/server suites pass. |
| Logging saved-search transaction follow-up | Pass: caller-owned saved-search create/update/delete reuse the logging transaction executor. Audit failure rolls back search state and returns fail-closed `503 audit_unavailable`; delete re-reads ownership inside the transaction. Audit stores a query digest and bounded metadata but never query text. Logging's static guard now covers all 15 high-risk entry points; focused saved-search and transaction suites pass. |
| Identity group-mapping transaction follow-up | Pass: mapping create/delete and administrative user resync use one transaction-bound multi-audit executor. Resync reads the persisted connector snapshot and reconciles all global/cluster/project group-sync bindings inside the transaction, writes per-binding plus aggregate audit intents, and invalidates the user's RBAC cache only after commit. Audit failure restores mapping/binding state and returns fail-closed `503 audit_unavailable`; three-entry-point static coverage and focused suites pass. |
| Maintenance transaction follow-up | Pass: maintenance-window create/update/delete, encrypted idempotent defer enqueue, and pending deferred-operation cancellation use transaction-bound audit executors. Update/delete/cancel lock their target rows before state-dependent decisions; audit failure rolls back CRUD/cancel with fail-closed `503 audit_unavailable`, while a failed defer transaction safely returns the blocking response without retaining a queued operation. Evaluator-cache tests prove rollback leaves the valid cache intact and successful window commits invalidate it. Renaming the SQL source removes the accidental Windows-only generated filename and duplicate hand-maintained CRUD shim, leaving canonical sqlc plus one narrow idempotency scan helper. Entry-point, domain+audit rollback, generated-drift, and focused handler/worker/server/database suites pass. |
| Control-plane transaction follow-up | Pass: policy update, alert acknowledgement, and silence create/delete use one transaction-bound state/audit executor. Audit failure restores all four mutation shapes and returns bounded `503 audit_unavailable`; silence delete now returns the deleted row so missing IDs produce a truthful `404`. Audit detail identifies controller/condition/duration without copying the operator-entered silence reason. Four-entry-point static coverage and focused handler/server/database suites pass. |
| Native-RBAC transaction follow-up | Pass: native Kubernetes/CRD grant create/delete use one transaction-bound state/audit executor. Delete locks and re-reads the grant inside the transaction; audit failure restores either mutation, returns bounded `503 audit_unavailable`, and never invalidates the user's native authorization cache. Two-entry-point static coverage and focused handler/server/database suites pass. |
| Agent-upgrade transaction follow-up | Pass: agent upgrade commits its idempotent lifecycle-operation row and mandatory audit intent in one transaction. Audit failure removes the operation and idempotency claim, returns bounded `503 audit_unavailable`, and publishes no cluster-agent change event; success publishes only after commit. Audit detail omits the private target-image location. Domain+audit+event-order coverage and focused handler/server suites pass. |
| Cluster-registration transaction follow-up | Pass: options, confirmation, failed-step retry, and cancellation use a transaction-scoped registration service for state/step writes and mandatory audit. Retry locks the failed step before its state decision; auto-step errors now propagate. SSE and Prometheus effects buffer until commit, while audit failure restores phase/options/timeline state, returns bounded `503 audit_unavailable`, and publishes nothing. Four-entry-point, row-lock, buffered-effect, rollback, and focused registration/handler/server/database suites pass. |
| Snapshot lifecycle transaction follow-up | Pass: workload snapshot create/delete/restore and schedule create/update/delete use one transaction-bound state/task/audit executor in production. Snapshot and restore specs remain in PostgreSQL; identifier-only create/restore tasks use the tunnel queue, HTTP performs no member-cluster mutation before commit, deletion carries only the non-secret external reference required after the local row is removed, and deterministic external names make crash replay idempotent. Schedule update/delete lock their rows before state-dependent decisions. Control-plane snapshot trigger likewise commits its pending row, durable privileged-Job task, and mandatory audit together; the worker records only a sanitized retry status while returning the full transport error to Asynq. Task/audit rollback, no-precommit-remote-call, no-post-rollback-event, row-lock, stale-intent, replay, retry, generated-inventory, sqlc drift, focused handler/worker/server, and lint gates pass. |
| Task-outbox administrative retry transaction follow-up | Pass: retry locks the durable outbox row, rejects delivered work under that lock, resets the delivery-attempt budget and stale lease/error fields, and commits the state transition with a mandatory sanitized audit intent. Audit failure rolls the retry back and returns bounded `503 audit_unavailable`; raw payload and dispatcher error content are absent from audit. Focused handler/server/sqlc tests pin commit, rollback, delivered-row conflict, SQL row locking, and retry-budget reset. |
| API-server allow-list transaction follow-up | Pass: PUT locks existing desired state before the monitor-to-enforce safety decision, then commits the allow-list row, identifier-only reconcile task, and CIDR-redacted audit intent together. On-demand reconcile commits its task and audit under the same row lock. Production HTTP performs no cloud-provider call or direct queue-only handoff; task/audit failure rolls state back, rollback publishes no SSE event, and the periodic sweep remains crash repair. Focused handler/server/sqlc tests pass. |
| Compliance-baseline transaction follow-up | Pass: apply and revert lock the active/application rows before state-dependent decisions and commit all setting, quota, application-history, and mandatory audit writes together. Audit failure rolls every domain write back with bounded `503 audit_unavailable`; audit records note presence/length but exclude operator note content. Gauges refresh only after commit. Apply/revert rollback, lock, and redaction tests pass. |
| Gatekeeper-constraint transaction and reconciliation follow-up | Pass: create/delete atomically commit desired state, an identifier-only tunnel task, and mandatory audit intent, then return truthful `202` receipts. Generation-fenced workers apply/delete after commit, treat Kubernetes `404` delete as converged, persist only sanitized failures, suppress stale outcomes, and repair pending or cooldown-eligible failed rows. Transaction rollback, row locking, payload redaction, no-precommit-remote-effect, focused normal/race backend tests, sqlc drift, migration policy, API contract, two frontend test files (7 tests), and TypeScript checks pass. |
| Extension and platform-default-template transaction follow-up | Pass: extension install/enable/disable/bundle verification and platform-default update/reapply use exact production transaction runners. Locked state and mandatory audit roll back together; reapply additionally commits a real identifier-only `cluster_template:apply` task; request handling does not directly enqueue Redis work. Manifests and bundle configuration are excluded from audit. All-entry guards, rollback/lock/redaction tests, focused normal/race suites, sqlc drift, and production-wiring checks pass. |
| Chart-rating transaction follow-up | Pass: create/upsert/update/delete lock the chart and natural key as required, commit rating plus aggregate recomputation plus mandatory audit as one decision, and return truthful cross-chart/not-found semantics. Audit omits note/review text. Four-entry structural coverage, exact production wiring, rollback/conflict/redaction tests, focused normal/race suites, sqlc drift, and deploy schema-19 contract tests pass. |
| General-settings and SSO transaction follow-up | Pass: general settings and SSO create/delete commit state and mandatory audit together under singleton/provider-key/row locks. Cache invalidation and provider registration/removal occur after commit; failed registration triggers audited compensation, while dual failure reports repair-required truthfully. Secrets, client IDs, issuer query data, and organization details are excluded from audit. The API surface was extracted from `resources.go`, reducing it from 2,355 to 1,877 lines. Focused normal/race, all-entry, exact production-wiring, sqlc drift, redaction, compensation, and complexity tests pass. |
| Extension/rating/settings combined backend gate | Pass: `./scripts/verify-enterprise.sh backend` after all three follow-ups and the settings/SSO extraction. Migration safety, sqlc drift, build, vet, pinned lint, full normal and race Go suites, 99-document/533-link validation, changed-code and seven-hotspot complexity budgets, nine dependency rules across 1,140 files/8,293 imports, release compatibility, 715/715 route coverage, 739 operation-quality checks, generated client/embed drift, 222-code error catalog, and security metadata all pass. Live agent identity correctly remains skipped without explicit test context. |
| Raw Kubernetes expert-boundary audit | Pass: every mutating passthrough request durably persists fail-closed intent before tunnel dispatch; response evidence is sanitized and best-effort because the remote effect cannot share the PostgreSQL transaction. Focused handler/server normal and race tests pin dependency failure, pre-effect ordering, and response redaction. |
| DLQ and managed-resource saga tranche | Pass: migrations 020/022, durable phase and active-target fences, identifier-only tasks, namespace-aware current-capability receipt checks, replay convergence, error/payload redaction, and migration/sqlc/key-rotation coverage. Crash tests distinguish initial missing, phase-before-effect, and consumed-after-effect replay. |
| Workload image/pod saga tranche | Pass: image rescan and pod delete use durable workload operations; terminal/retry updates CAS on operation plus attempt; stale workers emit no outcome; current originating capability or scoped support-read is required to poll. Generated frontend operations use the shared bounded/cancellable poller and terminal-only accessible feedback. |
| Management-backup and node saga tranche | Pass: migrations 021/023, generation/active-target fences, Secret/CronJob resource-version preservation, manual/scheduled Job fence, bounded Job observation, truthful terminal outcome, node action-aware receipt RBAC, eviction-disappearance observation, sanitized error taxonomy, fixed retry cadence, and key-rotation coverage. CLI mutations send idempotency keys and consume `202` receipts while drain dry-run retains synchronous preview. |
| Durable-operation API semantics tranche | Pass in focused handler/middleware/API-contract validation: monitoring, logging test/retry, tools, catalog installation/retry, and workload mutation/retry require bounded durable idempotency keys and return the pre-existing `{data: ...}` receipt envelope with `Location` and `Retry-After`. Catalog list-envelope and eight pagination-parameter drifts are corrected; CLI callers generate stable UUID keys; oversized bodies use a request-ID-bearing JSON error envelope; generated TypeScript/Go clients and embedded OpenAPI are synchronized. The remaining non-opted-in async families are explicitly retained debt rather than claimed complete. |
| Frontend generated-client tranche | Pass: network-policy-template administration and SMTP get/update/test use generated operations with explicit wire/view mapping; four focused tests, touched-file ESLint, full TypeScript, and the raw-transport gate pass. That checkpoint reduced the ratchet to 290 ordinary compatibility calls; the Charlie-admin and Delivery tranches below reduce the current ratchet to 233, alongside 25 intentional Kubernetes adapter calls and marker-checked SSE/WebSocket/text/blob exceptions. |
| Charlie-admin generated-client tranche | Pass: all 24 Charlie-admin raw calls use exact generated operations with explicit wire/view mappers, AbortSignal propagation, UUID retry idempotency, and preserved 180-second mode/emergency timeouts. OpenAPI now includes diagnostics `next_action`, and no-argument generated calls accept request options. Focused contracts passed 12/12; the authoritative frontend run passed 142/142 files and 985/985 tests, TypeScript, production build, OpenAPI drift, and the later Delivery tranche reduced the current ratchet to 233 ordinary/25 Kubernetes adapter calls. |
| Delivery generated-client tranche | Pass: all 33 Delivery raw calls use generated operations with explicit wire/view mappers, AbortSignal propagation, generated response status/header metadata, ETag-aware reads and conditional actions, and exact nested placement/renderer/reconciliation/rollout/frozen-plan/condition schemas. Request-only schemas preserve server-defaulted compatibility while strict response schemas eliminate shadows; the one required orphan `project_id` contract change has a narrow breaking-change review. Focused tests, OpenAPI/Spectral/oasdiff, generated drift, TypeScript, ESLint, build, and the 233/25 transport ratchet pass. |
| Cluster-detail generated-client tranche | Pass: all 48 original raw calls, including nine Velero status/snapshot/restore/schedule calls and the remaining 39 registry/template/vulnerability/mirror/allow-list/mesh/apps/catalog calls, use exact generated operations with explicit wire/view mapping and cancellation. Snapshot and schedule bodies match Go; image-list filtering/pagination, allow-list modes, registry/template/allow-list/mesh response schemas, and catalog idempotency are exact; no unsupported ETag contract was invented. Focused tests pass 9/9; full frontend passes 143 files/990 tests; TypeScript, ESLint, 142/142 request bindings, generation, Go CLI, inventories, and the reduced 185/25 transport ratchet pass. The module has zero residual raw transport. |
| Project-detail, Charlie, account-security, and GroupMapping generated-client tranche | Pass: all 59 raw JSON calls across project-detail (23), Charlie (18), account-security (14), and GroupMapping settings (4) now use generated operations with explicit snake_case wire mapping and cancellation propagation; the repository ratchet is 126 ordinary compatibility calls plus 25 intentional Kubernetes adapters. Generated login retains typed 423 challenge data, TOTP enrollment carries both proofs, and administrative group resync has an exact operation. Template-bound clusters are backed by a SQLC/handler/OpenAPI route with mandatory authorization wiring, dual permission gates, exact scope filtering, route-golden coverage, and truthful applying state. GroupMapping CRUD and resync are mounted in the canonical security router, use named generated schemas, and have explicit admin/superuser/audit classifications. Project policy persists network-policy mode and canonical quota fields. Focused frontend contracts pass 24/24, the full frontend passes 144 files/1,000 tests, handler/server tests pass, and TypeScript, ESLint, OpenAPI quality/sync (753 operations), Spectral, compatibility, generated drift, SQLC drift, and diff checks are green. |
| Extensions, Vault, and workloads generated-client tranche | Pass: all 27 raw calls across extensions (9), Vault (9), and workloads (9) now use generated operations with explicit wire/view mappers and cancellation, reducing the current ratchet to 99 ordinary compatibility calls plus 25 intentional Kubernetes adapters. Workload scale/restart supplies stable idempotency keys, consumes typed operation receipts, and uses the shared bounded poller; list pagination maps page/page-size to limit/offset. Vault preserves explicit null-default semantics. Extension manifests expose typed CSP and backend API scopes. Six focused files/20 tests, TypeScript, ESLint, code health, 728/728 API coverage, all 753 operation contracts, generated clients/embed, and the final all-scope gate pass. |
| Webhook settings, cluster groups, and registration generated-client tranche | Pass: all 22 raw calls across webhook settings (8), cluster groups (7), and registration (7) now use generated operations with explicit mapping/cancellation, reducing the current ratchet to 77 ordinary compatibility calls plus 25 intentional Kubernetes adapters. The migration removed ignored webhook fields, corrected limit/offset pagination, treats test/retry as asynchronous operation receipts, makes cluster-group responses exact, and preserves registration manifest text plus rotation-token headers. Four focused files/12 tests, TypeScript, ESLint, code health, compatibility, generation/embed, and the full 728-route/753-operation API contract pass. |
| Cluster-agent, compliance-baseline, and SIEM generated-client tranche | Pass: all 22 raw calls across cluster agents (8), compliance baselines (7), and SIEM forwarders (7) now use exact generated operations with cancellation and explicit mapping, reducing the current ratchet to 55 ordinary calls across 14 modules plus 25 intentional Kubernetes adapters. JSON diagnostics, registration-token receipts, `required_totp`, and SIEM `201/202/204` semantics are truthful; no unsupported ETag/idempotency behavior is invented. Three focused files/9 tests, TypeScript, full ESLint, code health, generation/embed/SDK, 767-operation quality, 145/145 request bindings, and full API contract pass. |
| Projects, backup-drill, and notification-template generated-client tranche | Pass: all 18 raw calls across projects (6), management backup drill (6), and notification templates (6) use exact generated operations with cancellation/mapping, reducing the current ratchet to 37 ordinary calls across 11 modules plus 25 intentional adapters. Project create uses required `cluster_id`; backup delete returns its `202` reconciliation receipt and test/run preserve idempotency; notification templates gain exact CRUD contracts. Three focused files/9 tests, TypeScript, ESLint, code health, 774-operation quality, 148/148 request bindings, generation/embed/SDK, and full API contract pass. |
| Complete logging common API semantics | Pass: all 15 durable/asynchronous logging operations require bounded idempotency keys and return typed `202` receipts with operation `Location` and `Retry-After`: output and pipeline create/update/delete/enable/disable, both token-rotation aliases, hosted-Loki attach when it writes, test, and retry. Desired state, operation, and audit remain transactionally coupled. Attach true no-op stays typed `200`; saved-search CRUD is synchronous, query is read-like, and no detach route exists. Handler and 10/10 frontend logging tests, TypeScript/ESLint, build/vet, OpenAPI quality/coverage/route sync/Spectral/compatibility/generation/embed checks pass. |
| Worker PostgreSQL/Redis production-handler harness | Pass in normal (110.677s) and race (87.903s) execution against disposable PostgreSQL 16 and Redis 7: 43/83 current descriptors execute twice through their production owners, queues, CoreRuntime/sqlc dependencies, advisory leader, and handlers with `MaxRetry(0)`. Durable database/outbox effects and real catalog, monitoring, notification, telemetry, SMTP, webhook, SIEM, guarded-TLS Flux source resolution, delivery/system rollout, Charlie alerts, management backup, hermetic GitOps, four-store Fernet credential migration, and real archived-Redis administration remain idempotent across replay. The enlarged harness exposed and fixed rollout SQL defects plus nil GitOps metadata that violated database constraints while its sweep masked the source error. The automatic same-task lease-expiry recovery and duplicate-delivery proof remains green. The 40 residual descriptors are exactly classified as 38 Kubernetes/cloud-backed active handlers plus two retired compatibility consumers; none is credited through a synthetic task handler. Command: `./scripts/test-worker-runtime-integration.sh` (set `WORKER_INTEGRATION_RACE=1` for race). |
| Worker tunnel/Kubernetes follow-up | Pass in normal (91.824s) and race (98.607s) execution: production coverage rises to 50/83 by adding pod deletion, generic resource/node operations, vulnerability rescan, mesh detection, CRD ownership drift, and Gatekeeper constraint reconcile through registered tunnel handlers. Authenticated requester/dynamic fakes terminate only at the Kubernetes boundary; durable attempts/generations/events/conditions and exact external calls prove replay behavior. Gatekeeper SQL typing was corrected with named/cast parameters and its generation fence retained. Thirty-three descriptors remain exactly listed in the implementation item. |
| Worker backup/allow-list/security follow-up | Pass in normal (107.562s) and race (100.203s) execution: production coverage rises to 58/83 by adding backup execution, restore execution, allow-list reconcile/all, CIS ingestion/recovery, and both retired fail-closed handlers. Active paths use real database state, leader/outbox, provider registry, and authenticated tunnel boundary; replay assertions cover timestamps, snapshots, drift, report counts, and exact effects. Twenty-five tunnel-owned descriptors remain exactly listed in the implementation item. |
| Redis outage and recovery drill | Pass in normal and race modes against disposable PostgreSQL 16 and Redis 7. With Redis stopped, the real alert-channel test mutation returns success only after exactly one task intent and one audit intent commit in PostgreSQL; no receiver or canonical-audit effect exists yet. The production task dispatcher records a bounded/redacted failed attempt, while the production audit dispatcher independently persists exactly one audit row. Restarting the same Redis container converges the original outbox row through the registered production notification handler to one exact HTTP effect; repeat dispatch remains a no-op. Credential, body, and raw payload canaries are absent from the stored failure. Command: `make test-redis-outage-recovery` (set `REDIS_OUTAGE_RECOVERY_RACE=1` for race). |
| Tunnel-owner HA and queue-isolation harness | Pass on disposable PostgreSQL 17 and Redis 7 after migrations 1–23: production `NewTunnelWorker`/`NewWorker`, two OS consumer processes, the real WebSocket Hub, and a capability-correct synthetic agent prove response-loss failover from owner A to B yields two DELETE requests and one converged effect. With both tunnel consumers stopped, the standalone worker leaves the tunnel task and durable operation pending. Command: `./scripts/test-tunnel-queue-ha.sh`. |
| CRD runtime feature contract | Pass: `CRD_ENABLED` is typed, defaults off outside Helm, and is propagated to server and scheduler through the shared chart configuration. Disabled composition omits the handler, binding, descriptor, and five-minute producer; enabled composition requires the dynamic client and queries and fails startup if either is missing. Focused normal/race ownership and runtime-binding tests plus Helm lint/render/diff pass. |
| Management-backup runtime feature contract | Pass: `MANAGEMENT_BACKUP_ENABLED` is typed and Helm-propagated to server and worker. All five disabled mutations reject with `503` before transaction/outbox writes while status reads remain available; disabled workers own neither descriptor; enabled startup fails without a ready executor and owns both descriptors when ready; tunnel-core construction has no backup dependency. Focused normal/race server/worker tests and Helm lint/render/diff pass. |
| Disposable live-process fixture | Pass for its current non-agent scope after one honestly retained setup failure: the corrected runner uses disposable PostgreSQL 16 and Redis 7, applies schema 23, builds and runs migrator/server/worker/frontend, uses distinct random metrics ports, checks every component before and after Playwright, and passes 4/4 bootstrap-cookie/session-revocation, SSE cluster-appearance, offline reconnect/re-mint, and invalidation specs in 10.0 seconds. Cleanup left no processes or containers. Evidence: `/tmp/astronomer-live-browser-1856110-1787593956-9e4d36fc`. A real controllable agent/tunnel remains required by item 606. |
| Expanded no-retry live-browser fixture | Pass: 8/8 Playwright journeys in 1.1 minutes with retries disabled against disposable PostgreSQL 16/Redis 7, real server/worker/frontend, and an authenticated production tunnel fixture. New journeys prove no-role RBAC denial, CIS wizard through durable completion/findings, tenant-scoped system-Loki query, and fenced rollout pause/resume; the four prior session/SSE/reconnect journeys remain green. Failure trace/video/screenshots/logs and before/after process/container state are retained. Evidence: `/tmp/astronomer-live-browser-2666527-1787609993-c8dbe61f`. |
| Twelve-journey no-retry live-browser fixture | Pass: 12/12 Playwright journeys in 2.1 minutes with retries disabled. New UI journeys prove YAML dry-run/preview/apply/refetch, known-good rollout rollback, proxy kubeconfig download/structural validation without token logging, and delete through real decommission plus authenticated acknowledgement/tombstone; the prior eight remain green. Server evidence records dry-run/apply `PATCH 200`, rollback `POST 202`, and decommission `DELETE 202`. Evidence: `/tmp/astronomer-live-browser-2855260-1787611718-2bf121db`. |
| Post-tranche consolidated gate | Pass on the settled 2026-08-24 tree via `./scripts/verify-enterprise.sh all`: migrations 1–23, sqlc drift, build, vet, pinned lint with zero findings, uncached complete normal and race Go suites; generated OpenAPI client/types; 747-operation quality and schema lint; 722/722 route coverage; 142/142 Go request shapes with zero drift; Spectral and reviewed breaking compatibility; 185/25 frontend transport ratchet; both generated inventories; 99-document/564-link validation; seven complexity ceilings across 746 changed production files; nine dependency rules across 1,179 files/8,633 imports; 143 frontend files/990 tests; TypeScript, production build, bundle budget, route drift, zero-vulnerability audit; and Helm lint/render/contracts. Artifacts: `/tmp/astronomer-enterprise-wave2-20260824`. |
| Live-discovered startup and isolated-k3d fixes | Pass in focused tests and the subsequent real-agent run: development with the embedded delivery distribution now no-ops only when both signed artifact repository and digest are absent, while either partial signed-release direction fails closed; fresh smoke derives the management Docker network from its supplied k3d context; and the Gateway host set includes the actual advertised server hostname with DNS/IPv4/bracketed-IPv6 parsing and deduplication. System-release, deploy contract, Bash syntax, focused Go, and diff checks pass. |
| Catalog-defined baseline reconciliation | Pass in focused Go/Python/migration/generated-contract validation: `deploy/bundles/catalog.json` is the sole v1.1 baseline membership contract; fresh schema 23 seeds neither the dead imperative template nor its default FK; the coverage API derives default-enabled components from the embedded catalog without changing its response shape; current docs/OpenAPI describe compatible Ready Flux inventory and immutable rollouts; and smoke compares the exact Ready/current `spec.targetNamespace/spec.releaseName` set while recording Trivy scanning as skipped because it is not default-enabled. Existing upgraded databases may retain an inert legacy row pending a reviewed cleanup migration. |
| Multi-source immutable built-in provisioning | Pass: catalog validation accepts only canonical bounded public HTTPS repositories and rejects ambiguous/conflicting immutable identities. Provisioning plans one deterministic source per distinct URL, binds each component to its exact resolved source, and preserves the current Prometheus source ID/name/spec inputs. Release archives separate colliding source/chart names without changing the legacy flat path. Disposable PostgreSQL 16 tests prove same-source replay, multi-source binding, conflict rollback, and six-way convergence; the concurrency test exposed and drove a fix that takes the session advisory lock before the serializable snapshot. Focused normal/race tests, real bundle build plus `--check`, migration policy, SQLC drift, and diff checks pass. |
| Partial fresh-cluster diagnostic | Honest non-signoff evidence at HEAD `c8534cce`: management creation/import/bootstrap passed in 16/15/67 seconds; management API, authentication, adopted-cluster creation, 6,517-line manifest application, and real agent heartbeat passed. The run was manually interrupted with SIGINT/130 after 273 seconds during the impossible legacy-tool wait, before its 900-second deadline, so it does not claim baseline/Flux/shell/scanning completion. Permission-restricted artifacts contain no detected credential values and cleanup left only pre-existing `member-a`/`member-b`: `/tmp/astronomer-smoke-e2e-824e`. |
| Final owned fresh-cluster Flux smoke | Pass on the exact settled tree after rebuilding the affected server/worker/migrator images: image rebuild 209 seconds, management creation 16 seconds, six-image import 18 seconds, bootstrap 68 seconds, and full smoke 76 seconds. Eleven checks pass: management API/authentication, adopted-cluster creation and 6,517-line manifest, real agent tunnel heartbeat, exact three-controller Flux set and signed image digests, exact two-release catalog set generation-current and Ready, both monitoring workloads, API shell open/close, registration ready with no orphan template step, Kubernetes proxy over 13 namespaces, and OpenAPI/Swagger. `vulnerability_reports_not_in_default_baseline` is one explicit skip, not pass. Evidence status is pass/exit 0, source fingerprints match before/after, credential scans are clean, and cleanup left only `member-a`/`member-b`: `/tmp/astronomer-smoke-final-20260824`. |
| Final post-reconciliation consolidated gate | Pass via `VERIFY_ARTIFACT_DIR=/tmp/astronomer-enterprise-final-20260824 ./scripts/verify-enterprise.sh all`: 37 checks passed, zero failed, and one explicit live-context skip; 100 tested Go packages plus 14 no-test packages pass in normal and race suites; lint has zero issues; frontend passes 143 files/990 tests, builds 3,843 modules within the 227-chunk budget, and has zero audited vulnerabilities; OpenAPI covers 722/722 mounted routes with 747 quality checks and 142/142 request bindings at zero drift; Helm lint, development/production renders, and deploy contracts pass; `git diff --check` is clean. Artifacts: `/tmp/astronomer-enterprise-final-20260824`. |
| Wave-three stopped-tree qualification | Pass after using the broad gate once to expose two integration defects and correcting both. The rerun passed migrations 1–23, sqlc drift, build, vet, pinned lint with zero issues, the complete normal Go suite, and the complete race suite before route coverage detected the two newly documented routes were absent from the canonical security fixture. The corrected full API-contract scope passes 728/728 mounted routes with zero missing/extra operations, route sync/quality, Spectral, reviewed compatibility, exact request bindings, generated clients/embed/Go SDK, route golden, and security contracts. The full frontend scope passes code health at the 126/25 transport ratchet, lint, TypeScript, 144 files/1,000 tests, a 3,843-module production build, all 227 bundle-budget chunks, route-tree drift, and a zero-vulnerability audit. Helm input/lint, development and production renders, and chart contracts pass. `git diff --check` is clean. Artifacts: `/tmp/astronomer-enterprise-wave3-20260824-rerun`, `/tmp/astronomer-verify-enterprise`, `/tmp/astronomer-enterprise-wave3-frontend-rerun-20260824`, and `/tmp/astronomer-enterprise-wave3-helm-20260824`. |
| Process-restart qualification | Pass in normal and race modes against disposable PostgreSQL 16 and Redis 7. Server A persists a real CIS no-report poll and its next durable generation; server B, using production `NewTunnelWorker`, resumes the same scan through an authenticated capability-correct protocol agent to one report and one audit. Worker A exits after the original notification task/audit transaction and before scheduled Redis wakeups; production `NewWorker` B drains the original rows to one exact webhook effect and one audit, and repeat dispatch is a no-op. Command: `make test-process-restart-qualification` (set `PROCESS_RESTART_QUALIFICATION_RACE=1` for race). The evidence is process/constructor level with a synthetic agent, not a live Kubernetes rollout. |
| Wave-four consolidated enterprise gate | Pass via `VERIFY_ARTIFACT_DIR=/tmp/astronomer-enterprise-wave4-rerun-20260824 ./scripts/verify-enterprise.sh all` after the gate's first pass caught and drove two test-only lint fixes. Migrations 1–23, SQLC drift, build, vet, pinned lint with zero issues, complete uncached normal and race Go suites, 99-document/564-link validation, seven complexity ceilings across 752 changed production files, nine dependency rules across 1,182 files/8,625 imports, compatibility, 728/728 mounted API coverage with zero missing/extra routes, all 753 operation quality checks, Spectral, reviewed breaking changes, 143/143 request bindings, generated clients/embed, and security metadata pass. Frontend code health passes at 99 ordinary/25 intentional Kubernetes transports; ESLint, TypeScript, 146 files/1,006 tests, the 3,843-module production build, all 227 bundle-budget chunks, route-tree drift, and zero-vulnerability audit pass. Helm lint, development/production renders, and deploy contracts pass. The sole explicit skip is live agent identity because `AGENT_IDENTITY_TEST_CONTEXT` was not supplied. |
| Combined PostgreSQL-outage fail-closed qualification | Pass in normal (1.20s final) and race (4.014s) modes against disposable PostgreSQL 16. A drift-failing inventory covers all 54 production shared transaction interfaces; actual RBAC, login, SMTP-test, and raw-Kubernetes HTTP boundaries prove standard outage responses with no credential, cookie, remote send, or dispatch. Real compliance apply/revert and delivery planning, rollout action/approval, deployment control, and system-rollout services prove healthy atomic state+audit, pre-effect outage failure, and zero state/audit/secret leakage after recovery. The drill exposed and fixed shared begin/commit error mapping and rollout SQL parameter inference. Command: `make test-postgres-outage-qualification` (set `POSTGRES_OUTAGE_QUALIFICATION_RACE=1` for race). |
| Wave-five consolidated enterprise gate | Pass via `VERIFY_ARTIFACT_DIR=/tmp/astronomer-enterprise-wave5-20260824 ./scripts/verify-enterprise.sh all`: migrations 1–23, SQLC drift, build, vet, pinned lint with zero issues, complete uncached normal and race Go suites, 99-document/564-link validation, seven complexity ceilings across 753 changed production files, nine dependency rules across 1,185 files/8,630 imports, compatibility, 728/728 mounted API coverage with zero missing/extra routes, all 753 operation quality checks, Spectral, reviewed compatibility, 143/143 request bindings, generated clients/embed, error/security metadata, and the explicit live-context skip pass. Frontend code health passes at 77 ordinary/25 intentional Kubernetes transports; ESLint, TypeScript, 149 files/1,016 tests, the 3,843-module production build, all 227 bundle-budget chunks, route-tree drift, and zero-vulnerability audit pass. Helm lint, development/production renders, and deploy contracts pass. |
| Wave-six consolidated enterprise gate | Pass via `VERIFY_ARTIFACT_DIR=/tmp/astronomer-enterprise-wave6-final-20260824 ./scripts/verify-enterprise.sh all` after clearing only recoverable build cache and failed temporary evidence: migrations, SQLC drift, build, vet, zero-issue lint, complete normal/race Go suites, docs/complexity/dependency contracts, compatibility, 728/728 route coverage, 767 operation-quality checks, Spectral, 145/145 request bindings, generated/embed/security contracts, and the explicit live-context skip pass. Frontend code health passes at 55 ordinary/25 intentional transports; ESLint, TypeScript, 151 files/1,017 tests, production build, all 227 bundle-budget chunks, route drift, and zero-vulnerability audit pass. Helm lint, development/production renders, and contracts pass. |
| Wave-seven consolidated enterprise gate | Pass via `VERIFY_ARTIFACT_DIR=/tmp/astronomer-enterprise-wave7-20260824 ./scripts/verify-enterprise.sh all`: migrations, SQLC drift, build, vet, zero-issue lint, complete normal/race Go suites, docs/complexity/dependency contracts, compatibility, 728/728 route coverage, 774 operation-quality checks, Spectral, 148/148 request bindings, generated/embed/security contracts, and the explicit live-context skip pass. Frontend code health passes at 37 ordinary/25 intentional transports; ESLint, TypeScript, 154 files/1,026 tests, production build, all 227 bundle-budget chunks, route drift, and zero-vulnerability audit pass. Helm lint, development/production renders, and contracts pass. |
| Kubectl-shell, nodes, and read-audit generated-client tranche | Pass: all 15 ordinary transports across kubectl shell (5), nodes (5), and read-audit policy settings (5) now use exact generated operations, reducing the ratchet from 37 to 22 ordinary calls across eight modules while 25 intentional Kubernetes adapters remain unchanged. Shell creation/close preserve truthful `201`/`200` status, polling and React Query propagate cancellation, node lists traverse every 200-item page and explicitly map raw Kubernetes keys, and read-audit CRUD has exact schemas including `204` delete without invented ETag/idempotency. Four files/12 focused tests, TypeScript, full ESLint, code health, 779-operation quality, 728/728 route coverage, 150/150 request bindings at zero drift, Spectral/compatibility/generation/embed/SDK, and route/security checks pass. |
| Worker project/template/registry/NetworkPolicy replay tranche | Pass in normal (113.447s) and race (115.687s) execution: production replay coverage rises from 58 to 66 of 83 descriptors with the project reconcile pair, cluster-template apply/drift pair, registry-secret apply/drift pair, and NetworkPolicy apply/drift pair. Two `MaxRetry(0)` passes use the actual tunnel-owned runtimes, real PostgreSQL/SQLC/leader/outbox/encryption and recovery enqueueing, with only the authenticated tunnel boundary controlled. Exact state, audit, replay, fan-out, unique-template, identical-SSA-body, and applied/drift/reapply assertions pass; focused tests, vet, SQLC drift, and diff checks are green. Seventeen exact descriptors remain in item 183. |
| PostgreSQL failover certification | Pass via `make test-postgres-failover-certification` against a real PostgreSQL 16 primary and physical synchronous standby. The drill commits 24 canaries through the existing Astronomer pool, kills the primary with `SIGKILL`, observes readiness `503`, promotes the standby with `pg_ctl promote`, switches the stable writer endpoint, and proves pool recovery plus durable write/read. Final schema-v1 evidence records 24/24 recovered, RPO 0 rows/0 seconds, RTO 526 ms against 30,000 ms, eight required checks, exact image/source/timestamps/topology, retained logs and failed-bootstrap evidence, and the external managed-provider/DNS residual. Artifact: `/tmp/astronomer-postgres-failover-wave8-final`. Static runner contract, focused Go tests/vet, full Go lint, docs/workflow contracts, cleanup, and diff checks pass; PR CI retains artifacts for 90 days. |
| Wave-eight consolidated enterprise gate | Pass via `VERIFY_ARTIFACT_DIR=/tmp/astronomer-enterprise-wave8-20260824 ./scripts/verify-enterprise.sh all`: migrations 1–23, SQLC drift, build, vet, zero-issue lint, complete normal/race Go suites, 99-document/564-link validation, seven complexity ceilings across 753 production files, nine dependency rules across 1,193 files/8,613 imports, compatibility, 728/728 route coverage, 779 operation-quality checks, Spectral, 150/150 request bindings at zero drift, generated/embed/error/security contracts, and the explicit live-context skip pass. Frontend code health passes at 22 ordinary/25 intentional transports; ESLint, TypeScript, 157 files/1,035 tests, 3,843-module production build, all 227 bundle-budget chunks, route drift, and zero-vulnerability audit pass. Helm lint, development/production renders, and contracts pass. |
| Final generated-client and exact-wire tranche | Pass: the last 22 ordinary calls across administration, security, snapshots, metrics, native RBAC, resource search, compliance export, and email settings moved to generated operations, and direct-kubeconfig download subsequently moved off the raw adapter. The ratchet is now 0 ordinary/25 intentional opaque Kubernetes adapters. The global response camelizer and raw-wire bypass machinery are gone; exact wire casing is universal, with only explicit feature mappers and the live SSE envelope normalizer remaining. Full frontend validation passes 158 files/1,034 tests, TypeScript, ESLint, code health, generated drift, production build, 227 bundle budgets, route drift, and zero-vulnerability audit. |
| Complete worker replay certification | Pass: all 83/83 production task descriptors execute twice through registered owners with `MaxRetry(0)` against real PostgreSQL 16 and Redis 7/Asynq. Normal (100.506s) and race (97.739s) runs prove terminal state, idempotent replay, audit effects, fan-out, and recovery enqueueing. The work extracted reusable production deferred replayers and exposed/fixed a Prometheus label-cardinality panic in group metrics. |
| Complete async-contract and direct-kubeconfig tranche | Pass: all 104 actionable documented `202` operations satisfy bounded idempotency, exact durable replay/conflict behavior, typed receipts/status resources, and truthful polling headers; the only five exceptions are the exact locked ingest, privacy-acknowledgement, and interactive-message operations. The completed 36-route tranche includes Delivery, cluster/node/pod/policy, administration, Charlie, SIEM, and Dex families. Direct kubeconfig retains its separate 15-minute read-only TokenRequest identity, TLS/SSRF fences, fail-closed permission/audit behavior, generated contracts, and permission-aware UI. Focused normal/race and the final consolidated gates pass. |
| Real Flux, Velero, and direct-access live-browser qualification | Pass: 15/15 retry-disabled journeys in 3.5 minutes on disposable infrastructure prove actual CA-pinned Flux GitRepository/Kustomization readiness and desired-object reconciliation, plus UI backup/delete/restore through the production member tunnel and Velero worker. Velero Backup/Restore and PostgreSQL rows reach `Completed`, the MinIO tar archive exists, and the original ConfigMap is restored in UI and Kubernetes. Direct access additionally proves hidden unauthorized UI, authorized download, exact endpoint and parsed X.509 CA, 15-minute read-only identity, allowed ConfigMap read, denied write and Secret read, zero token logging, and credential deletion. Evidence: `/root/astronomer-live-browser-direct-signoff-1787622458`; cleanup preserved only user-owned `member-a` and `member-b`. |
| Wave-eleven deterministic signoff gate | Pass across the final shared tree. The consolidated rerun passes migrations 1–23, sqlc drift, build, vet, zero-issue Go lint, complete normal and race Go suites, 99-document/566-link validation, complexity/dependency/compatibility contracts, 729/729 mounted route coverage, all 786 operation-quality checks, Spectral, compatibility review, 151/151 request bindings at zero drift, generated/embed/error/security contracts, and the explicit live-context skip. After that run caught the final direct-download raw-call ratchet, the corrected frontend scope passed 158 files/1,034 tests, ESLint, TypeScript, 3,843-module build, all 227 bundle budgets, route drift, and zero-vulnerability audit; the Helm scope passed input contracts, lint, development/production renders, and deploy tests. Artifacts: `/tmp/astronomer-enterprise-wave11-final-rerun2-20260825`, `/tmp/astronomer-enterprise-wave11-frontend-20260825`, and `/tmp/astronomer-enterprise-wave11-helm-20260825`. |
| Wave-twelve backend deterministic signoff | Pass on the final async/Trivy tree: migrations 1–25, SQLC drift, build, vet, zero-issue lint, complete uncached normal and race suites (101 tested packages plus 16 no-test packages in each mode), documentation/complexity/dependency/compatibility gates, 754/754 mounted routes, 793 documented operations, 104 actionable async contracts plus five exact exceptions, 151/151 request bindings, generated frontend/SDK/embed contracts, and security/error metadata are green. The sole explicit skip is live agent identity because `AGENT_IDENTITY_TEST_CONTEXT` was not supplied. Artifact: `/tmp/astronomer-enterprise-wave12-backend-final-20260825`. |
| Wave-twelve frontend and Helm signoff | Pass: frontend code health, ESLint, TypeScript, 158/158 files and 1,034/1,034 tests, a 3,843-module production build, all 227 bundle-budget chunks, generated/route drift, and zero audited vulnerabilities are green. Helm input contracts, lint (one chart, zero failures), development render (49 resources), production render (48 resources), and chart contracts are green. Artifacts: `/tmp/astronomer-enterprise-wave12-frontend-20260825` and `/tmp/astronomer-enterprise-wave12-helm-20260825`. |
| Flux-native optional Trivy live qualification | Pass: the retry-disabled 16/16 suite installs pinned Trivy through production Flux-native Helm delivery, reaches succeeded/Ready generation-current durable state, runs the production MirrorSubscriber, ingests a real VulnerabilityReport into PostgreSQL, and displays it through the generated API and UI. The same run passes RBAC, CIS, Loki, Delivery reconciliation, raw-resource dry-run/apply, proxy/direct access, rollback, Velero restore, decommission, authentication, and SSE reconnect journeys. Artifact: `/tmp/astronomer-live-browser-trivy-signoff-20260825-final`. |
| Wave-thirteen exact corrected-tree backend signoff | Pass after the live lane exposed migration 026's required schema-version alignment. The first gate failed only the stale expected-schema contract and is retained separately; binary, canonical/generated release, Helm, documentation, and test contracts were advanced to 26, then the complete fresh-artifact rerun passed. Migration safety scans 26 files; SQLC, build, vet, zero-issue lint, 101 tested plus 16 no-test packages in both normal and race modes, documentation/complexity/dependency/compatibility, 754/754 route coverage, 793 operation-quality checks, 151/151 request bindings, Spectral/compatibility review, generated clients/SDK/embed, and security/error contracts are green. Only the explicit unset live-agent-identity context is skipped. Artifact: `/tmp/astronomer-enterprise-wave13-backend-final-rerun-20260825`; failed-first-pass evidence: `/tmp/astronomer-enterprise-wave13-backend-final-20260825`. |
| Wave-thirteen exact corrected-tree frontend and Helm signoff | Pass after supported inventory regeneration: frontend code health, generated/current inventories, zero ordinary and 25 intentional opaque Kubernetes transports, ESLint, TypeScript, 158/158 files and 1,034/1,034 tests, 3,843-module production build, 227 in-budget chunks, route-tree drift, and zero vulnerabilities are green. Helm lint, 49-resource development render, 48-resource production render, and deploy contracts are green. Artifacts: `/tmp/astronomer-enterprise-wave13-frontend-20260825` and `/tmp/astronomer-enterprise-wave13-helm-20260825`. |
| Scale-agent fixture authentication | Pass in `go test -race ./scripts/loadtest`, vet, and build: every synthetic agent receives a real API-created cluster plus matching short-lived registration credential, adopts the durable `CONNECT_ACK` credential for reconnects, never receives the admin bearer, preserves mapping under concurrent provisioning, fails fast on bad mappings, and requests bounded fixture cleanup on success/error unless debug retention is explicit. Production-scale execution remains release evidence. |
| Scale evidence provenance (historical intermediate) | The earlier gate bound source identity and a SHA-256 digest but did not authenticate it. This limitation is superseded by the 2026-08-25 keyless Sigstore per-rung and same-release aggregate implementation. No production-scale certification is claimed without external execution. |
| Fresh adopted-cluster evidence contract | Pass in Python contract tests, shell syntax, workflow YAML lint, and diff checks: the smoke harness atomically emits a sanitized versioned pass/fail manifest before cleanup, records out-of-baseline qualifications as explicit skips, and CI always retains it for 90 days with run identity. The settled-tree live execution passed and is recorded separately in this matrix. |
| Maintenance/control-plane/native-RBAC/agent-upgrade combined backend gate | Pass: `./scripts/verify-enterprise.sh backend` after all four follow-ups. Migration safety, sqlc drift, build, vet, pinned lint, full normal and race Go suites, 99-document/520-link validation, complexity/dependency boundaries, release compatibility, 715/715 route coverage, 739 operation-quality checks, 141 exact request shapes, generated frontend/embed drift, and the 222-code error catalog all pass. Live agent identity correctly remains skipped without explicit test context. |
| Quota/logging-saved-search/identity-mapping combined backend gate | Pass: `./scripts/verify-enterprise.sh backend` after all three migrations. Migration and generated-sqlc drift, build, vet, pinned lint, full normal/race Go suites, 99-document/520-link contract, complexity/dependency boundaries, release compatibility, 715/715 route coverage, 739 operation-quality checks, 141 exact request shapes, generated frontend/embed drift, and error-code/security metadata all pass. Live agent identity correctly remains skipped without explicit test context. |
| Offline signature verifier stability | Pass: repository-wide race verification exposed and fixed destructive whitespace trimming of binary DER signatures. Exact raw bytes are now retained while textual base64 is trimmed separately; 100 race-enabled focused repetitions and the subsequent full race suite pass. |
| API/OpenAPI contracts | Pass in the final backend gate: 722/722 routed operations, eight explicit nil-gated operations, 747 operation-quality checks, and 142 Go-bound request shapes with zero field drift, provisional schemas, or propertyless placeholders. Every intentional async runtime contract break is recorded exactly in `docs/api-breaking-change-review.md`. |
| `./scripts/verify-enterprise.sh frontend` | Pass as part of the final shared-tree `all` gate: code health and both generated inventories, 185/25 raw-transport ratchet, ESLint, TypeScript, 143 test files/990 tests, production build, bundle budget, route-tree drift, and zero-vulnerability dependency audit. Artifacts: `/tmp/astronomer-enterprise-wave2-20260824`. |
| Frontend bundle budget | Pass; explicit 650,000-byte raw and 220,000-byte gzip per-chunk ceilings. The final largest chunk is 487,682 bytes raw and 136,223 bytes gzip. |
| `./scripts/verify-enterprise.sh helm` | Pass on the final tree; chart input contract, lint, development render, fully wired production render, and chart contracts. |
| PostgreSQL round trip | Pass on 2026-08-24 on real PostgreSQL 16 and 17 through schema 23: every reversible down/up edge, complete down/reapply, transactional audit rollback/replay, durable audit-to-SIEM replay, and normalized schema/seed signature equality. Command: `./scripts/migrate-roundtrip-smoke.sh`. |
| PostgreSQL release upgrade matrix | Pass on 2026-08-24 on real PostgreSQL 16 and 17: 1.0.x schema 1 and 1.1.x schema 14 fixtures upgraded to schema 23 with seeded settings/clusters preserved; concurrent installers serialized; forced backend termination rolled transactional DDL back and retried cleanly. Command: `./scripts/release-upgrade-matrix.sh`. |
| Migration policy gate | Pass locally across all 23 migrations, including destructive-DDL and encrypted-column/key-rotation classification. Both the live PostgreSQL 16/17 seeded release-upgrade matrix and the independent every-edge down/up round trip now cover migrations 20–23. |
| Query-plan and cursor certification | Pass on 2026-08-24 on real PostgreSQL 17 with 100,000 clusters and 1,000,000 tool operations: `clusters_active_status_created_idx` and `tool_operations_cluster_created_idx` were used in analyzed plans; rows inserted ahead of a cursor remained visible, rows inserted behind it followed the documented forward-only boundary, and no ID repeated. Reports: `/tmp/astronomer-query-plan-final`. |
| Scale soak/leak harness | Pass: unit and race tests prove Prometheus goroutine/heap/open-FD capture, ramp-up exclusion, stable-series acceptance, missing certification-evidence rejection, and injected heap/open-FD leak rejection; the production-like four-hour run remains pending. |
| OpenAPI schema and compatibility | Pass: Spectral reports zero schema errors; pinned oasdiff 1.28.0 rejects unreviewed error/warning changes against the PR or prior-release spec; compatibility aliases and field decoding preserve the retired direct-kubeconfig path safely, the delivery terminology path, Grafana flag, Charlie threshold, project quota names, and RBAC scope; PR/release workflows retain review artifacts. |
| `npm run test:e2e:smoke` | Pass; 264 desktop/mobile route and serious/critical Axe checks. |
| `npm run test:e2e:visual` | Pass; 24 light/dark desktop/tablet/mobile snapshots. |
| `./scripts/check-legacy-delivery-surface.sh --fail` | Pass across 2,808 source files, production build artifacts, Helm render, and generated CLI help; zero active Argo CD, Rancher Fleet, or legacy fleet-operation matches. |
| Documentation and complexity | Pass; 99 current documents, 564 relative links, 746 changed production files, and seven hotspot ceilings. `server.go` remains below its no-growth ceiling after extracting management-backup enablement and node receipt authorization. |
| Dependency and generation drift | Pass; nine dependency rules cover 1,179 files/8,633 imports; npm audit reports zero vulnerabilities; OpenAPI TypeScript/Go clients, embedded spec, compatibility views, route/task/code-health inventories, and sqlc output are current. |

### Corrected exact-tree execution snapshot — 2026-08-25

| Gate | Current result |
|---|---|
| Full Go tree | Pass: `go test ./...` covers 101 tested packages plus 16 no-test packages. The server/handler decomposition additionally passes normal and race suites, full build, full vet, and pinned golangci-lint with zero issues. Evidence: `/tmp/astronomer-enterprise-wave14-final-20260825`. |
| API contract | Pass: 754/754 mounted routes, 22 exact nil-gated operations, 793 quality-checked operations, 104 actionable `202` mutations plus five exact exceptions, 151/151 request bindings, zero drift, Spectral validation, compatibility comparison, generated client/embed checks, and security route tests. Evidence: `/tmp/astronomer-enterprise-wave14-final-20260825/api`. |
| Frontend and Rancher-parity UX | Pass: full Vitest 159 files/1,042 tests, TypeScript, full ESLint, code-health, complexity, 24/24 focused detail/YAML tests, and 1/1 mocked keyboard/focus/conditions/YAML characterization. Resource detail is decomposed into cohesive modules with the largest at 669 lines. The optional Trivy live test is registered only when enabled; the flake policy has zero unowned skips or quarantines. |
| Helm and release chart | Pass: chart input contract, lint, development render, fully wired production render, and deploy contract tests. Evidence: `/tmp/astronomer-enterprise-wave14-final-20260825/helm`. |
| Documentation and architecture | Pass: 101 current documents, 591 relative links, 809 changed production files under the seven hotspot ceilings, nine dependency rules across 1,212 files/8,800 imports, valid flake policy, and security-wave review. |
| Scale/audit producer | Pass locally: loadtest race suite, fail-closed traffic/cardinality/error/RPS/audit-conservation tests, deterministic evidence/reducer tests, keyless signing/identity and same-release aggregation contracts, workflow parsing, and documentation contracts. No measured production capacity is claimed. |
| Cloud/release/RC producer | Pass locally: AWS resolver normal/race/redaction/refresh tests; four-provider reversible acceptance contracts; transitive image/SBOM/license/vulnerability/waiver qualification; protected approval validation; Kubernetes 1.33–1.35 matrices; fenced clean-restore/upgrade RC producer; shell, JSON, YAML, and evidence-schema checks. No credentialed cloud, registry, protected approval, or RC execution is claimed. |
| Owned live suite | Pass retained from the corrected product tree: retry-disabled 16/16 Flux/Trivy/Velero/direct-access journeys at `/tmp/astronomer-live-browser-trivy-signoff-20260825-final`. The final changes after that run are composition-only moves, offline evidence producers, release gates, and test-policy cleanup; no user-owned cluster was accessed. |

The eight unchecked entries are intentionally external. This workspace does not manufacture: credentialed EKS/GKE/AKS/DOKS sandbox execution; mandatory-audit certified-throughput results; measured production chart sizing; paired Rancher automated plus counterbalanced human benchmark evidence; a release-candidate NVDA/Narrator/VoiceOver matrix; protected release-engineering approval; certified retained scale profiles; or backup/restore/upgrade execution on the exact release candidate.

## 29. Definition of done

The program is complete only when:

1. Every task required by findings 1–20 is implemented or replaced by a documented, user-approved scope decision; no implementation item is silently dropped.
2. Every checkbox in the final validation matrix passes on the same release-candidate commit and artifacts.
3. The completion evidence matrix links to reproducible evidence for every finding.
4. Deterministic generation produces no unexplained drift; the intentional uncommitted implementation diff remains until the user chooses how to commit it.
5. No P0/P1 security, data-loss, upgrade, HA, API-contract, accessibility, or operator-trust defect found during execution remains open.
6. Scale and availability claims match measured results rather than aspirational configuration comments.
7. Current documentation consistently describes Flux, the actual release/schema versions, and the adopted-cluster-only product boundary.
