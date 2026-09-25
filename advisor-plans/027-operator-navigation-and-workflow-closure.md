# Plan 027 — Make navigation and operator workflows predictable

> Execution is in progress on `feat/027-operator-navigation-workflows` from preserved main `89229ce5`. This plan includes the second review of the current working tree and an independent cold review for executability. Complete each phase's verification before advancing; preserve existing unrelated changes. Do not infer that a previous plan marked DONE closes a newly reproduced behavior.

## Status and baseline

- Status: DONE — implemented and reviewed; frontend/backend enterprise gates and browser acceptance passed. See [final evidence](./027-ux-audit/artifacts/implementation/final/README.md).
- Priority: P1 for misleading state, scope/navigation mistakes and interrupted operational workflows; P2 for discoverability and presentation improvements.
- Effort: L overall; execute in bounded phases below, not one large change.
- Risk: MEDIUM overall: route/search state, permissions, operation ownership and responsiveness must stay coherent.
- Category: correctness, navigation, workflow, UX verification.
- Planned at: Astronomer HEAD `22f633ec`, 2026-09-24, including substantial staged, unstaged and untracked source changes.
- Dependencies: use the existing integrated UI primitives/navigation and current Plan 026 implementation. Do not reimplement Plans 018–025. Plan 016 retains release/GA authority; this plan makes no deployment or GA claim.
- Source report: `advisor-plans/ux-navigation-review-2026-09-24.md`. This plan inlines the execution context; the report is background, not a prerequisite.

## Why this matters

Operators need to find the correct page, recognize their cluster/project/namespace scope, act, follow progress and diagnose failures without searching for the same object again. The existing console has most of the required pages, but inconsistent navigation models, transient operation feedback and lost investigation context interrupt that sequence. This plan closes those transitions while preserving Astronomer's existing product boundary and lifecycle ownership.

## Product and implementation constraints

- Astronomer adopts and operates existing Kubernetes clusters; it does not provision them. Flux is the downstream reconciliation engine. Do not add Rancher provisioning, Fleet or a second delivery runtime.
- Keep single-open sidebar sections; this was deliberately restored. Keep discovery/capability filtering, pinned/recent clusters and favorites.
- Keep Apps, Tools and Flux-managed resources' authoritative owner clear. Consolidated navigation does not authorize a new path to mutate another owner's resources.
- Use React 19, TanStack Router/Query and existing generated API clients. Do not add ad-hoc transports, invent endpoints or weaken permission checks.
- Match `frontend/docs/design-system.md`: use PageHeader/ResourceMasthead, ActionButton, DataTable, QueryStates, StatusBadge, TabStrip and the existing form kit. Multi-field navigable create/edit flows belong on routes; confirmations/pickers remain dialogs.
- Keep frontend route entrypoints limited to Route configuration; place testable components in adjacent `-page.tsx` or shared component modules. TanStack generates `routeTree.gen.ts`; do not edit it by hand.
- Honor the complexity no-growth gate. Extract modules rather than enlarge ceilinged functions; only lower justified baseline values.
- This plan authorizes no commit, push, deployment or live mutation. Those require the implementation task's own authorization.

## Drift check and working-tree preservation

Run from the Astronomer repository root:

```sh
git rev-parse --short HEAD
git diff --stat 22f633ec..HEAD -- frontend/src frontend/tests
git diff --stat -- frontend/src frontend/tests
git diff --cached --stat -- frontend/src frontend/tests
git status --short -- frontend/src frontend/tests
```

The intended baseline includes uncommitted Plan 026 and observability work. A fresh worktree at HEAD alone is **not** the reviewed baseline. Capture the authorized working snapshot before implementation, compare the excerpts below, and preserve all unrelated work. If multiple agents edit the same route/scope layer, assign one integration owner first. Do not reset or stash the user's changes to make these checks clean.

## Scope

Allowed areas, narrowed further by each phase:

- `frontend/src/components/layout/`: navigation registry, sidebar, palette, cluster switcher/scope controls and header.
- `frontend/src/lib/cluster-scope*`, `frontend/src/lib/breadcrumbs*`, `frontend/src/lib/use-tab-param.ts`: only state/context behavior required by a phase.
- `frontend/src/components/resources/`: effective status, resource detail tabs and links, list/detail context.
- `frontend/src/components/monitoring/cluster-metrics-page.tsx` and cluster overview page: operational links.
- `frontend/src/routes/dashboard/delivery/` and cluster delivery layout: navigation and scope presentation only.
- `frontend/src/routes/dashboard/alerting/` and cluster alerting integration: investigation and state/filter semantics.
- `frontend/src/components/clusters/app-install-modal.tsx`, cluster Apps routes, Catalog operation components, and `frontend/src/lib/api/cluster-apps.ts`: operation receipt/presentation and release inspection supported by existing contracts.
- Adjacent unit tests and focused browser tests in `frontend/tests/e2e/` and `frontend/tests/e2e-smoke/`.
- Generated route artifacts and genuinely decreased complexity baseline, only through canonical generators.
- This plan, its review evidence, and its index row.

Second-review additions to allowed scope, only for the named behaviors:

