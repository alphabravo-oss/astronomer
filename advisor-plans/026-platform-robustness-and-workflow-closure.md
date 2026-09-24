# Plan 026: Platform robustness and workflow closure

Status: IN PROGRESS — implementation authorized by the user on 2026-09-22.
Base: `22f633ec`, branch `advisor/ui-overhaul-integration`, plus the 21 preserved
staged cleanup files recorded in the UI validation ledger.

Latest handoff (2026-09-23): pinned full Local CI run 62 **passed 25/25 lanes**,
including the final aggregate. Both original K3s nodes are Ready; the main
service and its workloads are unchanged. Local qualification/handoff items
27 and 32 are verified. Remaining partial, decision and external items below
remain open; this is not a declaration that the entire platform plan is done.
The user requested stopping after this run, with no further implementation batch.

## Objective and boundaries

Close the code-addressable findings from the expanded UI, integration, agent,
and delivery review. Preserve existing staged work. Plan 016 remains the
authority for external release qualification; this plan does not authorize
deployment, pushing, destructive infrastructure tests, or release approval.
Astronomer adopts existing clusters and uses Flux for downstream delivery.
Authorization, tenant scope, mandatory audit, secret redaction, and durable
mutation intent must not be weakened to make a UI workflow convenient.

Statuses below mean OPEN, PARTIAL (remaining scope listed below),
IMPLEMENTED (source changed, verification pending),
VERIFIED (named local checks passed), EXTERNAL (real environment/human evidence),
or DECISION (a product/contract decision is required). No local test is a
substitute for an EXTERNAL item. Record partial implementation explicitly.

## Phase A — Stop incorrect writes and broken agent communication

| ID | Finding and implementation | Acceptance | Status |
|---|---|---|---|
| 01 | Bound nested system resource inventories, preserve aggregate totals, disclose truncation, and isolate optional observations from delivery communication. | 128/129 and large inventories produce valid state requests/status; deterministic samples; no invented totals. | VERIFIED |
| 02 | Preserve Secret/ConfigMap/downward-API environment references when editing literal variables. | Mixed env edits preserve reference entries and order; UI regression. | VERIFIED |
| 03 | Replace volume sources explicitly and maintain mount references; remove fixed-position env-source assumptions. | Existing emptyDir/PVC/custom volume edits remain valid and preserve unrelated entries. | VERIFIED |
| 04 | Accept valid named HTTP/TCP probe ports. | Numeric bounds and named-port cases pass; invalid names fail. | VERIFIED |
| 05 | Reject malformed optional JSON bodies in agent upgrade/plan, Helm upgrade/rollback, and Vault probe. | Empty optional body accepted; malformed/type-invalid/trailing bodies rejected before effects. | VERIFIED |
| 06 | Remove unsupported HA claims from distribution/object-count observations. | K3s version and Gateway count do not imply HA; unknown/not-applicable stays explicit in UI. | VERIFIED |

## Phase B — Truthful operational state and complete selection

| ID | Finding and implementation | Acceptance | Status |
|---|---|---|---|
| 07 | Distinguish CVE unknown/loading/denied/unscanned/stale from measured zero. | Denied/missing scans never show a clean zero. | VERIFIED |
| 08 | Tool catalog/status/progress query states and recovery. | Unavailable status never means uninstalled or indefinitely queued; cached denied reads hidden. | VERIFIED |
| 09 | Security policy/template query states and unknown policy levels. | No synthetic privileged fallback; missing dependencies and mutation permissions explicit. | VERIFIED |
| 10 | Wire agent search through a bounded canonical API query and query key. | Search reaches agents beyond the first page and resets pagination. | VERIFIED |
| 11 | Agent diagnostics/history query states and Self-test update gate. | 403/404/offline display actionable states; read-only users cannot invoke Self-test. | VERIFIED |
| 12 | Replace silently capped estate lists and target pickers with server pagination/remote search. Preserve continuation metadata for policies/templates. | Targets beyond the first page reachable; error and loading states distinct; no unbounded eager fetch-all. | PARTIAL |
| 13 | Project-member metric truth and permission-aware subject names. | No false zero/stale count on denied binding reads; no required global user enumeration. | VERIFIED |
| 14 | Preserve all six registration privilege profiles. | Resumed operator/namespace/custom drafts retain profile; privileged options explain risk. | VERIFIED |
| 15 | Scope logging destinations to selected cluster and invalidate incompatible choices. | Cross-cluster outputs cannot be selected; switching scope clears stale destination selections. | VERIFIED |
| 16 | Make unsupported GitOps registry/tool presets fail explicitly rather than imply applied desired state. | Validation/status names unsupported fields before mutation; supported adoption unchanged. Durable preset reconciliation is a separately designed extension. | VERIFIED |

## Phase C — Complete safe operator workflows

Plan 025 excerpts are stale against this integrated tree. Reconcile against
live implementations; close Phase A data-loss bugs before expanding forms.
Retain server dry-run/diff and existing per-object authorization/audit paths.

| ID | Finding and implementation | Acceptance | Status |
|---|---|---|---|
| 17 | Indexed array editors for containers/init containers, ports/env/envFrom/mounts/volumes, rules, paths, tolerations and minimal affinity. | Characterization first; sibling preservation; add/remove/edit round trips; accessible labels. | VERIFIED |
| 18 | Namespace grouping and bounded sequential bulk restart/scale. | Persist grouping; preserve virtualization and page semantics; per-object authorization, confirmation, and partial results. | VERIFIED |
| 19 | Shared Clone/Download YAML actions for remaining resource-table families. | Live resource fetch, permission gates, sanitized clone metadata, correct discovered GVK and scope. | VERIFIED |
| 20 | Bounded discovery-aware related-resource navigation. | Owners resolved by kind/group/name/namespace (never UID GET); common child relationships; denied/error not empty. | PARTIAL |
| 21 | Project-authorized cross-cluster delivery lists instead of redirect-only global lists. | Canonical existing project APIs; pagination; no cross-tenant aggregation or unbounded fan-out. | VERIFIED |
| 22 | Self-contained rollout navigation. | Preserve project context in emitted links; a project-agnostic lookup requires a documented authorization-safe contract, not blind project scanning. | PARTIAL |
| 23 | Native RBAC grant management UI on canonical API. | List/create/delete with scope validation, permissions, confirmation, feedback and negative tests. | VERIFIED |
| 24 | Metadata-only certificate expiry and renewal visibility. | Additive generated contract if required; no private material; unknown/freshness explicit; boundary tests. | VERIFIED |
| 25 | Agent effective-privilege drift hardening. | Do not equate declared profile with measured authority; validate live-probe permissions and bounded result before claiming enforcement. Refusal semantics require explicit design. | PARTIAL |
| 26 | Shared primitives, mutation feedback, focus and recovery consistency. | Reduce existing debt without relaxing ratchets; test behavior rather than markup counts alone. | PARTIAL |

The `questions.yaml` item in Plan 025 remains a design-only ADR. No new chart
ingest/storage/runtime feature is authorized by that spike alone.

## Phase D — Verification and reproducible handoff

| ID | Work | Acceptance | Status |
|---|---|---|---|
| 27 | Diagnose the previous full Local CI Charlie failures and rerun representative/full local gates. | Root cause/evidence retained; no retries/quarantine/timeouts added to conceal failure. | VERIFIED |
| 28 | Add negative and boundary tests for every changed behavior. | Loading/403/404/offline/stale and beyond-first-page cases; mutation rejection before effects. | PARTIAL |
| 29 | Live agent identity and Charlie qualification. | Explicit context/credentials and retained exact-candidate runs; mocked success not sufficient. | EXTERNAL |
| 30 | Eight Plan 016 GA qualifications. | Adopted cloud, mandatory audit load, sizing, paired benchmark/human study, assistive technology, scale, DR rehearsal, protected approval. | EXTERNAL |
| 31 | Protect/improve eager bundle headroom. | Production build, CSP and bundle budgets pass unchanged; new substantial UI stays lazy. | VERIFIED |
| 32 | Reproducible source/evidence/promotion boundary. | Inventory preserved changes, format/stage only owned edits, report exact checks and outstanding work. Commit/push/deploy only with applicable authority. | VERIFIED |

## Verification sequence

1. Focused failing regression/characterization tests before each fix.
2. Focused Go/frontend tests after each phase; normal and race checks for touched
   agent/runtime behavior.
3. Run generators from canonical sources for SQL/OpenAPI changes; verify clean
   second generation. Never hand-edit generated artifacts.
4. Frontend type-check, lint, all unit tests, production build/bundle/CSP;
   relevant browser negative-path and keyboard checks against one immutable
   build. Do not overlap host-browser tests with Docker network changes.
5. Go vet and applicable enterprise backend/frontend/Helm gates, docs,
   dependency boundaries, raw transport, migration, complexity and staged format.
6. Local CI representative then full matrix when representative passes and host
   prerequisites permit. Record failures honestly and diagnose within scope.
7. Update this ledger with concrete evidence and remaining decisions/external
   prerequisites. A partial phase remains partial.

