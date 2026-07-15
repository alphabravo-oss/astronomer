# Plan 003: Make self-managed Argo acceptance operations race-free and release 0.3.0 only after deterministic live proof

> **Executor instructions**: Read this entire plan before changing code. Follow
> the phases in order and run every verification gate. This plan closes one P0
> correctness defect; it is not authorization to redesign self-management,
> weaken the approval gate, skip preflight, or manually manufacture successful
> Argo status. If a STOP condition occurs, contain the live cluster as described
> below and report the evidence—do not improvise.
>
> **Repository and branch**: execute in the Astronomer repository on branch
> `advisor/002-integration` (the planning worktree was
> `/root/astronomer-all/.worktrees/002-integration`).
>
> **Drift check (run first)**:
>
> ~~~bash
> git rev-parse --short HEAD
> git status --short
> git diff --stat 5915bc6..HEAD -- \
>   internal/server/self_manage_migration.go \
>   internal/server/self_manage_migration_test.go \
>   internal/server/self_manage_argocd.go \
>   deploy/chart/values.yaml \
>   deploy/chart/templates/preflight-job.yaml \
>   deploy/chart/templates/preflight-rbac.yaml \
>   deploy/chart/templates/preflight-networkpolicy.yaml \
>   deploy/chart_operational_contract_test.go \
>   scripts advisor-plans
> ~~~
>
> Expected planning baseline: source HEAD `5915bc6`, clean integration
> worktree, bundled Argo Application controller scaled to zero in the disposable
> acceptance cluster, and zero Astronomer Argo operations in `running` state.
> If an in-scope source file changed, reconcile every excerpt in “Current
> state” before proceeding. A semantic mismatch is a STOP condition.

## Status

- **Priority**: P0 — blocks enterprise release and 0.3.0 sign-off
- **Effort**: M for implementation and automated tests; L including three clean live runs and failover validation
- **Risk**: HIGH — this code owns the Argo Application that deploys the management plane
- **Depends on**: `002-enterprise-grade-closure-and-rancher-day2-assurance-plan.md`, especially Section 26
- **Category**: correctness, concurrency, deployment, tests
- **Planned at**: integration commit `5915bc6`, 2026-07-11
- **Target release**: 0.3.0; do not bump beyond or promote 0.3.0 until every gate below passes

## Why this matters

The self-manager and Argo CD currently share a Kubernetes object whose CRD has
no `status` subresource. Argo writes sync progress under
`Application.status.operationState`; Astronomer periodically performs
full-object `Update` calls to normalize metadata and stage or activate the
Application. A full-object write during an active Argo operation can disrupt or
replace the controller’s progress snapshot. In live testing, the preflight Job
was `Complete`, but the Application remained `Running` and Astronomer’s durable
operation never reached its terminal fold.

This is not a cosmetic status delay. It prevents deterministic approval,
leaves the management-plane deployment operation stuck, and makes live release
evidence impossible. The required invariant is:

> While a correctly bound, non-pruning self-management acceptance operation is
> `Running` or `Terminating`, Astronomer performs **zero writes** to that Argo
> Application. Argo has exclusive write ownership until it records a terminal
> phase. Astronomer may continue read-only validation and may reconcile other
> unrelated objects, but it must not normalize Application metadata, restage
> its spec, or remove `.operation`.

The fix must preserve the existing safety model: destructive, mismatched, or
unverifiable operations must never gain approval or automated prune/self-heal.

## Current state and confirmed evidence

### Relevant files

- `internal/server/self_manage_migration.go` — creates, stages, validates,
  scrubs, approves, and activates `astronomer-self-manage`.
- `internal/server/self_manage_migration_test.go` — current staged/approval and
  operation-safety unit coverage using the dynamic fake client.
- `internal/server/self_manage_argocd.go` — invokes self-management immediately
  and then every 30 seconds with a 60-second timeout.