- `frontend/src/components/delivery/shared.tsx`, `components/catalog/project-scope.tsx`, `components/workloads/pod-logs-viewer.tsx` and adjacent tests: project transactions and shareable investigation state.
- `frontend/src/components/clusters/custom-resources-page.tsx`, `custom-resource-list.tsx`, `tools-tab.tsx`, `snapshot-page-hooks.ts`, `snapshots-page.tsx`, `snapshot-dialogs.tsx`: custom-resource scope/return, template link, snapshot state and receipt handling.
- `frontend/src/routes/dashboard/clusters/$id/image-scans/index.tsx`, `apps/-page.tsx`, `apps/-queries.tsx`, `template/index.tsx`: scope applicability and persistent template access.
- `frontend/src/routes/dashboard/settings/smtp/index.tsx`: truthful load state and saved-configuration test semantics.
- `frontend/src/routes/dashboard/logging/-pipeline-modal.tsx`, `-pipelines-tab.tsx`, `-pipeline-outputs.tsx`, `-pipeline-namespaces.tsx`, plus new adjacent `-pipeline-editor.tsx`/tests and pipeline detail/edit routes if selected in Phase 9.
- `frontend/src/lib/api/workloads.ts`, `lib/hooks/workloads.ts`, `lib/api/cluster-velero.ts`, `lib/api/logging.ts`, `lib/hooks/logging.ts`, `lib/query-keys.ts`: existing contract adapters/query identity required by scope, progress or editor work.
- `frontend/src/components/catalog/catalog-operation-timeline.tsx` and proposed `frontend/src/components/catalog/installed-release-detail.tsx` plus tests: Apps progress/detail presentation.
- `frontend/scripts/generate-route-manifest.mjs`, `frontend/tests/e2e-smoke/stub-overrides.ts`: reviewed route count/fixture updates for newly added routes. Do not hand-edit generated stubs/route trees.
- Generated code-health/operation inventory documents: only if the canonical inventory scripts report that scoped additions require regeneration; retain the exact generator command and inspect the diff. No baseline inflation or unrelated normalization.

Execution scope amendment, 2026-09-24: the user explicitly authorized fixing underlying problems wherever found, including API limits. Required workload/custom-resource multi-namespace collection contracts, cluster snapshot restore list/detail/Location, installed-release metadata by ID, catalog search and operation outcome/envelope correctness, complete paged CRD discovery, and logging-pipeline by-ID retrieval and correct update behavior are now part of this execution. Include their authorization, SQL, OpenAPI, generated clients and regression tests. Preserve tenant boundaries and lifecycle ownership. Earlier phase instructions to defer a missing API are superseded for these increments: implement and verify the supported contract before claiming workflow completion.

Out of scope: runtime ownership changes, authentication redesign, external-tool internals, global visual rebranding, unrelated test cleanup, provisioning/Fleet, protected release qualifications and the separate all-offerings live qualification campaign. Evidence and contract decisions are recorded in [the execution review](./027-execution-review.md).

## Commands and conventions

Use the lockfile-pinned existing tooling. From `frontend/`:

```sh
npm run type-check
npm run lint
npm test -- <explicit-existing-or-new-test-files>
npm run build
npm run bundle:check
```

All must exit 0. Build is an implementation gate, not an action performed by this read-only planning review. For focused browser tests use the existing Playwright configuration, a dedicated unused PLAYWRIGHT_PORT and `--workers=1`; never silently reuse another worktree's server. From repository root, run `node scripts/check-complexity-budget.mjs` and `make verify-enterprise VERIFY_SCOPE=frontend` at integration. Backend/Helm gates belong to their owning plans unless this plan is explicitly respecified to touch them.

Existing exemplars: `components/layout/sidebar-navigation.test.ts`, `components/layout/command-palette.test.tsx`, `components/layout/cluster-discovery-navigation.test.ts`, `components/resources/related-resources.test.tsx` and `components/resources/create-resource-dialog.test.tsx`, `tests/e2e/pod-drilldown.spec.ts`, `tests/e2e/resource-drilldown.spec.ts`, and `tests/e2e/critical-workflows-keyboard.spec.ts`. Match the existing `installStubs`/`seedAuth` browser helpers. Fixture-backed browser checks are not evidence of successful real-cluster operations.

Prerequisites: Node `>=24.21.0 <25`, lockfile dependencies (`npm ci` only when absent/inconsistent), installed Playwright Chromium, and Python required by the enterprise scripts. Do not install/upgrade dependencies merely to address an unrelated audit failure. Record environmental, network-dependent audit and pre-existing baseline failures separately.

Exact baseline command from `frontend/`:

```sh
npm test -- src/components/layout/sidebar-navigation.test.ts src/components/layout/nav-open-groups.test.ts src/components/layout/command-palette.test.tsx src/components/layout/cluster-discovery-navigation.test.ts
```

Exact browser invocation patterns from `frontend/` (choose/check an unused port first; 32128 is the proposed implementation port):

```sh
env -u PLAYWRIGHT_REUSE_EXISTING_SERVER CI=1 PLAYWRIGHT_PORT=32128 npm run test:e2e -- tests/e2e/<phase-spec>.spec.ts --workers=1
env -u PLAYWRIGHT_REUSE_EXISTING_SERVER CI=1 PLAYWRIGHT_PORT=32128 npm run test:e2e:smoke -- tests/e2e-smoke/explorer-navigation.spec.ts --workers=1
```

Replace `<phase-spec>` with the exact spec basename named by that phase; do not type the placeholder literally. The npm scripts run the existing fixture/manifests pre-scripts. For the responsive spec's three-project run:

```sh
node scripts/generate-e2e-stubs.mjs
env -u PLAYWRIGHT_REUSE_EXISTING_SERVER CI=1 PLAYWRIGHT_PORT=32128 npx playwright test tests/e2e/operator-shell-responsive.spec.ts --project=chromium --project=tablet-chromium --project=mobile-chromium --workers=1
```