## Execution log

- 2026-09-22: Plan created before implementation. Existing 21 staged cleanup
  paths preserved. The preceding read-only audit reproduced env-reference loss,
  volume-source corruption and named-port rejection; 57 existing focused tests
  passed but did not cover those regressions.

## Implemented code and verification scope

- Agent delivery: deterministic 128-resource/128-volume/32-image samples retain
  aggregate observations and disclose truncation. Invalid optional observations
  become explicit unavailable records rather than invalidating delivery state.
  Certificate status exposes expiry/renewal metadata, never Secret contents.
- Form preservation: indexed regular/init containers, mixed env references,
  envFrom, volume-source replacement and mount references; numeric/named probes;
  all Service ports and all container images/probes validated. Guided RBAC rules,
  Ingress paths, tolerations and required node-affinity expressions are editable.
- Operational truth: unknown/denied/stale vulnerability metrics; workload-only
  redundancy language; tool/security/agent diagnostic query-state recovery;
  mutation permission gates; all six registration profiles retained.
- Integration boundaries: strict optional request bodies across five endpoints;
  agent search is canonical, bounded and cursor-bound; unsupported GitOps
  registries/tool presets reject the entire source before applying any cluster.
- Lists and selection: server-paged estates, agents, security policies/templates,
  native grants; remotely searched project/cluster/user targets; manually paged
  logging outputs scoped to cluster; namespace errors cannot mean an empty list.
- Operator workflows: persisted namespace grouping; up to 50 sequential audited
  workload actions with confirmation, per-target outcomes and stop-on-uncertainty;
  shared live YAML export/sanitized cloning; bounded owner/child navigation;
  real project-scoped global delivery lists; native RBAC grant create/delete.
- Performance: guided form code is lazy in create/edit dialogs. Bundle budgets,
  test deadlines and retry policy are unchanged. Vitest worker fan-out is capped
  at four after memory contention caused unrelated worker/import timeouts.

No generated wire fields were added for certificate metadata: it uses existing
resource detail fields. Agent search changed the canonical OpenAPI source and
regenerated the embed, frontend types and Go SDK. Complexity baselines only
tighten deliberately reduced existing oversized units.

## Remaining scope — do not mark the platform complete

| Priority / IDs | Remaining work and acceptance |
|---|---|
| P1 / 12 | RBAC users/roles/bindings, binding role/project choices, visible-binding names, monitoring storage selection, project Members and SSO mapping roles now use bounded pagination or exact-ID lookup. Project counts use canonical metadata; SSO choices match the mapping scope and reset on scope changes. Audit names use exact visible-ID lookups; Audit cluster/project filters page remotely with known-ID entry, and CIS history uses canonical 25-row pages. Audit other legacy selectors rather than claiming the entire repository is uncapped. Continuation verification is recorded below. |
| VERIFIED / 17 | Preferred node affinity, pod affinity/anti-affinity and Ingress resource/default backends now have indexed guided editors, union validation and preservation fixtures. Final-source frontend and browser qualification passed below. Untouched advanced fields remain preserved, with server dry-run authoritative; this does not claim a complete Kubernetes schema editor. |
| POLICY / 19 | Clone/download now covers built-in table families and custom-resource instances using exact group/version/plural/scope paths. Nodes, events, PVs and CRD definitions deliberately cannot be cloned; PVC cloning creates a claim, not a data copy. |
| FOLLOW-UP / 20 | Common direct ownership chains and both Pod-to-Service and Service-to-Pod navigation are bounded and paged; discovery-denial browser coverage is present. An exhaustive Kubernetes relationship graph remains outside this bounded implementation. |
| DECISION / 22 | Links emitted by delivery preserve project context. A bare entity ID without a project prompts project selection. Automatic project-agnostic resolution needs a documented server-side authorization-safe lookup, not scanning every project. |
| DECISION / 25 | Declared registration profile is explicitly not measured authority. A live effective-privilege probe and mutation-refusal policy require a bounded protocol, caller/probe authorization, freshness rules, upgrade compatibility and operator recovery semantics before enforcement. No new live privilege claim is made. |
| P1 / 26, 28 | Native grant create/delete denial and recovery, bulk partial/unconfirmed/cancelled outcomes, and denied relationship continuation have desktop/mobile workflow coverage. Virtualized grouping now has a separate three-viewport component suite plus a real CIS filtering regression. Extend the remaining loading/403/404/offline/stale cross-product; assistive-technology qualification remains separate. Canonical shared-table callers distinguish count certainty, preserve continuation for short pages and retain Previous on empty later pages. CVE detail preserves pagination/count certainty, context resets and denied-page recovery. |
| VERIFIED / 27, 32 | Full pinned Local CI run 62 passes all 25 lanes, including the aggregate, after the documented representative/diagnostic iterations. Earlier failures and cancellations remain retained, not relabeled. Exact source identity and restoration evidence are recorded below. This does not authorize promotion or replace protected external qualification. |
| EXTERNAL / 29 | Real agent identity and Charlie compatibility against an explicitly selected environment/credentials; do not use mocked browser success as evidence. |
| EXTERNAL / 30 | All eight Plan 016 protected/live/human qualifications remain separate from this local implementation. |
| DESIGN ONLY | Plan 025 questions.yaml/chart ingest remains an ADR/spike, not a shipped runtime capability. |

## Evidence ledger

Evidence directory for this task:
`/var/tmp/astronomer-plan026.VQnCQL` (local, not committed).

Completed before the final frozen-tree gates:

- Focused Go packages: agent/delivery, handler, gitops and worker/tasks passed.
  Go vet passed for internal/... and cmd/...; focused race checks passed.
- Optional request route regressions passed for agent upgrade/plan, Helm
  upgrade/rollback and Vault probe, including malformed, null, wrong types,
  trailing documents and unknown fields with no queued mutation/audit/probe.
- Inventory regressions passed at 128/129/500 resources, oversized images and
  volumes, invalid optional observations, and certificate date boundaries.
- Focused final frontend run: 4 files / 47 tests passed. Earlier dedicated
  negative/boundary runs cover diagnostics, tools, delivery scope, native grants,
  beyond-page APIs, bulk operations, cloning and guided sibling preservation.
- Full bounded-worker frontend run: 258 files / 1,587 tests passed. The final
  authoritative run must include the few subsequently added regressions.
- First production bundle check exceeded the application budget by 4,856 bytes.
  Lazy guided editors reduced it to 485,934 / 489,472 bytes, leaving 3,538 bytes
  without increasing the budget. Final build/budget rerun pending.
- Initial unbounded frontend runs failed unrelated imports/worker startup under
  memory contention. Logs are retained; no retries, quarantine or deadline
  increases were introduced. This diagnosis does not close prior Local CI
  Charlie qualification without rerunning that lane.

Final gate results will be appended after verification. Existing 21 staged
cleanup paths remain preserved; no commit, push, deployment or live destructive
qualification is authorized by this implementation request.

### Frozen backend and browser pass

- Enterprise backend passed, including full Go tests and the full race suite,
  build/vet/lint, reachable-vulnerability scan, migrations, generated SQL/OpenAPI/
  Charlie contracts and route-security checks. Evidence:
  `/var/tmp/astronomer-plan026.VQnCQL/enterprise-backend/20260922T181703Z-764635-22f633ece6a9/evidence.json`.
  The live agent-identity subgate explicitly skipped without an authorized
  context; this is not live identity qualification.
- Focused browser workflows: **50/50 desktop/mobile checks passed**, including
  persisted grouping/keyboard navigation, create/scale/restart/YAML/delete,
  resource-family detail pages and denied owner discovery. Older drilldown
  fixtures needed an exact Labels heading, a real owner API version and a
  canonical discovery response. Original failures remain in `browser-targeted.log`.
- Broad route crawl: **304/304 checks passed**, without overlapping Docker work.
  This build predates only the final overview-summary corrections below.
- Screenshot review caught empty tool status falsely reading “all installed”
  and namespace/custom profiles falling through to Admin. Both were corrected:
  unknown/denied tool state is explicit, all six declared profiles retain their
  identity, unknown profiles never default to admin, and the UI disclaims live
  authority measurement. Nine new overview regression tests passed.
- Native grant UI now also tests exact scoped creation through review/typed
  confirmation and removal through typed confirmation (five UI tests passed).
- Staged-format gate passed for 136 frontend files; original staged cleanup is
  backed up in `preserved-index.patch` and `preserved-index-paths.txt`.
- Initial enterprise frontend failed a shared-control lint rule in a new test
  double. The test now uses the shared Input; production already did. Backend
  source is unchanged after its passing snapshot. Final frontend/Local CI runs
  must include this test-only correction and this evidence update.

### Final workflow and verification hardening

- Custom-resource instance tables now use bounded 50-item server pages with
  opaque continuation, truthful query errors, and shared live YAML download /
  sanitized clone actions on exact group/version/plural/namespace paths.
  Service detail now lists selected Pods with bounded namespace pagination;
  selectorless Services do not enumerate Pods. Nine focused tests passed,
  including denied cached reads, permission gates and continuation tokens.