- `deploy/chart/templates/preflight-*.yaml` — separates Helm and Argo preflight
  ownership; Argo prerequisites and its Sync hook are now in wave `-5`.
- `deploy/chart/values.yaml` — currently removes the upstream catch-all
  `/status` resource-update suppression while retaining targeted exclusions.
- `deploy/chart_operational_contract_test.go` — protects preflight ownership,
  annotation, and ordering contracts.
- `scripts/verify-enterprise.sh` — authoritative static backend/frontend/Helm
  verification entrypoint.
- `advisor-plans/002-enterprise-grade-closure-and-rancher-day2-assurance-plan.md`
  — parent release gate; Section 26 requires three consecutive live passes.

### Load-bearing current code

At `internal/server/self_manage_migration.go:223-270`, a staged Application with
the expected hash enters this path:

~~~go
currentSpec, _, _ := unstructured.NestedMap(current.Object, "spec")
annotations := current.GetAnnotations()
if annotations[selfManagedPhaseAnnotation] == selfManagedPhaseAwaiting &&
    annotations[selfManagedHashAnnotation] == desiredHash {
    operationSafe := selfManagedAwaitingOperationSafe(
        current,
        stagedSelfManagedSpec(desiredSpec),
    )
    // ... approval checks ...
    stagedSpec := stagedSelfManagedSpec(desiredSpec)
    if reflect.DeepEqual(currentSpec, stagedSpec) && operationSafe {
        if selfManagedApplicationMetadataClean(
            current,
            selfManagedPhaseAwaiting,
            desiredHash,
        ) {
            return nil
        }
        updated := current.DeepCopy()
        setSelfManagedApplicationMetadata(
            updated,
            selfManagedPhaseAwaiting,
            desiredHash,
        )
        updated.SetFinalizers(obj.GetFinalizers())
        _, err = res.Update(ctx, updated, metav1.UpdateOptions{})
        return err
    }
}
~~~

That metadata-normalization update is unsafe while Argo owns an active sync.
Even when the copied object retains `.operation`, it is still a full-object
write against a CRD without a status subresource.

At `internal/server/self_manage_migration.go:349-353`, absence of top-level
`.operation` is currently treated as “safe”:

~~~go
raw, exists := application.Object["operation"]
if !exists {
    return true
}
~~~

This conflates two states that the reconciler must distinguish:

1. no operation exists and no Argo operation is active;
2. Argo accepted the operation, removed the top-level request, and now records
   the active operation under `status.operationState`.

At `internal/server/self_manage_migration.go:455-481`, terminal acceptance is
already strict: it requires Synced, Healthy, Succeeded, a safe completed
operation, and exact source/destination binding. Preserve these checks.

At `internal/server/self_manage_migration.go:484-498`, metadata is considered
clean only when there is exactly one platform label and exactly two platform
annotations. Transient Argo annotations therefore trigger normalization. Do
not broaden the permanent metadata allowlist merely to hide the race; establish
the active-operation write barrier first.

At `internal/server/self_manage_argocd.go:101-116`, reconciliation runs
immediately and then every 30 seconds. A normal multi-wave sync therefore
overlaps multiple self-manager passes by design; timing the sync to finish
inside one interval is not an acceptable fix.

### Live failure facts to reproduce without storing sensitive state

The final controlled attempts established all of the following:

- The Argo preflight Job reached Kubernetes `Complete=True`.
- The Application remained in `status.operationState.phase=Running`, waiting
  for that Job or a later wave.
- Managed-fields chronology showed an Argo controller write followed by an
  Astronomer server full-object write during the operation window.
- The durable Astronomer row had `attempt_count=1`; the earlier duplicate-sync
  defect was not recurring.
- Protected Secret and PVC checks did not report mutation.
- The failure trap returned the Application controller to zero and left zero
  local operations running.

Do not persist raw Application YAML, Helm values, Secret data, tokens, login
responses, or unfiltered controller logs as evidence.

## Design decision

