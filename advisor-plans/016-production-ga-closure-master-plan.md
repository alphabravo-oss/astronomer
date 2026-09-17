# Plan 016: Finish Astronomer production and GA closure from one reproducible release candidate

> **Executor instructions:** This is the sole active advisor execution plan.
> Read it completely before changing source. Execute phases in order; do not
> claim a later qualification from an earlier unit or static test. Preserve all
> existing working-tree work. Never discard, reset, or rewrite a change whose
> ownership is unclear. Run every phase gate and retain the named evidence. If a
> STOP condition occurs, stop and report instead of weakening a security,
> durability, or evidence requirement.
>
> **Drift check (run first):**
>
> ```bash
> git rev-parse HEAD
> git status --short
> git log --oneline --decorate -20
> ```
>
> This plan was reconciled at commit
> `5567da3bbba8c72bb95924f1824f5c100d147058` on 2026-09-16, on branch
> `advisor/011-015-hardening`, against a deliberately preserved large dirty
> integration tree. Before this documentation-only reconciliation, the tree
> contained 1,175 changed tracked paths (including 39 deletions) and 508
> untracked paths. A different count is not automatically a failure, but the
> executor must account for every change before Phase 0 closes.

## Status

- **Priority:** P0 for Phases 0–3; P1 for Phases 4–5; release-blocking Phase 6;
  claim-gated Phase 7.
- **Effort:** XL; multiple implementation and evidence waves, not one PR.
- **Risk:** HIGH. The remaining work crosses authentication, encryption,
  shutdown, backups, data access, deployment policy, and production evidence.
- **Depends on:** none. Completed historical work is archived under
  `advisor-plans/archive/` and must not be reimplemented.
- **Category:** correctness, security, DR, supply chain, durability, scale,
  observability, accessibility, release qualification.
- **Planned at:** `5567da3bbba8c72bb95924f1824f5c100d147058`, 2026-09-16,
  plus the preserved dirty integration tree described above. Execution was
  reconciled through `11a243bf251b770299dc375e74d8e8247e2dbf4d` on
  2026-09-17.
- **Branch:** `advisor/016-production-ga-closure`.

## Product and authority boundaries

- Astronomer adopts and operates existing Kubernetes clusters. It does not
  provision clusters, machines, or node pools.
- Flux is the only downstream delivery engine. Do not restore Argo, Rancher
  Fleet, the retired fleet-operation engine, or compatibility aliases for them.
- PostgreSQL owns durable product intent. Redis/asynq is a delivery mechanism,
  not the source of truth for accepted mutations.
- Security-sensitive dependencies fail closed. Authentication, authorization,
  tenant scoping, mandatory audit, and secret redaction are invariants.
- Static/local success is not GA evidence. Production-scale, cloud, assistive-
  technology, backup/restore/upgrade, and protected-approval claims require the
  actual retained executions in Phase 6.
- The Constellation/K3s/RKE2 campaign in Phase 7 blocks only those explicit
  interoperability claims unless the release owner promotes it to a GA gate.

## Reconciliation verdict

The historical plan set is archived, but its evidence is preserved. The
following outcomes are complete on the current tree and are not work for this
plan unless a new regression is proven:

| Area | Verified completed outcome |
|---|---|
| Frontend migration | Vite, React 19, TanStack Router/Query, generated route tree, static nginx container, and no Next app tree. Current `npm run type-check` and `npm run lint` pass. |
| Delivery architecture | Flux-native delivery is the only v1 engine; retired Argo/Fleet work is historical. |
| Charlie P1 platform catalog | Approved P1 selection contract and platform packs shipped; demand-gated P2 packs are optional product work. |
| Audit tombstones | `audit_archive.archived_cluster_name`, archival population, guarded scheduled retention, and focused retention tests are present. The backfill-before-purge invariant remains mandatory. |
| Release baseline repairs | Worker capability and rollout-route fixtures are repaired; ordinary Go/frontend gates passed during the 2026-09-16 implementation run. |
| Runtime/API safety | Normal HTTP closure, ordered audit/telemetry drains, per-pool saturation state, request-bound idempotency, active-only cluster reads, and controlled persisted-JSON errors are implemented. |
| Auth/API first tranche | Revocation uncertainty fails closed with retryable 503, exact HS256 is required, interactive access tokens are bounded to 5–15 minutes, SSO cookies are secured, production DSNs are parsed structurally, and public 5xx details are redacted. |
| Chart authority first tranche | Backup/restore pods do not mount the platform ServiceAccount/token; management log mounts are read-only with restricted security settings. |
| Data first tranche | Required PostgreSQL contracts cannot silently skip; template-recovery enqueue accounting is truthful and retryable. |
| Frontend safety first tranche | Forced-password and feature-flag route gates mount fail closed; current-user/feature requests support cancellation; production build includes TypeScript checking. |
| Enterprise implementation ledger | Archived Plan 008 completed 352/360 local items; its eight external executions are Phase 6 below. |