- Backup-drill result decoding now rejects claimed success without a result ID
  and valid chronological timestamps. The screenshot fixture's fabricated
  success/epoch result is replaced with the contract's genuine null result.
  The final focused workflow/API batch passed **18 tests in 3 files**.
- The frozen frontend enterprise run before these final additions passed:
  **260 files / 1,605 tests**, lint/type-check, production build/CSP, generated
  contracts and dependency audit. Evidence:
  `/var/tmp/astronomer-plan026.VQnCQL/enterprise-frontend/20260922T183131Z-816434-22f633ece6a9/evidence.json`.
  Its application bundle was 485,949 / 489,472 bytes (3,523 bytes headroom).
- An additional **12/12** browser checks passed on that production build for
  navigation/preferences/denials. New custom-resource/Service browser checks
  and a final frontend gate follow the later additions above.
- Local CI diagnostic run `run-1790102136108` was stopped after its backend
  evidence guard failed. All 40 backend subchecks passed, but the pinned
  runner's `chmod -R 777` changed every regular source file's executable bit;
  generators subsequently restored normal modes. Contents were unchanged
  (`git -c core.filemode=false diff` was empty), but the source identity correctly
  differed. PostgreSQL integration tests also passed before cancellation;
  this is not a passing representative run.
- The version-bound Local CI install patch changes only that private-workspace
  command to `chmod -R a+rwX`. It preserves executable identity, is idempotent,
  and rejects changed versions/ambiguous source. Two regression tests pass.
  No source hash check, CI deadline, retry or quarantine rule was relaxed.
- Visual regression baselines dated before the UI overhaul were stale. They
  were regenerated with the pinned Chromium revision 1243 after fixing the
  clock/locale/timezone. Reviewed examples cover all eight routes, both themes
  and desktop/tablet/mobile layouts. These are initial-view screenshots, not
  exhaustive below-the-fold or assistive-technology acceptance.
- A frontend enterprise attempt started during final visual regeneration was
  intentionally cancelled; it is not evidence for a frozen candidate.

### Handoff verification — 2026-09-22

- Final host frontend and Helm enterprise scopes both **passed with stable
  source identity** `5daa038f153257be281b98af7c7a981da863c85e80342ca3406da7e107ad622f`.
  Frontend: **262 files / 1,620 tests**, lint/type-check, generated contracts,
  formatter tests, build/CSP/bundle, route-tree drift and dependency audit.
  App bundle: **486,309 / 489,472 bytes** (3,163 bytes headroom).
  Manifests under the evidence directory:
  `enterprise-frontend-final-frozen/20260922T190703Z-1070925-22f633ece6a9/evidence.json`
  and `enterprise-helm-final/20260922T190722Z-1071691-22f633ece6a9/evidence.json`.
- Final custom-resource/Service browser batch: **14/14 passed**. Reviewed
  visual matrix: **48/48 comparisons passed**, plus two auth setup tests, with
  no snapshot update flag (`visual-final.log`).
- Local CI run `run-1790104120434` lost its control server following the turn
  interruption. Its container continued and recorded **37 passing backend
  subgates including full Go/race**, then lease cancellation prevented final
  evidence. The copied artifacts are in `local-ci35-backend-interrupted/`.
  This is incomplete qualification, not a passing backend job.
- Resumed representative run `run-1790104964249`: frontend enterprise **passed**
  with all 1,620 tests (the previous Charlie failures did not recur), PostgreSQL
  integrations **15/15 required tests passed with no skips**, and PostgreSQL
  failover certification **passed**. Local artifact ZIPs are retained under
  `local-ci-cache/artifacts/`: frontend `780892_blob.zip`, integration
  `525597_blob.zip`, failover `216613_blob.zip`.
- That representative run is **not green**. Backend exited 2 during the
  migration CLI `go install` before any migrations; retained output does not
  establish the underlying cause. Its expected backend evidence upload also
  failed because the enterprise gate never ran. Earlier diagnostic runs passed
  migration roundtrips; they do not replace final-candidate qualification.
- CI browser tests found **eight failures: four stale assertions on each
  viewport**. Sources is now a real project-scoped navigation link; General
  uses its own heading and tab role; ARIA grid row count includes the header;
  Pod node-link accessible names include the metric label. Corrected assertions
  preserve actual navigation, settings mutation and windowing checks.
  The complete rerun passed **138/138 desktop/mobile tests**, including visual
  comparisons and serious axe checks (`browser-complete-final.log`).
- Final-build desktop/mobile route crawl: **304/304 checks passed**
  (`browser-route-final.log`). Final assertion-file lint and frontend type-check
  passed; all **148 staged frontend files** passed formatting. Documentation,
  complexity, dependency-boundary and zero-quarantine policy checks passed;
  staged and working-tree whitespace checks were clean.
- The diagnostic CI run was stopped before disposable live-environment setup
  to correct those tests. The CLI itself aborted during interrupt cleanup;
  retained in `local-ci-representative-resumed.log`. Live E2E, remaining supply
  chain lanes and the full matrix were not qualified. No timeout, retry,
  quarantine, bundle or source-integrity threshold was relaxed.
- Only browser assertions and this documentation changed after the passing
  frozen enterprise scopes; production source remains identical. Original
  staged work is preserved. No commit, push, merge or deployment was performed.

Next execution order: diagnose the migration CLI setup failure with retained
stderr; obtain a green representative run, then the full matrix. Continue the
explicit remaining code items (legacy RBAC lists/pickers and advanced guided
unions) as focused patches. Product-contract decisions and authorized live /
protected qualification remain separate from local code completion.

### Continuation — remaining pagination and picker safety

- RBAC Users now uses 25-row server pages and server search, resetting its page
  when search changes. Global/cluster/project roles use 25-row server pages;
  unsupported global search/sort is not implied by client filtering one page.
- Bindings now page by explicit scope and resolve only unique IDs on the current
  page. Name lookup is permission-gated; failures and denied cached reads fall
  back to IDs. Removed eager capped user/role/project/cluster name maps and the
  partial synthetic global-role assignment list. Effective context names resolve
  selected IDs, with other targets retaining their IDs.
- Binding creation uses paged role and remote project pickers. Monitoring storage
  configuration selection uses 25-row pages; known IDs remain usable without
  granting backup enumeration. No unbounded fetch-all was added.
- The backend already emits pagination on all three binding lists. Corrected six
  OpenAPI response declarations (existing canonical/alias paths), regenerated
  embedded spec/frontend types/Go SDK, and preserved existing runtime contracts.
  API-contract enterprise scope passed with stable source identity; evidence:
  `continuation-api/20260922T195616Z-1390467-22f633ece6a9/evidence.json`.
- Focused pagination/name-resolution/monitoring/API tests: **73 passed**.
  Cases include roles and bindings beyond 200, search reset, exact revoke target,
  scope changes, deduplicated visible-ID lookup, no-read permission and denied
  cached reads. Full lint passed before the subsequent overlay correction.
- A failing nested-dialog regression exposed competing parent/child focus traps.
  Only the topmost overlay now handles Tab/Escape; picker Escape preserves the
  draft and restores trigger focus. Enter in nested search cannot implicitly
  submit a surrounding parent form. Final frontend/browser checks follow.
- First full frontend run: 1,642 tests passed; one error-harness test still mocked
  the retired role-query path rather than the new paginated query. Updated that
  fixture to explicitly allow reads and reject the actual query; all ten harness
  tests pass. The failed run remains in `continuation-frontend.log`; it is not a
  passing enterprise result. No retry, timeout or assertion was relaxed.
- No representative/full Local CI or live qualification is claimed by this
  continuation. Earlier Local CI migration CLI installation failure remains open.
- Corrected full frontend enterprise run passed **266 files / 1,643 tests**,
  lint/type-check/build, contracts, bundle/CSP and audit with a stable tree:
  `continuation-frontend-final/20260922T200357Z-1407018-22f633ece6a9/evidence.json`.
  A subsequent small picker guard also hides cached counts when local read
  permission disappears while the picker is open. Its focused picker/overlay
  suite passed **19 tests**; final frozen-tree qualification follows below.
- Built desktop/mobile E2E: **138/138 passed**, including **12 keyboard workflow
  checks** (two auth setups included) and serious axe checks. Retained logs:
  `continuation-browser-full.log`, `continuation-keyboard.log`.
  Reviewed mobile, desktop and tablet RBAC screenshots; refreshed only the six
  RBAC baselines to remove obsolete page-local search/filter/sort controls.
  Two mobile images exceeded the existing diff threshold; the smaller desktop
  and tablet differences were within tolerance, so all six were explicitly
  refreshed. Visual thresholds were not changed.
- Complete final visual matrix passed **48/48 comparisons plus two auth setups**
  with `--update-snapshots=none` (`continuation-visual-final.log`). These remain
  initial-viewport comparisons, not full assistive-technology qualification.
