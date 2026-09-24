# Plan 028 — Qualify every offered application and integration through supported APIs

## Status, scope and execution authority

**PLANNED — independently reviewed; no new live qualification has been run for this plan.** Written 2026-09-24 against Astronomer HEAD `22f633ec` **including the current dirty working tree**, and the local catalog HEAD `3e24ab98f0805bd54d6591d9b9b961bbfd1f5db5`. Priority P1; effort L, split by offering; implementation risk medium, live lifecycle tests high unless confined to disposable targets.

The user's requirement is every offering, not only Dex, Velero and monitoring, and proof through the API without workarounds. The companion [offering-by-offering inventory and test matrix](./028-offering-test-inventory.md) is mandatory scope. It lists the 21 local catalog applications, additional Tools offerings, baseline components, provider variants and platform integrations. Enumerate the deployed API inventories again before execution: this static list is a baseline, not permission to omit a newly offered item.

Plan 016 remains the release/GA evidence authority. Plan 026 owns existing robustness remediation and Plan 027 owns navigation/workflow presentation. This plan supplies functional evidence and concrete defect handoffs to those owners; it does not reopen completed implementation or imply that documentation, mocks or prior test runs qualify current artifacts. Writing this plan performs no installations, provider sends, upgrades or destructive recovery drills.

## What counts as working

For **each offering × exposed variant × supported lifecycle action × deployment profile**, require:

1. Discoverability and prerequisites are accurate in the supported API and user-facing offering.
2. An authorized request is accepted with the documented response and operation identity, where asynchronous.
3. The operation reaches the correct terminal state through its own public status API; installed inventory agrees on version, revision, owner and desired/observed generation.
4. A real functional canary proves the promised outcome through the normal product API, plus the destination's supported API/protocol when needed: actual login, recovered bytes, workload metric, collected log, delivered notification, enforced policy, etc.
5. A new client/session can read the result. Repeat/update/upgrade/recovery/removal work as advertised, and unauthorized or wrong-tenant requests are denied without side effects.
6. API-managed cleanup removes only run-owned resources and leaves the intended retained data. Verify cleanup, including partial failures; do not suppress errors.

HTTP 200 with `ok:false`, HTTP 202, a redirect to an IdP, a successful preview, a running pod, a Helm Ready condition, an empty graph, or a saved configuration **alone never qualifies the feature**. A destination connectivity test does not prove delivery. A direct Prometheus/Loki query does not qualify Astronomer's native metric/log query path.

Use result states `NOT_RUN`, `RUNNING`, `PASS`, `FAIL`, `BLOCKED`, `NOT_SUPPORTED`. Missing credentials/hardware/provider accounts produce `BLOCKED`, not PASS or a silent skip. `NOT_SUPPORTED` requires an explicit offering contract (for example catalog rollback=false); it cannot excuse an action advertised in the UI/API. Tests must verify unsupported actions are rejected honestly. A feature flag being off does not remove an offered opt-in feature from the inventory: qualify both off and enabled states in separate disposable profiles.

## API-only rule

- Drive Astronomer workflows through its normal authenticated public API, using legitimate login/session or API-token issuance and normal RBAC, token scope, CSRF and idempotency behavior. Browser automation may follow real OAuth/SAML forms and test rendering; it must not inject sessions or intercept backend responses in the live lane.
- Use the documented tool, catalog, monitoring, logging, identity and backup owners. The authenticated Kubernetes API proxy is acceptable for ordinary test workload/CR creation and independent observation. **It must not replace an advertised dedicated lifecycle/configuration endpoint or edit resources owned by that endpoint.** Do not patch generated Flux resources, Helm state, Dex runtime Secrets or monitoring configuration to turn a failure green.
- No database writes, forged tokens, security disabling, fake status updates, manual Helm/kubectl repair, direct worker invocation, replacement release under a new name masquerading as recovery, hidden fixtures, or inferred operation IDs. No TLS verification bypass or schema-skew waiver in a qualified run.
- External prerequisites (adopted clusters, normal management installation with chart-bundled Dex, real IdP tenant, object store, DNS zone, Linux storage requirements) must be declared before the run. Their documented installation belongs to environment preparation, not a bypass of Astronomer's offered install path. Record provenance and separate prerequisite readiness from product qualification. Do not provision clusters through Astronomer; it is adoption-only.
- Destination/provider APIs and real workload protocols are allowed as independent evidence. For example, an API-created canary Job can write/read a PVC or emit a log, with its result read through Astronomer. A syslog/SMTP receiver can expose an authenticated evidence API. Such a receiver proves that protocol; a fake Slack/PagerDuty/Datadog server does not certify the real vendor integration.
- If the public API cannot observe completion, recover a failure, or remove an owned resource, record an API gap and stop that case. Implement the smallest normal product contract with tests in a separate reviewed change, then rerun from a clean fixture. Keep failing evidence.

