# Plan 002: Close enterprise-grade correctness, trust, scale, and Rancher day-2 assurance gaps

> **Executor instructions**: This is a human-reviewable master program and a
> handoff specification for implementation agents. Read the entire document
> before changing code. Execute phases in order unless the dependency graph
> explicitly permits parallel work. Run every verification gate and preserve
> the evidence named in the exit criteria. If a STOP condition occurs, stop and
> report it; do not silently weaken a security control, skip a failing gate, or
> invent validation evidence.
>
> **Repository root**: /root/astronomer-all/astronomer
>
> **Drift check — run first**:
>
> ~~~bash
> git rev-parse --short HEAD
> git status --short
> git diff --stat 9256f0d..HEAD -- \
>   internal/server internal/handler internal/auth internal/events \
>   internal/agent internal/worker/tasks scripts/loadtest frontend/src \
>   deploy/chart deploy/agent docs .github/workflows
> ~~~
>
> Expected at planning time: HEAD is 9256f0d and the working tree is clean.
> If any in-scope path has changed, compare the live code against Section 4
> before implementation. A material mismatch is a STOP condition until this
> plan is reconciled.

## 1. Program status

| Field | Value |
|---|---|
| Priority | P0 enterprise closure program |
| Overall effort | XL; approximately 8–14 engineering weeks depending on parallelism and live-environment availability |
| Overall risk | MED–HIGH; trust-boundary, multi-replica, agent bootstrap, NetworkPolicy, and tunnel changes can affect connectivity |
| Planned at | commit 9256f0d, 2026-07-09 |
| Depends on | Plans 000 and 001 as historical context; the Wave A–C implementation already landed in 9256f0d must not be reimplemented |
| Categories | correctness, security, HA, performance, tests, deployment, GitOps, agent lifecycle, UX, architecture, supply chain, documentation |
| Intended outcome | Enterprise-GA evidence for adopted-cluster day-2 management with Argo CD |
| Explicit exclusions | Cluster provisioning, machine drivers, node pools, CAPI lifecycle, Fleet compatibility, Rancher Prime commercial features |

> **2026-07-10 live-acceptance hold:** The integrated implementation at
> `advisor/002-integration` commit `6783004f97b270d3b1e3fa1ac105dd234a534e08`
> passes the static enterprise gate and produced six correctly labelled 0.2.2
> candidate images, but it is **not release-approved**. A controlled live Argo
> acceptance run exposed the P0 asynchronous-operation and retained-resource
> ownership defects specified in Section 26. The Argo Application controller
> was returned to zero replicas, the test operation was terminalized, and the
> release remains blocked until every Section 26 gate passes.

## 2. Why this program exists

Astronomer already has a serious management-plane architecture: outbound
agents, durable token rotation, multi-replica tunnel location, Argo CD
multi-instance support, ApplicationSets, projects and RBAC, SSO/SCIM/MFA,
audit and SIEM, Kubernetes explorer operations, monitoring, compliance,
backup/restore, air-gap packaging, SBOMs, and signed release artifacts.

The remaining gap is not primarily feature count. It is whether the shipped
defaults, documentation, tests, multi-replica behavior, and scale claims all
describe the same system.

At commit 9256f0d:

1. Required backend and frontend verification gates are red.
2. The production-default Argo CD proxy listener omits the cluster-scoped
   bearer-token control that the threat model, OpenAPI, and auto-registration
   implementation say exists.
3. JWT revocation and RBAC changes invalidate only the current server process.
4. Redis event fan-out synchronously blocks publishers for up to two seconds.
5. The advertised synthetic-agent load harness cannot authenticate its
   randomly generated clusters against the real tunnel validator.
6. Session, agent privilege, and agent installation defaults contradict other
   parts of the product.
7. Bundled Argo CD is excluded from namespace default-deny policy.
8. Fleet-operation previews are incomplete above 500 clusters.
9. Rancher-relative claims are based on broad feature presence without the
   live scale, HA, failure-recovery, and UX evidence needed for enterprise GA.

This program turns those findings into a dependency-ordered delivery plan. It
does not redesign the product around Rancher. Rancher remains the benchmark
for adopted-cluster operational confidence, structured resource UX, tenancy,
and ecosystem maturity. Astronomer remains intentionally different:

- Import and adopt clusters; do not provision them.
- Use Argo CD and ApplicationSets; do not implement Fleet Bundle CRDs.
- Preserve Postgres plus asynq and the existing outbound agent model.
- Keep YAML and server-side apply as the universal Kubernetes escape hatch.

## 3. Target enterprise posture

The program is complete only when all of the following are true:

| Area | Target |
|---|---|
| Release integrity | Every required local and CI gate is green from a clean checkout |
| Argo trust | Every Argo-to-agent proxy request is authenticated, cluster-scoped, rate-limited, and audited |
| Multi-replica auth | Logout, deactivation, role removal, and binding changes converge across replicas within the declared SLO and fail safely during Redis loss |
| Event fan-out | Product request paths never wait on Redis Pub/Sub; overload and drops are observable |
| Agent bootstrap | Default privilege is consistently least-privilege; manifest reapply cannot overwrite the durable credential; bootstrap tokens are not stored in client-side apply annotations |
| Network isolation | Production Argo components are covered by explicit ingress and egress policy with documented external-destination configuration |
| Scale evidence | Synthetic agents use real cluster identities and credentials; resource proxy traffic targets connected agents; production-like pass rows exist |
| Fleet safety | Selector preview and execution share one resolver and remain exact beyond 500 clusters |
| Explorer parity | Common resources have schema-driven structured create/edit flows while YAML/SSA remains available |
| Tunnel posture | One supported primary tunnel is selected only after auth, HA, and feature matrices are green |
| Supply chain | CI actions are immutable, images support digest pinning, Helm renders without unexplained warnings |
| Product claims | Comparison, threat model, OpenAPI, runbooks, and marketing rules match tested behavior |

## 4. Audited current state

This section is the implementation baseline. Executors must re-open these
locations before changing them.

### 4.1 Verification baseline is red

Observed commands:

| Command | Observed result at 9256f0d |
|---|---|
| go vet ./... | pass |
| go test ./... | fail |
| make verify | fail |
| go test ./internal/handler/ ./internal/worker/tasks/ -count=1 | fail |
| cd frontend && npm run type-check | pass |
| cd frontend && npm run lint | pass with one warning |
| cd frontend && npm test -- --runInBand | 55 suites and 447 tests pass |
| cd frontend && npm run code-health | fail |
| cd frontend && npm audit --audit-level=high | zero vulnerabilities |
| helm lint deploy/chart | exit 0 with a value-coalescing warning |

The directly observed failing Go tests are:

- TestRegisterClusterWithArgoCD_StampsLabels
- TestPollRunningOperations_FansOutConcurrently
- TestExecuteSyncCallsUpstreamAndReflectsResponse
- TestPollRunningOperationCompletesOnSucceeded
- TestPollRunningOperationFailsOnTerminalFailed
- TestRender_PromSparkline_RoundTrip
- TestRender_PromStat_RoundTrip
- TestDatasource_CRUD_And_Test
- TestArgoCDAutoRegisterSweepUpsertsClusterWithStandardLabels

They attempt to reach loopback httptest servers through newly dial-guarded
SafeClient instances. The intended test seam exists:

~~~go
// internal/httpclient/ssrf.go:16
// Intended as: defer httpclient.DisableGuardForTest()()
func DisableGuardForTest() (restore func)
~~~

Existing focused usage is demonstrated by
internal/handler/argocd_local_cluster_test.go:195. A package-level TestMain is
used only in the dedicated internal/dashboards test package. Do not install a
package-global guard disable in internal/handler or internal/worker/tasks:
those packages also contain SSRF regression tests that must run with the guard
enabled.

The frontend code-health gate reports:

- direct HTTP call at frontend/src/hooks/use-resource-watch.ts:116;
- page-local query key at
  frontend/src/app/dashboard/argocd/[instanceId]/page.tsx:311;
- stale docs/rancher-quality-phase0-code-health-inventory.md.

The governing rules are in scripts/code-health-inventory.mjs:
direct fetch/axios calls belong under frontend/src/lib/api or
frontend/src/lib/api/, and app routes must use frontend/src/lib/query-keys.ts.

### 4.2 Argo default proxy path contradicts its security contract

The public router correctly applies the machine-token middleware:

~~~go
// internal/server/routes.go:1075
r.With(
    rateLimit(appmiddleware.ClassArgoCDProxy),
    requireArgoCDClusterProxyToken(deps.ArgoCDProxyTokens),
    auditArgoCDK8sProxyMutations(deps.AuditWriter),
).HandleFunc("/api/v1/internal/argocd/clusters/{cluster_id}/k8s/*", ...)
~~~

The dedicated listener used by the default configuration explicitly omits it:

~~~go
// internal/server/routes.go:1463
// Unlike the public route, this listener is NOT token-gated.
// network isolation IS the authentication boundary.

// internal/server/routes.go:1480
r.With(
    rateLimit(appmiddleware.ClassArgoCDProxy),
    auditArgoCDK8sProxyMutations(deps.AuditWriter),
).HandleFunc(...)
~~~

The default cluster proxy URL points to port 8090:

~~~go
// internal/config/config.go:291
argocd_cluster_proxy_base_url =
  http://astronomer-server.astronomer.svc.cluster.local:8090
~~~

Auto-registration nevertheless generates, encrypts, hashes, persists, and
places a cluster-scoped bearer token into the Argo cluster registration:

~~~go
// internal/worker/tasks/argocd_auto_register_cluster.go:566
Config: argocdclient.ClusterConfig{
    BearerToken: token,
    TLSClientConfig: tlsConfig,
}
~~~

The threat model at docs/threat-model.md:75 and OpenAPI description at
docs/openapi.yaml:20460 both describe hash-validated, cluster-scoped tokens.
The current internal-listener test at
internal/server/routes_security_test.go:1968 deliberately expects tokenless
PATCH, discovery, and secret-list requests to reach the proxy.

### 4.3 Multi-replica cache invalidation is local

deploy/chart/values-production.yaml sets three server replicas.

internal/auth/jwt.go contains a 30-second positive validation cache. At
internal/auth/jwt.go:397, a cache hit skips the database revocation check.
InvalidateCache at line 465 replaces only the current manager's map.
Logout calls that local method at internal/handler/auth.go:960.

internal/server/middleware/rbac_cache.go uses a 15-second local TTL/LRU cache.
Invalidate and InvalidateAll at lines 258 and 273 affect only the current
process. RBAC mutation handlers call those local methods through
internal/handler/rbac.go:91.

The expected enterprise contract is immediate normal-path convergence across
all replicas and safe degradation when Redis is unavailable. It is not enough
to shorten the TTL and continue accepting stale privileges.

### 4.4 Cross-replica event publication blocks callers

internal/events/bus.go:219 performs local nonblocking broadcast, then calls
publishRedis synchronously. publishRedis uses a background context with a
two-second timeout and waits for Redis Publish.

internal/metrics/publisher.go:211 publishes a cluster.metrics event for every
active cluster. The package contract at line 19 says the publisher must never
block the bus. Request and registration handlers also publish through the same
Bus.

### 4.5 Load harness identities are impossible on a secured plane

scripts/loadtest/main.go loads one administrator JWT at line 173. Each
synthetic agent receives that same token and a random UUID at lines 206 and
352. It sends the JWT both as the WebSocket Authorization bearer and in the
CONNECT payload at lines 482–505.

The production validator in internal/tunnel/connectauth/validate.go accepts
only:

1. a registration token belonging to the requested cluster and allowed by the
   post-adoption replay gate; or
2. a durable agent token belonging to that cluster.

The HTTP cluster_pods scenario in scripts/loadtest/scenarios.go:25 targets the
all-zero cluster ID, so it does not measure a real connected tunnel.

scripts/loadtest/README.md currently tells operators to provide an admin JWT
and claims the agents behave like real agents. docs/scale-baseline.md correctly
contains no pass row. loadtest-report-20260708.md records only an unreachable
server.

### 4.6 Settings and agent defaults disagree

- internal/config/config.go:270 defaults session_timeout_minutes to 60.
- internal/handler/platform_settings.go:153 reports
  session.timeout_minutes default 480.
- internal/server/server.go:739 returns zero when the setting has never been
  persisted, leaving the 60-minute boot configuration active.
- That entire runtime provider is currently inside the encryptor/TOTP branch
  beginning at internal/server/server.go:705.

Agent privilege behavior has the same class of drift:

- deploy/agent/template.go:117 normalizes empty or unknown profile to viewer.
- internal/agent/config.go:79 defaults standalone runtime to admin.
- internal/agent/health.go:289 reports admin when profile is empty.
- internal/handler/agent_fleet.go:628 calls admin the default.
- cmd/agent/main.go:222 enables secret watches based on that runtime profile.

### 4.7 Agent bootstrap secret ownership is unsafe

deploy/agent/install.yaml.template:477 documents both problems:

- client-side apply stores the short-lived registration token in the
  kubectl.kubernetes.io/last-applied-configuration annotation;
- reapplying the manifest overwrites the agent-written durable token with the
  registration token.

The primary UI still emits client-side commands at
frontend/src/app/dashboard/clusters/register/[id]/connect/page.tsx:108,
including all trusted-CA, private-CA, and insecure variants.

### 4.8 Argo pods are outside default deny

deploy/chart/templates/networkpolicy.yaml:17 excludes every pod labeled
app.kubernetes.io/part-of=argocd from default deny. The stated reason is that
Argo needs DNS, Kubernetes API, repository, and intra-Argo access and that
upgrade hooks could otherwise deadlock.

This is understandable operationally, but it leaves repository credentials
and downstream cluster access in a high-value unrestricted trust zone.
Production must use explicit, tested policies rather than a blanket exclusion.

### 4.9 Self-managed Argo serializes management credentials into Application state

Live acceptance discovered that the self-managed Astronomer Application placed
JWT signing material, the Fernet key, bootstrap credentials, Dex credentials,
and database credentials in `spec.source.helm.values`. Argo serializes the
Application operation and can log that source. Argo also duplicates an
`ApplicationSource` into status comparison, operation-result, and revision
history fields, so replacing only the desired spec does not remove every live
copy. The Argo API and the bundled UI reverse proxy can echo the same source to
authorized readers. Raw values and logs are excluded from evidence; the test
controller remains stopped until a source-level migration is integrated.

This is a release-blocking P0 because the unsafe representation crosses the
Kubernetes API, etcd, controller logs, API responses, audit/backup history, and
support workflows. NetworkPolicy and Argo RBAC do not make plaintext secret
serialization acceptable.

Required closure:

1. The self-managed Application may contain only stable Secret names and keys,
   never credential values, URLs containing credentials, Helm parameters, or
   secret-bearing value files.
2. Core, bootstrap, bundled database, external Redis, and Dex credentials need
   explicit existing-Secret contracts used by every chart consumer.
3. Installer/server-owned credential Secrets must have durable ownership,
   rotation, backup, restore, prune, uninstall, and disaster-recovery rules.
4. Migration must remove unsafe spec, operation, comparison, operation-result,
   and history copies while the controller is quiesced, then prove two
   reference-only fixed-point reconciliations.