- Final unchanged-candidate frontend enterprise gate passed **266 files / 1,644
  tests**, lint/type-check, generated/code-health checks, formatter tests,
  production build/CSP/bundle, route-tree drift and dependency audit. Manifest:
  `continuation-frontend-handoff/20260922T201250Z-1431196-22f633ece6a9/evidence.json`.
  Source identity stayed stable. Formatting passed for all **170 staged frontend
  files**; docs, complexity, dependency boundaries, raw-transport and zero-quarantine
  checks passed. Only status/evidence documentation changed afterward.
- Temporary preview stopped. Original staged work remains preserved; no commit,
  push, merge, deployment or live infrastructure mutation was performed.

### Continuation — project membership, SSO scope and truthful counts

- Project Members now uses project-filtered 25-row binding pages, with names
  resolved only for visible IDs. The overview shares its first-page query and
  reports role assignments, not a synthetic unique-person count. Changing
  projects resets pagination and dialogs; read denial hides cached rows/counts.
  Removing the final binding on a later page retains a usable Previous action.
- SSO group mappings now page roles from the selected global/cluster/project
  scope. The former selector always used global roles. Scope changes clear the
  role and target and remount the picker; scoped IDs are preserved in creation.
- Shared table callers now carry count certainty separately from navigation.
  Missing totals with continuation show “at least”; short pages still allow
  Next; empty later pages retain Previous. Loading/denied pages withhold counts.
  An empty offset beyond a shrinking collection is unknown, not an exact total.
  Updated Audit/Delivery/CIS count labels and omitted unknown app-tab badges.
  Removed the redundant delivery-only count wrapper. The separate image-scan
  detail CVE count remains explicitly listed above, not silently declared fixed.
- Focused API/UI suites passed **61 tests**, followed by **11 pagination edge
  tests** covering the final unknown/error-count changes. Existing member error,
  empty, add and revoke assertions are retained. Added real-query tests for
  beyond-200 membership, project isolation, exact revoke and cached denial.
- New browser fixtures initially omitted a global role/project quota and used
  the wrong group accessible name; corrected the fixtures and selector. An
  existing keyboard test also raced the cluster picker's animation-frame focus
  restoration. It now explicitly waits for the picker to close and regain
  focus before continuing, without retries or longer deadlines. Failed traces
  remain under `pagination-keyboard-output/` and
  `pagination-keyboard-final-output/`; the corrected 16-check suite passed.
- The first frozen frontend gate passed **270 files / 1,658 tests**. After the
  final four count-state regressions, the final gate passed **270 files / 1,662
  tests**, lint, type-check, generated/code-health checks, formatter tests,
  build/CSP/bundle, route-tree drift and dependency audit (zero findings):
  `pagination-frontend-final/20260922T210458Z-1506874-22f633ece6a9/evidence.json`.
  Source identity stayed stable at
  `ba255a31b4cf1ec05445fca3135cbd28c6782000d0e247cca50a989161d28003`.
  Only evidence/status documentation changes after that gate.
- Complexity ceilings tightened for CIS (272 → 268) and Audit (462 → 445);
  no ceiling increased. No generated contract or backend runtime change in this
  continuation. No Local CI, real agent/Charlie or protected/live qualification
  is claimed; those remain separate acceptance work.
- Final-build desktop/mobile E2E passed **142/142**, including the two added
  membership/SSO workflows on both viewports and the strengthened keyboard focus
  check (`pagination-browser.log`). Visual checks passed **48 comparisons plus
  two auth setups** across desktop/tablet/mobile (`pagination-visual.log`) with
  `--update-snapshots=none`. No screenshot baseline, retry or threshold changed.
- Final-build route crawl passed **304/304 desktop/mobile checks**
  (`pagination-routes.log`). Formatting passed for all **191 staged frontend
  files**; documentation, dependency boundaries, complexity, transport,
  cancellation and zero-quarantine checks passed. Temporary preview stopped;
  original staged work is preserved. No commit, push or deployment performed.

Next focused work: extend native-grant/bulk/relationship negative browser
workflows, then qualify pinned Local CI representative/full matrices. Advanced
guided unions and the separate API/agent product decisions remain open; no
local mock result substitutes for live/protected qualifications.

### Continuation — Audit selection, CIS history and CVE detail

- Audit resolves only visible actor IDs and selected target IDs, sharing a
  permission-aware exact-ID resolver with RBAC. Cluster/project filters page
  remotely, reset Audit pagination and retain known-ID entry without inventory
  read permission. Server or local read denial hides cached rows and detail.
- CIS history now uses 25-row server pages with exact visible cluster names.
  Summaries explicitly describe only the current page; unsupported page-local
  search/sort controls are removed. Empty CIS history no longer implies missing
  image reports or recommends installing Trivy on that unsupported inference.
- CVE detail preserves canonical pagination, including absent totals and
  continuation beyond 100 rows. Report, cluster and severity changes reset the
  page; denied reads hide stale rows and retain Previous recovery. Severity
  selection is inside the shared keyboard-safe drawer, with a keyboard-accessible
  report opener. Screenshot review caught toolbar overlap during page animation;
  the shared drawer now mounts outside page stacking contexts.
- Narrow API/query/UI tests cover beyond-200 Audit/CIS selection, beyond-100
  CVEs, missing totals, exact lookup, permission withdrawal, cached denial and
  drawer focus/stacking. Browser coverage exercises these flows on desktop and
  mobile, including close-button interaction, Escape and restored focus.
- Final qualification evidence follows below. No backend/generated API change,
  Local CI rerun, live agent/Charlie qualification or deployment is claimed.
- Final focused API/query/overlay suite passed **29 tests**. Initial drawer-test
  type-check caught a Testing Library/Playwright option mismatch; removed the
  invalid test-only option and rebuilt successfully. The first browser probe
  before that successful rebuild is not evidence for the portal change.
- Full four-worker E2E passed **147/148**: desktop keyboard resource creation
  timed out waiting for Done, with no create request in the trace. The cause is
  not established; the modal used by that test was not changed in this batch.
  Failure evidence remains in `audit-cis-browser-output/`. No assertions,
  deadlines or retries were relaxed. Single-worker qualification follows.
- Final-build visual matrix passed **48 comparisons plus two auth setups**
  (`audit-cis-visual.log`, `--update-snapshots=none`); route crawl passed
  **304/304** desktop/mobile checks (`audit-cis-routes.log`). Reviewed the new
  mobile CVE screenshot: toolbar overlap is gone and the filter remains inside
  the drawer. No screenshot baseline or tolerance changed.
- Documentation, dependency boundaries, complexity, raw transport, cancellation
  and zero-quarantine checks pass. Complexity ceilings only tightened: CIS
  268 → 248, Audit 445 → 431, image scans 669 → 611. Formatting passed for
  all **206 staged frontend files**.
- Full single-worker E2E passed **148/148** on the same production build
  (`audit-cis-browser-serial.log`), including desktop/mobile keyboard creation.
  This passing run does not establish the cause of the retained concurrent-run
  failure or claim that intermittent browser behavior has been fixed.
- Enterprise attempts retained in `audit-cis-frontend-final.log` and
  `audit-cis-frontend-handoff.log` caught a two-line stale code-health inventory,
  an unused icon import and an inline test query key. Regenerated the inventory,
  removed the unused import and used the canonical key; no runtime behavior or
  browser assertions changed during this cleanup.
- Final frozen-source frontend gate passed **274 files / 1,677 tests**, full
  lint/type-check, contracts/code-health, formatter tests, build/CSP/bundle,
  route-tree drift and dependency audit (zero vulnerabilities). Evidence:
  `audit-cis-frontend-qualified/20260922T214545Z-1615191-22f633ece6a9/evidence.json`.
  Source identity stayed stable at
  `97db2c9e2300c9098332aa4310f9fbd0337d049185b17918766235a8b8ceb05f`.
  Only evidence/status documentation changes after qualification.
- Final enterprise-build Audit/CIS/CVE desktop/mobile workflows passed **8/8**
  including the two auth setups (`audit-cis-browser-qualified.log`). Temporary
  preview stopped; unrelated staged work preserved. No commit, push or deployment.

Next: native-grant/bulk/relationship negative browser workflows, remaining
selector inventory and advanced guided unions; resolve pinned representative
Local CI before the full matrix. Product/API decisions and live/protected
qualifications remain explicitly open above.

### Continuation — negative workflow coverage

- Add desktop/mobile native-grant review, exact mutation, denial and disabled-API workflows.
- Exercise bulk terminal partial results, unconfirmed polling and cancellation;
  assert exact targets and that no later writes are sent after stopping.
- Exercise paged related resources with a denied continuation, preserving
  Previous without showing cached names or claiming an empty collection.
- Fix evidenced UI defects, run focused regressions and frontend qualification,
  then record exact evidence. Virtualized grouped-row coverage, advanced guided
  fields and pinned Local CI qualification remain separate pending work.

- The new relationship regression failed before the fix: a 403 on a later page
  removed both navigation controls. Navigation now lives outside the query-state
  renderer; Previous survives errors while Next is disabled, and cached links
  remain hidden. The focused model/UI suite passed **16 tests** after the fix.