## Current evidence and known traps

These are grounded review inputs, not assertions that all old failures still exist:

| Evidence | Consequence for qualification |
|---|---|
| `internal/handler/tools_catalog.go:85` rejects Dex Tool installation because Dex is bundled in the management chart | Qualify Auth settings/apply/SSO, plus truthful Tools behavior. Do not invent per-cluster Dex install. |
| `internal/handler/tools_catalog.go` responds with `/api/v1/tools/operations/{id}/` after audited, idempotent enqueue | Follow the returned operation, then prove the actual effect. Do not count enqueue as install success. |
| `internal/db/migrations/044_istio_release_plan.up.sql` offers ordered base + istiod, default/development presets, no gateway | Include Istio multi-release recovery/removal. `docs/cluster-tools-and-governance.md` still says it is not offered; reconcile this stale doc instead of dropping the test. |
| `../astronomer-catalog/catalog.json` has 21 applications; kube-prometheus-stack, Loki, Longhorn and CloudNativePG declare rollback=false | Test each supported action and truthful rollback rejection. Different Tools/managed-stack and catalog versions are separate cases. |
| `advisor-plans/addon-validation-2026-09-23.md` records native metric routing, corrective upgrade, uninstall/acknowledgement, pipeline PUT, private Loki querying and rollback-audit gaps | Reproduce against the frozen current build; close only with a fresh API run of the formerly failing path. A new clean Fluent Bit release did not qualify the failed release's corrective upgrade. |
| `advisor-plans/grafana-observability-validation-2026-09-23.md` records real Grafana checks, but also manual admission-hook resource restoration and create/disable instead of pipeline edit | Retain useful historical evidence; rerun interrupted upgrade and pipeline update without those repairs. Grafana Live/WebSocket tail is documented unsupported, not a passing feature. |
| `scripts/validate-live-dex.sh` clears all connectors, uses a dummy provider and stops at redirect/config observation | Do not run unchanged. Preserve other connectors and complete a real provider login/callback/authorization flow. |
| `scripts/validate-live-velero.sh` installs with Helm and creates/deletes fixtures directly with kubectl | Useful scenario reference, not API-only product lifecycle evidence. Rewrite/adapt the lane before use; require explicit target IDs rather than first active cluster. |
| `internal/handler/cloud_credentials_crud.go:385` returns HTTP 200 with OK=false for unsupported/no-op provider testing | Assert semantic result, and prove a real consuming workload can use the materialized credential. Generic is not cloud identity verification. |
| [027 restore contract checkpoint](./027-ux-audit/restore-tracking-contract.md) distinguishes cluster snapshot restore rows from the general Velero BackupHandler restore operations | Never poll `/backups/restores/{id}/` using a cluster-snapshot receipt unless the actual API contract establishes that identity. Missing readback is a blocker. |

## Allowed implementation scope and conventions

This is an implementation handoff. The advisor has changed documentation only. The executor may add a single Go qualification runner under proposed `scripts/qualify-offerings/`, non-secret scenario manifests under proposed `scripts/testdata/offering-qualification/`, focused runner tests, and the bounded CI/browser integration specified below. Inspect `scripts/delivery-qualification/main.go`, `scripts/testdata/live-browser-fixture/main.go`, `pkg/astroclient`, `scripts/check-enterprise-qualification-producers.py`, and existing release-evidence schemas for reusable contracts. Use the generated API client where suitable and an explicitly bounded HTTP adapter for supported provider protocols; do not invent a second lifecycle controller.