5. Platform Application create/patch paths, including the UI reverse proxy,
   must reject secret-shaped inline Helm sources. All Application response
   paths must redact source material defensively.
6. The embedded chart version must change so Argo cannot reuse a cached archive
   for the old target revision; tests must render the packaged archive itself.
7. Every credential that existed in the unsafe Application, controller logs,
   etcd history, audit records, or backups must be rotated or the disposable
   test cluster rebuilt before acceptance resumes.

### 4.10 Dex runtime and static-client credentials cross plaintext stores

The Dex Apply path decrypts connector fields and writes the complete runtime
configuration to a ConfigMap. The Astronomer static-client registration path
also places the client secret in `dex_settings.public_clients` JSONB, and the
settings response returns that JSON. Encryption of `dex_connectors.config`
therefore does not provide end-to-end protection: plaintext reappears in a
broadly readable Kubernetes object and in an unredacted database/API field.

Dex runtime configuration must be Secret-backed, static-client credentials
must be encrypted or referenced at rest, and every API representation must be
redacted. The migration must handle existing ConfigMaps, database rows,
Deployment volume sources, rollback, and Kubernetes/DB backups without ever
logging decrypted configuration.

### 4.11 Fleet preview truncates at 500

frontend/src/components/fleet/create-fleet-operation-dialog.tsx:44 requests one
page of 500 clusters and computes label suggestions and the match count in the
browser. Group membership is not resolved in the preview. The actual worker
uses internal/worker/tasks/fleet_selector.go and
ListClustersForSelectorEvaluation, so preview and execution do not share one
resolver.

### 4.12 Operational and maintainability debt

Current large-file concentration:

| File | Lines at audit |
|---|---:|
| internal/handler/monitoring.go | 3,815 |
| internal/handler/argocd.go | 2,948 |
| internal/crd/controller.go | 2,919 |
| internal/server/server.go | 2,182 |
| internal/server/routes.go | 2,155 |
| frontend resource explorer page | 2,882 |
| frontend/src/lib/api.ts | 2,776 |
| frontend/src/types/index.ts | 2,538 |
| frontend/src/lib/hooks.ts | 2,223 |

All GitHub Actions use mutable tags such as actions/checkout@v4 and
docker/build-push-action@v6. The Helm image helpers build tag references only;
there is no first-class digest value. Helm lint reports:

~~~text
warning: destination for argo-cd.server.env is a table.
Ignoring non-table value ([])
~~~

The warning is not proven to change the rendered deployment today. Treat it
as an investigation with a required render contract, not as permission for a
breaking values-schema rename.

## 5. Findings and work-item inventory

| ID | Priority | Work item | Effort | Fix risk | Confidence |
|---|---|---|---|---|---|
| BASE-01 | P0 | Restore all Go tests after SafeClient hardening | S | LOW | HIGH |
| BASE-02 | P0 | Restore frontend code-health inventory gate | S | LOW | HIGH |
| BASE-03 | P1 | Make one local and CI enterprise verification command authoritative | M | LOW | HIGH |
| BASE-04 | P3 | Resolve or explicitly waive Helm value-coalescing warning | S–M | MED | MED |
| ARGO-01 | P0 | Authenticate the production-default Argo proxy listener | M | MED | HIGH |
| ARGO-03 | P0 | Remove secret material from self-managed Argo state, logs, and APIs | XL | HIGH | HIGH |
| DEX-01 | P0 | Move Dex runtime/static-client credentials out of ConfigMaps and plaintext JSONB | L–XL | HIGH | HIGH |
| AUTH-01 | P1 | Distribute JWT and RBAC invalidation across replicas | L | MED–HIGH | HIGH |
| EVENT-01 | P1 | Make Redis event fan-out asynchronous and bounded | M | MED | HIGH |
| SET-01 | P1 | Make session timeout one source of truth outside TOTP wiring | S–M | LOW | HIGH |
| AGENT-01 | P1 | Make viewer the universal implicit agent profile | S–M | MED | HIGH |
| AGENT-02 | P1 | Separate bootstrap and durable agent credential ownership | L | HIGH | HIGH |
| ARGO-02 | P1 | Put bundled Argo under explicit production NetworkPolicies | L | HIGH | HIGH |
| SCALE-01 | P0 | Provision real load-test identities and credentials | L | MED | HIGH |
| SCALE-02 | P1 | Send HTTP resource traffic through connected synthetic agents | M | LOW–MED | HIGH |
| SCALE-03 | P1 | Automate HA and dependency failure drills | L | HIGH | HIGH |
| SCALE-04 | P1 | Record reproducible production-like baselines and budgets | M | MED | HIGH |
| FLEET-01 | P2 | Add authoritative server-side fleet selector preview | M–L | MED | HIGH |
| UX-01 | P2 | Build a schema-driven common-resource form framework | XL | MED | HIGH |
| TUNNEL-01 | P2 | Converge on one tunnel only after feature and HA parity | XL | HIGH | HIGH |
| ARCH-01 | P2 | Decompose highest-change god modules behind stable interfaces | XL | MED–HIGH | HIGH |
| SUPPLY-01 | P2 | Pin CI actions and support image digest deployment | M | MED | HIGH |
| DOC-01 | P1 | Reconcile threat model, OpenAPI, runbooks, and Rancher claims | M | LOW | HIGH |

## 6. Dependency graph and phase order

~~~text
Phase 0: BASE-01 + BASE-02 -> BASE-03 -> BASE-04
                         |
                         v
Phase 1: ARGO-01 + ARGO-03 + DEX-01 + SET-01 + AGENT-01
                         |
                         v
Phase 2: AUTH-01 + EVENT-01
                         |
                         +--------------------+
                         v                    v
Phase 3: AGENT-02 + ARGO-02             FLEET-01
                         |
                         v
Phase 4: SCALE-01 -> SCALE-02 -> SCALE-03 -> SCALE-04
                         |
                         v
Phase 5: UX-01
                         |
                         v
Phase 6: TUNNEL-01 + ARCH-01 + SUPPLY-01
                         |
                         v
Phase 7: DOC-01 + GA evidence and sign-off
~~~

Rules:

- No production trust-boundary change begins until Phase 0 is green.
- ARGO-01 must land before Argo NetworkPolicy work; otherwise policy tests can
  accidentally bless a tokenless route.
- ARGO-03 blocks restoration of the live Argo application-controller and all
  later Argo evidence. ARGO-02 policies are defense in depth and cannot be used
  to waive plaintext Application state.
- DEX-01 follows the static reference portion of ARGO-03 and must close before
  ARGO-02/live acceptance, because self-heal cannot safely own a Dex ConfigMap
  that another controller fills with decrypted credentials.
- SCALE-01 must land before any baseline is recorded.
- SCALE-02 must prove the resource scenario reaches the intended connected
  agent before its latency is used in a product claim.
- AGENT-02 requires AGENT-01 so bootstrap migration has one privilege default.
- TUNNEL-01 cannot flip the install default until Phase 4 validates the chosen
  tunnel under multi-replica failover.
- ARCH-01 must begin with characterization tests and must not overlap a
  functional change in the same module.

## 7. Repository commands and conventions

### 7.1 Required commands

| Purpose | Command | Expected |
|---|---|---|
| Build | make build | exit 0 |
| API contract | make verify | exit 0 |
| Go suite | go test ./... | exit 0 |
| Go race suite | go test -race -count=1 ./... | exit 0 |
| Vet | go vet ./... | exit 0 |
| Migration safety | make check-migrations | exit 0 |
| SQL generation check | make sqlc-check | no generated diff |
| Frontend code health | cd frontend && npm run code-health | exit 0 |
| Frontend lint | cd frontend && npm run lint | exit 0 with no warnings |
| Frontend types | cd frontend && npm run type-check | exit 0 |
| Frontend units | cd frontend && npm test -- --runInBand | all pass |
| Frontend build | cd frontend && npm run build | exit 0 |
| Frontend E2E | cd frontend && npm run test:e2e | all required projects pass |
| Dependency audit | cd frontend && npm audit --audit-level=high | zero high/critical |
| Helm lint | helm lint deploy/chart | exit 0, no unexplained coalesce warnings |
| Dev render | helm template astronomer deploy/chart > /tmp/astronomer-dev.yaml | exit 0 |
| Production render | use the production render command in .github/workflows/pr-validation.yaml | exit 0 |
| Chart contracts | go test ./deploy/ -count=1 | pass |
| Load harness | make load-test with an explicit profile and target | literal VERDICT: pass |

### 7.2 Conventions to preserve

- Go errors are wrapped with context and exposed through the existing
  apierror/RespondRequestError helpers.
- Handler dependencies use narrow interfaces so tests can provide fakes.
- Operator-supplied HTTP destinations use internal/httpclient SafeClient
  constructors; tests use explicit injection or narrowly scoped guard disable.
- Database access is sqlc-generated. Update query files first, regenerate, and
  commit generated changes together.
- Frontend HTTP belongs under frontend/src/lib/api or feature modules beneath
  that directory.
- React Query keys belong in frontend/src/lib/query-keys.ts and include every
  argument that changes the returned dataset.
- OpenAPI source is docs/openapi.yaml. Generated frontend types and
  internal/handler/assets/openapi.yaml must remain synchronized.
- Kubernetes chart changes require Helm lint, development and production
  renders, and deploy package contract tests.
- Commit messages follow the existing conventional style, for example:
  fix(argocd): require cluster proxy token on internal listener.

### 7.3 Git workflow

- Use one branch per phase or tightly coupled work item:
  advisor/002-phase-0-green-baseline,
  advisor/002-argo-proxy-auth, and so on.
- Keep commits logically reviewable; do not mix functional changes with god
  module moves.
- Do not push, publish, or open a PR unless the operator requests it.
- Every PR description must include commands run, live topology, evidence
  artifacts, rollback notes, and any remaining waiver.

## 8. Scope boundaries

### In scope

- Source, tests, generated API artifacts, Helm templates/values, load tools,
  CI workflows, and documentation named by the work items.
- New internal packages needed for distributed invalidation, shared selector
  resolution, or extracted module boundaries.
- Non-breaking API additions for preview and operational evidence.
- Schema migrations only when a task cannot meet its correctness contract
  without persistent state.

### Out of scope

- Provisioning Kubernetes clusters or infrastructure.
- Machine drivers, node pools, RKE/RKE2 lifecycle management, CAPI providers.
- Fleet CRDs, Fleet Bundle compatibility, or replacing Argo CD.
- A controller-runtime rewrite of all reconcilers.
- Full structured forms for every possible CRD.
- Replacing Postgres, Redis/asynq, Next.js, or the outbound agent architecture.
- Relaxing SSRF, RBAC, CSRF, token scoping, or NetworkPolicy merely to make a
  test pass.
- Publishing unsupported scale numbers.

## 9. Phase 0 — Restore a trustworthy green baseline

**Objective**: A clean checkout gives one deterministic answer to whether the
product is safe to change. This phase changes no production behavior except
where needed to move frontend transport into its intended layer.

**Estimated effort**: 1–3 engineering days.

### Task 0.1 — BASE-01: repair SafeClient-affected Go tests

Files to inspect and modify:

- internal/handler/argocd_fleet_labels_test.go
- internal/handler/argocd_poll_concurrency_test.go
- internal/handler/argocd_test.go
- internal/handler/dashboards_test.go
- internal/worker/tasks/argocd_auto_register_cluster_test.go
- production constructors only if an explicit client factory seam is required

Implementation requirements:

1. Classify each failing test:
   - If the subject accepts an HTTP client or client factory, inject a normal
     bounded test client pointed at the httptest server.
   - If the production constructor intentionally owns SafeClient creation,
     add defer httpclient.DisableGuardForTest()() inside only the test that
     needs loopback.
2. Do not add TestMain guard disabling to internal/handler or
   internal/worker/tasks.
3. Do not change SafeClient production defaults.
4. Do not replace loopback denial assertions in SSRF tests.
5. Do not call t.Parallel in a test that toggles the process-global test guard.
6. Add a short comment at every guard disable explaining which loopback
   upstream is under test.
7. Run the focused tests twice, then the full suite.

Verify:

~~~bash
go test ./internal/handler/ -run \
  'TestRegisterClusterWithArgoCD_StampsLabels|TestPollRunningOperations_FansOutConcurrently|TestExecuteSyncCallsUpstreamAndReflectsResponse|TestPollRunningOperationCompletesOnSucceeded|TestPollRunningOperationFailsOnTerminalFailed|TestRender_PromSparkline_RoundTrip|TestRender_PromStat_RoundTrip|TestDatasource_CRUD_And_Test' \
  -count=2
go test ./internal/worker/tasks/ \
  -run TestArgoCDAutoRegisterSweepUpsertsClusterWithStandardLabels -count=2
go test ./internal/httpclient/ ./internal/handler/argocd/ -count=1
go test ./... -count=1
~~~

Expected: all exit 0; SafeClient loopback-denial tests still pass.

### Task 0.2 — BASE-02: restore frontend architecture gates

Files:

- frontend/src/hooks/use-resource-watch.ts
- frontend/src/lib/api/resource-watch.ts or another focused module under
  frontend/src/lib/api/
- frontend/src/lib/query-keys.ts
- frontend/src/app/dashboard/argocd/[instanceId]/page.tsx
- corresponding tests
- docs/rancher-quality-phase0-code-health-inventory.md

Implementation requirements:

1. Move the fetch-stream transport, request construction, credentials,
   AbortController ownership needed by the request, status checks, and NDJSON
   reader into a feature API module allowed by the inventory gate.
2. Keep React state, cache folding, lifecycle subscription, and fallback
   behavior in the hook.
3. Preserve cancellation on unmount and incomplete-frame buffering.
4. Add a query-key factory such as
   queryKeys.argocd.operationsWithParams(params), or a specific
   recentOperations(limit), so limit 5 and limit 100 cannot share a cache.
5. Replace the page-local array with that factory.
6. Add/adjust tests for stream success, abort, malformed frame, partial frame,
   non-2xx fallback, and network failure.
7. Regenerate the inventory only after the code is correct:

~~~bash
node scripts/code-health-inventory.mjs --write
~~~

Verify:

~~~bash
cd frontend
npm run code-health
npm run type-check
npm run lint
npm test -- --runInBand use-resource-watch
~~~

Expected: all exit 0 and the inventory lists no direct HTTP or local query-key
hard-gate violations.

### Task 0.3 — BASE-03: make enterprise verification authoritative

Files:

- Makefile
- .github/workflows/pr-validation.yaml
- .github/workflows/api-contract.yaml
- .github/workflows/README.md
- scripts/verify-enterprise.sh if a script is needed

Implementation requirements:

1. Add a single non-mutating target, recommended name verify-enterprise, that
   runs:
   - migration safety and sqlc drift checks;
   - go build, vet, normal tests, and race tests;
   - API/OpenAPI/generated-type/route/error-code gates;
   - frontend code health, lint, typecheck, unit tests, and build;
   - Helm lint and both renders;
   - deploy chart contract tests;
   - high/critical dependency audit.
2. Keep expensive Playwright and live-cluster suites in named CI jobs, but make
   their relationship to verify-enterprise explicit.