- Native-grant tables no longer offer page-local sorting for an API that does
  not support sorting. Added a unit assertion and built-browser assertion.
- New desktop/mobile workflow suite passed **16/16 including two auth setups**
  (`negative-workflows-qualified.log`). It checks typed confirmations before
  mutations, exact scoped grant bodies and deletion targets, denied mutations
  without false success, disabled/denied API states, unique bulk idempotency
  keys, terminal partial results, denied polling and cancellation, and encoded
  relationship continuation with Previous recovery.
- Earlier browser attempts are retained (`negative-workflows.log` and
  `negative-workflows-final.log`): grant assertions initially omitted the full
  error-toast message and transport-normalized trailing slash. Corrected those
  fixture expectations; no retry, timeout, baseline or tolerance was changed.
- No new backend/API contract, Local CI, live agent/Charlie or protected
  qualification is claimed. Broader frontend evidence follows below.
- Full production-build E2E passed **162/162** desktop/mobile checks with one
  worker (`negative-browser-full.log`). The separate three-viewport visual
  matrix passed **48 comparisons plus two auth setups** (`negative-visual.log`)
  with `--update-snapshots=none`. The earlier unrelated concurrent-run failure
  remains historical evidence, not a defect claimed fixed by this batch.
- Formatting passed for all **209 staged frontend files**. Documentation,
  complexity, dependency boundaries and zero-quarantine checks passed. No
  complexity ceiling changed. Temporary preview stopped. The 304-route crawl
  was not rerun in this batch; its earlier evidence remains separately scoped.
- Final frozen-source frontend enterprise gate passed **274 files / 1,678
  tests**, lint/type-check, generated/code-health checks, formatter tests,
  build/CSP/bundle budgets, route-tree drift and dependency audit (zero findings):
  `negative-frontend-final/20260922T220832Z-1658531-22f633ece6a9/evidence.json`.
  Source identity remained stable at
  `e50307c187507547ec844b4f7c868971825877abf5d431ff55300def4d3dee65`.
  Only status/evidence documentation changed afterward. Existing staged work
  is preserved; no commit, push, deployment or live infrastructure mutation.

Next focused work: virtualized grouped-row keyboard/selection coverage and the
remaining state combinations, followed by the remaining selector inventory and
advanced guided fields. Pinned Local CI representative/full qualification and
the API/agent decisions remain open; these local results do not replace them.

### Continuation — virtualized grouped table coverage

- Add a separately built browser fixture for the real DataTable and shared CSS;
  current resource pages use server pagination and cannot exercise this shared
  component combination. No test-only route enters the operator build.
- Cover group boundaries and ARIA row indices, editable-control keyboard events,
  selection across windows/sorting/grouping/filtering, and focus recovery after
  filtering a far row out. Fix reproduced defects and record exact qualification.
- Browser traces reproduced a stale-index crash (`row.original` after filtering)
  and row handlers stealing arrow keys from editable cells. The virtual body
  now ignores stale window indices until the post-commit virtualizer catches up,
  handles row navigation only for the row itself, and clamps keyboard reentry
  to the current row model after filtering. Group headers remain non-selectable.
- The first fixture build omitted Tailwind's shared-source scan, causing its
  grid to render without a height cap. Corrected the test-only CSS configuration;
  the production stylesheet was not changed. Initial failures remain in
  `grouped-red.log` and `grouped-red-output/`; they are not passing evidence.
- Focused unit suite passed **28 tests** (`grouped-unit.log`). Separately built
  component browser checks passed **12/12** across desktop/tablet/mobile
  (`grouped-components-final.log`), including bounded mounted rows and zero
  uncaught page errors. Run with `npm run test:browser-components`; its README
  documents the dedicated port, artifacts and qualification boundary.
- Strengthened the actual CIS findings browser workflow to filter a 1,200-row
  window down to one/zero rows and restore it, checking keyboard recovery and
  uncaught errors. Both viewports plus auth setups passed **4/4**
  (`grouped-cis.log`). Production Vite manifest contains no fixture entry.
- No backend/API change, Local CI run or live/protected qualification is claimed.
  Final full frontend/browser evidence follows below.
- Full unchanged-runtime console E2E passed **162/162** with one worker
  (`grouped-browser-full.log`). Three-viewport visuals passed **48 comparisons
  plus two auth setups** (`grouped-visual.log`, `--update-snapshots=none`).
  No baseline, timeout or retry policy changed. Generated fixture build/results
  have narrowly scoped lint exclusions; fixture sources remain linted and typed.
- Final frozen-source frontend gate passed **274 files / 1,680 tests**, full
  lint/type-check, generated/code-health checks, formatter tests, build/CSP/bundle,
  route-tree drift and dependency audit (zero findings). Evidence:
  `grouped-frontend-final/20260922T224047Z-1713074-22f633ece6a9/evidence.json`.
  Source identity remained stable at
  `5327abef8e7131a4c057d5f66c9b304da4fb6a58a91bd264f65ad8bba9024095`.
  Only status/evidence documentation changes afterward. All owned previews
  stopped; existing staged work preserved. No commit, push or deployment.
- The separate 304-route crawl and pinned Local CI matrices were not rerun;
  prior evidence remains tied to its original source snapshot. Component tests
  are an explicit standalone command, not a claim of protected CI qualification.

Next focused work: audit remaining list/picker caps and state combinations, then
advanced guided unions. Pinned representative/full Local CI qualification and
the documented API/agent decisions remain open.

### Continuation — Catalog project reachability and CIS detail recovery

- Replace the Catalog and cluster Apps first-200 project selectors with the
  existing server-search/paged project picker. Cluster Apps uses the canonical
  cluster-project endpoint instead of filtering a global first page locally.
- Resolve requested project IDs directly. A two-row bounded query may select a
  default only when pagination proves there is exactly one project. Missing,
  denied and wrong-cluster deep links must not silently select another project;
  lookup failures clear the usable scope but leave the picker available.
- Hide project-bound chart/install/upgrade dialogs while the selected scope is
  unavailable; reset Apps install form state when its project changes. Preserve
  cluster-scoped uninstall and repository actions independently of project lookup.
- Selected-project lookup errors in the shared picker stay local rather than
  escalating a 5xx to the page error boundary; other project-hook callers retain
  their existing behavior. Browser coverage includes 403/404/503 deep-link recovery.
- CIS detail resolves only its scan's cluster name, not the first 200 clusters.
  Render explicit permission/missing/offline/server-error states before empty
  data handling, hide cached findings after failure, propagate cancellation and
  stop automatic polling after query errors. Align read/create UI decisions with
  the existing global security route middleware.
- Keep failed re-run confirmations open; send the reviewed cluster/profile and
  navigate only after success. Nonterminal exports have no usable link target.
- Add focused scope/query/action tests and real desktop/mobile browser checks.
  Verification results are appended below after qualification; previous passing
  evidence above remains scoped to its original snapshot.

Remaining cap candidates found during this pass (not declared fixed): Catalog
chart/version and installed-app collections; cluster template attachment;
delivery source/bundle/version/template selectors and rollout event windows;
namespace workload/event summaries and shared monitoring management-cluster
discovery. Each needs endpoint-specific pagination/state review, not a blanket
increase of its limit. Advanced guided unions, wider state/assistive-technology
coverage, representative/full Local CI and the documented live/API decisions
remain open.

#### Catalog/CIS continuation evidence

- Focused unit checks passed **28 tests** across five files
  (`catalog-scan-unit-complete.log`, under the existing evidence directory).
- Final-build console E2E passed **180/180** with one worker
  (`catalog-scan-browser-full.log`). Visual qualification passed **48 comparisons
  plus two auth setups** across desktop/tablet/mobile (`catalog-scan-visual.log`,
  `--update-snapshots=none`). No timeout, retry or snapshot policy was relaxed.
- Initial build/type failures were corrected. Two early frontend gate attempts
  were deliberately stopped to include the dialog and picker recovery fixes;
  `catalog-scan-frontend-final` and `catalog-scan-frontend-qualified` are not
  passing qualification. Final frozen-source gate evidence follows separately.
- Reviewed complexity changes only lower the existing ceilings: CatalogPage
  351→310, ClusterAppsPage 352→340 and FindingsSection 246→243. Shared collapse
  handling removes duplicated finding-expansion state updates.
- Final frozen-source frontend gate passed **277 files / 1,699 tests**, full
  lint/type-check, formatter tests, generated/code-health checks, build/CSP,
  route-tree drift, unchanged bundle budgets and dependency audit (zero findings).
  Evidence: `catalog-scan-frontend-complete/20260922T231007Z-1765636-22f633ece6a9/evidence.json`.
  Source identity stayed stable at
  `82ca006512304bb22e6bff9f60e26290ba5f4032f74c144ab6dc5436fab0c83e`.
  Only status/evidence documentation changed afterward. Formatting checked all
  228 staged frontend files; existing work remains staged and preserved.
- Owned previews stopped; no backend/API change, commit, push or deployment.
  The separate component-browser suite, 304-route crawl, pinned representative/
  full Local CI matrices and live/protected qualifications were not rerun.