The local k3s smoke environment was also healthy at reconciliation time: Helm
revision 23, chart/app 1.1.0, server/worker/frontend and bundled development
PostgreSQL/Redis Ready, and `/health/` plus `/readyz` returning 200. This proves
the current integration tree runs locally; it does not prove reproducibility or
production readiness.

## Current remaining evidence

The stale live-code observations from the original review have been closed by
the implementation commits recorded below. The plan remains active for these
observed residuals:

- The final clean candidate must be rebuilt and redeployed after the scale
  scheduler and declarative bundled-PostgreSQL changes at `1c7fdfe8` and
  `11a243bf`, then receive one final static/stateful replay.
- Estate-100 is not yet a pass. The 2026-09-17 exact-image local run completed
  its 30-minute window with good latency, bounded resources, successful
  100-agent reconnect, and conserved accepted audit intents, but failed the
  database empty-acquire threshold. The run also exposed scheduler/accounting
  defects fixed after the run. Retained evidence is in
  [`docs/scale-evidence/estate-100-2026-09-17-local-fail.md`](../docs/scale-evidence/estate-100-2026-09-17-local-fail.md).
- The eight credentialed, signed, human, assistive-technology, DR, scale, and
  protected-approval executions in Phase 6 have not been supplied. Local
  harness success cannot substitute for them.
- The live agent-identity lane remains explicitly unavailable without
  `AGENT_IDENTITY_TEST_CONTEXT`; this is declared rather than silently skipped.
- Phase 7 still lacks the signed six-host Constellation inventory and explicit
  destructive operator authorization. No physical-interoperability claim may
  be made.

## Execution ledger

- **Phase 0 baseline, final replay pending:** the preserved integration tree was
  classified and committed, and clean-clone static enterprise verification
  passed at `32f464e5150c25dffd10ddd802b4349872b2a376` on 2026-09-17. The full
  normal/race Go tree, PostgreSQL integration, frontend, generated/API,
  documentation, Helm, worker/restart/outage/failover, tunnel-HA, and live
  browser lanes subsequently passed during implementation. Exact images for
  `0e0a321dd20a9b7f3072afb6fb705c150c1f710d` were built, imported, and
  atomically deployed as Helm revision 45 with immutable digests; `/health/`
  and `/readyz` were green. The scale run then produced the two later source
  fixes above, so the final exact-image build/deploy and broad replay remain
  open rather than being claimed from the earlier candidate.
- **Phase 1, complete locally:** commit
  `a84e393a3fb81ec8c4f5f229cc9154e45e7aa5ff` gives every production runtime
  loop a named supervisor or joined component owner, connects critical failure
  to `/readyz` and process termination, makes shutdown idempotent and ordered,
  joins Charlie generations, and keeps dependency pools open when a hung loop
  exceeds the shutdown deadline. Failure-injection tests cover worker crash,
  intentional worker exit, hung join, live-dependency audit drain, and repeated
  shutdown. `go test -race ./internal/charlie ./internal/server/... ./cmd/server/... -count=1`,
  focused normal tests, vet, complexity, dependency-boundary, and environment-
  access gates passed on 2026-09-17.