3. Ensure CI calls the same underlying commands, not a parallel hand-maintained
   approximation.
4. Add a timeout per job and artifact upload for failing Playwright, Helm
   renders, and test reports.
5. Do not make a red test optional. Quarantine requires an owner, expiry date,
   issue, and explicit separate job.

Verify:

~~~bash
make verify-enterprise
git diff --exit-code -- docs/openapi.yaml internal/handler/assets/openapi.yaml \
  frontend/src/types/openapi.generated.ts docs/generated-route-inventory.json
~~~

Expected: exit 0 from a clean checkout and no generated drift.

### Task 0.4 — BASE-04: resolve the Helm Argo env warning

This task begins as an investigation.

1. Produce a minimal Helm values/coalescing reproduction showing why the
   parent server.env map conflicts with the argo-cd server.env list.
2. Render with:
   - no custom server environment;
   - one Astronomer server environment value;
   - one Argo server environment item;
   - production values.
3. Assert that values do not leak between the parent server and Argo server.
4. Prefer a backward-compatible value alias or explicit subchart mapping.
5. If Helm itself emits the warning despite correct output and eliminating it
   requires a breaking parent values rename, STOP. Document a narrowly scoped
   waiver with Helm version, upstream issue, render assertions, owner, and
   review date instead of silently renaming server.env.
6. Add a chart contract test that detects actual environment leakage.

Verify:

~~~bash
helm lint deploy/chart 2>&1 | tee /tmp/helm-lint.txt
! grep -q 'destination for argo-cd.server.env' /tmp/helm-lint.txt
go test ./deploy/ -count=1
~~~

Expected: warning absent, or the README contains an approved time-bounded
waiver and the render-isolation contract passes.

### Phase 0 definition of done

- [ ] All nine observed Go regressions pass twice.
- [ ] go test ./... passes.
- [ ] make verify passes.
- [ ] npm run code-health passes with regenerated truthful inventory.
- [ ] Frontend lint has zero warnings.
- [ ] verify-enterprise exists and passes.
- [ ] No security test was weakened or skipped.
- [ ] Helm warning is fixed or explicitly waived with a render contract.

## 10. Phase 1 — Align production trust contracts and secure defaults

**Objective**: Defaults, generated credentials, route enforcement, platform
settings, and agent-reported capabilities describe the same secure system.

**Estimated effort**: 3–6 engineering weeks after ARGO-03 and DEX-01 were
discovered during live acceptance; the original trust-default work alone was
3–6 engineering days.

### Task 1.1 — ARGO-01: authenticate the actual internal Argo listener

Primary files:

- internal/server/routes.go
- internal/server/routes_security_test.go
- internal/config/config.go
- internal/server/server.go
- internal/worker/tasks/argocd_auto_register_cluster.go
- deploy/chart/templates/server-service.yaml
- deploy/chart/templates/networkpolicy.yaml
- docs/threat-model.md
- docs/openapi.yaml and generated artifacts
- live Argo validation scripts

Required design:

1. Apply requireArgoCDClusterProxyToken to
   NewInternalArgoCDProxyRouter before forwarding.
2. Require a configured token querier. Nil configuration must fail closed.
3. Keep the Argo rate-limit class and mutation audit middleware.
4. Keep NetworkPolicy as a second control, not the sole identity check.
5. Continue using the per-cluster token generated by
   ensureArgoCDClusterProxyToken and stored in the Argo cluster Secret.
6. Validate token hash, expiry, and requested cluster ID.
7. Confirm token deletion/revocation on decommission and repair behavior when
   either the DB row or Argo Secret is missing.
8. Decide whether the duplicate public-port route remains:
   - retain it only if a supported caller uses it;
   - otherwise deprecate it with route/OpenAPI compatibility handling.
9. Correct the comments claiming Kubernetes discovery/apply is anonymous.

Tests:

- Tokenless GET, PATCH, CONNECT, discovery, and secret-list requests return 401.
- Wrong-prefix token returns 401.
- Valid token for another cluster returns 401.
- Expired/revoked token returns 401.
- Valid token for the path cluster reaches the proxy handler.
- Mutation audit records the cluster and Kubernetes object reference.
- Rate limiting still applies.
- A real bundled Argo instance can register a cluster, perform discovery, and
  sync a namespace/configmap through port 8090.

Live verification:

1. Deploy three server replicas, bundled Argo, Redis, and one adopted test
   cluster.
2. Confirm the Argo cluster Secret has bearerToken material but do not print it.
3. Sync a baseline ApplicationSet.
4. Rotate the proxy token and prove the old token is rejected and Argo recovers
   after its Secret is reconciled.
5. Attempt a token for cluster A against cluster B and record only the status
   and audit event, never the token.

Verify:

~~~bash
go test -race -count=1 ./internal/server/ \
  -run 'ArgoCDInternal|ArgoCDProxy|Route'
go test -race -count=1 ./internal/worker/tasks/ \
  -run 'ArgoCDAutoRegister|ArgoCDManaged'
make verify
~~~

STOP if a real Argo cluster Secret does not send its configured bearer token.
Do not restore the tokenless route. Capture the Secret schema without secret
values and design mTLS or a dedicated authenticated proxy sidecar for review.

### Task 1.2 — ARGO-03: make self-management reference-only and scrub legacy state

Primary files:

- internal/server/self_manage_argocd.go and focused tests
- internal/handler/argocd.go and the Argo client response helpers
- internal/handler/argocd_ui_proxy.go and proxy tests
- deploy/chart/Chart.yaml, values, schema, helpers, and credential consumers
- deploy/chartrepo.go and packaged-archive tests
- deploy/chart/templates/serviceaccount.yaml or a narrower local Argo Role
- upgrade, credential-rotation, backup/restore, and incident runbooks

Implementation sequence and reasoning:

1. Inventory every secret-bearing value currently generated or preserved by
   self-management. Cover core JWT/Fernet keys, bootstrap password, bundled
   and external Postgres, Redis authentication, Dex static clients, OAuth
   values, arbitrary server/worker environment maps, tracing headers, and Helm
   parameters. Use unique synthetic canaries in tests; never use a live value.
2. Add explicit chart reference contracts for the core credential Secret,
   bootstrap Secret/key, bundled Postgres password, database DSN, full Redis
   URL or address plus password reference, and Dex client Secret. Every pod,
   Job, hook, backup, restore, and drill consumer must use the same contract.
3. Give server-owned migration Secrets stable names, purpose labels, retention
   and Argo no-prune metadata, no workload ownerReference, and one documented
   rotation owner. A bundled database's password and derived DSN must rotate as
   one operational transaction; reference names remain stable.
   Derive the retention/evidence inventory from the final audited values, not
   from a hand-maintained subset. The closed collector must recognize every
   chart Secret contract, including `*SecretRef.name`, existing/explicit Secret
   names, image pull Secrets, bundled Postgres, backup/restore credentials and
   wrapping bundles, logging tokens, TLS/additional CA, core/bootstrap,
   external database/Redis, and Dex. Malformed or ambiguous secret-shaped
   structures fail closed. Require each nonempty reference to exist, protect
   applicable retained objects, then capture only post-protection UID and
   resourceVersion evidence.
   The vendored Argo values vocabulary requires its own complete, pinned path
   taxonomy because several upstream `*Secret` objects contain inline data
   rather than references. Classify every admitted secret-shaped Argo path as
   forbidden inline material, a conditional/implicit external Secret
   reference, or justified safe metadata. Reject inline admin, webhook,
   certificate key/cert, and connector credentials; collect external
   notification and other create-disabled/existing-Secret names. Any nonempty
   secret-shaped path absent from the audited taxonomy fails closed, and a
   vendored-chart update must fail completeness tests until re-audited.
   Resolve conditional references from the pinned effective Argo defaults plus
   operator overrides, including names that templates hard-code rather than
   expose in supplied values. Collect default external Redis, notification,
   and core Argo Secrets only when the effective create/topology flags make
   them external; do not require explicit repetition of a valid default name,
   and do not collect a Secret that the selected chart path will create.
4. Before changing the Application, metadata-only patch pre-existing core,
   bootstrap, and migration Secrets with `Prune=false,Delete=false` and an
   explicit comparison policy. The migration must never replace Secret data
   merely to add ownership metadata.
5. Require a complete takeover values source: decode the original Helm release
   Secret in memory or consume an explicit operator-owned Secret. Preserve a
   schema-validated allowlist of non-sensitive operational configuration,
   including Argo settings, pull secrets, air-gap images, resources,
   scheduling, storage, disruption/network policy, backup, and observability.
   Rewrite known credential paths to stable references. Never recursively
   carry an unknown top-level map, `env`, header, annotation, parameter, value
   file, or value object into the Application; ambiguous nonempty content
   blocks takeover. Replica-only live reconstruction is not complete enough.
   After takeover, a strictly validated reference-only Application becomes the
   mutable operator source; overlay live image tuples, pull-secret names,
   replica counts, agent image config, and stable credential refs so upgrades
   remain a fixed point instead of reverting to the frozen Helm release.
   Treat the chart's global image registry as part of that exactness contract:
   because it overrides per-component registries, rebase nested repositories
   relative to the global prefix and fail closed when a live component cannot
   be represented without changing its pull reference. Never "fix" this by
   clearing the global mirror unless every first- and third-party image value
   has first been made explicit and proven equivalent.
   After takeover, ignore same-chart-revision live drift, but support the
   bootstrap problem for a real platform upgrade: when a platform-owned safe
   Application targets an older valid chart revision than the fully rolled-out
   server's embedded revision, adopt the exact live first-party image tuples,
   pull-Secret names, replicas, and configured agent image into the newly
   staged revision. The next cycle must treat that staged Application as
   canonical again. Never use arbitrary target mismatches as authorization to
   adopt drift.
   “Ignore same-revision live drift” is a strict fixed-point rule: validate and
   retain the canonical reference-only Application values, but do not
   rediscover and merge live server URL, credential references, Dex fields,
   images, or replicas into them. A same-revision live mutation must never
   produce a new desired hash or Application restage.
   Optional workload topology must come from the highest deployed Helm
   release's effective values, never from momentary workload existence. Apply
   this rule to frontend, bundled Postgres, bundled Redis, and Dex. Stage an
   explicit disablement only when the deployed release records or defaults to
   disabled and the workload is absent. A missing workload with enabled
   intent, a still-present or terminating workload with disabled intent, or
   any non-NotFound API error fails closed until rollout convergence. When
   intent and existence agree, adopt the exact live image/replica or stable
   reference configuration required by that component. This prevents a
   transient deletion from becoming a durable topology change and prevents a
   terminating workload from silently reverting an intentional disablement.
   Preserve Redis URL semantics exactly as well: only decompose an audited
   `redis`/`rediss` URL with no unsupported query, fragment, or user-info
   component; otherwise move the complete URL to a Secret reference or stop.
6. Quiesce the Application operation before migration. Replace the spec with
   reference-only values, remove any top-level operation source, and clear
   unsafe status comparison, operation-result, and revision-history sources.
   The bundled Argo 9.5.21 CRD currently declares no status subresource, so an
   intentional full-object update can clear these fields atomically; preserve
   approved metadata and finalizers explicitly. Detect a future CRD status
   subresource and use narrowly scoped status-update privilege or fail closed.
   Set a zero or justified minimal history limit. The upgrade runbook must stop
   the application-controller while this transaction and rotation run. Detect
   an unsafe legacy Application and verify both CRD semantics and controller
   quiescence before creating, rotating, or metadata-patching any credential
   Secret; repeat the guard at the final Application write boundary to close
   the time-of-check/time-of-use window.
   Classify sources with the same closed path/type vocabulary used by takeover,
   not only a blacklist of sensitive key names; otherwise a plugin/env canary
   can be selected as the "safe" source and fail before the scrub is reached.
   Treat local-manifest operations, free-form operation info, hybrid
   Helm-plus-plugin sources, and every corresponding status copy as unsafe.
   A desired-spec restage must clear any queued operation. While awaiting
   approval, permit at most the exact staged revision with prune disabled and
   no force/replace/local-manifest/source-override option.
7. Bump the embedded chart version. Package the pinned argo-cd dependency and
   all required transitive chart archives under the embedded chart instead of
   relying on runtime internet access. Assert that `targetRevision`, repository
   index, archive name, archive content, and an offline Helm render of the
   actual TGZ all resolve to the reference-capable chart. Source-directory
   rendering alone is insufficient because it misses stale/missing archives.
   Enforce the documented upstream archive SHA-256 in a test; filenames,
   versions, successful rendering, and `Chart.lock` alone do not authenticate
   the vendored TGZ bytes.
8. Centralize Argo Application response redaction. Apply it to live list,
   create, patch, history/operation responses, and JSON responses passing
   through `/argocd/`; retain health and diagnostic fields. Reject new
   secret-shaped inline Helm sources on typed create/patch and on the mutating
   reverse-proxy paths used by the Argo UI.
9. Deploy the fixed server while the live controller remains stopped. Persist
   the scrubbed/create takeover Application with automated sync disabled, an
   `awaiting-approval` phase, and a hash of the safe desired spec. Prove
   structurally—without printing values—that spec, operation, compared source,
   operation-result source, and every history entry contain references only.
   Restore the controller only to compute a safe diff. Automated prune may be
   armed only after an operator records approval for the exact current hash;
   stale or mismatched approval is rejected.
   Where Argo permits it, perform and record a manual synchronization with
   pruning disabled, then verify health and stable protected-Secret identity
   before recording approval. If this cannot be made an enforced state-machine
   transition, the runbook must say unambiguously that approval immediately
   arms automated prune and that no-prune metadata is the first-sync Secret
   protection boundary.
   Snapshot only each protected Secret's name, UID, and resourceVersion before
   that bounded sync and compare them immediately afterward. An unchanged
   resourceVersion proves both metadata and data were untouched without
   emitting a value or a brute-forceable data hash; pause external rotation for
   the duration and print only pass/fail.
   The migration must also prove the management-server rollout is complete
   before first creation, every hash-changing restage, scrubbing, and hash
   promotion: every desired replica is updated/ready/available and no old or
   terminating server Pod remains. This prevents an old reconciler from
   rewriting the legacy Application after the new reconciler has scrubbed it
   or alternating a different safe desired hash.
   For initial takeover and bounded live-upgrade adoption, carry all evidence
   used to construct the staged values to that final write boundary. Reverify
   that the controller is still quiesced; the same highest Helm release Secret
   remains selected with the same UID/resourceVersion; the server and worker
   Deployments have the same UID/resourceVersion; every referenced Secret has
   the same UID/resourceVersion observed after retention protection; every
   ConfigMap whose legacy fallback content informed the result is unchanged;
   and every optional workload still has the same absent state or
   UID/resourceVersion observed during construction. Never place Secret data
   in this evidence. Any controller restart, newer/replaced release, referenced
   object mutation, or workload create/delete/replace/update aborts before the
   Application write. Rebuilding without an atomic compare-before-write guard
   merely moves the race and is insufficient.
10. Rotate every affected credential or rebuild the disposable acceptance
    cluster. Treat controller logs, etcd revisions, Kubernetes audit history,
    backups, and previously captured support material as compromised until
    their retention/rotation actions are complete.