Next implementation batch: endpoint-specific delivery/template picker pagination
and remaining Catalog collection limits, followed by advanced guided unions.
The broader state/assistive-technology work and documented API/live decisions
remain open; this continuation does not claim the entire UI audit is closed.

### Continuation — paged collections and project-scoped install contracts

Implementation in progress; qualification is not yet complete:

- Replace first-200 delivery bundle/version/source/configuration-template and
  cluster-template selectors with bounded native paged controls. Preserve
  selected IDs across pages; hide denied cached options and block invalid form
  submissions. Template binding failures must not masquerade as no binding.
- Page Catalog charts, versions, repositories and installed releases, plus the
  corresponding cluster Apps collections. Keep recovery navigation outside
  failed/empty content and label unsupported server searches as page-local.
- Use one canonical project-scoped chart-version implementation. Correct Apps
  default-value reads to use the project ID rather than the cluster ID; block
  installation when selected version/default data is unavailable.
- Preserve the documented complete-array repository response contract without
  inventing totals for paged endpoints. Describe bulk failed-release deletion
  as covering all matching pages, without claiming failed releases have no live
  resources.
- Lazy-load target creation to preserve the unchanged eager bundle budget.
  Focused unit checks currently pass 20 tests; production build and bundle
  budgets pass. Browser and full-source qualification remain in progress.

Next: qualify collection/picker recovery, implement advanced guided scheduling
and Ingress unions, review remaining capped summaries, then attempt the pinned
Local CI qualification. Live/protected evidence and explicit API decisions
remain separate completion requirements.

#### Advanced fields and remaining collection review — implementation

- Guided preferred node affinity and required/preferred pod affinity and
  anti-affinity now have indexed editors. Namespace-selector omission/null is
  preserved until explicitly changed; empty selectors are described as broad
  matches. Weight, expression-operator and topology-key checks complement the
  existing authoritative server dry-run.
- Ingress rule and default backends support service/resource references and
  explicit type replacement. Port-name/number and backend unions are validated;
  sibling rules, metadata, TLS and unknown fields survive unrelated edits.
- Rollout event timelines and logging operations use bounded server pages with
  retained recovery navigation. Logging filters remain server-side; text filters
  are explicitly page-local. Failed timeline/operation queries hide cached data.
- Namespace resource tabs now query the selected namespace at the server and
  page each resource type independently. Error/loading states are distinguished
  from empty pages, denied cached resources are hidden, and health/count labels
  describe loaded pages rather than fabricated namespace totals. Cluster events
  remain a documented recent-window API (200 globally / 500 for the namespace
  summary), not complete history.
- Shared monitoring installation requires the existing searchable cluster
  picker instead of guessing the management cluster from the first 100 results.
  Existing installed-stack configuration remains authoritative.
- Focused checks currently pass 75 tests across six files. Catalog/install and
  delivery/template browser journeys pass on desktop/mobile (four new scenarios,
  eight executions plus auth setups). Initial test failures exposed selector
  assumptions, not grounds for retries or timeout relaxation; corrected tests
  explicitly select the management cluster and reload after changed permissions.
- Full frozen-source frontend, final-build browser/visual and pinned Local CI
  qualification still pending. Earlier evidence remains snapshot-specific.