Concrete new integration files: `.github/workflows/offering-api-qualification.yaml` (manual `workflow_dispatch`, existing protected `release-candidate` environment, explicit immutable candidate and target/config references); `deploy/release/offering-qualification.schema.json` (closed inventory/result/artifact schema); `scripts/tests/offering_qualification_test.py` (schema/coverage false-green tests); `frontend/tests/offering-qualification/` with its dedicated Playwright configuration and live specs. The browser companion must import the same frozen inventory/case IDs, log in normally, emit schema-valid results into the same run directory and never import smoke stubs or `seedAuth`. Proposed command after implementation: `cd frontend && npx playwright test --config tests/offering-qualification/playwright.config.ts`. Provide the existing Node version/locked dependencies and API fixture configuration; absence of provider credentials is BLOCKED, never a skipped test.

The workflow uploads a run-specific `offering-qualification` artifact containing inventory, results, hashes, sanitized request evidence and browser results; the Go runner's `verify` mode is its consumer/gate. It must not rebuild or mutate the candidate mid-run. Extend `.github/workflows/pr-validation.yaml` only with non-live runner/schema tests and an offline source-inventory coverage check; no provider credentials on PR jobs. Preserve existing checks and pinned workflow conventions. Repository environment/secrets configuration is an execution prerequisite, not invented in the script.

This is supplementary release-candidate evidence under Plan 016, **not a silent new protected producer or a replacement for any existing producer**. Changing `scripts/check-enterprise-qualification-producers.py`, `scripts/validate-release-approval.py`, `scripts/fetch-release-qualification-artifacts.py`, `scripts/verify-enterprise.sh`, release-approval schemas or promotion workflows is outside this increment. Record the immutable artifact reference in Plan 016's evidence ledger. If mandatory promotion-gate consumption is required, specify that coordinated Plan 016 follow-up separately across those exact producer/consumer contracts; do not add a token to one file and claim protected integration is complete.

Changes to product code, SQL, APIs, charts or the sibling catalog are separate defect increments linked to a failed case. Match existing domain boundaries; change SQL sources and regenerate sqlc, change OpenAPI and regenerate clients, never edit generated artifacts by hand. Preserve all existing staged/unstaged/untracked work. Do not opportunistically normalize or refactor unrelated files.

Current authoritative examples:

```go
// internal/handler/tools_catalog.go — asynchronous acceptance, not completion
RespondAcceptedOperation(w, "/api/v1/tools/operations/"+op.ID.String()+"/", toolOperationResponse(op))
```

```go
// internal/server/routes_tools_controlplane.go — canonical lifecycle and recovery
r.Get("/operations/{id}/", deps.ClusterResources.Tools.GetOperation)
r.With(mutationWriteScope).Post("/operations/{id}/retry/", deps.ClusterResources.Tools.RetryOperation)
```

```json
{"slug":"kube-prometheus-stack","lifecycle":{"install":true,"upgrade":true,"rollback":false,"uninstall":true}}
```

The JSON excerpt selects fields from the catalog; it is not an install request body. Resolve methods, trailing-slash behavior, operation IDs, request schemas and permission gates from `docs/openapi.yaml` **and mounted server routes** before constructing requests. Stop an affected case on drift rather than guessing a path or payload.

## Phase 0 — Freeze the inventory and baseline