After adding routes: add representative values for any new dynamic parameters in `scripts/generate-route-manifest.mjs`, deliberately update its `EXPECTED_ROUTE_COUNT` (144 in the reviewed source), run the normal build, then `node scripts/generate-route-manifest.mjs` and the smoke gate. Build alone does not refresh the smoke manifest.

## Phase 0 — Establish the behavioral baseline

1. Record source revision and working-tree state; compare the excerpts below and the second-review addendum before editing.
2. Enumerate all dashboard route families, mapping each to sidebar, contextual link, settings hub or intentional deep-link-only access. Identify required role/capability and scope for each family. Do not treat every parameterized detail route as requiring a sidebar row.
3. Characterize the confirmed failures with focused tests before changing navigation/state contracts. Keep tests for genuine behavior, not copies of registry implementation details.
4. Capture desktop/tablet/mobile shell and key flows over controlled fixtures, including a restricted user, empty/new cluster, failed operation and many custom types. Record the exact source snapshot and fixture limits.

Persist coverage in `advisor-plans/027-ux-audit/route-scope-matrix.md` and file hashes in `advisor-plans/027-ux-audit/source-manifest.json`. Include untracked source in the hashes; HEAD and `git diff` alone do not define this baseline. During implementation, append execution evidence without replacing the planning snapshot.

Restricted-role fixture order is load-bearing: `installStubs` → `seedAuth` → explicit overrides of `/rbac/my-permissions/**`, `/clusters/*/namespaces/**`, projects and discovery → navigate. `seedAuth` otherwise installs empty effective permissions and empty namespaces. Use a valid project-only operator with at least one allowed namespace and actual rows, a disallowed namespace, an estate inventory-only reader, a cluster operator and a superuser. Assert allowed rows render and denied data requests do not occur; hidden links or empty pages alone are insufficient. Distinguish GET request records from mutation records.

Verify: the four existing navigation test files from the initial review pass (65 tests at the review baseline). New characterization tests must expose the specified behavior; label evidence clearly and do not present intentionally reproduced defects as a green release gate.

## Phase 1 — Make scope selection govern the actual inventory

Priority P1; dependencies: Phase 0. This supersedes the first review's optimistic assessment of scope consistency.

Current facts:

- `components/resources/resource-list-page.tsx:244` queries Workloads with kind/search/page but no namespace; `explorer-data-table.tsx:242` filters that already-paginated page. `lib/hooks/workloads.ts:46` does not inject scope. The workload API at `lib/api/workloads.ts:274` accepts only a single `namespace`; `internal/handler/workloads_routes.go:29` reads it before server pagination. Do not send comma-separated namespaces to this singular parameter.
- `components/clusters/custom-resource-list.tsx:46` requests an unscoped CR page with limit/continue and ignores shared namespace selection. Namespaced and cluster-scoped CRs require different treatment.
- Image Scans uses independent local namespace state (`routes/dashboard/clusters/$id/image-scans/index.tsx:94`); Installed Apps requests cluster+pagination without namespace (`apps/-queries.tsx:27`). The topbar displays scope controls on all cluster routes.
- Header project selection updates project and namespaces together (`cluster-scope-controls.tsx:124`), whereas Apps (`apps/-page.tsx:86`) and Delivery (`components/delivery/shared.tsx:117`) replace only project. Old project-derived namespaces survive.
- The project trigger recognizes only `project.clusterId`, while other product surfaces support `project.clusterIds` as well (`cluster-scope-controls.tsx:115`).

Steps:

1. Define destination/tab scope applicability: global, cluster, project-filtered, namespaced, or mixed with explicitly labeled subsections. Add this to the route/scope matrix and shared navigation model. Cluster-wide inventory can stay cluster-wide if labeled and not represented as filtered by the selected namespace.
2. Route every project picker through one transaction: validate project membership including secondary cluster membership; replace project-derived namespaces; retain only explicitly selected valid overrides under a documented rule; reset pagination and object selection. Clearing project restores only authorized scope. Pending/failed scope resolution must not launch dependent collection queries as all-namespaces.
3. For one namespace, send that namespace before workload pagination, include scope in query identity, and reset page on scope changes. For all authorized namespaces retain the server authorization filter. For empty selection show an explicit no-namespaces state without an all-cluster query.
4. Implement the authorized bounded multi-namespace workload contract and supported custom-resource collection traversal. Filter and authorize before pagination; document continuation semantics. Do not download the entire cluster in the browser, merge independently paginated browser pages as a global page, or show false totals. Update OpenAPI/clients and consume the supported contract in the UI; an unsupported multi-scope message is only an intermediate milestone, not completion.
5. For CRs, use discovery scope and namespaced API paths; reset continuation tokens when scope/type changes. Multiple namespaces require a truthful bounded pagination model; if unsupported, gate the combined view rather than quietly ignoring selection. Label cluster-scoped CRs explicitly.
6. Bind Image Scans and supported Apps inventories to shared scope or explicitly label them cluster-wide and make the scope control's applicability clear on those tabs. Do not imply chart repository or cluster-wide health cards are namespace-filtered.

Verification fixtures: 50 workloads outside team-b before a team-b match; two namespaces with same-name CR instances; all/one/multiple/none selection; A→B project switch through Header, Apps and Delivery followed by Pods; multi-cluster project; invalid/denied/deleted project; late response during rapid switching.

Verify: create `src/components/resources/workload-scope-pagination.test.tsx`, extend `src/components/clusters/custom-resource-list.test.tsx` and `src/lib/cluster-scope.test.tsx` if present (otherwise create this explicitly named test), and create `tests/e2e/operator-scope-consistency.spec.ts`. Run `npm test -- src/components/resources/workload-scope-pagination.test.tsx src/components/clusters/custom-resource-list.test.tsx src/lib/cluster-scope.test.tsx`, then the phase browser invocation pattern with `operator-scope-consistency`, type-check and lint. Contract-gated multi-scope cases must stay recorded as unfulfilled, not skipped and called complete.