Implement a state-aware **Application write barrier**, not a timer, retry,
cache flush, controller restart, or test-only workaround.

### Required state classification

Add a small pure helper in `self_manage_migration.go` that classifies the
acceptance operation independently of metadata cleanliness. The helper should
return enough information to distinguish:

- `idle`: no top-level operation and no `Running`/`Terminating` status;
- `active-safe`: staged spec/hash binding is exact and the operation is a
  validated non-pruning, non-force, non-dry-run operation for the expected
  revision;
- `active-unsafe`: an operation is active but its contract is destructive,
  malformed, missing required evidence, or revision-mismatched;
- `terminal`: status phase is `Succeeded`, `Failed`, or `Error`.

For active status, read the operation in this order:

1. top-level `.operation`, when still present;
2. `status.operationState.operation`, after Argo has consumed the request.

Use the existing `selfManagedAwaitingOperationSafe` validation rules rather
than creating a weaker duplicate. Refactor that helper if necessary so
“operation absent” is not silently equivalent to “operation present and safe.”

### Required write behavior

When all of the following are true:

- phase annotation is `awaiting-approval`;
- spec-hash annotation matches the freshly computed desired hash;
- current spec exactly equals `stagedSelfManagedSpec(desiredSpec)`;
- status or top-level state identifies an active operation;
- the active operation passes the existing non-pruning safety contract;

then return without calling Application `Create`, `Update`, `Patch`, or
`Delete`. Do not remove transient annotations or alter finalizers during this
window. Metadata normalization is deferred until Argo records a terminal
phase.

For an `active-unsafe` or active-but-unverifiable operation, fail closed. Do
not approve it and do not perform a concurrent full-object rewrite merely to
make progress. Return a specific error instructing the operator/reconciler to
quiesce the Argo controller before sanitation. Preserve the existing stopped-
controller scrub path for legacy unsafe state.

After a terminal `Succeeded` phase, the existing exact acceptance checks may
run. A metadata repair or activation update must start from a fresh GET and
retain terminal status until approval validation finishes. Failed/Error
operations remain awaiting approval and must not arm automation.

### Explicit non-solutions

Do not “fix” this by:

- increasing the 30-second self-management period;
- shortening or skipping the preflight Job;
- flushing Redis/repo-server caches;
- restarting Argo during acceptance;
- patching Job annotations to wake Argo;
- manually removing `status.operationState`;
- directly setting a durable operation row to completed;
- weakening `selfManagedAcceptanceStatusReady`;
- permitting prune or force during acceptance;
- globally ignoring Application differences or status;
- enabling the Application CRD status subresource as part of this patch.

A future status-subresource migration may be architecturally desirable, but
the current legacy scrub explicitly depends on its absence. That migration
requires its own compatibility design and is out of scope for 0.3.0 closure.

## Commands the executor will need

| Purpose | Command | Expected on success |
|---|---|---|
| Focused self-management tests | `go test ./internal/server -run 'TestSelfManaged.*(Operation|Acceptance|Approval|WriteBarrier)' -count=1` | exit 0; all selected tests pass |
| Focused race tests | `go test -race ./internal/server -run 'TestSelfManaged.*(Operation|Acceptance|WriteBarrier)' -count=20` | exit 0 in all 20 repetitions |
| Complete server package | `go test ./internal/server -count=1` | exit 0 |
| Complete chart contracts | `go test ./deploy -count=1` | exit 0 |
| Backend enterprise gate | `VERIFY_ARTIFACT_DIR=/tmp/astronomer-verify-003-backend ./scripts/verify-enterprise.sh backend` | exit 0; standard and race suites green |
| Helm enterprise gate | `VERIFY_ARTIFACT_DIR=/tmp/astronomer-verify-003-helm ./scripts/verify-enterprise.sh helm` | exit 0; dependency, lint, renders, and contracts green |
| Full enterprise gate | `VERIFY_ARTIFACT_DIR=/tmp/astronomer-verify-003-all ./scripts/verify-enterprise.sh all` | exit 0; backend, frontend, Helm, API contracts green |
| Dirty-tree check | `git status --short` | only the files explicitly allowed by this plan appear before commit; empty after the final commit |