1. Record `git rev-parse HEAD`, staged/unstaged diff hashes and untracked source hashes for Astronomer; record catalog revision/content digest, catalog channel, API version, schema version, server/worker/agent/frontend image digests, chart versions, Kubernetes versions/architectures, agent profiles, feature flags and provider versions. Do not copy credentials or secret files into artifacts. HEAD alone is insufficient for this working tree.
2. Reconcile the companion matrix with **all pages** of Tools, catalog applications/charts/discovery/repositories, existing installed apps/tools, system components, connector types, SSO presets, cloud providers, enabled extensions, monitoring stacks, notification channels and settings/feature registries. Include configured third-party catalogs and newly discoverable offers. Record source and visibility scope per entry, including disabled/opt-in entries. Detect duplicates by owner + source + slug + version, not display name.
3. Distinguish curated applications, arbitrary repository charts, installable Tools, bundled management services and discovery-only integrations. Every entry actually exposed as installable needs a test case; never certify an entire upstream repository by testing one chart. If extra charts need functional specifications, list each as `BLOCKED: functional contract missing` with an owner until specified. No undocumented sampling.
4. Expand every named provider, preset, transport, profile and advertised lifecycle flag into case IDs. Join UI/API/seed/catalog inventories and fail coverage for orphan offerings, unsupported advertised actions or unassigned cases. Include package-specific setting fields and UI-generated request bodies, not only hand-crafted happy-path requests.
5. Provision a dedicated test estate using normal environment installation procedures: management profile, at least two member clusters/projects/namespaces for isolation, correct writable/read-only agents, permitted provider sandboxes and task-owned destinations. Record target allowlists and run-owned names before mutation. No selecting a convenient existing cluster implicitly. External/HA/architecture/air-gap qualifications remain separately visible under Plan 016.

Phase 0 verification is a read-only source/environment inventory and explicit prerequisites, using existing documented GET requests if available. Do not require the not-yet-built runner here. After Phase 1 implements and tests inventory mode, run the executable reconciliation gate: GET-only API calls, complete paginated counts and zero unmapped offering/variant IDs. No mutating case starts until that gate passes. Missing accounts/environments remain required manifest cases marked BLOCKED.

## Phase 1 — Build the evidence runner and enforce truthful results

Implement a resumable runner with bounded polling, explicit per-case timeouts, request correlation, operation receipt persistence, fresh readback, allowlisted target IDs and API cleanup journal. Persist non-secret ownership IDs before/after each mutation so interrupted runs can recover their own resources without deleting others. Respect documented idempotency; replay the same key/body only where supported, reject conflicting reuse, and demonstrate no duplicate side effects. Do not blanket-retry mutations.

Each case records: offering/variant/action/profile, owner, prerequisites, `depends_on`, `fixture_owner`, `exclusive_resources`, `cleanup_after`, frozen build/source/catalog digests, actor role, target IDs, sanitized request/response metadata, operation/audit identities, timestamps, terminal state, canary expectation/observation, cleanup result, result state and blocker/failure reason. Raw authorization, cookies, provider codes, Secret manifests, decrypted values and backup contents never enter public evidence. Private evidence stores use restricted permissions; retain only canary hashes in shareable reports.

Build a dependency DAG before scheduling: metrics/exporter tests depend on a qualified query backend; log/alert tests depend on their receivers; Velero depends on storage/plugin fixtures; Image Scans depends on scanner ingestion. Retain a dependency until all consumers finish, then clean in reverse order. Lock cluster-scoped CRDs/controllers/releases, management singleton settings, destination configurations and shared provider fixtures across the whole run, including resumed runs. Tools/catalog/baseline entrypoints for the same software must run serially on exclusive fixtures or use separate clusters; they must not install competing owners. Capture current ownership via APIs before acquiring a fixture and refuse collisions with non-run objects. Independent lanes means disjoint declared resources, not merely different test names. Add runner tests for lock contention, dependency failure propagation, interrupted cleanup and preventing a dependency uninstall while consumers remain.

Runner tests must prove false-green rejection: 202 forever; 200/ok=false; operation success with no effect; stale previous-generation samples; wrong-cluster data; omitted pagination; missing provider credentials; unexpected skips; failed cleanup; transient error followed by actual recovery; duplicate/replayed mutation; unknown/new offering; missing test for a preset; disabled flag hiding an unqualified opt-in. Fixtures test the runner, and are labeled synthetic; they never count as live qualification.

**Proposed interface to implement, not existing commands:**