- **Phase 2, complete locally:** commits `4c55a686`, `341ecaae`, and
  `f73a1234` bind JWTs to exact issuer/audience/subject/type/purpose context,
  add versioned key-identified ciphertext envelopes and measurable legacy
  rewrap, and make browser sessions cookie-only with transactionally durable,
  single-use refresh families. Concurrent refresh reuse revokes the family,
  invalidates distributed caches, and writes mandatory audit without retaining
  raw session identifiers. The schema/API/chart/generated clients moved
  together to migration 47. On 2026-09-17, all 15 required PostgreSQL
  integration tests, the full Go tree, vet/build, focused auth/handler/server
  race suites, all 1,200 frontend tests plus type-check/lint/production build,
  Helm/deploy tests, and SQLC/OpenAPI/config/compatibility drift checks passed.
- **Phase 3, complete locally:** `009f7578`, `1b0bbcf2`, `63ae40b6`, and
  `90493f65` close authenticated-before-extraction air-gap handling,
  production networking/image/runtime policy, authenticated immutable backup
  custody and restore contracts, and strict kubectl checksum/signature
  verification. Render, archive-negative, DR-crypto, release-contract, and
  supply-chain gates passed. Real-CNI and exact protected DR executions remain
  Phase 6 evidence, not local implementation work.
- **Phase 4, implementation complete locally; scale gate open:** `cb1931dc`,
  `5115a90f`, `7aacdc00`, `d0a62779`, `5239c7e7`, `3c251a63`, and
  `a4efe473` implement durable notification intent, bounded cursor/SQL access,
  JSON/deletion governance, generated-query ownership, typed services, and
  correlated telemetry. Later qualification repairs cover restart, PostgreSQL
  and Redis outages, failover, tunnel HA, reconnect accounting, credential
  bootstrap, and measured-window scheduling. Estate-100 must pass before a
  higher rung starts.
- **Phase 5, automated implementation complete locally:** `9719a216`,
  `b8fb7feb`, `a85db0f6`, `db38d75f`, `e4c036f4`, `5dfdf2ae`,
  `02637f37`, `94dc0aef`, and `3a47f9b2` close authoritative fleet/workload/
  alert views, remote action search, cancellable generated requests, automated
  accessibility, Node 24/tooling alignment, reusable role state, accepted
  architecture decisions, and keyboard-reachable overflow. Frontend unit,
  type, lint, production build, 124 main E2E, 264 smoke, 50 visual, and the
  15-scenario live-browser qualification passed. The manual AT matrix remains
  Phase 6.

## Commands and authoritative gates

| Purpose | Command | Expected result |
|---|---|---|
| Source hygiene | `git diff --check` | exit 0 |
| Go unit/integration-safe tree | `go test ./... -count=1` | all packages pass |
| Go race | `go test -race ./... -count=1` | all packages pass on a host with sufficient resources |
| Go static/build | `go vet ./internal/... ./cmd/... && go build ./...` | exit 0 |
| PostgreSQL contracts | `./scripts/test-postgres-integration.sh` | every declared required test executes; zero hidden skips |
| Frontend | `cd frontend && npm run type-check && npm run lint && npm test && npm run build` | exit 0; zero lint warnings |
| Frontend browser | `cd frontend && npm run test:e2e` | zero retries; required projects pass |
| Helm/chart | `helm lint deploy/chart -f deploy/chart/values-dev.yaml && go test ./deploy/... -count=1` | exit 0 |
| Documentation/API drift | `node scripts/check-docs.mjs` | exit 0; no mounted/stale operation or link/semantic finding |
| Static enterprise | `make verify-enterprise VERIFY_SCOPE=all` | all recorded subgates pass on one exact tree |
| Stateful enterprise | `make verify-all` | every available required stateful/browser lane passes; prerequisites are declared, never silently skipped |
| Local k3s | `KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl get deploy,statefulset,pods -n astronomer` | intended workloads Ready after an atomic Helm upgrade |

Do not use a narrower green command as evidence for a broader row. In
particular, `verify-enterprise` is static/local verification, not scale,
accessibility, cloud, DR, or release-candidate proof.

## Scope and git workflow

Phase sections list the expected files. Touch additional source only when it is
a direct dependency required by the named outcome, add it to that phase's
review notes, and keep generated source paired with its generator.

- Use conventional commits matching current history, one logical outcome per
  commit (`fix(auth): ...`, `fix(runtime): ...`, `test(scale): ...`).
- Never commit generated output without its source change and a clean second
  generation check.