## Phase 2 — Repair failed-action recovery and truthful read states

Priority P1; dependencies: Phase 0, scope-aware cases after Phase 1. Scope is exactly the create-resource dialog, custom-resource caller/shared detail, snapshots status, SMTP and deployment-detail files named below plus adjacent tests.

1. **Guided retry:** `components/resources/create-resource-dialog.tsx:225` reuses failed `{body,path}` while guided `onChange={setManifest}` at 407 does not invalidate those results. YAML changes already clear them. After a guided edit rebuild the affected pending payload from current inputs; distinguish edited submission from unchanged failed-only retry. Preserve successful multi-document outcomes, stable idempotency where relevant, and avoid creating successful objects again.
2. **CR return:** `custom-resources-page.tsx:79` passes the CR plural into `ResourceDetail`, whose `backTo` is `/clusters/id/${resourceType}` at 156. Add an explicit canonical collection destination prop and pass `crListHref(clusterId, group, version, plural)` with valid scope. Both Back and delete-success use it. Generic built-in callers retain correct defaults.
3. **Query states:** status failure at `snapshot-page-hooks.ts:34` becomes false installation, and `snapshots-page.tsx:99` advertises Install Velero. SMTP `settings/smtp/index.tsx:439` substitutes DEFAULT_CONFIG on failed reads. Delivery deployment detail at `delivery/deployments/$deploymentId/index.tsx:145` keeps a loading description when its detail query has failed. Use explicit loading/permission/error/not-found/success-empty rendering before configuration/installation assumptions. Preserve retry and previously valid stale data with honest labeling.
4. **SMTP testing:** the form at `settings/smtp/index.tsx:89` sends only recipient, so it tests saved config. Label Test saved configuration and disable or explain it while unsaved edits exist, requiring save first. Do not add an unplanned temporary-credentials API or send secret draft values through a new path.

Tests: failed create → edit guided field → retry posts corrected body; unchanged failed-only batch retry excludes successes; CR Back and delete success retain group/version/scope; Velero 403/offline versus successful installed=false; SMTP 403/offline versus genuinely unconfigured; delivery 403/404/network; draft SMTP changes make saved-only testing unambiguous. 5xx can be owned by the existing global boundary; do not assert that all errors currently follow the same branch.

Verify: `npm test -- src/components/resources/create-resource-dialog.test.tsx src/components/resources/create-resource-manifest.test.ts` plus new `src/components/clusters/custom-resource-return.test.tsx`, `src/components/clusters/snapshots-read-state.test.tsx`, `src/routes/dashboard/settings/smtp/smtp-read-state.test.tsx`, `src/routes/dashboard/delivery/deployments/deployment-read-state.test.tsx`. Run the browser pattern with new `operator-failure-recovery.spec.ts`; type-check/lint.

## Phase 3 — Correct displayed status and connect operational summaries

Current excerpts:

```tsx
// components/resources/workload-resource-tabs.tsx:71
accessor: (pod) => <StatusBadge status={pod.phase} />,
// lib/api/workloads.ts:135 already supplies this value:
status: wire.status ?? phase,
```

`components/monitoring/cluster-metrics-page.tsx:63,144` renders node/namespace names as plain text; tables at 329/345 have no row destination. Cluster overview `routes/dashboard/clusters/$id/-page.tsx:355` has unlinked CPU/Memory/Nodes/Pods cards next to linked CVE/Tools cards.

1. Use the existing effective pod status consistently with primary pod list/detail, handling blank/missing status explicitly and preserving meaningful phase separately if needed.
2. Link node/namespace names to existing detail/list destinations with applicable scope. Link Nodes/Pods totals to inventories and utilization to Metrics. Use real RouterLinks, not click-only table cells.
3. Preserve available permission filtering and truthful unavailable states; do not fabricate routes or zero values when data is unavailable.

Tests: workload Pods displays CrashLoopBackOff/ImagePullBackOff/terminating/healthy states; summary links resolve with keyboard and preserve valid scope.

Verify: `npm test -- src/components/resources/workload-resource-tabs.test.tsx` after creating/extending that exact test, plus a new `tests/e2e/operator-summary-navigation.spec.ts` run with `--project=chromium --project=mobile-chromium --workers=1`; then type-check/lint. Every test must pass after this phase.

## Phase 4 — Unify Delivery destinations and clarify scope

Dependencies: Phase 1's scope model for scoped links. Add the following second-review navigation fixes to this phase:

- Match visibility to supported destination permissions. Estate currently requires `delivery_targets:list` in the sidebar but checks `delivery_inventory:read` on the page, with a project fallback. `use-sidebar-navigation.ts:56` passes only global/cluster scope to permission filtering. Define the allowed alternatives for entering a workspace separately from the current project/object's action permission; never grant actions because a workspace is visible. Test inventory-only reader, project-only delivery operator, cluster operator and superuser with nonempty authorized fixtures.
- Keep applied onboarding template management reachable after tools install. The only existing link at `components/clusters/tools-tab.tsx:99` is inside `noToolsInstalled && !isDisconnected`; `/clusters/$id/template` still provides binding/reapply/detach. Add a persistent Onboarding template entry under Cluster Management and in page search, with the route's read/action gates.
- Resolve one most-specific active sidebar destination. Metrics `/dashboard/monitoring` currently prefix-matches Shared stacks `/dashboard/monitoring/stacks`; `sidebar-nav-items.tsx:28` marks both active. Verify actual `aria-current` and router `activeOptions`, not only CSS classes; root/logo links can also receive TanStack's default active attributes. Favorites may intentionally duplicate a destination visually, but the canonical active leaf must be unambiguous within each navigation landmark.