11. Restore the controller and require two complete reconcile intervals with
    stable resource versions, healthy/synced state, no secret canary in bounded
    logs or API responses, and no credential Secret pruned or rewritten.

Required tests and validation:

- Unit table tests for safe references and forbidden secret-shaped keys at
  every nesting level, including `env`, headers, annotations, parameters,
  `valuesObject`, `valueFiles`, top-level operation, and all status copies.
- Fake/dynamic-client migration tests that seed a synthetic canary in every
  legacy sink and assert the full replacement removes it with the bundled CRD;
  add a future-status-subresource branch test.
- RBAC tests showing only the intended service account can update the required
  Application resource and cannot read unrelated Secrets; grant status-update
  privilege only if the detected CRD requires it.
- Helm lint, schema, production, bundled/external database, Redis, Dex,
  bootstrap, upgrade, and backup/restore render cases.
- Packaged-TGZ render test that asserts Secret references reach all consumers,
  credential strings are absent from ConfigMaps and Application values, and
  legacy credential Secrets retain no-prune protection.
- Takeover-diff tests for production, air-gap/imagePullSecrets, custom
  resources/scheduling, persistent storage, backup/observability, and bundled
  Argo values; no case may reset an immutable StatefulSet field silently.
- Build-level image tests for a nonempty global mirror with nested component
  repositories, incompatible distinct registries, configured agent images,
  and two exact reconciliation cycles. Silent image-reference rewriting is a
  hard failure.
- Initial-takeover and bounded-upgrade topology tests for frontend, bundled
  Postgres, bundled Redis, and Dex: enabled/present exact adoption,
  disabled/absent explicit disablement, enabled/absent and disabled/present
  refusal, and forbidden/transient workload reads. Tests must exercise chart
  defaults as well as explicit enablement values.
- Initial-takeover and bounded-upgrade write-boundary race tests restart the
  controller, replace or supersede the selected Helm release Secret, mutate or
  replace server/worker Deployments, mutate referenced Secrets or legacy
  fallback ConfigMaps, and create/delete/replace/update each snapshotted
  optional workload after values construction; every drift case must perform
  zero Application create/restage writes, while an unchanged snapshot succeeds.
- Secret-reference collector table/invariant tests cover every supported chart
  path and reject malformed or previously unknown secret-shaped structures.
  Initial and bounded post-build mutation tests must cover core/bootstrap,
  bundled Postgres, external database/Redis, Dex, backup/restore, logging,
  TLS/CA, and image pull Secret categories with zero Application writes.
- Pinned vendored-Argo taxonomy tests reject representative inline
  admin/webhook/certificate credentials in initial, bounded, and same-revision
  sources; exercise every external/create-disabled Secret-reference category
  through mutate/delete/replace zero-write tests; and fail if a chart update
  adds an unclassified secret-shaped path.
- Conditional Argo-reference tests omit default names and exercise standard
  Redis with Secret initialization disabled, Redis HA with default/overridden
  existing Secret, notifications with creation disabled, and fixed core Argo
  Secret semantics. Initial and bounded mutate/delete/replace cases must bind
  the effective external name; chart-created branches must be excluded.
- Same-revision fixed-point tests make live server URL, Dex, credential refs,
  images, replicas, and referenced-object mutations disagree with the active
  Application, then prove the validated canonical values/hash remain unchanged
  and no Application restage occurs.
- Reference-only backup/restore renders using nondefault core Secret key names;
  the wrapped key bundle must still expose the canonical filenames consumed by
  the restore drill, without copying key bytes into values or ConfigMaps.
- Controller-on refusal tests at the outer reconcile boundary that assert zero
  writes to credential Secrets, protected legacy Secret metadata, and the
  self-managed Application.
- Redis migration tests for passwordless URLs, user-info URLs, query-bearing
  URLs, fragments, TLS, unsupported schemes, and database indexes; output must
  either remain byte-semantics-equivalent or use the full-URL Secret contract.
- Redis password-placeholder cases must accept only the exact empty-username
  form the chart can reconstruct. ACL usernames, username-only userinfo, or an
  unresolved placeholder fail closed rather than silently losing identity.
- Backup key-bundle tests must cover both the default runtime core Secret with
  configurable key names and an independent `encryptionKeyBackup.secretName`
  that already exposes canonical restore filenames. Runtime key-name overrides
  must never be assumed to apply to that independent BYO Secret.
- Hostile existing Secret metadata containing case/whitespace variants of
  `Prune=true` and `Delete=true`; the migration must remove all conflicts and
  emit exactly one canonical false option while retaining unrelated options.
- Closed-source regressions for Argo env canaries, hybrid Helm/plugin sources,
  local `operation.sync.manifests`, free-form operation info, status copies,
  queued operations on restage, and force/replace/prune attempts while awaiting
  approval.
- Partial management-server rollout tests, including an old terminating Pod;
  migration must perform no Secret or Application write until all reconcilers
  run the new build.
- Handler and reverse-proxy tests for both request rejection and response
  redaction, including compressed and uncompressed JSON where supported.
- Inventory proxy streaming/non-JSON endpoints by path. Application watches,
  SSE/NDJSON operations/events, streamed manifests, and controller/application
  logs must be safely sanitized with bounded framing or denied fail closed;
  generic pass-through is permitted only for a proven-safe endpoint class.
  Unique canaries must prove no JSON media-type switch or stream framing bypass.
- API response headers are default-deny; unknown token headers and credentialed
  Location/Link values cannot bypass body sanitation. Malformed or oversized
  encoded JSON wrappers fail closed, embedded signed URLs lose query/fragment,
  and free-text reasons are sanitized before durable operation persistence as
  well as audit publication.
- Compatibility regressions preserve credential-free SCP/SSH Git URLs, safe
  ApplicationSet generator scalars and approved Argo annotations, project
  wildcard destinations, ordinary bracket-leading descriptions, and bounded
  multi-source `$values`/`ref` workflows while continuing to reject inline
  Helm values, plugin env, traversal, credential URLs, and secret-shaped data.
- A controller-off live migration and credential-rotation drill followed by
  a controller-on/manual diff approval, authenticated Argo discovery/sync, and
  two fixed-point reconciliations.

Definition of done:

- [ ] No credential value or credential-bearing URL exists in any self-managed
      Application spec, operation, status, history, API/UI response, log, or
      retained evidence artifact.
- [ ] All chart consumers use a Secret reference and production validation
      accepts reference-only configuration while rejecting missing keys.
- [ ] First sync cannot prune core, bootstrap, database, Redis, or Dex Secrets.
- [ ] Rotation, backup, restore, uninstall, rollback, and disaster recovery
      name the owner and exact lifecycle of every stable credential Secret.
- [ ] The embedded chart revision is new and the packaged archive passes the
      full reference-only render contract.
- [ ] Live credentials have been rotated/rebuilt and two reconciliations are
      healthy, stable, and canary-free before the controller is left running.

STOP if the controller cannot be quiesced, all status/history copies cannot be
scrubbed atomically or with bounded privilege, an old Secret would be pruned, or any
validation step would require printing a live value. Keep the controller
stopped and report the blocker instead of accepting a partial spec-only fix.

### Task 1.3 — DEX-01: make Dex configuration Secret-backed end to end

Primary files:

- internal/handler/dex_config.go and its handler/render/apply tests
- internal/server/dex_bootstrap.go and bootstrap tests
- Dex settings/connectors queries, generated sqlc, and a forward-only migration
- deploy/chart/templates/dex-configmap.yaml, dex-deployment.yaml, and values
- OpenAPI/types, auth runbook, backup/restore, and upgrade documentation

Required design:

1. Replace the runtime `ConfigMap/config.yaml` write with a stable Kubernetes
   Secret mounted read-only by Dex. Do not rely on generic connector environment
   expansion unless the pinned Dex version proves arbitrary quotes, slashes,
   dollar signs, Unicode, and newlines round-trip safely.
2. Keep non-sensitive Dex configuration separate where useful, but never put a
   connector password, bind password, client secret, static-client secret, or
   token in a ConfigMap, Deployment value, annotation, or rollout marker.
   Do not substitute a content-derived digest for the value: low-entropy
   credentials can be brute-forced from a hash. Compare existing Secret bytes
   only in memory for fixed-point detection, and use the Kubernetes-returned
   Secret resourceVersion or another non-content-derived generation token to
   trigger a changed rollout without logging or persisting secret fragments.
3. Encrypt static-client secrets in the database using the same governed
   encryptor lifecycle as connector secret fields, or store only a stable
   Kubernetes Secret reference. `public_clients` must not retain plaintext.
4. Redact static and connector credentials from settings, create, update,
   apply, audit, error, support-bundle, and OpenAPI response shapes. Preserve a
   boolean “configured” marker so the UI can edit without receiving a value.
5. Add a forward migration for existing plaintext `public_clients`. Migration
   code may decrypt/re-encrypt in memory but must not log, audit, or include the
   value in an error. Define behavior when the encryption key is unavailable;
   fail closed rather than copying plaintext forward.
   Use an expand/compatibility/backfill/cutover/contract sequence. The expand
   release must remain correct while old and new replicas coexist; it cannot
   scrub a column an old replica still reads or permit old writes to diverge
   from the encrypted envelope. Backfill is durable, resumable, observable,
   and gated on old-replica quiescence before contraction. A down migration
   must never drop the only recoverable credential copy; use a forward-fix stub
   when safe automatic reversal is impossible.
6. Migrate the live Dex ConfigMap to the runtime Secret before changing the
   Deployment volume source. The transition must be rollout-safe, idempotent,
   and reversible for one supported release without recreating plaintext.
7. Give the runtime Secret one owner, purpose labels, no-prune/retention rules,
   backup and restore coverage, and a documented rotation workflow. The chart
   and dynamic Apply handler must not fight over the volume source or Secret
   metadata.
   Do not grant an agent namespace-wide Secret `create` merely because native
   RBAC cannot resourceName-scope that verb. Prefer a chart/Argo-created,
   metadata-only stable Secret with no owned `data`/`stringData` fields; the
   dynamic handler may then get/update the exact resourceName and must fail
   closed if provisioning has not occurred. Prove Helm/Argo reapplication
   preserves dynamically owned data and that neither field manager can erase
   or reclaim the other's fields.
8. Rename or compatibly alias `configmap_name` in database/API contracts so the
   field does not falsely imply that credentials belong in a ConfigMap.
   Return the deprecated non-sensitive alias for one compatibility release,
   reconcile migrated/default runtime names with the actual release without
   overwriting operator intent, and validate Kubernetes DNS/name limits across
   database, API, bootstrap, Deployment, and Secret contracts.
9. Close every connector, static-client, and `extra` schema. Unknown or nested
   secret-shaped fields must be rejected rather than stored outside the known
   encryption/redaction registry; connector type is immutable unless a full
   validated transition explicitly replaces all configuration.
10. Key rotation must include a CAS-safe bulk rotation of connector JSONB and
    encrypted static clients. Any row that cannot be re-encrypted blocks old-key
    removal; documentation and tests must use the real API/job path and prove
    operation after the fallback key is removed.
11. Registering the platform as a Dex client is an end-to-end transaction, not
    merely a database transaction. Require a configured cluster, settings, and
    enabled connector; stage the paired ciphertexts, update the runtime Secret,
    verify the Secret-backed Dex rollout, then enable server SSO. Compensation
    and retries must never leave the server using a secret the running Dex has
    not loaded. Product install flows must provision the same retained Secret,
    exact writer RBAC, and derived runtime names before advertising success.
12. Direct Helm upgrades need an explicit two-release cutover or a post-upgrade
    cleanup that waits for the Secret-mounted Dex rollout. Argo sync-wave
    annotations do not order Helm's normal resource updates, so the legacy
    ConfigMap cannot be scrubbed in the volume-switch release. Preflight must
    validate Secret identity, DNS-compatible names, bounded decoded YAML, and
    mandatory Dex semantics without logging the document.

Tests and validation:

- Unit matrices for every connector secret field and static-client secret,
  including hostile special characters and synthetic canaries.
- Closed-schema canaries cover alternate casing/spelling, unknown/nested
  password/token/private-key fields, `extra`, malformed stored records, and
  connector type-change attempts across DB/API/audit/log/error surfaces.
- Database migration tests proving old plaintext is replaced by ciphertext or
  a reference and cannot be recovered through list/get/settings APIs.
- Mixed-version tests run old and new readers/writers across expand and
  backfill, dormant-row resume, rollout quiescence, cutover, forward-fix down,
  and rollback without credential loss or divergent client sets.
- Render/apply tests asserting the ConfigMap is secret-free, the Secret holds
  the runtime document, and the Deployment mounts only the intended source.
- RBAC tests showing Dex Apply can write the exact runtime Secret but ordinary
  workload/config readers cannot retrieve its data through Astronomer APIs.
- Field-ownership tests prove metadata-only chart/Argo reapplication preserves
  runtime Secret data, while the dynamic writer cannot mutate unrelated
  Secrets and receives no namespace-wide Secret-create authority.
- Rollout, repeat Apply, rotation, rollback, backup, and restore tests.
- Registration tests prove DB/runtime-Secret/Deployment/server-OIDC agreement,
  including apply or rollout failure compensation and retry. Direct Helm
  upgrade tests inject pod restarts between each phase and prove the legacy
  source remains usable until Secret-mounted replicas are ready.
- Metadata tests assert that Secret/config content hashes, credential fragments,
  and other content-derived rollout markers never appear in annotations,
  status, logs, audits, or responses.
- Live OIDC and LDAP authentication after migration, followed by canary scans
  of bounded API responses, ConfigMaps, logs, audits, and retained evidence.

Definition of done:

- [ ] No Dex credential exists in a ConfigMap or plaintext database JSONB.
- [ ] No Dex API, audit, log, support bundle, or UI response returns a secret.
- [ ] Runtime Secret ownership, prune, rotation, backup, restore, and rollback
      behavior is documented and tested.
- [ ] Existing OIDC/LDAP/static-client configurations migrate without forced
      credential re-entry unless decryption is impossible.
- [ ] Repeated Apply is a fixed point and does not churn the Deployment or
      Secret when configuration is unchanged.

STOP if the migration would need to expose a live ConfigMap/DB value in test
output, if the pinned Dex parser cannot safely consume the proposed reference
mechanism, or if chart reconciliation would restore the plaintext ConfigMap.

### Task 1.4 — SET-01: unify session timeout semantics

Recommended product decision: preserve the effective shipped default of 60
minutes because it is least disruptive and more conservative. Change the
platform registry and UI to 60 unless the product owner explicitly approves
480 with a security review.

Files:

- internal/config/config.go
- internal/handler/platform_settings.go
- internal/server/server.go
- internal/auth/jwt.go
- auth and platform-setting tests
- frontend platform settings page
- docs describing session behavior

Implementation:

1. Define one shared default constant in a package that does not introduce an
   import cycle.
2. Use it for boot configuration, settings registry fallback, UI display, and
   documentation.
3. Move sessionTimeoutMins, SetSessionTimeoutPolicy, and
   SetAccessTokenTTLProvider outside the encryptor/TOTP conditional.