- Never push or publish a release unless the operator explicitly authorizes it.
- Build release images from a clean committed tree and bind them to Git SHA and
  immutable digest. Do not promote the mutable local `advisor-011-015` tags.
- Preserve all unrelated existing work. Never use `git reset --hard`, broad
  checkout/restore, or unreviewed cleanup.

## Phase 0 — Establish one reproducible source and release baseline

This phase is first because the currently running local images were built from
the full dirty integration tree, not solely from the 15 commits on
`advisor/011-015-hardening`. A clean clone of HEAD cannot yet reproduce the
running system.

### Work

1. Inventory every modified, deleted, and untracked path. Classify each as an
   intended product change, generated consequence, evidence/artifact, or
   unrelated operator work. STOP on ambiguous ownership; do not discard it.
2. Partition intended product work into reviewable commits. Keep SQL/OpenAPI/
   route/client/config/CLI outputs with their canonical generators and verify a
   second generation is clean.
3. Reconcile the currently missing mounted OpenAPI operation through the route
   dump and canonical OpenAPI generation workflow; do not hand-edit generated
   clients or suppress the documentation contract.
4. Remove generated test evidence from the source tree only when it is ignored,
   reproducible, and clearly owned by this effort. Preserve operator data.
5. Rebase or merge onto current `origin/main` only after the work is committed
   and conflict ownership is understood.
6. From a fresh worktree at the resulting exact commit, run the static
   enterprise gate, PostgreSQL integration lane, frontend gate, Helm gate, and
   applicable stateful lanes.
7. Build every image with the exact commit/version labels, import or push by
   immutable digest, atomically upgrade local k3s, and recheck `/health/`,
   `/readyz`, rollout status, migrations, and logs.
8. Record the exact commit, chart digest, image digests, commands, tool versions,
   and results under the repository's established evidence contract.

### Expected files

- Existing dirty product and generated files after classification.
- `advisor-plans/README.md` only for status/evidence updates.
- Existing evidence scripts/schemas when a reproducibility defect is found.

### Gate

`git status --short` is clean in the fresh verification worktree; all intended
source is committed; the deployed manifests use the exact verified digests;
static/stateful gates and local health pass against the same commit.

## Phase 1 — Finish runtime lifecycle and critical-loop health

**Status: DONE locally at
`a84e393a3fb81ec8c4f5f229cc9154e45e7aa5ff`; exact-candidate deployment and
release qualification remain governed by Phases 0 and 6.**

### Work

1. Give every server-owned goroutine/loop one explicit owner, cancellation
   source, completion signal, bounded join, and shutdown order. Include
   reconcilers, metrics reporters, tunnel queue worker, Charlie runtimes,
   publishers, and any leader loops started by `NewApp`.
2. Add a critical-runtime health coordinator. Unexpected tunnel-worker or other
   mandatory-loop exit must make readiness fail immediately and initiate an
   orderly process shutdown; intentional shutdown must not report failure.
3. Make shutdown order executable and tested: stop ingress/agents, stop task
   intake, cancel and join loops, drain audit/telemetry, close leader sessions,
   then Redis/PostgreSQL.
4. Add failure-injection tests for normal HTTP close, worker crash, worker exit
   during shutdown, hung loop deadline, audit drain with live DB, and repeated
   shutdown. Run race tests.

### Expected files

- `internal/server/server.go`, `internal/server/readiness.go`,
  `internal/server/app_runtime_foundation.go`, focused server tests.
- `cmd/server/main.go`, `internal/db/db.go`, and worker lifecycle adapters only
  as required by explicit ownership.

### Gate

- Every owned loop is canceled and joined under a bounded timeout.
- An unexpected critical-loop exit yields readiness 503 before or while orderly
  termination begins.
- Normal shutdown exits 0 and no resource is used after close.
- `go test -race ./internal/server/... ./cmd/server/... -count=1` passes.

## Phase 2 — Complete browser-session, JWT-context, and ciphertext boundaries

**Status: DONE locally at `f73a1234`; final exact-candidate replay remains
governed by Phase 0.**

### Work

1. Add durable refresh-session families. Store a hashed refresh JTI/family,
   consume each token exactly once transactionally, rotate within the family,
   detect reuse, revoke the entire family, invalidate caches, and emit mandatory
   audit. Add concurrent double-refresh and replay-after-rotation tests.