Additional exact verification: `npm test -- src/components/layout/sidebar-nav-items.test.tsx src/components/layout/sidebar-navigation.test.ts src/components/layout/command-palette.test.tsx` and new `tests/e2e/navigation-role-contract.spec.ts` via the phase browser pattern. Include installed-tools template access and Shared stacks active-state assertions.

Current state: `components/layout/sidebar-navigation.ts:138` exposes Estate/Templates/Overrides. `routes/dashboard/delivery/route.tsx:13` also exposes Sources/Bundles/Targets/Rollouts/Deployments. Estate exact-matching leaves those pages without a matching sidebar destination, and global palette entries are derived from the smaller registry. The layout's project selector sits above an estate query that is not project-filtered (`delivery/-page.tsx:67`).

1. Define one canonical Delivery destination model with path, label, permission and scope semantics. Sidebar, palette, section selection and any retained page navigation must consume it.
2. Expose actual durable destinations under Continuous Delivery. Avoid duplicate competing navigation for the same level; retain contextual tabs for resource detail views.
3. Label the estate page as all-cluster scope and only show a project selector where it controls the content, or explicitly explain and visually separate its purpose. Do not change backend inventory semantics to make a label true.
4. Preserve project search state through sidebar, palette and detail links. Match permissions per destination; a delivery-target permission is not automatically the contract for every inventory page.
5. Validate breadcrumbs and document titles against the resulting label registry. Keep existing URLs unless a route change is explicitly justified.

Tests: every Delivery page has a discoverable entry and the correct active group; direct links, refresh, browser Back, missing project, denied project, and project changes preserve accurate scope. Test admin and restricted roles.

Verify: `npm test -- src/components/layout/sidebar-navigation.test.ts src/components/layout/command-palette.test.tsx` plus new `src/components/layout/delivery-navigation.test.ts`; browser `tests/e2e/delivery-navigation-context.spec.ts` on desktop/mobile; type-check/lint. If route files are added, regenerate using the normal build and check route inventory.

## Phase 5 — Make page finding and cluster switching predictable

Current excerpts:

```tsx
// components/layout/cluster-switcher-menu.tsx:249
const subRoute = clusterId
  ? pathname.slice(`/dashboard/clusters/${clusterId}`.length)
  : "";
const nextPath = `/dashboard/clusters/${next.id}${subRoute}`;
```

The page palette opens only on Ctrl/Cmd+K (`command-palette.tsx:13`). The visible global search is resource search; its palette hint is noninteractive. Cluster results use only `page.label` (`command-palette-dialog.tsx:255`), producing ambiguous Overview entries. Dynamic navigation is capped at 40 types and reused as the entire palette source.

1. Extract a testable route-transition policy: preserve supported list/overview routes, map object detail to its canonical parent list, and fall back to overview for unavailable capabilities. Use route semantics, not unrestricted suffix concatenation.
2. Classify transferable search keys. Restore the destination cluster's valid project/namespace selection; remove old object IDs, install triggers and incompatible filters. Do not copy `returnTo` or mutation-driving query state without validation.
3. Add a visible accessible Go to page control opening the existing palette. Preserve the existing keyboard shortcut and resource search behavior.
4. Display/search parent section and API group. Index the complete authorized discovery model for page search while keeping sidebar display and resource-count queries bounded.
5. Verify cluster scoped return links and actual target availability; do not issue unbounded lookups for every cluster/type merely to render the switcher.

Transition decision table (required behavior, not a generic suffix-preservation helper):

| Starting destination | Destination after switch | Transferable state |
|---|---|---|
| Cluster overview | Target overview | Valid target scope only |
| Built-in resource list | Same supported list type | Target scope; search/sort only when explicitly accepted by that list's URL schema; reset page/selection |
| Namespaced or cluster-scoped built-in detail | Parent collection | No old object name/namespace/UID; target scope only |
| Custom-resource list/detail | Same group/version/plural list only after selected target discovery confirms it; otherwise overview with explanation | Target scope; clear old object, continuation and selected tab |
| Delivery entity detail | Corresponding target-cluster collection or overview | Revalidate target project's membership; remove old entity ID and dependent pagination |
| Apps browse/installed/operation | Apps landing for target | Drop install trigger, release/operation IDs and modal state; revalidate project |
| Optional add-on destination | Same destination only if capability known available | Target scope; no old operation/action parameters |
| Unknown/unclassified path | Target overview | No arbitrary suffix or unknown search keys |

Destination scope/discovery pending: show a resolving state with dependent requests disabled. Failure: show retry/explanation without claiming absence or silently broadening to All namespaces. It is acceptable to navigate to target overview first and resolve the intended list after selected-target facts arrive; only the latest selection may win. A→B→C must not let B's late response redirect C or overwrite C's remembered scope. Reuse cached selected-target facts when valid and avoid prefetching all possible destinations.

Palette entries need stable unique command values derived from destination identity, not `${page.label} cluster`; section/API-group descriptions alone do not fix duplicate keyboard selection identity. Test selecting each of two Overview entries by keyboard and verify different intended URLs.

Tests: list/detail/custom-resource/delivery/add-on transitions; same-named objects in two clusters; destination lacks namespace/capability; no permission; ambiguous labels; >40 custom types; pointer/keyboard/touch palette opening.