```sh
go test ./scripts/qualify-offerings/... -count=1
go run ./scripts/qualify-offerings --mode inventory --config "$QUAL_CONFIG" --out "$QUAL_EVIDENCE"
go run ./scripts/qualify-offerings --mode run --config "$QUAL_CONFIG" --inventory "$QUAL_EVIDENCE/inventory.json" --out "$QUAL_EVIDENCE"
go run ./scripts/qualify-offerings --mode verify --inventory "$QUAL_EVIDENCE/inventory.json" --results "$QUAL_EVIDENCE/results.json"
```

`QUAL_CONFIG` identifies a permission-restricted configuration file with explicit targets and credential-file references, not inline credentials. `QUAL_EVIDENCE` is a run-specific ignored artifact directory. `inventory` exits nonzero on unknown/unmapped offers; `run` checkpoints failures and may continue independent cases; `verify` exits 0 only when every required case PASSes and every NOT_SUPPORTED action has a verified contract. FAIL/BLOCKED/NOT_RUN/RUNNING, stale artifacts, missing cleanup or missing cases make it nonzero. A partial report remains useful but is never labeled “all working.”

## Phase 2 — Qualify product lifecycle paths

For every applicable matrix row, execute discovery → preview/validation → create/install/configure → poll → actual function → update → upgrade from an explicitly supported predecessor → function again → recovery → uninstall/disable → absence/retention → reinstall. Execute adoption where offered using a legitimately pre-existing installation, preserving ownership rules. Chart-bundled and discovery-only services follow their actual supported lifecycle rather than a fabricated uninstall path.

For each exposed preset run its actual values, not a rewritten minimal substitute. Catalog and Tools/managed-stack paths for the same software remain distinct cases. Test dependency ordering, version/digest pinning, wrong-cluster rejection, namespace restrictions and conflict with an existing owner. Unsupported rollback must reject cleanly; supported rollback must use a recorded prior version and re-prove function. Only use offered APIs to recover an unhealthy installation. For Istio verify base-before-istiod and reverse removal, and interruption between releases without orphaning resources.

Verify using the API routes in the companion matrix and the exact returned receipts; every terminal success must correlate with observed resources and a new canary. Continue independent cases if one offering fails.

## Phase 3 — Prove functional outcomes and provider variants

Execute **every row and named variant** in the companion matrix. In particular:

- Identity: complete login through each actual provider/connector, callback/session, mapped role and denied action; rotate/revoke; no mock callback or fabricated token. Validate preserved unrelated connectors on apply and successful fresh login after update.
- Backup: verify resource manifests **and persistent data bytes** recovered into an isolated destination, using run-specific checksums, declared RPO/RTO and original data changed after backup. Distinguish Velero resource/volume restore, control-plane snapshot inventory/guidance and management DR. Restore scope mapping, schedules, storage-provider errors and cleanup are separate cases. Missing API restore progress remains BLOCKED.
- Metrics/logs/traces: canaries originate in real workloads; match unique cluster/project/namespace and timestamps through native APIs. Test shared/member routing, absent/stale/unreachable backends, query permissions and negatives from the other tenant. Direct backend success cannot substitute for a failing product endpoint.
- Policy/storage/networking/scanning: demonstrate the intended effect on a canary, not only CR creation. Probe allowed and denied traffic/admission as appropriate, write/read retained data, certificate issuance/renewal, DNS resolution, real scan ingestion, autoscaling and database persistence.
- Destinations: provoke an actual source event and observe the matching message in the destination, with durable product delivery state where offered. Test bad credentials, unreachable receivers, retries, deduplication, disabling and credential rotation. SMTP/syslog are real protocol tests; SaaS variants need real sandbox endpoints to pass vendor qualification.

For profiles promising HA, air-gap, private CA/proxy, multi-architecture or particular Kubernetes versions, expand the supported compatibility matrix from release contracts and retain those cases under Plan 016; a single local development run cannot certify them.

## Phase 4 — Recovery, isolation, persistence and UI agreement

Use supported APIs to induce recoverable failures on run-owned objects (bad configuration/credential, unavailable test destination, dependency conflict). Change configuration and retry the same owned installation; verify new generation and effect. Where process/node/network failure is essential and cannot be induced through the approved environment API, mark that resilience scenario BLOCKED in this API-only lane and link Plan 016's separately controlled drill. Do not call shell intervention an API pass.