Artifact directories must be private and excluded from git. Never attach raw
login responses, tokens, Application values/status, or Secret data.

## Scope

### In scope

- `internal/server/self_manage_migration.go`
- `internal/server/self_manage_migration_test.go`
- `internal/server/self_manage_argocd.go` only if a named sentinel error or
  bounded observability hook is required
- `deploy/chart/values.yaml` only to reconcile the current Argo resource-update
  claim with measured behavior
- `deploy/chart/README.md` and
  `deploy/chart_operational_contract_test.go` only if the chart contract is
  clarified or changed by measured evidence
- `scripts/validate-live-self-management.sh` (new committed, redacting live
  harness)
- `docs/runbooks/self-manage-secret-migration.md` or a new focused
  self-management acceptance runbook
- `advisor-plans/002-enterprise-grade-closure-and-rancher-day2-assurance-plan.md`
  and `advisor-plans/README.md` for final status reconciliation

### Out of scope

- cluster provisioning, machine/node lifecycle, Fleet compatibility
- generic Argo application CRUD or non-local Argo instances
- changing the Argo CD dependency version
- changing the Application CRD to add a status subresource
- weakening Secret retention, PVC protection, spec-hash approval, source
  redaction, or at-most-once operation semantics
- redesigning the durable Argo operation schema
- unrelated frontend, agent, worker, catalog, monitoring, or backup features
- publishing images, pushing branches, or opening a PR without explicit
  operator instruction

## Git workflow

- Continue on `advisor/002-integration` or create
  `advisor/003-self-manage-write-barrier` from `5915bc6` if isolation is
  required.
- Match the repository’s conventional commits. Examples:
  `fix(self-manage): preserve safe acceptance syncs` and
  `fix(argocd): make async operations at-most-once`.
- Prefer one implementation/test commit and one documentation/evidence commit.
- Do not commit private live artifacts or `/tmp` helper files.
- Do not push or open a PR unless the operator asks.

## Implementation phases and tasks

### Phase 0: Re-establish safe containment and a clean baseline

1. Confirm the disposable cluster context explicitly; never rely on whichever
   context happens to be current.
2. Confirm `astro-argocd-application-controller` desired replicas are zero.
3. Confirm there are zero `argocd_operations` rows in `running` state.
4. Confirm the self-managed Application remains `awaiting-approval` and has no
   approved hash. Read only phase/revision/hash-presence fields; do not dump
   values.
5. Confirm all management workloads are Ready and the database schema is
   current.
6. Run the focused and complete server tests before editing. Record only
   command, exit code, test count, and duration.

**Verify**:

~~~bash
git status --short
go test ./internal/server -count=1
kubectl --context <disposable-context> -n astronomer \
  get statefulset astro-argocd-application-controller \
  -o jsonpath='{.spec.replicas}{"\n"}'
~~~

Expected: clean tree, tests pass, controller output is `0`.

### Phase 1: Add failing characterization tests before changing logic

Extend `TestSelfManagedApplicationRequiresMatchingApprovalAndThenIsNoOp` only
if it remains readable; otherwise split focused cases into a new table-driven
test in `self_manage_migration_test.go`.

Add these cases:

1. **Top-level active safe operation** — exact staged spec/hash, safe
   top-level non-pruning operation, transient Argo annotation. Two consecutive
   reconciliations must produce zero dynamic-client Application write actions.
2. **Status-only active safe operation** — top-level operation absent,
   `status.operationState.phase=Running`, safe operation stored under
   `status.operationState.operation`, exact staged spec/hash, transient
   annotation. Reconcile must produce zero writes and preserve the complete
   status object byte-for-byte.