Verify: new `src/components/layout/cluster-navigation-transition.test.ts` and `src/components/layout/command-palette-pages.test.ts` plus existing palette/discovery tests. Run new `tests/e2e/cluster-switching-context.spec.ts` desktop/mobile and existing `tests/e2e-smoke/explorer-navigation.spec.ts` under the two route-smoke projects; type-check/lint.

## Phase 6 — Keep investigation state in URLs and contextual links

Current excerpts from `components/resources/resource-detail.tsx`:

```tsx
const [tab, setTab] = useState<ResourceDetailTabId>("overview"); // 56
const backTo = `/dashboard/clusters/${clusterId}/${resourceType}`; // 156
```

1. Use the existing validated URL tab pattern for resource detail. Only admit tabs supported by kind and permissions; invalid values fall back to Overview.
2. Preserve investigation scope/filter and originating workload when drilling into a pod. Provide an explicit Back to workload link where the relationship is known, while retaining canonical navigation for direct entry.
3. Keep container/time/filter choices shareable where the existing APIs support them. Do not automatically execute or restore a terminal session from a URL.
4. Ensure browser Back restores the selected workload Pods tab and list state. Use safe internal destinations; never arbitrary external return URLs.

History contract: retain `useTabParam`'s existing **replace** behavior for selecting a same-page tab. Navigating to a pod **pushes** history; browser Back returns to the workload URL already containing its selected tab and valid filters. Do not change the shared hook globally to push history. Test using a real router/browser history, not only a mocked navigate callback. Preserve the canonical CR collection supplied by Phase 2.

Tests: direct logs/events links, refresh, back/forward, permission change, invalid tab, deleted parent, multi-container pod and direct entry without origin.

Verify: new `src/components/resources/resource-navigation-context.test.tsx`; existing `tests/e2e/pod-drilldown.spec.ts`, `tests/e2e/resource-drilldown.spec.ts`, and new `tests/e2e/resource-context-history.spec.ts` desktop/mobile; type-check/lint.

## Phase 7 — Turn alerts into investigation entry points

Current state: `routes/dashboard/alerting/-events-tab.tsx:59,66,79` displays nonlinked rule/cluster and clipped message. Its actions at 103 offer Ack/Resolve only. The Active Alerts tab defaults to an unfiltered status collection.

1. Provide a URL-addressable alert selection with full message, rule, cluster, timestamps and current state using existing returned fields. Decide drawer versus route based on the need for navigable subcontent; use existing overlay primitives if a drawer suffices.
2. Add explicit rule/cluster/metrics investigation links and only show resource links when a real resource identity exists in the data.
3. Make Active Alerts semantics accurate and offer History separately. Do not assume the API accepts multiple statuses; inspect the contract and choose a supported query/label design.
4. Persist relevant filters and selected alert through navigation/back. Pending/error feedback for Ack/Resolve must remain visible, and repeated submission must not be encouraged by enabled duplicate controls.

Tests: long messages, resolved history, acknowledged/firing alerts, missing rule/cluster, denied destination, pending and failed mutation, pagination and deep-link recovery.

Verify: new `src/routes/dashboard/alerting/alert-investigation.test.tsx` and `tests/e2e/alert-investigation.spec.ts` desktop/mobile; applicable alert hook tests, type-check/lint. If current fields cannot support reliable deep links, record that exact contract gap rather than invent an endpoint.

## Phase 8 — Complete Apps progress and release inspection

Current excerpts:

```ts
// lib/api/cluster-apps.ts:234
return { id: wire.data.installation.id ?? "" };
// components/clusters/app-install-modal.tsx:208
onSuccess: () => { /* toast, invalidate installed query, close */ }
```

Global Catalog retains `receipt.operation.id` (`routes/dashboard/catalog/-install-chart-modal.tsx:101`) and renders CatalogOperationTimeline. Cluster Installed rows show plain release names (`clusters/$id/apps/-installed-tab.tsx:253`), and ordinary releases expose Upgrade/Uninstall without inspection. Tools-owned rows already pivot to their authoritative owner.

1. Preserve the canonical install/upgrade/uninstall operation receipt through the cluster API adapter and dialogs where the generated response exposes it. Match the existing Catalog receipt pattern.
2. Show persistent operation progress and a direct View installed release action. Make progress recoverable after navigation/refresh using existing operation contracts; do not claim session-only state is durable recovery.
3. Make release inspection navigable, using existing installed-release, operation and resource APIs. Include namespace/version, effective status, values/history where supported, related resources and diagnostics. Do not show unavailable sections as empty success.
4. Preserve owner-specific controls for Tools/Flux/catalog releases. Make global versus cluster repository scope explicit and avoid divergent install behavior across global Catalog and cluster Apps.
5. Keep managed-resource mutation warnings and eligibility decisions grounded in the actual ownership contract. Do not add a bypass path.

Tests: install/upgrade/uninstall accepted, running, failed and complete; revisit operation; missing release; permission failure; tool-owned release; namespace/resource pivots; no invented success on a failed poll.

Verify: adapter tests under `src/lib/api/cluster-apps.test.ts`, existing cluster Apps modal tests, new `tests/e2e/apps-operation-navigation.spec.ts` desktop/mobile, type-check/lint. A full release-detail route is a bounded second increment after receipt/progress parity. Implement the authorized metadata GET-by-ID alongside existing values/revisions/operation contracts, with authorization and redaction tests.

## Phase 9 — Inspect and edit logging pipelines without recreating them