4. On a missing setting, return the shared default rather than zero.
5. On malformed/out-of-range stored data, log an actionable error and use the
   safe default; do not mint an unbounded token.
6. Preserve absolute access-token TTL language. Do not market this as idle or
   sliding expiration.
7. Verify password, refresh, SSO, and TOTP completion mint paths all use the
   provider.

Tests:

- No encryptor and no DB row: 60-minute access token.
- Encryptor and no DB row: same result.
- Explicit 120-minute row: every mint path uses 120.
- Malformed, below-minimum, and above-maximum rows use safe behavior.
- Settings GET displays the effective default.

### Task 1.5 — AGENT-01: make implicit privilege least-privilege everywhere

Files:

- internal/agent/config.go
- internal/agent/health.go
- internal/handler/agent_fleet.go
- deploy/agent/template.go
- cmd/agent/main.go
- docs/agent-privilege-profiles.md
- chart and agent tests

Implementation:

1. Use viewer for every empty or unknown profile.
2. Keep admin available only through explicit configuration/annotation.
3. Ensure secret watching remains false for viewer and requires an explicitly
   compatible profile.
4. Make heartbeat and self-test report the normalized effective profile.
5. Emit a startup warning when an old deployment omits the profile and would
   previously have received implicit admin.
6. Add an upgrade note telling operators who intentionally relied on implicit
   admin to set admin explicitly before upgrading.
7. Do not silently promote viewer because a requested feature is unavailable;
   report denied capabilities instead.

Tests:

- Empty, whitespace, unknown, viewer, operator, namespace profiles, custom, and
  explicit admin.
- Heartbeat effective profile equals RBAC template effective profile.
- Secret informer is disabled for implicit viewer.
- Self-test calls admin an explicit full-management profile, not the default.

### Phase 1 definition of done

- [ ] Production-default Argo listener rejects missing/wrong-cluster tokens.
- [ ] Real Argo sync succeeds with the generated bearer token.
- [ ] NetworkPolicy remains a second boundary.
- [ ] Self-management is reference-only, operator-approved before first prune,
      and fixed-point across upgrades without clearing Argo status.
- [ ] Legacy Application spec/operation/status/history copies are scrubbed and
      affected credentials are rotated before the controller remains active.
- [ ] Dex runtime/static-client credentials exist only in governed Secrets or
      encrypted storage and every API/UI representation is redacted.
- [ ] Session setting has one documented default and works without TOTP.
- [ ] Every agent implicit-profile path resolves to viewer.
- [ ] Threat model and OpenAPI match the listener behavior.
- [ ] verify-enterprise passes.

## 11. Phase 2 — Make multi-replica security and events correct

**Objective**: Adding replicas does not create stale authorization windows or
couple user request latency to Redis Pub/Sub.

**Estimated effort**: 1–2 engineering weeks.

### Task 2.1 — AUTH-01: distributed cache invalidation with safe degradation

Recommended architecture:

- Create internal/cacheinvalidate or an equivalently focused package.
- Use a dedicated Redis channel and versioned wire schema, separate from
  user-visible events.
- Maintain monotonic Redis epochs for JWT and RBAC invalidation.
- Each replica subscribes and also periodically reconciles the current epoch.
- A subscriber disconnect or epoch-read failure marks distributed cache state
  unhealthy. While unhealthy, positive JWT and RBAC cache hits are bypassed
  and the authoritative database is queried.
- On reconnect, read epochs, invalidate stale local state, then mark healthy.

Suggested wire shape:

~~~json
{
  "version": 1,
  "kind": "jwt_jti|jwt_user|rbac_user|rbac_all",
  "subject_id": "non-secret identifier",
  "epoch": 42,
  "origin": "pod identity",
  "time": "RFC3339 timestamp"
}
~~~

Never place JWTs, API tokens, cookies, role rules, or secret values in Redis
messages or logs.

Files likely involved:

- new internal/cacheinvalidate package and tests
- internal/auth/jwt.go
- internal/server/middleware/rbac_cache.go
- internal/server/middleware/rbac_queries.go
- internal/handler/auth.go
- internal/handler/users.go
- internal/handler/password_reset.go
- internal/handler/rbac.go
- internal/handler/projects.go
- internal/handler/sso.go
- internal/handler/group_mappings.go
- internal/handler/resources.go
- internal/server/server.go
- metrics and readiness wiring

Steps:

1. Inventory every successful mutation that can revoke a session or change
   effective permissions.
2. Introduce a narrow invalidator interface with targeted user/JTI and global
   methods.
3. Implement local invalidation first, Redis epoch increment second, message
   publication third. Return an error/metric if cross-replica publication
   fails, but do not roll back the already-committed security mutation.
4. Make all replicas notice Redis unavailability and disable positive caches.
5. On healthy messages:
   - targeted JWT JTI invalidates that entry if supported;
   - user cutoff can clear the JWT cache conservatively or use a user index;
   - RBAC user invalidates one entry;
   - role definition or namespace ownership changes invalidate all.
6. Add metrics:
   - cache_invalidation_publish_total by kind/result;
   - cache_invalidation_receive_total;
   - cache_invalidation_epoch;
   - cache_invalidation_lag_seconds;
   - security_cache_bypass_total by cache/reason;
   - subscriber health gauge.
7. Add readiness degradation for multi-replica production when the coordinator
   cannot establish initial epoch state. Single-replica development may use
   local-only behavior with an explicit warning.

Tests:

- Two JWT managers: prime both, revoke through A, B rejects without waiting TTL.
- Two RBAC caches: prime both, remove binding through A, B misses immediately.
- Role mutation invalidates all replicas.
- Duplicate/out-of-order messages are idempotent.
- Missed Pub/Sub message is detected by epoch reconciliation.
- Redis disconnect makes caches bypass rather than serve positive stale hits.
- Reconnect invalidates before re-enabling.
- No secret material in serialized messages or logs.
- Race tests for concurrent validation and invalidation.

Acceptance SLO:

- Healthy Redis: revocation/permission reduction observed by every replica
  within one second at p99 in the integration environment.
- Redis unavailable: no positive cached authorization is trusted after the
  local coordinator detects disconnect; database checks become authoritative.

### Task 2.2 — EVENT-01: bounded asynchronous Redis fan-out

Files:

- internal/events/bus.go
- internal/events/bus_remote_test.go
- internal/server/server.go
- internal/metrics/publisher.go
- observability metrics/tests

Required design:

1. Publish performs local broadcast and a nonblocking enqueue only.
2. A bounded relay worker owns JSON serialization and Redis Publish calls.
3. Queue capacity is configurable with a safe default and hard maximum.
4. Queue-full behavior is drop plus metric, matching the bus's documented
   lossy semantics. It must never spawn one goroutine per event.
5. Worker retries are bounded; do not reorder indefinitely or retain unbounded
   memory during outage.
6. Context cancellation drains only for a bounded shutdown interval.
7. Preserve origin echo suppression and Remote semantics so SIEM/webhook taps
   do not double-deliver.
8. Expose:
   - queue depth/capacity;
   - enqueued/published/dropped totals;
   - publish latency;
   - relay health and last success.
9. Rate-limit debug logging during Redis outage.

Tests:

- A fake Redis Publish that blocks for two seconds does not make Bus.Publish
  exceed a small local latency budget.
- Full queue drops and increments the correct metric.
- Relay eventually publishes when Redis recovers.
- Shutdown does not leak workers.
- Remote events reach SSE subscribers once.
- Webhook and SIEM taps skip remote duplicates.
- Race suite is clean under many concurrent publishers/subscribers.

Suggested performance gate:

~~~bash
go test -run TestBusPublishDoesNotBlockOnRedis -count=20 ./internal/events/
go test -race -count=1 ./internal/events/ ./internal/metrics/
~~~

Expected: Publish never waits for Redis; no races or goroutine leak.

### Phase 2 definition of done

- [ ] Multi-replica logout and permission removal meet the one-second p99 target.
- [ ] Redis loss disables positive security caches.
- [ ] Event Publish is independent of Redis latency.
- [ ] Queue overload is bounded and observable.
- [ ] Two-replica SSE still works without double SIEM/webhook delivery.
- [ ] Race and enterprise gates pass.

## 12. Phase 3 — Harden agent credential ownership and the Argo trust zone

**Objective**: Reapply and upgrades cannot regress agent identity, and bundled
Argo receives least-privilege network access in production.

**Estimated effort**: 1–2 engineering weeks.

### Task 3.1 — AGENT-02: separate registration bootstrap from durable identity

Do not treat adding --server-side alone as the final fix. It removes the
last-applied annotation exposure but does not create a clean ownership model.

Target model:

- Bootstrap Secret: astronomer-agent-registration-token, created by the
  install manifest and owned by the installer field manager.
- Durable Secret: astronomer-agent-token, created and updated only by the
  agent after successful adoption.
- On startup, the agent prefers a valid durable token. It falls back to the
  bootstrap token only when durable material does not exist.
- Manifest reapply never writes the durable Secret.
- An expired re-created bootstrap token does not displace a valid durable
  token.

#### AGENT-02 implementation amendment — evidence from real Kubernetes RBAC

Live implementation testing on Kubernetes `v1.30.4+k3s1` proved that a
server-side apply PATCH which creates an absent Secret still requires the
top-level `create` verb. Kubernetes RBAC cannot constrain that verb with
`resourceNames`; granting it would let a compromised default viewer agent
create arbitrary Secrets in `astronomer-system`. Documentation-only admission
guidance does not satisfy this plan's least-privilege definition of done.

The accepted implementation design therefore preserves separate bootstrap and
durable objects while changing who creates the empty durable container:

- `astronomer-agent-registration-token` remains the installer-owned bootstrap
  Secret.
- `astronomer-agent-identity` is rendered as an empty, purpose-labelled Secret;
  the installer owns only its object/container fields and never renders
  `data.token`.
- The agent receives exact-name `get` and `patch`, but no Secret `create`,
  `update`, `list`, `watch`, or `delete`. It owns only the durable token field.
- The old mixed-use `astronomer-agent-token` name is treated as a read-only
  migration source. Once the server accepts that credential, the agent writes
  the validated durable credential to `astronomer-agent-identity`.
- Reapplying a cached pre-split manifest can then modify only the ignored legacy
  Secret name; it cannot overwrite the active durable identity.
- An empty, correctly purpose-labelled identity container may fall through to
  legacy/bootstrap adoption. Malformed or unexpected non-empty durable state
  fails closed.

This amendment must be proven against a real API server, not only YAML/fake
clients: fresh SSA, exact-name authorization and arbitrary-name denial,
bootstrap reapply, client-side-to-server-side upgrade, cached old-manifest
reapply, managed-field stability, annotation cleanup, rotation, and restart are
all required acceptance cases. STOP if any reapply can alter the active token
or if field ownership is nondeterministic.

Files:

- deploy/agent/install.yaml.template
- deploy/agent/template.go and tests
- internal/agent/config.go
- internal/agent/token_persistence.go and tests
- tunnel client startup/reconnect code
- frontend registration command page
- public registration manifest handler/OpenAPI/docs
- agent privilege RBAC rules if Secret create/update access changes

Steps:

1. Characterize current first-connect, durable-token ACK, persistence,
   reconnect, pod restart, rotation, and re-import behavior.
2. Add distinct config fields for bootstrap and durable Secret names/keys.
3. Implement token resolution:
   - durable present and valid shape -> use it;
   - durable absent -> bootstrap;
   - durable read error other than NotFound -> fail closed with diagnostics;
   - never log either token.
4. Persist rotations only into the durable Secret.
5. Ensure the ServiceAccount can create/patch only the named durable Secret,
   not arbitrary Secrets.
6. Change all UI commands to:

~~~text
kubectl apply --server-side --field-manager=astronomer-bootstrap -f -
~~~

7. Add an upgrade migration path for existing installations where the durable
   token already occupies astronomer-agent-token:
   - continue reading it as durable;
   - render the new bootstrap Secret for new manifests;
   - never delete an existing durable Secret.
8. Add diagnostics that state which credential source is active without
   exposing material.
9. Update the install caveat and rotation/decommission runbooks.

Tests:

- Fresh install adopts and writes durable Secret.
- Reapply before and after bootstrap expiry preserves durable token.
- Pod restart uses durable token.
- Durable rotation persists and survives restart.
- Deleted durable Secret falls back only to an unexpired/re-minted bootstrap.
- Wrong-cluster durable token fails.
- RBAC prevents writing arbitrary Secret names.
- Rendered YAML contains no last-applied annotation.
- All UI variants use server-side apply.

STOP if the Kubernetes Secret field-ownership behavior cannot be made
deterministic across supported Kubernetes versions. Keep two physical Secrets;
do not collapse back to two keys in one Secret without a compatibility design.

### Task 3.2 — ARGO-02: explicit production NetworkPolicies for Argo

Required approach:

1. Render and inventory every bundled Argo component and hook for the pinned
   argo-cd 9.5.21 chart.
2. Add a chart setting with explicit modes:
   - unrestricted for local development;
   - restricted for production.
3. values-production.yaml must use restricted mode.
4. In restricted mode, include Argo pods in default deny and add component
   policies for:
   - DNS;
   - Kubernetes API using configured CIDRs;
   - server/controller/repo-server/ApplicationSet/Redis intra-Argo traffic;
   - Astronomer server to Argo server UI/API;
   - Argo server/controller to Astronomer port 8090;
   - repository Git/HTTPS/SSH/OCI destinations through explicit CIDR values;
   - pre-install/pre-upgrade hook jobs.
5. NetworkPolicy cannot express FQDN. Production preflight must reject a
   restricted configuration that requires external repositories but provides
   no egress CIDRs or an explicitly documented CNI FQDN-policy integration.
6. Do not add unrestricted 0.0.0.0/0 egress to make sync pass.
7. Document external Argo behavior separately; Astronomer cannot impose policy
   on a BYO Argo namespace it does not own.

Tests:

- Golden/render tests prove every bundled Argo workload is selected by at
  least one policy in restricted mode.
- Development install/upgrade remains usable.
- Production install and upgrade hooks complete under an existing default
  deny.
- Git HTTPS, Git SSH, OCI, and in-cluster repository cases work with their
  configured egress.
- Unlisted destinations fail.
- Argo sync through the authenticated internal proxy succeeds.

Rollback:

- Chart rollback may set Argo mode to unrestricted only as an emergency,
  time-bounded operational action with an audit note. It is not the production
  steady state.

### Phase 3 definition of done

- [ ] Reapplying an old manifest cannot overwrite durable identity.
- [ ] No primary install command uses client-side apply.
- [ ] Viewer remains the implicit profile after upgrade.
- [ ] Production Argo pods and hooks are covered by explicit policy.
- [ ] Argo registration, sync, upgrade, rollback, and repository access pass.
- [ ] Chart preflight catches unusable restricted configurations.

## 13. Phase 4 — Build valid scale, HA, and failure evidence

**Objective**: Replace aspirational scale claims with reproducible evidence
from authenticated agents and production-like management-plane topology.

**Estimated effort**: 2–4 engineering weeks including environment work.

### Task 4.1 — SCALE-01: provision real load-test clusters and credentials

Files:

- scripts/loadtest/main.go
- new focused files such as api_client.go, provision.go, cleanup.go
- loadtest tests
- scripts/loadtest/README.md
- Makefile
- CI/nightly workflow

Required lifecycle:

1. Load an administrator JWT only for setup and HTTP workload authentication.
2. For each requested synthetic agent:
   - POST /api/v1/clusters with a deterministic load-run prefix;
   - POST /api/v1/clusters/{id}/register;
   - retain that cluster's registration token only in memory;
   - start the agent with the returned cluster ID and token;
   - receive CONNECT_ACK and adopt the returned durable token when the protocol
     supplies one;
   - use the durable token on reconnects.
3. Bound setup concurrency and retry only idempotent operations.
4. Tag every created cluster with run ID, profile, owner, and expiry metadata.
5. Add --reuse-run and --cleanup modes.
6. Cleanup only clusters carrying the exact run ID. Never delete unrelated
   clusters.
7. Do not write agent tokens to the Markdown report, logs, profile files, or
   process arguments.
8. Reports record counts of created, connected, adopted, durable, cleaned, and
   failed agents.
9. Exit before the load phase unless the configured minimum connected/adopted
   fraction is reached.

Tests:

- API setup happy path with fake server.
- Partial create/register failure and cleanup.
- Registration token belongs to cluster.
- CONNECT_ACK durable-token adoption.
- Reconnect uses durable token.
- Resume/reuse does not create duplicates.
- Cleanup refuses an untagged or different-run cluster.
- Logs/report contain no token-shaped material.

### Task 4.2 — SCALE-02: route workload to real connected agents

1. Replace the static zero cluster ID with a scenario function that selects
   from the connected-agent pool.
2. Record the selected cluster ID and correlate the K8S_REQUEST/K8S_RESPONSE
   without high-cardinality Prometheus labels.
3. Count:
   - resource requests attempted;
   - requests that reached a synthetic agent;
   - successful responses;
   - timeout/error classes;
   - latency from HTTP request and tunnel round-trip.
4. A cluster_pods sample counts toward latency only when the intended agent
   received it.
5. Add realistic paginated PodList sizes without allowing the load driver to
   exhaust its own memory.
6. Separate management API, tunnel proxy, and harness-self resource metrics.

Acceptance:

- At least 99.9 percent of successful cluster_pods responses in a stable small
  run are correlated to a connected synthetic agent.
- A deliberate agent disconnect causes the corresponding resource requests to
  fail visibly rather than being counted as healthy.

### Task 4.3 — SCALE-03: automate enterprise failure drills

Create a reproducible test environment with:

- three server replicas;
- three worker replicas;
- external or HA-mode Postgres and Redis suitable for failure tests;
- bundled Argo with Phase 3 restricted policy;
- frontend replicas and ingress/gateway;
- at least one real lightweight adopted Kubernetes cluster in addition to
  synthetic agents.

Automate or script:

1. Kill the server pod owning a sample of agent connections.
2. Rolling-restart all server pods.
3. Kill a worker during active fleet and Argo operations.
4. Interrupt Redis and restore it.
5. Fail over or restart Postgres.
6. Restart Argo application-controller and repo-server.
7. Rotate Argo proxy and agent durable tokens during load.
8. Trigger a reconnect storm according to the profile.
9. Run backup and restore drill.
10. Upgrade then rollback the Helm release.
11. Verify RBAC revocation during Redis loss.
12. Verify no duplicate operation execution after worker/server failure.

Every drill must produce:

- start/end timestamps;
- build and chart versions;
- topology and resource requests/limits;
- action performed;
- expected and observed recovery;
- data loss/duplicate count;
- relevant metric snapshots;
- verdict and artifact link.

Do not mark a profile pass when a declared drill is merely printed into the
report. Planned labels are not evidence.

### Task 4.4 — SCALE-04: establish release profiles and budgets

Use the existing profile ladder:

- small: 5 clusters, nightly smoke;
- medium: 50 clusters, release-candidate gate;
- large: 250 clusters, enterprise evidence;
- extreme-lab: 1,000 simulated clusters, ceiling exploration only.

Initial budgets should use existing documented thresholds until measured data
supports a reviewed change:

| Signal | Initial gate |
|---|---|
| cluster_list p99 | at most 500 ms |
| connected resource proxy p99 | at most 2,000 ms |
| required connected fraction | 100 percent for small/medium; reviewed threshold for failure drills |
| operation duplicate execution | zero |
| authorization revocation p99 | at most 1 second with Redis healthy |
| event publisher wait on Redis | zero synchronous waits |
| unbounded goroutine/memory growth | none; end-state within reviewed ratio |
| DLQ growth | returns to baseline after drill |
| agent reconnect | profile-specific SLO recorded before run |

Process:

1. Run each profile at least three times from the same build and topology.
2. Record median and worst run, not the best run.
3. Include database size, Redis mode, pod resources, Kubernetes version,
   network environment, and Argo version.
4. Keep raw reports as CI artifacts.
5. Append only passing, reproducible results to docs/scale-baseline.md.
6. A failed/blocked run goes into the failed-attempt table.
7. Marketing may claim only the highest passed release profile, with its
   topology and limits.

### Phase 4 definition of done

- [ ] Synthetic agents use real cluster rows and per-cluster credentials.
- [ ] Reconnects use durable tokens.
- [ ] cluster_pods traffic reaches the selected connected agent.
- [ ] Cleanup is safe and run-scoped.
- [ ] Medium profile passes three times with required drills.
- [ ] Large profile has a reviewed pass or remains explicitly unclaimed.
- [ ] Redis/Postgres/server/worker/Argo failure evidence is attached.
- [ ] docs/scale-baseline.md contains no invented or uncorrelated metrics.

## 14. Phase 5 — Close high-value Rancher day-2 UX gaps

**Objective**: Improve blast-radius safety and structured resource management
without recreating Rancher provisioning or every Steve feature.

**Estimated effort**: 2–5 engineering weeks.

### Task 5.1 — FLEET-01: authoritative selector preview

Backend:

1. Extract the selector parser and target resolver used by execution into a
   shared package callable by both handler and worker.
2. Add POST /api/v1/fleet/operations/preview using the same request selector
   shape as create.
3. Resolve labels, expressions, groups, eligibility, decommission status, and
   hard caps exactly as execution does.
4. Return:
   - exact matched_count;
   - evaluated_count;
   - a bounded sample with cluster ID/name;
   - warnings;
   - capped boolean;
   - a stable selector digest;
   - optional exclusion counts by reason.
5. If evaluation exceeds a safety cap, return capped=true and do not describe
   matched_count as exact. The UI must prevent submission until narrowed or an
   authorized explicit override is designed.
6. Execution always re-resolves at start and stores the actual target count;
   preview is time-bound information, not a lock.
7. Rate-limit and authorize preview as fleet_operations:read/list.

Frontend:

1. Remove the pageSize 500 cluster fetch as the match-count authority.
2. Debounce preview requests.
3. Display exact count, sample, exclusions, warnings, and preview timestamp.
4. Require explicit confirmation above a reviewed blast-radius threshold.
5. Use server-provided label suggestions or a separately paginated endpoint;
   never infer completeness from one page.

Contracts/tests:

- 0, 1, 499, 500, 501, 5,000 cluster fixtures.
- Labels, expressions, group-only, mixed group/label, empty selector, and
  decommissioned/ineligible cases.
- Preview count equals target rows created by execution from unchanged input.
- OpenAPI and generated frontend types updated.
- Playwright verifies large blast-radius confirmation.

### Task 5.2 — UX-01: schema-driven structured common-resource forms

This is a bounded framework, not full Steve parity.

Phase A — design and characterization:

1. Extract the current generic explorer page responsibilities:
   route parsing, discovery, list/watch cache, YAML editing, SSA dry-run/apply,
   actions, logs/exec, and special resource rendering.
2. Define a resource descriptor interface keyed by group/version/kind with:
   columns, summary fields, form schema, validation, defaults, capability/RBAC
   requirements, and YAML fallback.
3. Preserve unknown CRDs through generic list/detail/YAML.
4. Reuse existing UI primitives and permission helpers.

Phase B — first-class forms:

- ConfigMap: multiple keys, binary/value handling, metadata.
- Secret: typed data entry with no readback of redacted values.
- Service: type, selector, multiple ports, target ports.
- Deployment: image, replicas, command/args, env, resources, probes, strategy.
- Ingress: host/path/service/TLS.
- Namespace and common metadata/labels/annotations.

Phase C — safety:

1. Every submit performs server-side dry-run first.
2. Show diff and field-manager conflicts before apply.
3. Force conflicts is an explicit elevated action, never the default.
4. Keep YAML view/edit available and round-trip unknown fields.
5. Use live watches without duplicate fetch implementations.
6. Honor namespace-scoped RBAC in visibility and server authorization.

Tests:

- Descriptor lookup and unknown-kind fallback.
- Form-to-object and object-to-form round trips.
- Unknown-field preservation.
- Dry-run error and conflict display.
- Secret redaction.
- Namespace/RBAC denial.
- Watch updates after apply.
- Playwright create/edit/delete for ConfigMap, Service, Deployment, and Secret
  metadata without exposing secret values.

Definition of parity for this plan:

- Common daily resources no longer require YAML for routine fields.
- Any Kubernetes object remains manageable through YAML/SSA.
- The framework can add a new form without modifying the 2,882-line route page.

### Phase 5 definition of done

- [ ] Fleet preview remains exact beyond 500 and matches execution.
- [ ] Blast radius is explicit before creation.
- [ ] Common resource forms use one descriptor framework.
- [ ] Dry-run and conflict handling precede writes.
- [ ] Unknown CRDs and fields retain YAML support.
- [ ] Namespace RBAC tests and E2E pass.

## 15. Phase 6 — Converge tunnels, architecture, and supply chain

**Objective**: Reduce dual-stack and god-module change risk after behavioral
contracts are proven.

**Estimated effort**: multi-sprint.

### Task 6.1 — TUNNEL-01: choose and complete one tunnel spine

Current docs/dual-tunnel-matrix.md correctly says:

- install default is legacy connect;
- connect2 shares auth but lacks locator HA;
- exec/logs, heartbeats/state, desired-state, Helm/internal RPCs, and lifecycle
  operations are not all ported.

Steps:

1. Turn the matrix into executable conformance tests.
2. Define one interface for:
   - connection identity and token rotation;
   - owner location and cross-pod forwarding;
   - unary K8s requests;
   - streaming watch/log/exec;
   - Helm/internal RPC;
   - heartbeat/state frames;
   - desired-state and lifecycle messaging;
   - audit and metrics.
3. Decide after measured evidence whether:
   - remotedialer becomes primary and gains missing capabilities; or
   - legacy hub remains primary and connect2 is removed/de-scoped.
4. Do not maintain two indefinite feature-complete implementations.
5. Add multi-replica owner failover for the selected spine.
6. Dual-run canary clusters and compare success, latency, reconnect, and error
   metrics.
7. Provide a versioned rollout:
   - opt-in canary;
   - default for new agents;
   - default for upgrades;
   - old path disabled;
   - old code removed after support window.
8. Keep rollback available until at least one full release has passed.

Cutover gates:

- Phase 4 medium and large applicable tests pass on selected path.
- No missing install-critical matrix item.
- Agent N-1 compatibility is tested.
- Locator/owner failure recovers within declared SLO.
- Token rotation, logs, exec, watch, Helm, Argo proxy, and decommission pass.

### Task 6.2 — ARCH-01: decompose hotspots without behavior changes

Order by churn and risk, not line count alone.

Backend extraction candidates:

- internal/handler/monitoring.go:
  datasource CRUD, reconciliation operations, alert integrations, rendering.
- internal/handler/argocd.go:
  instance CRUD, application operations, managed clusters, UI proxy.
- internal/crd/controller.go:
  per-kind reconcilers and status writers.
- internal/server/server.go:
  constructors/wiring modules by subsystem.
- internal/server/routes.go:
  route registrars by bounded domain while preserving golden inventory.

Frontend extraction candidates:

- generic resource page:
  list/detail/YAML/action/stream form boundaries.
- frontend/src/lib/api.ts:
  continue feature modules under frontend/src/lib/api/.
- frontend/src/types/index.ts:
  feature types and generated OpenAPI aliases.
- frontend/src/lib/hooks.ts:
  feature hook modules with centralized query keys.

Method:

1. Measure churn and dependency fan-in.
2. Add characterization tests before moving code.
3. Define stable narrow interfaces.
4. Move one domain at a time with no API behavior change.
5. Preserve route inventory, OpenAPI, audit actions, query keys, and exports.
6. Add temporary compatibility re-exports only with deletion criteria.
7. Do not combine extraction with feature work.

Per-extraction done criteria:

- Public behavior and generated inventories unchanged.
- No new circular dependency.
- File has one clear owner/domain.
- Tests pass before and after.
- Compatibility re-export has an issue/removal release.

### Task 6.3 — SUPPLY-01: immutable CI and deployable image digests

GitHub Actions:

1. Resolve every third-party action tag to a reviewed full commit SHA.
2. Keep a comment with the human-readable release tag.
3. Configure Dependabot/Renovate to propose reviewed SHA updates.
4. Use least job permissions and explicit permissions per job.
5. Preserve artifact attestations, SBOMs, signing, and image scanning.

Helm:

1. Add optional digest fields for first-party and third-party image values.
2. Image helper precedence:
   - digest set -> repository@sha256:digest;
   - otherwise repository:tag.
3. Production preflight may require digests under an explicit immutable-images
   policy.
4. Air-gap image extraction must include digest references correctly.
5. Add render tests for global registry plus digest, per-image registry plus
   digest, tag fallback, and invalid simultaneous values.
6. Document upgrade behavior: changing tag has no effect while digest is set.

Verification:

- No unpinned uses entries remain.
- Release images verify signature/attestation.
- Chart renders valid digest references for every component.
- images.txt and air-gap docs match.

### Phase 6 definition of done

- [ ] One tunnel has an approved cutover/deprecation plan and executable matrix.
- [ ] No install-default flip occurred before HA/feature gates.
- [ ] Highest-risk modules are split behind tested stable boundaries.
- [ ] CI actions use full SHA pins.
- [ ] Production images can be pinned by digest.
- [ ] No route/API/query-key drift was introduced.

## 16. Phase 7 — Documentation, release qualification, and GA sign-off

**Objective**: Produce an auditable evidence package and ensure no document or
marketing statement outruns tested behavior.

### Task 7.1 — DOC-01: reconcile contracts

Update:

- docs/threat-model.md
- docs/openapi.yaml and embedded/generated artifacts
- docs/rancher-astronomer-comparison.md
- docs/scale-baseline.md
- docs/dual-tunnel-matrix.md
- docs/agent-privilege-profiles.md
- deploy/chart/README.md
- scripts/loadtest/README.md
- upgrade, secret rotation, DR, Argo, and incident runbooks
- .github/workflows/README.md

Required statements:

- Provisioning and Fleet compatibility remain excluded.
- Argo is authenticated by cluster-scoped token plus network policy.
- Session timeout is absolute, with its actual default.
- Viewer is the implicit agent profile.
- Bootstrap and durable credentials have separate ownership.
- Exact tested scale and topology are named; no universal scale claim.
- Namespace-scoped RBAC default is stated honestly.
- Tunnel default and deprecation state are current.
- Structured forms are described as coverage for named common resources, not
  every CRD.