3. **Terminating safe operation** — same as above with phase `Terminating`;
   zero writes.
4. **Unsafe active prune** — `prune=true` must never be treated as safe or
   approved. The test must assert fail-closed behavior and must not silently
   convert it into active automation.
5. **Unsafe force/dry-run/revision mismatch** — each remains rejected by the
   existing validator.
6. **Missing active-operation evidence** — phase Running with neither a valid
   top-level nor status operation must return the explicit fail-closed result;
   it must not normalize metadata.
7. **Terminal success** — once status becomes Succeeded/Synced/Healthy with
   exact compared/result source and destination, approval proceeds exactly as
   today.
8. **Terminal failure** — Failed/Error remains awaiting approval and never
   arms automated prune/self-heal.
9. **Fixed point** — after terminal metadata normalization, the next reconcile
   performs no write.

Do not assert only that `.operation` remains present. Assert **zero Update or
Patch actions** during active-safe cases; this is the invariant that prevents
status clobbering.

**Verify before implementation**: the new active-status/transient-metadata
test must fail against `5915bc6` because the code performs the update at lines
265–268. If it unexpectedly passes, STOP and re-check the live root-cause
model.

### Phase 2: Implement the operation classifier and write barrier

1. Refactor operation detection so absence, active-safe, active-unsafe, and
   terminal states are distinct.
2. Reuse `selfManagedAwaitingOperationSafe` for operation payload validation.
   Do not copy a reduced set of checks.
3. Bind active-safe classification to all of:

   - exact staged spec;
   - awaiting phase annotation;
   - exact desired spec hash;
   - expected chart revision;
   - prune false;
   - force false;
   - dry-run false;
   - empty/approved sync options only;
   - acceptable initiator metadata.

4. Insert the no-write return before metadata-cleanliness repair and before
   the generic restage path.
5. Ensure the no-write branch also applies after Argo consumes top-level
   `.operation` and only status evidence remains.
6. Do not clean transient Argo annotations during the active interval. Update
   the current test at lines 135–150, which presently expects normalization;
   it must instead expect preservation and zero writes until terminal state.
7. Keep active-unsafe behavior fail closed. If safe sanitation cannot occur
   without racing Argo, return a specific operator-action error and require a
   quiesced controller.
8. Add a concise comment at the write barrier explaining the missing status
   subresource and single-writer interval. Do not describe it as a timing
   workaround.

**Verify**:

~~~bash
go test ./internal/server \
  -run 'TestSelfManaged.*(Operation|Acceptance|Approval|WriteBarrier)' \
  -count=1
go test -race ./internal/server \
  -run 'TestSelfManaged.*(Operation|Acceptance|WriteBarrier)' \
  -count=20
~~~

Expected: all new cases pass; no active-safe case records an Application
Update/Patch/Delete action.

### Phase 3: Audit every Application write boundary

Review every `res.Create`, `res.Update`, `res.Patch`, and `res.Delete` in the
self-management path. Produce a short code comment or test mapping each write
to one legal ownership state:

| Write | Legal owner/state |
|---|---|
| First staged create | controller quiesced, complete rollout, adoption snapshot verified |
| Unsafe legacy full-object scrub | controller quiesced, CRD status-subresource contract verified |
| Desired-spec restage | no active Argo operation; rollout and adoption snapshot verified |
| Unsafe-operation sanitation | explicit fail-closed/quiesced path |
| Terminal metadata repair | terminal state only; fresh resourceVersion |
| Activation | exact approved hash plus terminal acceptance evidence |

Add tests proving no other state writes the Application. Kubernetes
resourceVersion conflicts must be returned/retried through a fresh
reconciliation; never retry an Update using a stale object without re-running
the state classifier.

Do not introduce an unconditional retry loop around `Update`. A conflict means
Argo wrote new evidence and the entire decision must be recomputed from a new
GET.

**Verify**:

~~~bash
rg -n 'res\.(Create|Update|Patch|Delete)' \
  internal/server/self_manage_migration.go