Priority P1/P2; dependencies: Phase 1 scope semantics. Current `routes/dashboard/logging/-pipelines-tab.tsx:120` renders only output count, at 133 toggles enabled, and at 154 exposes Delete. `-pipeline-modal.tsx:18` is create-only; the update adapter already exists and is used at `-pipelines-tab.tsx:49`.

1. Add an inspect action showing every namespace, destination and filter, with names resolved without dropping unknown/denied IDs. Use an explicit unavailable state for inaccessible destinations, not an empty replacement list.
2. Extract shared typed fields into proposed `routes/dashboard/logging/-pipeline-editor.tsx`. Prefer navigable detail/edit routes for the multi-field resource (`logging/pipelines/$pipelineId/index.tsx` and `edit/index.tsx`) with testable adjacent modules; retain existing creation URL/entry behavior until its migration is deliberately included. Account for route-generator fixture/count updates.
3. Initialize edits from the complete existing pipeline payload. Preserve unsupported filter variants verbatim; the current create form's include-label mapping must not flatten or delete other existing filter types. If editing a variant cannot be represented safely, show read-only details and explain the limitation.
4. Save via existing `updateLoggingPipeline`; preserve cluster/namespace/output eligibility and feedback on pending/failure. Do not implement delete/recreate as an edit. Add an unsaved-changes guard using the established form pattern.

Verify: new `src/routes/dashboard/logging/pipeline-editor.test.tsx` covering no-op round-trip, supported field changes, unknown filters, missing destinations and failed save; browser `tests/e2e/logging-pipeline-edit.spec.ts` via the phase pattern; existing `src/lib/api/logging.test.ts`; type-check/lint and smoke generator after route additions. Implement the authorized GET-by-ID contract and repair relevant update/association defects so editing does not depend on a bounded list lookup. Preserve unknown supported filters and verify the complete round trip.

## Phase 10 — Restore receipt continuity and resolve the tracking contract

Priority P1 for outcome visibility; L; dependencies: Phase 2 truthful snapshot states. This phase has a **contract discovery gate** and must not be described as a guaranteed frontend-only change.

Current `components/clusters/snapshot-dialogs.tsx:348` ignores the returned restore, toasts Restore queued and closes; `lib/api/cluster-velero.ts:242` models a SnapshotRestore with ID/source/target/phase/counts. The snapshots page has no restore history. Legacy `/dashboard/backups/restores/$restoreId` redirects to management backup settings.

1. Preserve the cluster snapshot restore receipt in UI state and show source snapshot, source/target cluster, restore ID and queued status explicitly. Do not label the source backup's Completed status as the restore outcome.
2. Trace the **cluster snapshot** restore ID through the generated contracts and handlers before selecting a polling API. `docs/openapi.yaml` defines SnapshotRestoreResponse separately from RestoreOperationResponse; `/api/v1/backups/restores/{id}` and general Velero backup restore history exist but are not automatically the same identity or storage domain. `internal/handler/cluster_snapshots_restore.go:134` stores `cluster_restores`, with separate query interfaces in `cluster_snapshots.go:66`. Do not substitute the similarly named general Velero backup endpoint.
3. Record the verified retrieval route/permission/state contract in `advisor-plans/027-ux-audit/restore-tracking-contract.md`. Implement the now-authorized target-cluster restore list/detail backend/OpenAPI/client extension, source/target authorization and correct Accepted Location. Reuse the existing durable restore rows and poller.
4. Once retrieval is verified/authorized, add a durable linked restore detail/history surface in the correct cluster context. Support queued/running/completed/partial/failed, source/target distinctions, refresh/back and read denial. While retrieval is unavailable, state that outcome tracking is unavailable and expose the receipt; do not show an endless invented progress state.

Verify the existing client adapter using `npm test -- src/lib/api/cluster-velero.test.ts` after creating/extending that exact file, new `src/components/clusters/snapshot-restore-receipt.test.tsx`, and browser `tests/e2e/snapshot-restore-tracking.spec.ts` via the phase pattern. Fixture tests must assert source/target identity and must not treat queued as successful restore. Real restore verification remains separately authorized under the existing live qualification plan; never restore into a live cluster to complete this planning task.

## Phase 11 — Verify and repair responsive shell behavior

The second review reproduced clipping and selector overlap against current source with stubbed APIs at 390/412/768/1024px. At 390px the user menu starts at x=572 and Import spans x=399–484. At 1024px the cluster and project controls start at the same x=264 and the user menu extends to x=1096. The 1280px controls fit but the selector edges still overlap slightly. Retained geometry and screenshots are under `027-ux-audit/artifacts/review-measure-current-source-shell-at-five-widths/`. Recheck current controls at all five explicit widths with long names, restricted roles, selected scope and open overlays; the three default Playwright project widths alone are insufficient.

1. Keep cluster and namespace context readable at narrow widths. Move secondary actions into an accessible overflow control as required by measured geometry.
2. Ensure account, notifications, Import, shell and kubeconfig remain reachable with mouse, keyboard and touch. Menus must fit the viewport and restore focus to a still-visible trigger.
3. Fix the now-reproduced closed mobile sidebar focus leak: Tab from Skip to main content focuses its off-screen logo at x=-224. Remove closed off-canvas content from sequential keyboard navigation using an appropriate inert/hidden strategy while preserving desktop visibility. Verify open sidebar focus containment/closing behavior, visible active destination and return to its invoking control.

Do not assume the side navigation shares ModalShell behavior: inspect its own overlay/focus code and assert it independently. Preserve existing proven modal focus containment/restoration; this plan does not claim those primitives lack it.
4. Inspect repeated/contradictory header actions on overview versus global chrome; keep one predictable primary location and intentional contextual shortcuts.