2. Require exact JWT issuer, audience, subject, token purpose/type, JTI, and
   session-family context. Configure validation through typed config; use exact
   algorithms for every token class. Define an explicit exception contract for
   non-browser service/API tokens rather than weakening interactive sessions.
3. Make browser authentication cookie-only. Login and refresh responses must
   not expose access/refresh bearer material to JavaScript. Keep HttpOnly,
   Secure-in-production, SameSite, CSRF, host/path scope, logout clearing, SSO,
   TOTP, forced-password, and generated API contracts coherent.
4. Introduce a versioned authenticated ciphertext envelope containing format,
   algorithm, key ID, and ciphertext. New writes use the primary key ID; reads
   accept measured legacy Fernet values during a bounded migration. Add key
   inventory, batch rewrap, progress, rollback, and unknown-key failure tests.
5. Update OpenAPI/generated clients, secret inventory, key-rotation runbook, and
   migration tests together. Never log token or plaintext/ciphertext material.

### Expected files

- `internal/auth/jwt.go`, `internal/auth/jwt_session.go`, `internal/auth/crypto.go`.
- `internal/handler/auth.go`, `internal/handler/auth_sessions.go`, SSO/TOTP
  handlers and focused tests.
- New canonical SQL/migration files under `internal/db/queries/` and
  `internal/db/migrations/`, regenerated `internal/db/sqlc/`.
- `internal/config/`, `docs/openapi.yaml`, generated API clients/types, key
  rotation/security docs.

### Gate

- Refresh tokens are single-use; reuse revokes the family and is audited.
- Every JWT validates exact context and algorithm.
- Browser JavaScript never receives session bearer material.
- Every new ciphertext identifies format/algorithm/key and legacy migration is
  measurable, reversible, and lossless.
- Focused auth race tests plus full backend/frontend/API-contract gates pass.

## Phase 3 — Close production networking, DR, air-gap, and supply-chain gaps

**Status: DONE locally through `90493f65`; real-CNI and protected exact-RC DR
qualification remain governed by Phase 6.**

### Work

1. Add narrow NetworkPolicies for backup, restore-drill, and management-logging
   pods. Inventory DNS, database, object store, Kubernetes API, metrics, OTLP,
   and configured log-sink traffic; allow only required directions/destinations.
   Add a chart-owned-pod coverage contract and real-CNI production smoke.
2. Make backups confidential and tamper-evident. Require authenticated client-
   side encryption or validated provider SSE-KMS/SSE-S3, sign/authenticate a
   manifest covering archive/schema/key/source identity, and verify before
   restore.
3. Separate backup writer and retention/deleter identities. Validate versioning,
   Object Lock, or equivalent immutable retention before production startup.
   Retention must be a distinct dry-run-capable workflow.
4. Replace AES-CBC key wrapping with AEAD/versioned envelope handling coordinated
   with Phase 2. Authenticate before extraction and retain an explicit legacy
   read-only compatibility sunset.
5. Rework air-gap loading so archive paths, member types, closed index schema,
   release-manifest binding, checksums, and Sigstore identity are validated
   before any extraction or registry write. Reject links, traversal, duplicate
   members, extra payloads, digest mismatch, wrong release, and unsafe legacy
   runtimes.
6. Make production chart input reject every mutable first- and third-party image
   reference. Rendered release and air-gap manifests must contain only
   `repository@sha256` identities.
7. Checksum/signature-verify downloaded kubectl. Remove mutable `apk upgrade`
   behavior, minimize runtime packages, run as non-root/read-only where
   compatible, and prove deterministic rebuild identity or document unavoidable
   differences.
8. Add dedicated gosec/SARIF and full-history secret scanning without printing
   findings' secret values. Remove reusable credential literals from tracked
   examples or make them unmistakably non-secret generated fixtures with gates.
9. Extend automated dependency update coverage to Go, npm, Actions, and
   containers with controlled grouping/major-version policy.
10. Either produce a separately built/tested FIPS-capable artifact with accurate
    language, or record an accepted non-goal ADR and remove unsupported FIPS
    implications. Never claim certification without certification evidence.

### Expected files