go test -race ./internal/server -count=1
~~~

Expected: every write matches the table and the package race suite passes.

### Phase 4: Reconcile Argo hook and resource-update configuration with evidence

Keep the wave `-5` prerequisite/Job co-location unless a zero-Astronomer-write
test proves a separate Argo defect. The current chart correctly orders
NetworkPolicy, ServiceAccount, RBAC, then Job by Argo kind ordering.

The comment at `deploy/chart/values.yaml:1210-1215` currently implies removing
the catch-all status suppression is sufficient to observe hook completion.
Live testing did not isolate that claim because the server continued writing
the Application. After Phase 2:

1. Run one controlled sync with the current targeted resource-update settings.
2. Prove the server performs zero Application writes while phase is Running or
   Terminating.
3. If the hook completes and Argo advances normally, retain the targeted
   optimization and rewrite the comment/docs to state its measured role
   accurately.
4. If the hook still stalls while zero server writes are proven, STOP. Treat
   that as a separate Argo cache/hook defect. Do not add
   `resource.ignoreResourceUpdatesEnabled=false`, replace the hook, or weaken
   preflight without a focused reproduction and controller-load assessment.

Any chart change requires a render contract proving exact annotations and an
explicit performance trade-off. Disabling resource-update optimization
globally is not an unreviewed quick fix for a correctness problem.

### Phase 5: Commit a deterministic, redacting live-acceptance harness

Create `scripts/validate-live-self-management.sh`, modeled after the existing
`scripts/validate-live-argocd*.sh` conventions. It must accept an explicit
Kubernetes context and refuse to run without a disposable-cluster
acknowledgement.

The harness must:

1. Use `set -Eeuo pipefail`, private `0700` output directories, `0600` files,
   and an EXIT trap.
2. On every failure, stop the Application controller, wait for controller Pods
   to terminate, and report any still-running durable operation without
   changing it to success.
3. Verify controller zero, zero running operations, awaiting approval, exact
   target revision, absent approval, complete rollout, and current schema.
4. Capture only protected-resource names, UIDs, key **names**, and whole-object
   digests; never emit values.
5. Capture PVC name, UID, requested size, storage class, and phase.
6. Authenticate through the public API using private temporary files. Delete
   password, token, and login-response files immediately after the operation
   is created.
7. Scale the controller to one and wait for both Kubernetes readiness and
   Argo’s initial cluster cache/list-watch readiness before submitting sync.
   Prefer an observable readiness condition over a fixed sleep.
8. Submit exactly one non-pruning sync through Astronomer’s API.
9. After creation, poll the durable row or reconnect the API transport so the
   harness survives the expected server Deployment rollout. A one-shot
   `kubectl port-forward` tied to a replaced Pod is not sufficient.
10. Record operation ID, phase, status, attempt count, poll count, and bounded
    timestamps only.
11. Assert exactly one upstream-call event and one successful preflight Job
    UID/lifecycle.
12. While phase is active, sample only safe Application metadata and
    managed-field manager/timestamp evidence. Fail if manager `server` writes
    the Application during the active interval.
13. Require completed/Succeeded, Application Synced/Healthy, exact
    source/destination/revision/hash binding, and no running operation.
14. Compare Secret/PVC evidence byte-for-byte.
15. Apply approval by copying the exact current hash; never patch sync policy
    directly.
16. Require transition to active and reviewed automated prune/self-heal.
17. Verify zero running operations, no operation replay, workloads Ready,
    schema current, and controller desired replicas one only on GO.
18. Emit a minimal `RESULT.txt` containing GO/NO-GO and non-sensitive counts.

The harness must never use Redis flushes, manual Job annotations, controller
restarts during sync, Application status removal, direct successful database
updates, or raw Application dumps. A pass requiring any of those is invalid.

### Phase 6: Run static verification before rebuilding images