Verify: new `tests/e2e/operator-shell-responsive.spec.ts` asserts individual actionable control rectangles are inside the viewport and reachable, not merely `scrollWidth <= clientWidth`; run desktop/tablet/mobile projects, existing keyboard workflows and route-smoke axe checks. Preserve failure screenshots. Type-check/lint must pass.

## Phase 12 — Integration and task acceptance

Run type-check, lint, complete frontend unit tests, build, bundle budget and complexity gate; then the frontend enterprise gate. Run the focused browser journeys serially against a known build snapshot. Do not accept changed screenshot baselines without inspecting controls and scope at all target widths.

Required task evidence:

- Find a failing workload/pod, identify its real state, inspect/share Logs/Events and return to the originating view.
- Switch clusters from a list and detail without carrying an inappropriate object identity.
- Find every Delivery destination, see correct scope and retain valid project context through navigation.
- Investigate an alert without manually searching for its cluster/rule; mutate it with truthful progress/error handling.
- Install an app and recover operation progress/diagnostics after navigation/refresh; inspect its resulting release/resources.
- Find a custom-resource type beyond the sidebar display cap using visible page navigation.
- Complete the above as permitted for admin and restricted roles, and reach shell controls on mobile.

## Done criteria

- [x] All required behavioral tests exist and pass; all common commands exit 0.
- [x] Navigation/route inclusion and scope matrix is current; intentional omissions are documented.
- [x] Every P1 finding from the final second-review addendum is either implemented with evidence or explicitly respecified with a concrete blocker.
- [x] Source drift and unrelated changes are preserved; no unexplained out-of-scope modifications.
- [x] Fresh reviewed screenshots and exact test commands/results are retained with the implementation handoff.
- [x] Remaining external/live qualifications stay open under the owning plans.
- [x] Update this plan and its README row only when work actually meets these criteria.

## STOP conditions and maintenance

Investigate and repair contract/permission problems on the authorized workflows; update canonical specifications and tests together. Stop the affected phase only for an unresolved external dependency, a required lifecycle ownership change, or a route/namespace decision that would broaden the user's effective scope. Continue independent authorized phases when possible; do not use one unknown to abandon the entire plan.

Future routes must enter the canonical navigation/scope model and its behavior tests. Search indexing may be complete while sidebar rendering/count queries stay bounded. Review cross-cluster transitions, query state and operation receipt persistence whenever new resource types or Apps owners are added.

## Second-review addendum

Completed: [second-review findings and evidence](./027-ux-second-review.md), [route/scope matrix](./027-ux-audit/route-scope-matrix.md), [working-source hashes](./027-ux-audit/source-manifest.json), and [restore contract checkpoint](./027-ux-audit/restore-tracking-contract.md).

The second review adds namespace pagination/CR scope, inconsistent project transactions, role-dependent navigation, post-install template reachability, wrong CR return routes, stale guided retries, false query states, restore tracking, logging edit, SMTP test semantics, active-state ambiguity and closed-sidebar focus. A closed mobile sidebar must also be unavailable as navigation to assistive technology; verify both keyboard focus and accessibility-tree behavior. Fresh source browser checks substantiate six defect scenarios plus header geometry at five widths. The initial render observations covered 22 representative pages; source review cataloged 22 families and all 144 existing route-fixture records. None of these is a claim of full live-backend qualification.

Independent cold review revisions are incorporated: realistic restricted-role fixtures, exact baseline/browser commands and prerequisites, corrected allowed file scope, route generator prerequisites, source snapshot hashes, cluster transition policy, explicit history semantics and unique palette identities. The initial report's "solid foundation" assessment remains about category coverage and shared primitives; scope correctness and recovery are now the first implementation priorities.

Recommended execution waves: (A) Phases 0, 1 supported contracts, 2 and 11 functional defects; (B) Phases 3–6 navigation coherence; (C) Phases 7–9 and verified portions of 10 workflow completion; (D) Phase 12 integration. These numbers identify work packages, not an instruction to postpone responsive defects until after all other phases. Required API increments in Phases 1, 8, 9 and 10 run in isolated worktrees and are integrated before final acceptance; they must not delay independent frontend fixes.

API functionality qualification is tracked separately in [Plan 028](./028-all-offerings-api-qualification.md) and its [complete offering test matrix](./028-offering-test-inventory.md). Fixture-based navigation checks in this plan do not qualify an integration as working.


## Completion record

Completed on `feat/027-operator-navigation-workflows`, with the original work preserved on `main`/`origin/main` at `89229ce5`. Final production acceptance is bound to `5b4c2bc8`; `b137a31d` adds a separately verified permission-change browser case. All phases 0–12 are implemented, including necessary API, SQL, generated-client, CLI and transport corrections. Consolidated test filenames are mapped in the retained evidence rather than duplicating the proposed filenames from this plan.

Both enterprise gates pass. Frontend: 289 files/1,792 tests. Backend: 111 packages ordinary and 111 race. Required disposable PostgreSQL: 18/18, zero required skips. Browser:134 operator/keyboard/resource cases,308 route/accessibility cases,4 tablet cases, and14 history/permission cases pass; counts include setup and overlap. All 146 representative routes are covered. See [exact commands, source identities, reports and screenshot limitations](./027-ux-audit/artifacts/implementation/final/README.md).

The complete route matrix and all P1 findings are closed by the implementation and tests. Additional concrete findings from integration—Delivery bootstrap loading and create-template initialization—were fixed before acceptance. No API cap was bypassed, no budget ceiling increased, and no live integration was marked qualified by fixture results. Plan 028 and protected release qualifications remain open under their existing authority.