### Task 7.2 — Enterprise evidence bundle

Create a versioned release evidence directory or CI artifact containing:

- commit, chart, image digests, SBOM, signatures, and provenance;
- verification command results;
- supported Kubernetes/Postgres/Redis/Argo/browser matrix;
- small/medium/large load reports;
- HA/failure drill reports;
- backup/restore and upgrade/rollback results;
- authorization revocation measurements;
- NetworkPolicy render and live-connectivity results;
- known limitations and approved waivers;
- security threat-model review sign-off;
- support/operations runbook review sign-off.

Do not commit credentials, kubeconfigs, tokens, customer data, or raw logs that
contain them. Evidence should use identifiers and redacted excerpts.

### Task 7.3 — GA decision gate

Enterprise GA is approved only when:

1. No P0 or P1 item is open.
2. No required CI job is skipped or allowed to fail.
3. verify-enterprise passes from a clean checkout three consecutive times.
4. Medium profile passes three consecutive runs.
5. Large profile has a pass for any marketed large-fleet claim.
6. Failure drills meet their reviewed recovery and duplicate-execution budgets.
7. Two consecutive release candidates complete a minimum 72-hour soak in the
   production-like topology without an unresolved severity-1/2 defect.
8. Backup restore and Helm rollback both succeed from released artifacts.
9. Security and operations owners sign the evidence bundle.
10. Every waiver has owner, reason, compensating control, expiry, and issue.

### Phase 7 definition of done

- [ ] Documentation and generated contracts match runtime behavior.
- [ ] Evidence bundle is complete and redacted.
- [ ] GA checklist is signed or the release remains a controlled pilot.
- [ ] advisor-plans/README.md statuses are current.

## 17. Program-wide test matrix

| Layer | Required coverage |
|---|---|
| Unit | token scoping, cache invalidation, event queue, settings defaults, profile normalization, token source selection, selector resolver |
| Race | auth caches, invalidation relay, event bus, agent reconnect/token rotation, tunnel owner changes |
| Handler integration | Argo auth, fleet preview, session mint paths, RBAC mutation invalidation |
| Database integration | revocation/cutoff, role changes, fleet targets, operation uniqueness and claims |
| Chart contract | development/production renders, NetworkPolicy coverage, image digests, hooks, environment isolation |
| Frontend unit | stream transport, query keys, blast preview, forms, dry-run/conflict state |
| Playwright | registration commands, fleet confirmation, common resource CRUD, RBAC denial, Argo operation visibility |
| Live cluster | agent adoption/reapply/rotation, Argo sync, logs/exec/watch/SSA, backup/restore |
| Multi-replica | logout and RBAC convergence, event delivery, owner failover, no duplicate operations |
| Dependency failure | Redis, Postgres, Argo controller/repo, worker/server pod loss |
| Performance | authenticated synthetic agents, correlated resource proxy, UI large-list responsiveness |
| Supply chain | action pins, scans, SBOM, signatures, attestations, digest render |

## 18. Observability requirements

No enterprise feature is complete without operational signals.

Add or confirm:

- Argo proxy auth attempts by result and reason, without token material.
- Argo proxy request latency and downstream tunnel result.
- Security invalidation publish/receive/lag/health/bypass metrics.
- Event relay queue depth, drops, latency, and Redis errors.
- Agent credential source and rotation outcome counts, without secrets.
- Fleet preview evaluated/matched/capped latency.
- Load harness connected/adopted/durable/resource-correlated counts.
- Tunnel owner failover and reconnect duration.
- NetworkPolicy-denied connectivity surfaced through actionable diagnostics.

Alerts/runbooks:

- distributed invalidation unhealthy on multi-replica production;
- event relay sustained drops;
- Argo proxy 401 spike;
- agent durable persistence failures;
- fleet selector cap reached;
- load or failure drill regression;
- restore or rollback failure.

## 19. Rollout and rollback strategy

| Change | Rollout | Rollback |
|---|---|---|
| SafeClient test fixes | normal PR | revert tests only; never revert production guard |
| Argo proxy auth | canary Argo instance and one cluster, then bundled | rollback code only if prior token-gated listener remains; never expose tokenless publicly |
| Distributed invalidation | shadow metrics, then cache coordinator active | disable positive caches, not invalidation |
| Async event relay | deploy with generous queue and metrics | disable Redis fan-out while retaining local bus |
| Session default | release note and explicit setting override | operator sets documented explicit TTL |
| Viewer agent default | warn one release, explicit admin opt-in | explicit profile configuration, not implicit code fallback |
| Split agent Secrets | backward-compatible read path for old durable Secret | retain old read compatibility; do not overwrite durable |
| Argo NetworkPolicy | restricted canary namespace, then production | emergency time-bounded unrestricted mode |
| Fleet preview | additive endpoint; UI switches after contract stable | UI can fall back to disabling creation, not truncated count |
| Tunnel cutover | opt-in, new installs, upgrades, deprecation | versioned old-path feature flag during support window |
| Digest images | optional first, production policy later | clear digest to return to tag behavior |

## 20. Risk register

| Risk | Mitigation |
|---|---|
| Guard-disable test helper masks SSRF tests | use test-scoped injection/disable; never handler package TestMain |
| Argo unexpectedly omits bearer header | STOP and design mTLS/sidecar; never bless tokenless route |
| Redis Pub/Sub loses invalidation | monotonic epochs, periodic reconciliation, cache bypass while unhealthy |
| Event queue loses updates | bounded loss is explicit and metered; critical durable work remains DB/outbox, not event bus |
| Viewer default breaks manual agents | upgrade warning and explicit admin configuration |
| Secret split strands upgraded agents | durable-first backward-compatible read path and N-1 tests |
| NetworkPolicy blocks Helm upgrade hooks | render hook labels and test upgrade under an existing deny policy |
| Load cleanup deletes real clusters | exact run-ID labels, prefix checks, dry-run, refusal for untagged resources |
| Selector preview races execution | state preview timestamp; execution re-resolves and stores actual targets |
| Tunnel cutover causes feature regression | executable matrix, canaries, old-path support window |
| God-module split changes APIs | characterization tests and move-only PRs |
| SHA pin maintenance stalls | automated update PRs with review |
| Scope expands into Rancher provisioning/Fleet | reject as out of scope for this program |

## 21. STOP conditions

Stop and report rather than improvising if:

1. HEAD or an in-scope excerpt materially differs from Section 4.
2. A required gate fails twice after a reasonable scoped correction.
3. Fixing a test appears to require weakening SafeClient or skipping an SSRF
   regression.
4. Real Argo traffic does not send the configured bearer token.
5. Multi-replica production can run without Redis and no safe cache-bypass
   design is available.
6. A cache invalidation message would need to contain credential material.
7. Agent upgrade requires deleting or replacing an existing durable Secret.
8. Restricted Argo networking requires blanket internet egress to function.
9. A load profile cannot prove agent authentication and resource correlation.
10. A requested baseline would use invented, best-of-only, or unrepeatable data.
11. Preview and execution cannot share the same selector semantics.
12. The tunnel default would change before locator and feature parity.
13. A module refactor overlaps active behavioral work in the same module.
14. A chart values fix requires an undocumented breaking rename.
15. Work expands into provisioning or Fleet compatibility.
16. Any secret value appears in a plan, test fixture, report, log, or evidence
    artifact.

## 22. Definition of done — enterprise-grade closure

All boxes are mandatory unless an explicitly approved waiver exists:

### Correctness and release integrity

- [ ] make verify-enterprise passes from a clean checkout.
- [ ] go test -race -count=1 ./... passes.
- [ ] Frontend code-health, lint, types, unit, build, and E2E pass.
- [ ] Helm lint/render/contracts pass without unexplained warning.
- [ ] Generated OpenAPI, types, routes, errors, and sqlc artifacts are current.

### Security and tenancy

- [ ] Argo internal proxy is token-authenticated and cluster-scoped.
- [ ] NetworkPolicy is defense in depth, not identity.
- [ ] JWT revocation and RBAC reductions converge across replicas within SLO.
- [ ] Redis loss disables positive security cache trust.
- [ ] Agent implicit privilege is viewer everywhere.
- [ ] Bootstrap tokens are not placed in client-side apply annotations.
- [ ] Manifest reapply cannot overwrite durable agent identity.
- [ ] Production Argo is covered by explicit NetworkPolicies.

### HA and scale

- [ ] Event publication never blocks on Redis.
- [ ] Synthetic agents have real identities and durable credentials.
- [ ] Resource load is correlated through connected tunnels.
- [ ] Three-replica server/worker failure drills pass.
- [ ] Redis, Postgres, Argo, upgrade, rollback, and restore drills pass.
- [ ] Medium baseline passes three times.
- [ ] Any marketed large-fleet claim has a large-profile pass.

### Product capability and maintainability

- [ ] Fleet preview is authoritative above 500 clusters.
- [ ] Common resource forms use a reusable schema/descriptor framework.
- [ ] YAML and SSA remain universal fallbacks.
- [ ] One tunnel has a validated convergence path.
- [ ] High-risk god modules have characterization-backed boundaries.
- [ ] Actions are SHA-pinned and images support digest pinning.

### Evidence and truthfulness

- [ ] Threat model, OpenAPI, runbooks, UI copy, and comparison agree.
- [ ] Scale claims name build, topology, limits, and date.
- [ ] No provisioning or Fleet compatibility is claimed.
- [ ] Evidence bundle is complete, redacted, and signed off.
- [ ] No P0/P1 issue remains open.
- [ ] Waivers are owned, dated, compensated, and expiring.

## 23. Suggested child-plan split after human approval

If execution is delegated to separate agents, create self-contained child plans
in this order:

| Child | Scope | Depends on |
|---|---|---|
| 003 | BASE-01 Go verification repair | none |
| 004 | BASE-02/03 frontend and enterprise gate | 003 |
| 005 | ARGO-01 internal proxy authentication | 004 |
| 005A | ARGO-03 reference-only self-management takeover and Argo redaction | 005 |
| 005B | DEX-01 Secret-backed runtime/static-client migration | 005A |
| 006 | SET-01 and AGENT-01 default consistency | 004 |
| 007 | AUTH-01 distributed security invalidation | 004 |
| 008 | EVENT-01 nonblocking Redis event relay | 004 |
| 009 | AGENT-02 bootstrap/durable credential split | 006 |
| 010 | ARGO-02 restricted Argo NetworkPolicies | 005A, 005B |
| 011 | SCALE-01/02 valid load identities and tunnel correlation | 005, 007, 008, 009 |
| 012 | SCALE-03/04 HA drills and baseline evidence | 010, 011 |
| 013 | FLEET-01 authoritative selector preview | 004 |
| 014 | UX-01 schema-driven explorer forms | 013 |
| 015 | TUNNEL-01 tunnel convergence | 012 |
| 016 | ARCH-01 module decomposition | functional phases for each module complete |
| 017 | SUPPLY-01 immutable workflows and image digests | 004 |
| 018 | DOC-01 evidence bundle and GA gate | all preceding P0/P1 plans |

Every child plan must include its own current-state excerpts, in-scope files,
commands, tests, done criteria, rollback, and STOP conditions. A child executor
must not rely on having read this master plan.

## 24. Maintenance notes

- Keep docs/rancher-astronomer-comparison.md as a tested capability ledger, not
  a marketing wish list.
- Re-run scale and failure qualification after changes to tunnel protocol,
  Postgres/Redis versions, server/worker concurrency, Argo version, chart
  topology, or resource limits.
- Any new security-sensitive cache must join the distributed invalidation
  health model or explicitly avoid positive authorization caching.
- Any new event consumer must declare whether remote events are accepted and
  whether duplicate delivery is safe.
- Any new Argo component or repository protocol must update restricted
  NetworkPolicies and their tests.
- Any new agent credential must have one explicit owner, rotation path,
  decommission cleanup, and redaction contract.
- Any new fleet selector feature must be implemented once in the shared
  resolver so preview and execution cannot drift.
- Any new common-resource form must preserve unknown fields and retain YAML
  escape-hatch support.
- Do not remove the legacy tunnel until the supported agent-version window and
  rollback policy have expired.

## 25. Final review checklist for the human approver

Before authorizing implementation, confirm:

- [ ] The recommended canonical session default is 60 minutes, or record an
      explicit alternative.
- [ ] Viewer is approved as the universal implicit agent profile.
- [ ] Two-Secrets bootstrap/durable ownership is approved.
- [ ] Redis is mandatory for multi-replica production, with cache bypass during
      unhealthy periods.
- [ ] Restricted Argo NetworkPolicy is mandatory in production values.
- [ ] Medium and large profile topology/budgets are acceptable.
- [ ] The selected Rancher comparison scope remains day-2 adopted clusters.
- [ ] No provisioning or Fleet work has entered scope.
- [ ] Child plans may be created and executed in the dependency order above.

## 26. Mandatory live-acceptance closure addendum (2026-07-10)

This section supersedes any earlier statement that ARGO-03 or the live
self-management cutover is complete. It records evidence from the first
credential-safe, non-pruning acceptance attempt and turns the failure into an
implementation-ready P0 closure plan. It does not expose Application Helm
values, Secret data, tokens, controller logs, or raw operation payloads.

### 26.1 Release disposition and proven facts

| Item | Result | Release meaning |
|---|---|---|
| Integrated source | `6783004f97b270d3b1e3fa1ac105dd234a534e08` on `advisor/002-integration` | Exact review baseline for this addendum |
| Chart/application version | 0.2.2 | Correctly differs from the prior 0.2.1 archive, avoiding same-version chart cache reuse |
| Static enterprise gate | Go standard and race suites; OpenAPI; frontend 59 suites/459 tests, build and audit; Helm lint/render/contracts all passed | Necessary, but insufficient for release |
| Candidate images | server, agent, worker, migrate, frontend, and shell tagged `accept-6783004f97b2` | OCI revision/version/created labels match the integrated commit |
| SBOM evidence | Six SPDX JSON documents generated with Syft 1.16.0, private mode 0600 | Supply-chain evidence complete for the candidate images |
| Current live Helm baseline | revision 13, chart/app 0.2.1, prior `accept-a316f9fba69e` management images | The 0.2.2 candidate was deliberately not promoted after the acceptance failure |
| Live workload health | management workloads remained Ready; preflight Job succeeded; PVC and protected-Secret identity checks did not report mutation | Failure is control-plane convergence, not an observed workload outage |
| Final containment | Application controller desired replicas 0; zero running Astronomer Argo operations; Application remains awaiting approval | Safe hold; automated prune/self-heal is not armed |

The candidate is therefore **build-verified / live-rejected**. Do not deploy it
to a non-disposable environment and do not describe it as enterprise-ready.

### 26.2 P0 finding A — asynchronous Argo syncs are replayed as stale work

**Evidence**

- `internal/db/queries/argocd_operations.sql:38-42` returns both `pending` and
  `running` rows from a query named `ListPendingArgoCDOperations`.