Run focused tests first, then the authoritative backend and Helm scopes. Only
after source is stable should the full enterprise gate run. Do not rebuild
images after every test iteration.

Required order:

1. focused server tests;
2. server race repetitions;
3. full server package;
4. deploy/chart contracts;
5. backend enterprise gate;
6. Helm enterprise gate;
7. full enterprise gate.

Every command must exit zero. Preserve only redacted summaries and failure
logs that contain no credentials or raw values.

### Phase 7: Build one immutable 0.3.0 candidate

After all static gates pass and the tree is committed:

1. Record the exact full commit SHA.
2. Build server and migrate from that same SHA and version 0.3.0. Build all six
   release images once for final promotion evidence.
3. Verify OCI `version`, `revision`, and `created` labels for every image.
4. Use one immutable tag derived from the commit for each component; no mixed
   source revisions in the final candidate.
5. Generate private SPDX SBOM evidence.
6. Import the immutable images immediately before deployment.
7. Deploy with the controller fenced at zero until staged-state validation
   completes.

The `.dockerignore` fix at commit `59f968a` must remain: root image build
context should stay near the actual source size and must not include nested
frontend `node_modules` or `.next` artifacts.

### Phase 8: Execute three clean live runs plus failover

Section 26.7 of Plan 002 remains authoritative. For this defect, additionally
require:

#### Run 1 — clean single-controller acceptance

- Fresh disposable cluster or fully reset documented baseline.
- One API request, one upstream sync, one preflight lifecycle.
- Zero server Application writes during Running/Terminating.
- Operation completed/Succeeded inside the declared SLO.
- Application Synced/Healthy, then exact approval and active phase.
- Protected Secrets/PVCs unchanged.

#### Run 2 — three-server reconciliation pressure

- Three Astronomer server replicas.
- Allow at least three 30-second self-management ticks during the operation.
- Prove every tick is read-only for the active Application.
- Attempt count remains one; poll claims remain HA-safe.

#### Run 3 — forced server failover

- Delete the server Pod that accepted or was polling the operation after
  upstream acceptance.
- Durable polling resumes on another replica without replaying sync.
- Application write barrier remains intact.
- Operation and approval converge normally.

Each run must start from a clean immutable deployment and must pass without
manual intervention. A retry after modifying live state is a new run, not a
continuation of the failed run.

### Phase 9: Reconcile documentation and release status

Only after all three runs pass:

1. Update Plan 002 Section 26 with the final commit and redacted evidence
   locations.
2. Mark Plan 003 DONE in `advisor-plans/README.md`.
3. Reconcile chart documentation so hook/resource-update claims match measured
   behavior.
4. Record 0.3.0 as build-verified and live-accepted.
5. Leave the controller at one and verify zero running operations.

If any run fails, keep Plan 003 BLOCKED, return the controller to zero, and do
not call 0.3.0 enterprise-ready.

## Test plan

### Unit tests

Add or refactor tests in `internal/server/self_manage_migration_test.go` using
the existing dynamic fake structure at lines 100–235.

Required assertions:

- exact active-safe classification from top-level operation;
- exact active-safe classification from status-only operation;
- Running and Terminating write barrier;
- zero dynamic write actions across repeated reconciliations;
- byte-for-byte status preservation;
- transient metadata retained during active ownership and normalized only
  after terminal ownership returns to Astronomer;
- unsafe prune/force/dry-run/revision mismatch rejected;
- missing active evidence rejected;
- Succeeded exact acceptance activates only after exact approval;
- Failed/Error never activates;
- terminal metadata repair is a fixed point;
- conflict requires fresh GET/reclassification rather than blind retry.

### Chart contract tests

Retain and run the existing preflight ownership/order contracts. If Phase 4
changes configuration, add an assertion for the exact rendered `argocd-cm`
key/value and document why the performance/correctness trade-off is bounded.

### Live tests

The committed harness is mandatory. Unit mocks cannot reproduce Argo’s
full-object/status ownership timing. All three live runs must assert the
single-writer interval directly.