- `deploy/chart/templates/networkpolicy.yaml`, management backup/restore/logging
  templates, chart values/schema, and render tests.
- `scripts/airgap-kit.py` and its negative tests.
- `deploy/docker/Dockerfile.*`, `.github/workflows/`, `.golangci.yml`,
  `.github/dependabot.yml`, release/evidence scripts and operator docs.

### Gate

- Production render works under default deny without broad catch-all egress.
- Backups are encrypted, authenticated, immutable, independently deletable only
  by the retention role, and restore into a usable exact release candidate.
- Malicious air-gap archives fail before extraction or registry mutation.
- Production renders contain no mutable image tags.
- Binary downloads are verified; security CI and dependency automation cover
  the declared ecosystems; FIPS language matches evidence.

## Phase 4 — Finish durable dispatch, bounded data access, scale, and telemetry

**Status: implementation DONE locally; estate-100 remains OPEN after the
retained 2026-09-17 failed engineering run. Do not start a higher rung.**

### Work

1. Route alert/control-plane notifications through one PostgreSQL durable outbox
   transaction. Preserve task options, destination identity, trace/audit
   correlation, uniqueness, retry/dead-letter state, and recovery after commit/
   before enqueue. Never report notification success for lost work.
2. Introduce opaque keyset/cursor pagination for fleet-scaled default APIs.
   Reject or bound pathological offsets before SQL. Keep stable sort plus ID,
   authorization scope, filters, and backward compatibility explicit.
3. Move GitOps, Vault, SCIM, and other load-all-then-page collections into
   scoped SQL count/filter/page queries. Do not fetch secret columns for list
   views.
4. Inventory durable JSONB and destructive deletes. Add versioned writer
   validation, compatibility/migration rules, retention ownership, and audit.
   Preserve the completed archive-name-before-tombstone-purge invariant.
5. Move handwritten query implementations out of generated `internal/db/sqlc`
   ownership and make `sqlc-check` reject extra/generated-directory drift.
6. Move domain logic behind typed services/interfaces; handlers remain transport
   adapters. Eliminate ambient environment reads outside typed configuration.
7. Complete API-to-worker/tunnel-to-agent trace propagation, explicit service/
   environment identity, zero-sampling semantics, command logging policy, and a
   usable local telemetry stack. Add correlated metrics/log assertions.
8. Run and retain estate-100 first, then 500 and 2,000 only after the previous
   rung passes. Record hardware, topology, dataset, configuration, duration,
   percentiles, backlog, error budget, audit conservation, and raw artifacts.

### Expected files

- Alert/control-plane handlers, notification tasks, task-outbox SQL/runtime.
- Shared pagination package, affected handlers, canonical SQL and regenerated
  sqlc output.
- JSON/deletion inventories and validation packages.
- `cmd/agent`, observability packages, local compose/telemetry config,
  scale harness, evidence schemas, and scale docs.

### Gate

- Accepted notification intent survives server, worker, Redis, and process
  failure without duplicate effective delivery.
- Default fleet lists have bounded database access and no deep offset reaches SQL.
- Stateful integration tests execute with zero hidden skips.
- A trace correlates API, durable task, worker/tunnel, and agent activity.
- Retained estate-100 evidence passes before any higher scale claim is made.

## Phase 5 — Finish estate-scale frontend truth, accessibility, and toolchain alignment

**Status: automated implementation and browser qualification DONE locally;
the human/manual assistive-technology matrix remains governed by Phase 6.**

### Work

1. Make cluster inventory and overview use server-side filters, stable sort,
   cursor/page, and authoritative authorization-scoped aggregates. Never infer a
   fleet total from the visible page.
2. Send workload kind/namespace/search/sort/page to the API and preserve
   continuation metadata through generated client, adapter, hook, and table.
3. Preserve alert event page envelopes and add authoritative aggregate counts;
   reconcile SSE updates with the correct pages/aggregates.
4. Replace eager cluster option downloads with debounced remote search,
   virtualization, and permission-scoped selection for catalog, scans, RBAC,
   registration, delivery, and credential targeting.
5. Complete keyboard, axe, focus, screen-reader name/description, form semantics,
   live-region, non-color state, reduced-motion, zoom/reflow, and manual AT
   checks for scope controls, logs, rule creation, RBAC binding, chart install,
   destructive confirmation, and error recovery.