Test restricted project-only/cluster-only readers/operators, read-scoped API tokens, wrong tenant and unauthenticated users. Verify write denial has no resulting operation/resource/recipient effect. Reconnect with a new client after accepted work; status, errors and audit must remain accessible to the appropriate role. Confirm uninstall during pending work and repeat removal cannot resurrect stale generations or delete another owner's resources.

After API qualification, use the real frontend and the same identities to confirm the offering is reachable, sends the supported payload, exposes the same operation and accurately renders success/failure/absence after refresh. No Playwright stubs in this lane. Plan 027's fixture browser evidence remains useful UX evidence only. Pure API checks cannot establish whether an iframe, form or sidebar actually renders; those rows require this additional browser check.

## Phase 5 — Remediation and regression gates

For each failure create a bounded defect entry in the results with owning source path, expected/observed behavior, minimal redacted API reproduction and exact failing case. Implement a focused fix under the appropriate owner, run narrow regression tests, and rerun the failed case plus affected lifecycle/tenant cases on a fresh frozen artifact. Never overwrite a failed run or relabel old evidence after changing the binary. If a setting advertised by the UI is unsupported, either implement it or explicitly change the offering contract; do not remove its test to regain green.

Use existing validation commands according to touched scope, not broad repeated checks on documentation:

```sh
go test ./internal/handler ./internal/server ./internal/dexconfig -count=1
go vet ./internal/... ./cmd/...
make test-postgres-integration
./scripts/verify-enterprise.sh api-contract
make verify-enterprise VERIFY_SCOPE=backend
make verify-enterprise VERIFY_SCOPE=helm
```

Run only relevant packages initially; PostgreSQL integration is required for changed transactional operation/association behavior. API changes require `make openapi-generate` followed by the API contract gate; SQL changes require generation and `make sqlc-check`. Frontend changes require `npm run type-check`, `npm run lint`, relevant `npm test -- --run <files>` from `frontend/`, then `make verify-enterprise VERIFY_SCOPE=frontend` at the phase boundary. Full release claims require Plan 016's corresponding gates. These commands validate code/contracts; the proposed live runner is still required to prove the offerings function.

## Completion and maintenance

- [ ] Inventory matches the frozen deployed APIs, UI offers, catalog and static registries with no omitted names, variants or lifecycle flags.
- [ ] Every required case has a named functional oracle, exact API sequence, owner and fresh PASS evidence; unsupported claims have verified explicit contracts.
- [ ] All supported install/update/upgrade/rollback/adopt/remove/recovery paths pass without workaround, and each offering performs its promised job.
- [ ] Real provider and tenant-isolation cases, persistent operation readback and cleanup pass; environment-blocked cases remain visibly unqualified until completed.
- [ ] Prior known failure paths have been rerun, including pipeline PUT, interrupted upgrades, native backend routing and restore tracking.
- [ ] UI/API disagreement and advertised-but-unsupported actions are resolved with Plan 027/026, not hidden from the inventory.
- [ ] Machine coverage verification fails on any newly offered item without a test. CI retains per-build results, and release evidence references them through Plan 016.
- [ ] Plan and index are updated to DONE only after all above are satisfied. Until then publish the full denominator and PASS/FAIL/BLOCKED/NOT_RUN counts, not “all extensions work.”

Execute inventory and harness first; then independent bounded lanes for identity, observability/destinations, backup/storage, security/networking and catalog applications; finish cross-tenant/recovery/UI agreement and the immutable evidence gate. A blocked provider does not prevent other lanes from progressing, but prevents the comprehensive claim. Future additions to any catalog, provider registry, feature flag or extension point must add qualification rows in the same change.

## Independent plan review

Cold-review corrections incorporated: SCIM uses the top-level `/scim/v2` token-authenticated surface; general Velero and management database backups have separate owners; control-plane snapshot POST is included; drill GET reporting is not a trigger; scheduling has explicit dependency/resource locks; executable inventory follows runner implementation; CI/browser paths and release-producer boundaries are concrete. No live result was inferred from this review.