## Definition of done

All boxes must be checked:

- [ ] Active acceptance state distinguishes top-level and status-only operations.
- [ ] A safe Running/Terminating operation causes zero Astronomer writes to the Application.
- [ ] Unsafe or unverifiable operations fail closed and never arm automation.
- [ ] Terminal Succeeded evidence remains exact and approval-gated.
- [ ] Failed/Error operations remain awaiting approval.
- [ ] Focused race tests pass 20 consecutive runs.
- [ ] Full backend, frontend, Helm, and API-contract enterprise gates pass.
- [ ] Final six images share version 0.3.0 and one exact source revision.
- [ ] One request produces one upstream sync event and `attempt_count=1`.
- [ ] Each live run has one successful preflight lifecycle and no stuck hook.
- [ ] Application reaches Synced/Healthy and then active through exact-hash approval.
- [ ] Three consecutive clean live runs pass, including three-server pressure and failover.
- [ ] Protected Secret and PVC identity/capacity evidence is unchanged.
- [ ] No raw Secret, token, password, Helm values, or Application status is committed or emitted.
- [ ] Failure cleanup leaves controller zero and zero running operations; GO cleanup leaves controller one and zero running operations.
- [ ] Plan 002 and the advisor index are reconciled.

## STOP conditions

Stop, contain, and report if any of the following occurs:

1. The new characterization test does not reproduce an Application write at
   `5915bc6`; the root-cause assumption must be revisited.
2. Correctness requires changing the Application CRD status-subresource
   contract.
3. A safe active operation cannot be proven from either top-level or status
   operation evidence.
4. Any implementation writes the Application during Running/Terminating.
5. Conflict handling retries without a fresh GET and full reclassification.
6. One API request produces more than one upstream mutation.
7. Argo preflight still stalls after zero server writes are proven.
8. Acceptance requires cache flush, manual Job mutation, controller restart,
   status removal, direct terminal database writes, or skipped hooks.
9. Approval arms prune/self-heal without exact Succeeded/Synced/Healthy and
   source/destination/hash binding.
10. Any protected Secret or PVC is deleted, replaced, resized, reclassed, or
    made prunable.
11. Any credential-shaped material appears in logs, artifacts, plans, or test
    output.
12. Full enterprise verification is red.
13. Final images have mixed revisions, mutable tags, missing labels, or
    unreviewed schema skew.

## Rollback and containment

The source rollback unit is the complete Plan 003 implementation commit; do
not cherry-pick only tests or chart configuration around the write barrier.

For any live failure:

1. scale `astro-argocd-application-controller` to zero;
2. wait for every controller Pod to terminate;
3. stop the harness and port-forward processes;
4. query safe durable operation fields and report running rows;
5. terminalize only the failed test operation through the supported failure
   path—never mark it completed;
6. do not remove Application status, hook finalizers, or cache data as part of
   release evidence;
7. verify workloads, protected Secret UIDs/key names, PVC identity, schema,
   and Helm revision;
8. roll Helm back to the last immutable accepted revision if workload health
   changed;
9. leave the controller zero until a new reviewed candidate is ready.

## Maintenance notes

- The write barrier is a single-writer ownership rule. Any future code that
  adds Application metadata, finalizers, annotations, or status cleanup must
  route through the same state classifier.
- If the bundled Argo CRD later gains a status subresource, re-evaluate the
  legacy credential scrub and replace full-object status removal with an
  explicit migration. Do not silently retain both models.
- Reviewers should scrutinize “helpful” metadata cleanup during active phases;
  that is the exact behavior this plan removes.
- Keep the Argo preflight and Redis bootstrap ownership tests. They guard
  separate lifecycle defects already found during live adoption.
- The Rancher-relative bar here is deterministic day-2 operation ownership,
  HA recovery, auditability, and rollback—not cluster provisioning or Fleet
  compatibility.