6. Replace high-risk handwritten wire unions/casts with generated OpenAPI types
   and propagate `AbortSignal` through every core list/detail request.
7. Align Node/tooling with the accepted technology baseline (currently the old
   plans require a Node 24 decision). Make local, Docker, CI, and docs agree.
   Add reusable Playwright role storage state; keep auth-specific tests isolated
   and retries at zero.
8. Write accepted ADRs for retained deviations: REST/OpenAPI vs guide choices,
   TanStack decisions, generated client boundary, lint/local reload approach,
   and any deliberate Node-version exception.

### Expected files

- Relevant routes/components under `frontend/src/routes/dashboard/` and
  `frontend/src/components/`.
- Generated-client adapters/hooks/types, `frontend/package*.json`, Dockerfile,
  Playwright config/fixtures/tests, CI, and architecture decisions.
- Backend aggregate/search/page endpoints plus OpenAPI/sqlc generated artifacts
  required by the UI.

### Gate

- Cluster/workload/alert views and action pickers are truthful at 2,001 records.
- Critical journeys pass keyboard, axe, zoom/reflow, and manual AT review.
- Core requests cancel on navigation/filter change and high-risk wire types are
  generated.
- Node versions agree everywhere; Playwright uses reusable role state with zero
  retries; every material technology deviation has an accepted ADR.

## Phase 6 — Execute the eight external GA qualifications

All eight outcomes are required for an unconditional GA approval. Harnesses and
validators already existing locally are not substitutes for executing them on
the exact release candidate.

1. Run the protected adopted-cloud acceptance against credentialed EKS, GKE,
   AKS, and DOKS sandboxes and retain verified signed evidence.
2. Run certified-throughput mandatory-audit load and prove conservation, no
   loss, bounded backlog/drain, and fail-closed outage behavior.
3. Derive production chart sizing/default recommendations only from retained
   comparable measurements.
4. Execute paired cold/warm Astronomer-versus-Rancher automated runs plus the
   preregistered counterbalanced human clarity/error-recovery study.
5. Execute the exact release-candidate accessibility matrix: NVDA/Chrome,
   Narrator/Edge, macOS VoiceOver/Safari, and critical iOS VoiceOver workflows.
6. Produce retained certified scale profiles at each claimed rung.
7. Run backup, clean restore, upgrade, rollback boundary, and application-level
   decrypt proof on the exact release-candidate artifacts.
8. Obtain protected release-engineering approval bound to the exact tag, commit,
   source run, chart/images and transitive runtime evidence, cloud evidence,
   scale/audit/sizing evidence, RC rehearsal, benchmark, and signed
   accessibility result.

### Gate

Every artifact has a closed schema, exact commit/tag/artifact bindings,
recomputed digests, trusted workflow identity/signature, source run and
conclusion, freshness, sanitization, and retained raw evidence. A missing or
failed execution keeps GA blocked; it is never converted to a prose waiver.

## Phase 7 — Run physical K3s/RKE2/Constellation qualification when claiming it

This phase is destructive and requires explicit operator authorization. It is
not executable until the real Constellation repository/contracts and a signed
six-host inventory are available.

### Work

1. Pin exact Astronomer and Constellation release candidates, contracts,
   signatures, supported kernel/runtime/Kubernetes versions, APIs/CLIs, and
   cleanup procedures. Do not invent a Constellation capability.
2. Prove three management hosts and three disposable target hosts are disjoint
   by address, machine ID, SSH host key, and Kubernetes node identity. Require
   exact ownership/expiry/destructive acknowledgement and pinned SSH host keys.
3. Implement a default-dry-run harness with closed inventory/evidence schemas,
   strict decoding, safe command arguments, path/service/resource allowlists,
   atomic sanitized evidence, failure manifests, and cleanup traps.
4. Build immutable workloads covering pre-existing and Astronomer-created
   resources, Flux delivery, stateful persistence, rollout/rollback, policy,
   vulnerability controls, and correlated HTTP/TCP/UDP/DNS/deny traffic.
5. Execute three passing repetitions of each mandatory topology: single-node
   K3s, single-node RKE2, three-server embedded-etcd K3s, and three-server
   embedded-etcd RKE2 (12 accepted runs total).