Kubernetes field semantics checked against the official [affinity reference](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/)
and [Ingress reference](https://kubernetes.io/docs/concepts/services-networking/ingress/).

Additional cap audit: delivery target preview's 100-row size already has explicit
cursor continuation and is not a first-page-only picker. Node and namespace
inventory adapters also already follow continuation. Remaining legacy first-page
candidates include admin dashboard/data-source/GitOps/quota collections, personal
tokens, logging pipelines, alert-rule summaries, the tool registry and Catalog
operation history. These require caller/contract review and are not declared
uncapped by the current continuation. Image vulnerability history, audit exports
and cross-cluster search also need window-versus-inventory classification before
changing limits. No limits were raised to imply completeness.

Qualification follow-up: the full static frontend gate passed 1,722 tests on
source `89f4a28435bec3b2d213a333dd9abda036d66ca4dffdf047f3af58d104a46a2f`.
The first full browser run passed 190/192; both failures were the Namespace
workspace fixture's empty auth-helper inventory. The corrected desktop/mobile
case passes without changing application behavior or test limits. Final-source
qualification is being repeated after that fixture correction and two exact-
offset adapter regressions: workload and template pages now forward server
offsets directly, avoiding fractional-page rounding. Focused offset checks pass
17 tests. Earlier failed/partial gate logs remain retained as such.

#### Paged collections and guided fields — final local frontend evidence

- Final frozen-source frontend enterprise gate passed **281 files / 1,724 unit
  tests**, lint/type-check, formatter tests, generated/code-health checks,
  production build/CSP, route-tree drift, unchanged bundle budgets and dependency
  audit (zero findings). Evidence, relative to
  `/var/tmp/astronomer-plan026.VQnCQL/`:
  `collections-guided-frontend-qualified/20260923T002600Z-1886790-22f633ece6a9/evidence.json`.
  Source identity remained stable at
  `25a2163ce1e82818e85f98dc29e2b797cb6736e3a5f0bad9040fd8e0fce18733`.
- The same production build passed **192/192** desktop/mobile workflow checks
  (`collections-guided-browser-final.log`), **48 visual comparisons plus two
  auth setups** across desktop/tablet/mobile (`collections-guided-visual.log`,
  `--update-snapshots=none`), and **304/304** route checks
  (`collections-guided-routes.log`). The separate virtualized-table component
  suite passed **12/12** across three viewports
  (`collections-guided-components.log`). No snapshot, timeout or retry policy
  was relaxed. Earlier failed fixture runs remain recorded, not counted green.
- All **272 staged frontend files** passed formatting. Documentation checked
  114 current documents / 596 relative links; staged and working-tree whitespace
  checks passed. Eager app closure is **489,291 / 489,472 gzip bytes** (181 bytes
  headroom): the gate passes, but further eager additions require extraction or
  savings, not a raised budget.
- Only evidence/status documentation changed after this source qualification.
  Existing staged work remains preserved; no commit, push or deployment.
  Pinned representative Local CI is next, followed by the full matrix only if
  representative passes. Those runs and the API/live/protected requirements
  are not covered by the frontend evidence above.

#### Pinned representative CI — diagnosed failures and follow-up

- `make local-ci-pr-representative LOCAL_CI_JOBS=1` ran the pinned 0.18.1
  workflow as `local-ci-39`, after prewarm 38. It finished in **33m 57s** with
  **six passing lanes / two failing lanes**, not a green representative run.
  The full matrix was therefore not started. Log:
  `collections-guided-local-ci-representative.log` under the evidence directory.
- Passing lanes: backend (including full normal/race Go suites and generated
  Charlie contracts), PostgreSQL integrations (**15/15 required tests, no
  skips**), PostgreSQL failover certification, frontend (**1,724 tests**), Helm,
  and the representative server image build/scan/SBOM lane (zero fixable
  HIGH/CRITICAL findings under the existing scan policy). Migration CLI setup,
  PostgreSQL 16/17 roundtrips and supported release upgrades passed; the earlier
  CLI installation failure did not recur, but its original cause remains unknown.
- Backend/frontend/Helm evidence all report stable source identity
  `2ed0bacee22be89c7b8a4f521659986b1e4926343d71dbc99f9322357ac3bf8e`.
  The runner's private snapshot commit is `39359f8d36d8`; it is not a host commit.
  Saved ZIPs in `dtu_cache/artifacts/`: backend `120336_blob.zip`, frontend
  `486309_blob.zip`, Helm `681145_blob.zip`. Backend evidence was additionally
  copied to `collections-guided-local-ci-backend/` before runner cleanup.
- Browser failure evidence is retained in `dtu_cache/artifacts/338316_blob.zip`
  and extracted under `collections-guided-ci-browser-failure/`. At the default
  eight workers, CIS 503 remained loading and the CVE drawer missed Escape.
  Traces showed a headers-only SSE open/drop loop repeatedly invalidating active
  queries. Catch-up now preserves failed query states and does not cancel reads
  in flight; reconnect backoff resets on a frame, not merely response headers.
  Three deterministic stream regression tests pass.
- A separate regression reproduced the drawer failure: an earlier native global
  key listener can synchronously rerender the parent and replace the drawer's
  listener before that same Escape reaches it. `OverlayShell` now keeps the
  listener stable and invokes the latest close callback through `useEffectEvent`.
  The new test fails before the fix and passes afterward. Combined focused
  stream/hook/drawer checks pass **13 tests**. No browser timeout, worker count,
  retry or quarantine policy was changed to conceal either failure.
- Disposable live-stack startup failed before application/browser qualification:
  k3d could not bind `172.17.0.1:443`, already owned by
  `k3d-test-run-live-2043-77f5-server-0`. A deeper host check also found a separate
  `k3s-server` listener on `*:6443`; the absence of a Docker-published 6443 port
  was not proof of availability. Both supported direct-API ports are occupied.
  Existing services were left untouched. Retained live evidence:
  `dtu_cache/artifacts/724510_blob.zip`, especially `k3d-create.log`.
  Completing live qualification requires a free supported port or an explicitly
  selected isolated Docker host, not weakening direct-endpoint validation.
- After printing its failure summary, the runner itself exited with a segmentation
  fault (139). This does not erase the saved lane artifacts, but is not a passing
  CLI result. No speculative runner patch was made for that cleanup failure.
- The stream/drawer source changes above postdate the passing CI static snapshot.
  Final frontend/browser qualification is being repeated for them; earlier
  evidence remains snapshot-specific. No backend, agent, API or Helm source
  changed in this follow-up.

- The first eight-worker host rerun after the stream/drawer fixes passed those
  two scenarios but found a picker interaction (**191 passed / 1 failed**): a
  background refetch could disable Next between pointer-down and pointer-up,
  losing the click. Paged selectors and shared offset controls now retain
  navigation from valid visible-page metadata during a background refresh;
  missing/failed metadata still prevents Next and Previous retains recovery.
  Both focused regressions fail before the change and pass afterward. Combined
  follow-up regression coverage passes **24 tests in five files**; the broader
  qualification below supersedes this intermediate failed browser run.

#### Final host qualification and authorized live-port recovery

- Final frontend enterprise qualification passes **282 files / 1,730 tests**,
  lint, types, generated/static contracts, build, unchanged bundle budgets and
  dependency audit. Stable source identity:
  `ad548e9b3716928023102f93978571c5e7e6f1845117d99fc457d9a1d476a224`.
  Evidence: `collections-guided-post-ci-frontend-final/` in the evidence root.
- Sequential final host browser runs pass **192 workflow tests**, **50 visual
  checks** (48 comparisons plus authentication setup), **304 route checks** and
  **12 browser-component checks**. Workflow/visual/routes used eight workers;
  no snapshots, retries, timeouts or assertions were relaxed. Logs use the
  `collections-guided-post-ci-` prefix.
- The user authorized temporarily freeing the disposable test cluster's port,
  explicitly requiring K3s restoration. Baseline: host `k3s.service` is active,
  `astronomer-dev-1-mj` is Ready on K3s v1.35.7+k3s1, API port 6443. The distinct
  Docker container `k3d-test-run-live-2043-77f5-server-0` is Ready and publishes
  its API on `172.17.0.1:443`. Only that exact disposable container may be
  temporarily stopped; the host K3s service, kubeconfig, other containers and
  cluster data remain untouched. Restart and both-cluster health verification
  are mandatory after the qualification attempt.

#### Representative recheck 41: browser recovery and startup migration gap

- Run `local-ci-41` (prewarm 40) completed in **39m 17s**, with **seven passing
  lanes / one failing lane**. Backend normal/race, PostgreSQL integrations
  (15/15), PostgreSQL failover, frontend (1,730 tests), workflow/route/visual
  browsers, Helm and representative server supply-chain checks passed. The
  runner again segfaulted after reporting its summary; CLI exit is not green.
  Log: `collections-guided-local-ci-representative-recheck.log`.
- The authorized temporary stop of `k3d-test-run-live-2043-77f5-server-0`
  resolved the 443 collision. The new disposable cluster successfully started
  Flux, MinIO and Velero. Astronomer then correctly failed closed at startup:
  `user_preferences.pinned_clusters` and `user_preferences.starred_types` lacked
  durable JSON governance registrations. Live artifact: `501899_blob.zip` in
  `dtu_cache/artifacts/`; workflow-browser artifact: `843279_blob.zip`.
- The new fixture cleaned up, and the older test container was restarted and
  verified Ready. Host `k3s.service` remained active, its node Ready and API
  readiness healthy throughout. No host cluster configuration was changed.
- Additive migration 066 registers the two existing non-null array contracts;
  the shared validation trigger and existing 20-item constraints remain intact.
  Its rollback removes only those registrations, not preference data or the
  trigger. The binary schema floor advances to 66. A fresh PostgreSQL regression
  fails before the migration and passes afterward, checking complete governance
  coverage, valid writes and trigger rejection without changing saved values.
  PostgreSQL 16/17 roundtrip checks now exercise that regression after initial
  migration and full rebuild so future missing registrations fail earlier.
- Focused DB/migration tests, migration safety and unchanged complexity gates
  pass. Full roundtrip/live and exact-candidate CI qualification are pending
  for migration 066; passing recheck-41 evidence predates this follow-up.

- Migration 066 now passes complete PostgreSQL 16/17 down/up/rebuild checks;
  the SQL generation check is current. The live rerun successfully starts the
  server and has reached Flux/Trivy reconciliation (final outcome pending).
- Runner crash diagnosis: importing `cpu-features`, `ssh2` or `dockerode` under
  the separately installed Node 24 reproduces aborts without running a workflow.
  The addons used Node 22 ABI headers and linked system `libnode.so.127`;
  reinstalling with Node 24 alone did not correct the distro build tool's
  linkage, even after supplying matching headers. Reinstalling and invoking the
  controller under system Node 22.22.1 (supported by runner 0.18.1) passes the
  isolated import/exit regression. Container jobs retain pinned Node 24.21.0.
  All three Local CI tooling tests pass. `make local-ci-install` now tests the
  real dependency import in a child process before starting any workflow;
  `--help` skips it and cannot prove ABI compatibility. Evidence logs use
  `collections-guided-runner-` prefixes; no third-party version was changed.

- The first migration-066 live run passed **15/16 journeys**, including real
  Flux-native Trivy reconciliation/ingestion. The backup/restore journey failed
  after successful backup and ConfigMap deletion because it expected deletion
  to return to browser history (`snapshots`). The unchanged production detail
  component deliberately navigates to the canonical resource collection.
  The test now asserts that ConfigMaps destination and the real 404, then
  explicitly returns to Snapshots to perform the restore. No product navigation,
  restore assertion, deadline or retry was changed. Failure evidence is retained
  under `collections-guided-live-migration066/`; a full live rerun is pending.
  The fixture cleaned up and the older test container returned Ready again.

- The complete live rerun now **passes all 16 journeys**, with Trivy enabled,
  followed by direct read-only credential validation, actual Flux reconciliation
  and durable Velero backup/restore checks (`snapshot=Completed,restore=Completed`).
  Log/artifacts: `collections-guided-live-migration066-navigation.log` and the
  matching directory. The temporary cluster was removed by its harness and the
  older test container restarted. This is disposable integration evidence, not
  protected cloud/DR/agent-identity acceptance against the user's main node.
- Release compatibility source, chart defaults/schema and generated Helm/docs
  views now consistently target schema 66. Deployment/release/protocol Go tests
  and four compatibility-generator tests pass; generated views are current.
  Frontend live-test lint and all 276 staged frontend files' formatting pass.
  Final pinned representative/full-matrix qualification is next, using the
  now-tested system Node 22 controller and unchanged Node 24 job containers.

- Run 43 was stopped after its backend lane again exited while independently
  installing upstream `golang-migrate` (exit 2, before migrations or backend
  evidence generation). The retained runner output does not explain that
  compiler exit; no network/OOM diagnosis is claimed. Later lanes were cancelled,
  not counted as passing, and the full matrix did not start. Log:
  `collections-guided-final-representative.log` and `local-ci-43-j1` diagnostics.
  Its owned PostgreSQL fixture was cleaned up; the older K3d container was
  restarted and both original nodes verified Ready.
- Roundtrip qualification now builds and exercises the repository's canonical
  `cmd/migrator`, as supported-release upgrade qualification already does,
  instead of selecting an arbitrary PATH CLI or installing a separate upstream
  dependency version. Cleanup is registered before the build. This also tests
  the shipped advisory-lock behavior. Full PostgreSQL 16/17 roundtrips and
  schema/seed equivalence pass, as do focused migrator/DB tests and ShellCheck.
  Evidence: `collections-guided-canonical-migrator-roundtrip.log`. This replaces
  the failed setup path without retrying, skipping or relaxing a migration test.

- Representative run 45 (prewarm 44) completed in **38m 54s** with **seven
  passing lanes / one failing lane**, exiting normally with failure status 1
  rather than a native crash. Backend (including migration 066, normal/race),
  PostgreSQL integration/failover, workflow/route/visual browser, **complete live
  integration including all 16 journeys and Flux/Trivy/Velero effects**, Helm and
  server image checks passed. Backend stable source identity:
  `2de4c3c029e3b1c6c9fa4818cbdd17c1e1374bed2b56a14fd1d4a1df7e3bd224`,
  private runner snapshot `9c91b22afc41`. Log:
  `collections-guided-canonical-representative.log`; backend ZIP `111777_blob.zip`.
- Frontend halted solely on stale generated inventory: the new migration test
  changes the Go query-reference scanned-file count from 1,970 to 1,971. The
  generator refreshed that one line; inventory and operation-task checks pass.
  No runtime code changed after run 45. Sequencing adjustment: proceed to the
  **complete** matrix after verifying this generated-document correction,
  instead of repeating the representative subset for a one-line inventory.
  The full matrix will rerun every lane against the corrected snapshot; run 45
  remains failed, and no lane or assertion is waived. Both original K3s nodes
  returned Ready after the disposable live fixture cleanup.

- Full-matrix attempt 47 (prewarm 46) again failed in early migration setup,
  before the backend gate, and was cancelled during the first stateful lane.
  The saved step output ends after dependency downloads, so it does not prove
  that compilation itself failed. Its owned PostgreSQL fixture was cleaned up
  and both original K3s clusters restored. This is not full-matrix qualification.
- Isolated diagnostics using the same pinned runner and initial setup steps
  pass: run 48 captures a cold migrator build (39s), and run 49 captures the
  entire traced PostgreSQL 16/17 roundtrip script (1m 51s). ZIPs `57493_blob.zip`
  and `506518_blob.zip` retain those logs. The intermittent setup cause remains
  unresolved; successful reproductions are not evidence that no issue exists.
- The real workflow now captures the complete migration setup/roundtrip output
  to an always-uploaded artifact while preserving the exact script exit status.
  This exposes any recurrence without retries, skips or deadline changes. The
  next full-matrix attempt uses this diagnostic improvement; isolated passes do
  not replace its required lanes.

- Attempt 51 passed the complete migration roundtrip, then failed the new log
  upload because Local CI 0.18.1 expands `runner.temp` to an empty value. The
  capture/upload now share `/tmp/astronomer-migration-roundtrip.log`, following
  the workflow's existing explicit temporary artifact paths. No migration result
  was changed. The attempt was cancelled before further qualification, its owned
  database cleaned up and older test cluster restarted. Log:
  `collections-guided-captured-full-matrix.log`. The next full run supersedes
  this failed diagnostic-path iteration, not its retained historical evidence.

### Full-matrix recovery qualification (2026-09-23)

- Run 53 (prewarm 52) completed the entire 24-lane matrix in **66m 18s**:
  **22 passed, two failed**. Backend/migration/upgrade, PostgreSQL integration
  normal/race, worker runtime normal/race, process restart normal/race,
  PostgreSQL outage normal/race, tunnel-owner HA, PostgreSQL failover, frontend,
  browser, live integration, Helm and all seven image checks passed. Frontend
  includes 1,730 unit tests; browser includes 192 workflows, 304 routes and 48
  visual comparisons (plus two setup checks); live includes all 16 journeys
  and actual Flux/Trivy/Velero effects. No protected/live identity qualification
  is implied. Log: `collections-guided-preserved-full-matrix.log`.
- Backend evidence ZIP `291218_blob.zip` records stable source identity
  `4ad8389d9692a234fddd31a9468622959a5c90b9a1d6f412a3320ffdd2db2dd4`,
  private runner snapshot `9901db88fefeb9cabaa18c65c52b0e0c38e633f7`.
  The two Redis-outage lanes fail after proving durable notification intent
  and mandatory audit persistence: their restart helper accepts only literal
  `127.0.0.1`, but the canonical Docker endpoint helper deliberately publishes
  to `172.17.0.1` for a containerized Local CI client. Both failures are retained;
  this run is not a passing complete matrix.
- The test-only restart helper now requires exactly one valid, nonzero port on
  the explicitly configured loopback/private bind address. It preserves the
  original connection hostname and URL options while refreshing Docker's
  reassigned port. Wildcard, unexpected/public addresses, missing configuration,
  multiple bindings, invalid ports and invalid Redis URLs fail closed. Eighteen
  table cases pass; the dedicated fixture ownership guard, outage assertions,
  recovery deadline and retry policy are unchanged. Host and actual Local CI
  normal/race qualifications and a final full matrix follow this correction.
- The user's main `k3s.service` remained active with the same PID (938597),
  API readiness and Ready node `astronomer-dev-1-mj` on port 6443. The older
  disposable K3d container was restarted as soon as the live lane released its
  bridge-only port 443, before the image lanes finished; its node returned Ready.
  No cluster configuration, default context or main-node workload was changed.
- Recovery correction qualification passes on both topologies: direct-host
  normal/race (`collections-guided-redis-recovery-host*.log`) and pinned Local
  CI run 54 normal/race (**2/2**, 4m 52s;
  `collections-guided-redis-recovery-local-ci.log`). Worker package tests,
  race-enabled endpoint boundary tests, repository-wide Go vet, generated
  inventory, complexity and documentation checks pass. This is a focused
  diagnostic run, not a replacement for the corrected complete matrix.
  The main node's pod names, readiness, status and restart counts still match
  the saved pre-work baseline exactly. The next complete run keeps the older
  K3d cluster online until its bridge port is needed for the live lane.

- Attempt 56 was stopped early after migration roundtrip and release-upgrade
  checks passed: inspection reproduced an independent pinned-runner limitation
  before its aggregate could run. `toJSON(needs)` expanded to an empty JSON
  string, and the runner retained output values without actual job results.
  This would prevent the unchanged PR aggregate from executing correctly.
  `collections-guided-redis-corrected-full-matrix.log` retains the cancelled run;
  it is not a complete qualification, and no original cluster was paused.
- The version-bound Local CI compatibility repair now records actual dependency
  results, preserves failure across matrix completion order and startup errors,
  and expands the canonical `{result, outputs}` context. Missing result evidence
  fails closed. All anchors/version checks must pass before installed files are
  rewritten. The actual PR aggregate and its all-gates-required rule are
  unchanged. Eight tooling tests pass after a clean pinned install, including
  execution of that real aggregate with successful and failed inputs.
- Real miniature workflow diagnostics validate both paths after the clean
  install: run 59 **3/3 passes** (including all ten protected-producer wiring
  records); run 60 intentionally fails its first producer and passes the later
  producer plus the assertion that its aggregate still sees `failure`. The
  command correctly exits 1; this is an expected negative test, not a passing
  product matrix. Logs: `collections-guided-needs-final-{success,failure}-local-ci.log`.
  Earlier diagnostics 57/58 also retain their results. No source hash, workflow
  assertion, external acceptance requirement or dependency version was relaxed.

### Final local qualification and handoff (2026-09-23)

- Full pinned Local CI run **62** (prewarm 61) passes **25/25 lanes in 67m 35s**,
  with command exit 0. This includes every expanded stateful and image matrix
  entry, not only representative entries, plus the actual final PR aggregate.
  Both previously failing Redis recovery variants pass in this complete run.
  The aggregate also validates all ten protected-producer wiring records;
  this is wiring validation, not execution/approval of those external producers.
- Backend includes migration roundtrips on PostgreSQL 16/17, supported-release
  upgrades, normal/race Go suites, build/vet/lint, vulnerability scan, generated
  contracts and Charlie contract checks. Frontend includes **1,730 unit tests**,
  type/lint/build/CSP and unchanged bundle budgets. Browser qualification passes
  **192 workflows, 304 routes and 48 visual comparisons plus two setup checks**.
  Live qualification passes **16 journeys**, direct read-only credential checks,
  real Flux/Trivy reconciliation and durable Velero backup/restore with both
  operations Completed. Helm and all seven image build/scan/SBOM lanes pass.
- Backend, frontend and Helm evidence each record the identical stable source
  hash `ebbb8a2ee800687be03c26943ae795aada5249ce37ce5e1df4406d1f691ef02f`.
  The backend private runner snapshot is
  `cba7dbdf412ba1c1e1a02aad71a901fbba3c33bb`, not a host commit. Evidence ZIPs:
  backend `842141_blob.zip`, frontend `406040_blob.zip`, Helm `137772_blob.zip`,
  live `190223_blob.zip`, under the task evidence directory's
  `dtu_cache/artifacts/`. Run log: `collections-guided-qualified-full-matrix.log`;
  per-lane results: `collections-guided-final-ci-results.jsonl`.
- Final health evidence: `collections-guided-final-k3s-health.log`.
  Main `k3s.service` remains active with its original PID **938597**, API
  `/readyz=ok` on **6443**, and node `astronomer-dev-1-mj` Ready. Pod names,
  readiness, status and restart counts exactly match the saved pre-work baseline.
  Older fixture container `d89d86c5261f` was paused only ahead of the live lane,
  then restarted before Helm/images completed. Its API is ready, its node is
  Ready, all non-completed pods are Running and fully ready, and its original
  **172.17.0.1:443** mapping is restored. The newly created live cluster was
  removed by its own harness; no original cluster was deleted or reconfigured.
- The source stayed frozen throughout run 62. Only this ledger and the UI
  status document are updated afterward for handoff; no runtime/test/tooling
  code changed after qualification. **379 staged paths** include the preserved
  earlier work. No commit, push or deployment was performed. The user requested
  stopping after this run, so no additional implementation batch was started.
- Still open: the listed legacy collection/picker audit and remaining UI state
  coverage (12/26/28); bounded-navigation follow-up and authorization/protocol
  decisions (20/22/25); real selected-environment agent identity/Charlie and all
  Plan 016 external/cloud/human/DR/approval evidence (29/30). The backend's
  unconfigured live identity subgate explicitly skipped and is not acceptance.