- `internal/db/queries/argocd_operations.sql:50-70` makes a running operation
  reclaimable when `started_at` is older than one minute and resets
  `started_at` on every claim.
- `internal/handler/argocd.go:1388-1491` sends the upstream sync mutation from
  every successful claim. It treats Argo's response as asynchronous, leaves
  the row running, and expects the separate poller to complete it.
- `internal/handler/argocd.go:329-347` runs pending processing every 20 seconds
  and polling on a separate cadence. A valid sync lasting beyond the one-minute
  lease is therefore submitted again even while polling is healthy.
- The controlled run produced one durable operation row with
  `attempt_count=9`, repeated Argo `OperationStarted` events approximately
  every 60–80 seconds, and no terminal success. This exactly matches the code
  path above.

**Impact**

One operator request can execute the upstream mutation repeatedly. Every
replay restarts the Argo operation and its `PreSync` hooks, preventing the
poller from observing a stable terminal state. In an HA deployment this can
duplicate migrations or other side effects and makes operation history,
timeouts, and user-visible status untrustworthy.

**Required design**

1. Treat dispatch and polling as different state-machine transitions.
   `pending -> running` is an at-most-once upstream mutation claim. A `running`
   Argo row is resumed only by polling; it is never sent through `Sync` again.
2. Change the pending claim query and CAS update so only `status='pending'`
   rows are eligible. Keep `ListRunningArgoCDOperations` exclusively for the
   poll path.
3. On process restart, invoke the running-operation poll pass immediately
   after initial pending processing. Do not replay the POST to recover a
   running row.
4. Accept the crash ambiguity explicitly: if the process dies after committing
   `running` but before or during the upstream POST, inspect the bound live
   Application through the poller. If no matching operation becomes observable
   before the bounded deadline, fail the local operation and require an
   explicit operator retry. At-most-once mutation is safer than silent replay.
5. Preserve the existing stable binding to instance ID, Application ID, and
   upstream UID on every poll and retry decision.

### 26.3 P0 finding B — HA polling has no durable claim and terminal writes lack CAS

**Evidence**

- `internal/handler/argocd.go:1730-1807` lists every running row and protects
  claims only with `h.mu`, which is process-local. Each server replica can poll
  the same row concurrently.
- `internal/db/queries/argocd_operations.sql:116-133` selects all running rows,
  then increments `poll_attempts` in a later unconditional update. Replica
  count therefore changes timeout behavior and permits write races.
- The progress, completion, and failure updates do not consistently require
  the row still to be running. A late request can overwrite fields after an
  operator, timeout, or newer desired state has terminalized/superseded it.
- Existing unit tests call the polling method directly with an in-memory
  recorder. They prove phase mapping, but do not run three reconcilers against
  a real database or assert one poll claim per cadence.

**Required design**

1. Add an atomic database poll-claim operation. A recommended shape is an
   `UPDATE ... FROM (SELECT ... FOR UPDATE SKIP LOCKED)` or equivalent CTE that
   selects eligible `running` rows whose `last_polled_at` is absent/expired,
   stamps the poll lease, increments `poll_attempts` once, and returns the rows.
2. Use a lease duration longer than the upstream HTTP timeout and shorter than
   the user-visible poll SLO. Define the cadence, lease, max wall-clock
   duration, and max attempts once; do not let server replica count multiply
   them.
3. Split "claim poll" from "fold poll result." Folding a result must not
   increment the attempt again.
4. Add `WHERE status='running'` CAS predicates to progress, complete, and fail
   writes. Treat `pgx.ErrNoRows` as a benign lost race after reading the
   terminal row; never resurrect or overwrite it.
5. Apply the same terminal-write audit to workload, tool, catalog, logging,
   and monitoring operation tables. Those paths share the one-minute generic
   lease pattern and may duplicate any synchronous execution that exceeds the
   lease unless they heartbeat or use a workload-specific recovery contract.
6. Emit metrics for initial dispatches, prevented replays, poll claims, lost
   CAS races, operation wall-clock age, and timeouts. Do not include payloads,
   tokens, values, or Secret identifiers beyond bounded resource metadata.

### 26.4 P0 finding C — retained Secret references deadlock safe acceptance

**Evidence**

- The staged Helm values correctly use `secrets.existingSecret` and
  `bootstrap.existingSecret`; the referenced Secrets are intentionally absent
  from the rendered desired manifests.
- Argo consequently marked those live objects as extraneous and requiring
  prune during the non-pruning acceptance run.
- `internal/server/self_manage_migration.go:438-465` requires overall
  `status.sync.status == Synced`, Healthy, a succeeded operation, and an exact
  compared/result source before activation.
- `internal/server/self_manage_migration.go:170-186` ultimately arms automated
  prune and self-heal. Activating that policy without a per-resource retention
  contract would make the external Secrets eligible for deletion.
- Argo's official behavior is explicit: `Prune=false` protects a resource but
  leaves the Application OutOfSync; `IgnoreExtraneous` removes an intentionally
  extraneous resource from overall sync status, and the documentation suggests
  combining the two.

**Required ownership contract**

1. Build the protected-resource set from the validated, reference-only Helm
   values and the existing secret-reference collector. Include core,
   bootstrap, TLS, external database/Redis, registry, backup/restore, Dex, and
   bundled-Argo references that are live but not rendered as desired objects.
2. Before staging or activating self-management, annotate only that exact
   retained set with both:

   - `argocd.argoproj.io/compare-options: IgnoreExtraneous`
   - `argocd.argoproj.io/sync-options: Prune=false`

   Merge annotations without replacing unrelated metadata. If either key has a
   conflicting operator value, STOP and require an explicit decision.
3. Record ownership of annotations with a bounded Astronomer metadata marker,
   not in Secret data. Disable/uninstall rollback may remove only annotations
   Astronomer added and only when the prior state is known.
4. Verify every referenced Secret exists and has the expected key names before
   staging. Never read, hash into logs, return, or copy Secret values.
5. At approval time revalidate Secret UID, reference name/key set, required
   retention annotations, Application source/destination identity, exact spec
   hash, successful non-pruning operation, and complete server rollout.
6. Add a periodic health check and alert for a missing retention annotation or
   a referenced Secret becoming unexpectedly managed/prunable. Fail closed by
   returning the Application to an operator-required hold before automated
   pruning can run.
7. Keep Argo hook support resources out of the retained-Secret mechanism.
   Their lifecycle is governed by hook annotations and must be tested
   separately; do not blanket-ignore all extraneous resources.

### 26.5 Phase A — operation state-machine implementation

**In scope**

- `internal/db/queries/argocd_operations.sql`
- regenerated `internal/db/sqlc/argocd_operations.sql.go` and querier contracts
- `internal/handler/argocd.go`
- Argo operation unit, database-integration, concurrency, and recovery tests
- a migration only if a dedicated poll-owner/lease column is necessary

**Tasks**

1. Write failing database-backed tests that reproduce one request becoming
   multiple upstream sync calls after 60 seconds.
2. Separate pending dispatch from running poll recovery and regenerate sqlc.
3. Add the atomic HA poll claim and CAS-guard every fold/terminal transition.
4. Make startup poll existing running rows without replaying their mutation.
5. Add crash-window tests for before-POST, after-POST/before-fold, process
   restart, timeout, explicit retry, supersession, and Application UID change.
6. Audit the shared operation runner consumers and either add heartbeats or
   document/prove why their execution is bounded below the lease. An
   unproven one-minute assumption is not acceptable.

**Phase A definition of done**

- [ ] One API request produces exactly one upstream sync POST.
- [ ] Three server replicas produce at most one poll per row per cadence.
- [ ] Timeout duration is invariant across one and three replicas.
- [ ] Restart resumes polling and never replays the mutation.
- [ ] Late progress cannot overwrite completed, failed, or superseded state.
- [ ] Race tests and real-Postgres concurrency tests pass 50 consecutive runs.

### 26.6 Phase B — retained-resource ownership implementation

**In scope**

- `internal/server/self_manage_values.go`
- `internal/server/self_manage_migration.go`
- focused self-management tests and chart render contracts
- operator documentation for retained Secrets and disable/uninstall behavior

**Tasks**

1. Write a table-driven retained-reference inventory covering every supported
   SecretRef shape and reject ambiguous/malformed names or keys.
2. Render the embedded chart and distinguish desired Secrets from referenced
   external Secrets; only the latter receive retention metadata.
3. Add conflict-safe annotation adoption, fixed-point reconciliation, and
   rollback ownership tracking.
4. Extend acceptance checks to prove the retained set before active
   prune/self-heal is written.
5. Test annotation removal, external-secret recreation, key rotation, chart
   upgrade/restage, rollback, disable, uninstall, backup, and restore.
6. Test that an unreferenced extraneous Secret remains visible/prunable and
   cannot be smuggled into the protected set.

**Phase B definition of done**

- [ ] Referenced external Secrets are excluded from overall sync status and
      protected from application-level pruning.
- [ ] No Secret value appears in an Application, operation, status, history,
      event, metric, response, test report, or committed evidence file.
- [ ] Conflicting operator annotations fail closed.
- [ ] Two reconciliations are byte-for-byte metadata fixed points.
- [ ] Removing a reference follows documented ownership-safe cleanup semantics.

### 26.7 Phase C — deterministic live acceptance protocol

Run only in a disposable, access-controlled cluster. Store evidence in a
private 0700 directory with files at 0600. Evidence may contain pass/fail,
resource names, UIDs, image digests, phases, counts, timestamps, and HTTP status
codes; it must not contain Secret data, tokens, raw Application values/status,
or unfiltered controller logs.

1. Build all six images from one clean commit. Verify OCI revision, version,
   created timestamp, architecture, non-root/runtime contracts, and generate
   SPDX SBOMs.
2. Import images immediately before deployment and verify every rendered
   first-party image is exact. Refuse mutable or mixed source tags.
3. Snapshot Helm revision/status, schema version, workloads, agents,
   referenced-Secret UID/key-name metadata, PVC UID/size/class, routes, and
   Application metadata while the Argo controller is zero.
4. Deploy the new chart with `--atomic --wait`; keep a post-render fence that
   renders the Argo Application controller at zero until staging is verified.
5. Require complete rollout, clean migration state, reference-only Application
   source validation, exact chart target revision, awaiting-approval phase,
   absent approval annotation, and correct retained-resource annotations.
6. Scale the controller to one and submit exactly one non-pruning sync through
   Astronomer's API. Assert the durable row has `attempt_count=1`, Argo emits
   one operation start, and one preflight lifecycle occurs.
7. Within the reviewed SLO require local operation `completed/Succeeded`,
   Application `Synced/Healthy`, no running operation field, and exact source,
   destination, UID, revision, and spec-hash binding.
8. Compare protected-resource and PVC evidence. Any deletion, replacement,
   unexpected resource-version change, storage shrink/class change, or prune
   is an immediate NO-GO.
9. Copy the exact current spec hash into the approval annotation. Require the
   server to transition to active and arm the reviewed automated policy; do
   not patch policy fields directly.
10. Prove self-heal on a harmless owned resource and prove a protected
    external Secret is neither deleted nor made OutOfSync. Re-run two
    fixed-point reconciliations.
11. Validate gateway/API/UI/internal-deny behavior, local Argo health, schema,
    all management workloads, both agents' readiness/durable identity/current
    image/heartbeat, operation and audit history, and zero credential-shaped
    material in bounded scans.
12. Perform Helm rollback and forward re-upgrade from immutable artifacts.
    Re-run steps 7–11 and confirm no operation replay or Secret/PVC mutation.
13. Leave the controller at one only after every gate is GO. Any failure trap
    must terminate the current operation where supported, terminalize the
    local test row without resurrection, and return the controller to zero.

**Required live assertions**

| Assertion | Pass condition |
|---|---|
| Upstream mutation cardinality | exactly 1 sync start for one Astronomer operation |
| Durable execution | `attempt_count=1`; one terminal transition; zero resurrection |
| HA polling | one durable claim per cadence independent of server replicas |
| Hook lifecycle | one successful preflight run; no repeated `BeforeHookCreation` cycle |
| Application | awaiting -> one sync -> Synced/Healthy -> exact approval -> active |
| Pruning | requested false during acceptance; protected retained set never eligible/deleted |
| Workloads | all desired/ready; migrations clean; no image skew |
| Data | Secret data never emitted; PVC identity/capacity/class preserved |
| Agents | ready, durable, heartbeat current, candidate image on each test cluster |
| Rollback | prior immutable release restores; forward upgrade repeats cleanly |

### 26.8 Phase D — release and Rancher-relative sign-off

Astronomer's intended comparison remains Rancher-style day-2 management of
already-provisioned clusters. Cluster provisioning is excluded, and Argo CD
replaces Fleet. The relevant Rancher-grade bar is therefore operational
correctness: durable at-most-once mutations, HA-safe reconciliation, explicit
resource ownership, safe upgrade/rollback, auditable approval, health and
agent recovery, and repeatable evidence.

Release sign-off requires:

- [ ] Sections 26.5 and 26.6 are implemented and independently reviewed.
- [ ] Section 26.7 passes three consecutive times from fresh disposable
      clusters, including one forced server failover during the running sync.
- [ ] A 72-hour three-server soak has zero duplicate upstream mutations,
      terminal-state resurrection, protected-resource drift, or stuck
      operation.
- [ ] Upgrade from the supported N-1 chart and rollback both pass.
- [ ] The Rancher comparison and runbooks state the intentional exclusions and
      cite measured evidence rather than feature-presence claims.
- [ ] No P0/P1 finding is waived. A failure in this addendum keeps the product
      at controlled-pilot status.

### 26.9 Focused commands and expected results

Exact target names must be reconciled with the implementation, but the
executor must provide one command for each gate with an unambiguous expected
result:

~~~bash
go test -race -count=50 ./internal/handler -run 'ArgoCD.*(Claim|Poll|Replay|Restart|Terminal)'
go test -race -count=20 ./internal/server -run 'SelfManaged.*(Retained|Acceptance|FixedPoint|Rollback)'
go test -count=1 ./internal/db/... ./deploy/...
go test -race -count=1 ./...
make verify-enterprise
helm lint deploy/chart
~~~

Expected: every command exits zero; the focused suites include database-backed
three-replica claim/poll coverage and chart-rendered retained-resource cases.
The live harness must additionally emit a single redacted `GO` summary only
after every Section 26.7 assertion passes.

### 26.10 STOP conditions specific to this addendum

Stop, contain, and report if:

1. One requested operation produces more than one upstream mutation.
2. Poll/timeout behavior changes with server replica count.
3. Recovery requires blindly replaying an upstream mutation.
4. A late goroutine can write after terminal/superseded state.
5. Application sync requires pruning a referenced external Secret.
6. A retention rule is broader than the exact validated reference set.
7. A conflicting operator annotation would be overwritten.
8. Approval can arm automated prune before exact successful acceptance proof.
9. A Secret or PVC UID changes, a PVC shrinks, or a protected resource is
   deleted during sync, rollback, or re-upgrade.
10. Any credential value, raw Helm values document, or unfiltered Application
    source/status enters logs or evidence.
11. The controller cannot be reliably returned to zero after a failed run.
12. Static tests pass but the deterministic live protocol does not.