6. In every run prove Astronomer adoption/agent/Flux/workload/audit/disconnect
   recovery and Constellation identity/sensor/vulnerability/traffic/process/
   restart recovery using independent Kubernetes and sink observations.
7. Preserve failed runs, validate signed per-run evidence before proceeding,
   remove only campaign-owned records/resources, uninstall the distribution,
   reboot, and prove clean residual state before reusing a host.

### Gate

Exactly 3/3 runs per topology pass with no skipped assertion; every artifact is
signed, fresh, sanitized, and digest-closed; cleanup passes after every run and
the final reboot. Until then, documentation may say the harness exists or a
topology is partially characterized, but may not claim complete physical
K3s/RKE2/Constellation interoperability.

## Final release done criteria

All core criteria are mandatory for GA; the final physical-interoperability row
is mandatory only when that claim is made.

- [ ] The intended source is committed and reproducible from a clean checkout;
      the exact verified commit produced the deployed digest-pinned artifacts.
- [ ] Static, race, PostgreSQL, browser, Helm, generated-contract, and applicable
      stateful gates pass on that exact commit with zero hidden skips.
- [x] Every server-owned critical loop has supervised health and bounded join.
- [x] Refresh replay, JWT context, cookie-only browser auth, revocation, error
      redaction, DSN parsing, and versioned ciphertext criteria pass.
- [ ] Default-deny networking, DR authority, authenticated immutable backups,
      safe air-gap input handling, immutable image inputs, and supply-chain
      gates pass.
- [x] Notifications are durable; default data access is bounded/keyset and SQL-
      scoped; JSON/deletion/sqlc ownership rules are enforced.
- [ ] Estate-scale UI truth, action search, cancellation, accessibility,
      Playwright, Node/toolchain, and ADR criteria pass.
- [ ] Estate-100 and every advertised higher scale rung have retained evidence;
      telemetry correlates API through agent.
- [ ] All eight Phase 6 external qualifications pass and protected release
      approval binds the exact artifacts.
- [ ] Documentation makes only evidence-backed production, scale, accessibility,
      Rancher-comparison, cloud, DR, and compliance claims.
- [ ] If physical interoperability is claimed, all 12 Phase 7 runs and cleanup
      gates pass on exact Astronomer/Constellation candidates.
- [ ] `advisor-plans/README.md` records Plan 016 DONE and links the retained
      evidence index. Archived plans remain immutable historical records.

## STOP conditions

Stop and report; do not improvise if any condition occurs:

- Existing working-tree ownership cannot be determined without risking user or
  operator work.
- A phase requires restoring Argo/Fleet, adding cluster provisioning, or
  weakening the adopted-cluster/Flux product boundary.
- A secret, token, credential-bearing URL, private key, kubeconfig, or plaintext
  encrypted value would enter a commit, log, artifact, command line, or plan.
- Refresh replay cannot be closed transactionally or browser bearer material
  cannot be removed without an explicit compatibility decision.
- A backup cannot be authenticated before restore, immutability cannot be
  independently enforced, or a restore cannot prove application decryptability.
- Air-gap input must be extracted or written to a registry before trust and
  closed membership are established.
- A durable API success would still precede durable intent.
- Scale, accessibility, Rancher, cloud, DR, or interoperability evidence is
  missing, self-asserted, stale, unsigned, mutable, or not bound to the exact RC.
- Constellation contracts/hosts are unavailable, host sets overlap, destructive
  acknowledgement is absent, or cleanup is incomplete.
- A required gate fails twice after a scoped fix or would require unrelated
  changes outside the phase without revising this plan.

## Maintenance and reviewer focus

- Reconcile this plan after every completed phase; mark criteria only from
  observed command/evidence results.
- Review authentication transaction boundaries, encryption migration rollback,
  shutdown ordering, backup custody/immutability, pagination authorization,
  outbox uniqueness, and evidence provenance more aggressively than diff size.
- Any new collection must define bounded access and continuation semantics.
- Any new encrypted field must use the versioned envelope/key inventory.
- Any new async mutation must commit durable intent before returning success.
- Any release claim must name its retained evidence producer and exact artifact
  binding.
