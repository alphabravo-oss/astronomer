# UI overhaul — status and plan (2026-09-22)

Single-page status for the Rancher-parity UI overhaul. The audit is
[017](./017-ui-overhaul-review-2026-09-21.md); the execution plans are 018–025;
the index with per-plan status rows is [README.md](./README.md).

## 1. Where things stand

### Latest local development deployment — 2026-09-23

The local K3s upgrade reached revision **114** with schema **62 → 66**, followed
by revision **115** restoring single-open sidebar sections. The latest add-on
follow-up is **revision 121**, with default-on/opt-out Trivy onboarding deployed.
The frontend passes **1,737 unit tests** and the complete frontend enterprise
gate. Real member-cluster metrics, Grafana, scan reports/API/UI, and Fluent Bit
to Loki workload-log ingestion passed. Native observability routing and failed
install/upgrade/deletion/status paths have remaining defects; see
[the detailed add-on validation report](./addon-validation-2026-09-23.md).
Both new K3d members remain attached; all five cluster connections are healthy.
The main K3s service, storage and ingress remain intact.

Fresh bootstrap exposed a CRD discovery-ordering defect: the combined manifest
needs a second apply after CRDs establish. This remains an explicit follow-up,
not a clean one-command enrollment claim. Exact versions, recovery evidence,
acceptance checks and limitations are in
[the local upgrade report](./dev-k3s-upgrade-2026-09-23.md).

### Latest working-tree implementation — Plan 026

Pre-deployment qualification (2026-09-23): full pinned Local CI run **62 passes 25/25
lanes in 67m 35s**, including the final aggregate, all stateful normal/race
variants and all seven image checks. Frontend: **1,730 unit tests, 192 workflow
checks, 304 route checks and 48 visual comparisons** (plus two setup checks).
All **16 live journeys**, actual Flux/Trivy reconciliation and Velero
backup/restore pass. Backend/frontend/Helm record the same stable source hash;
exact evidence is in Plan 026's final handoff section.

Both original K3s nodes are Ready. The main service was never stopped or
reconfigured and its workloads match the original baseline. The older test
cluster's bridge-only `443` mapping is restored; the main API remains on `6443`.
The user requested stopping after this run. Remaining legacy-selector/UI-state
work, design decisions and protected external qualifications remain open in
Plan 026. No commit, push or deployment was performed at that CI handoff;
the later authorized dev deployment is recorded above.

The user-authorized expanded review is tracked in
[Plan 026](./026-platform-robustness-and-workflow-closure.md), including the
implemented portion of Plan 025. It adds bounded agent inventories, strict
optional request validation, safe indexed forms, truthful operational states,
paged/remote selection, grouping and bulk workload operations, shared built-in
and custom-resource YAML actions, related-resource links, project-scoped global
delivery lists, and native RBAC grant management. Earlier staged cleanup is
preserved; these additions are now deployed to local dev K3s as recorded above,
but remain uncommitted and unpushed.

#### Earlier verification snapshots (superseded by the latest run above)

Host backend, frontend and Helm gates pass (scope and exact snapshots are in
Plan 026). Final frontend: 262 files / 1,620 tests; 138 desktop/mobile E2E tests,
304 route-crawl checks and 48 desktop/tablet/mobile visual comparisons pass.
Local CI frontend, PostgreSQL integrations and
failover certification also pass. The representative run is **not green**:
its backend lane failed during migration CLI installation, and its browser lane
exposed four stale assertions on both viewports. Those assertions are corrected
and the full browser rerun is recorded in Plan 026. Live/full-matrix qualification
remains open. The following merged-branch narrative is the earlier baseline.

The subsequent Plan 026 continuation adds server-paged RBAC users, roles and
bindings; visible-row name resolution; remote binding role/project and monitoring
storage selection; corrected binding-pagination OpenAPI contracts; and safe
nested-picker keyboard handling. API-contract and final frontend enterprise
qualification pass: 1,644 unit tests, 138 desktop/mobile E2E tests and 48 visual
comparisons. Exact evidence is recorded in Plan 026.

The latest continuation adds project-filtered Members pagination and truthful
assignment counts, scope-correct paged SSO roles, and shared pagination labels
that distinguish exact, lower-bound and unavailable totals. Empty later pages
retain recovery navigation. The frozen frontend gate passes **1,662 tests**,
lint/type-check/build and unchanged bundle budgets; the final build passes
**142 desktop/mobile E2E tests, 304 route checks and 48 three-viewport visual
comparisons**.
The next continuation replaces Audit's capped name/filter inventories with
exact-ID lookup and remote selection, pages CIS history with page-only summary
labels, and pages CVE details with truthful missing-total counts. It also fixes
the CVE drawer's mobile filter placement and toolbar overlap. Final frontend
qualification passes **1,677 tests**, lint/type-check/build and bundle budgets.
Browser checks pass **148 single-worker E2E tests, 304 route checks and 48 visual
comparisons**; an earlier four-worker keyboard-create failure is retained with
its cause unresolved. Exact evidence and cleanup scope are recorded in Plan 026.
Advanced guided unions, additional negative browser
workflows and the previously listed external/CI work remain open.

The negative-workflow continuation adds desktop/mobile native-grant denial and
recovery, bulk partial/unconfirmed/cancellation outcomes, and denied relationship
continuation. It fixes missing Previous navigation after relationship errors and
removes unsupported page-local sorting from native grants. Full E2E passes
**162 checks**, with **48 visual comparisons** unchanged. The frozen frontend
gate passes **1,678 tests**, lint/type-check/build, bundle budgets and dependency
audit. Exact evidence is recorded in Plan 026; remaining state combinations,
advanced guided fields and Local CI/live qualification remain open.

The latest continuation fixes virtualized-table crashes while filtering, arrow
keys escaping editable cells, and keyboard reentry after a far row is filtered
out. A separate three-viewport component suite checks grouping and selection;
the real CIS browser workflow covers the filtering regression. Qualification:
**1,680 unit tests, 162 console E2E checks, 12 component-browser checks and 48
visual comparisons**, with unchanged budgets and snapshots. No fixture route is
shipped. Remaining selector/state coverage, guided unions and CI/live work stay
open in Plan 026.

The latest continuation replaces capped Catalog/Apps project selection with remote
search/paging and direct deep-link resolution, and fixes CIS detail error and
re-run recovery states. Project-bound dialogs hide when their scope is unavailable;
selected-project lookup failures keep the picker usable. Qualification passes
**1,699 unit tests, 180 desktop/mobile browser checks and 48 visual comparisons**,
plus the frozen-source frontend gate with unchanged budgets. Plan 026 records the
exact snapshot and evidence. Delivery/template selectors, remaining Catalog
collection limits, guided unions and Local CI/live qualification remain open.

The 2026-09-23 continuation implements paged delivery/template pickers, Catalog
and Apps collections, rollout events, logging operations and namespace resource
tabs. Apps version/default-value reads now use the correct project scope.
Guided affinity and Ingress backend unions have preservation/validation tests;
shared monitoring requires an explicit searchable cluster choice. Final
qualification passes **1,724 unit tests, 192 workflow checks, 304 route checks,
12 component-browser checks and 48 visual comparisons**, with stable source
identity and unchanged budgets. Pinned Local CI qualification is next. See
Plan 026 for exact evidence, legacy collection candidates and remaining API/live
work; this is not a declaration that the platform audit is complete.

The integration follow-up fixes headers-only SSE reconnect churn, a lost drawer
Escape listener and pagination clicks lost during background refresh. Final
frontend qualification passes **1,730 tests**, plus **192 workflow, 304 route,
12 component-browser and 48 visual comparisons**. Representative CI recheck 41
passes seven lanes but exposed a live startup migration gap: the two newer
preference arrays lacked durable JSON contract registrations. Additive migration
066 and schema-66 release metadata fix it; PostgreSQL 16/17 roundtrips pass.
The complete disposable live harness now passes **16 journeys**, real Flux/Trivy
reconciliation and Velero backup/restore. A separate local-runner native-runtime
mismatch is caught by a new preflight and resolved using its supported system
Node runtime. Final representative/full-matrix reruns remain pending. The user's
main K3s service was not restarted or reconfigured; the older disposable cluster
was temporarily paused for its 443 port and restarted after each live attempt.

Complete Local CI run 53 finished **22/24 lanes passing**. Both failures are
the Redis-outage test helper rejecting Local CI's explicitly configured Docker
bridge address; production durable intent and audit assertions passed first.
The helper now validates that exact private/loopback binding, refreshes only
the changed port and preserves the client hostname, with 18 boundary cases.
Host and Local CI recovery reruns now pass in both normal/race modes (focused
run 54: 2/2). The corrected complete matrix is next.
All seven image checks, backend, frontend, browser, live and Helm passed in
run 53. Both original K3s nodes were restored Ready; see Plan 026 for evidence
and remaining design/external qualifications.

The final aggregate also exposed a pinned Local CI expression/result-context
limitation, reproduced before completing another long run. A version-bound
repair preserves actual matrix failures and supplies the unchanged workflow's
dependency context. Eight tooling tests and real passing/failing diagnostic
workflows validate it after a clean install. The complete matrix is rerunning;
cancelled attempt 56 is not counted as qualification.

### Previously integrated baseline

The seven reviewed branches are integrated on **`advisor/ui-overhaul-integration`**
at `642ab6c5`. Planning documents were committed as `e20f98db`. `main` remains
untouched at `59619920`; nothing has been pushed or deployed.

Plan 020 is complete and merged at `15025b73` on top of that integration:
discovery-gated navigation, CRD subgroups, persisted starred resource types,
bounded metadata counts, and migration 065. The final production revision
`7a41061d` passed all three enterprise scopes on a clean, unchanged tree;
1,495 frontend unit tests and the final 296-check desktop/mobile smoke crawl pass.
The browser-only selector correction is `c0f9252c`.
See the [validation record](./ui-overhaul-validation-2026-09-22.md) for retained
evidence and qualification limits. Plan 025 is the next feature phase, not part
of this implementation.

The subsequent review cleanup is implemented in the working tree: project
member reads now distinguish denial/errors from an empty roster; counts follow
visible desktop flyouts/mobile groups (including starred CRDs); and kind-less
YAML imports resolve installed CRDs by group, served version, plural, and scope.
The frontend enterprise gate and 12 targeted browser checks pass, as does the
full browser coverage when qualified per viewport (see the retained failed
concurrent run and passing isolated desktop rerun in the validation record). The attempted
representative Local CI run exposed Charlie-suite timeouts/assertion failures;
that preflight remains blocked. See the validation record's post-review section.

The integration also exposed issues that individual branch checks missed:
exported route components defeated automatic code splitting, and the Helm
preflight/release compatibility contract still targeted schema 62. Page modules
are now separate from route entrypoints, optional UI loads lazily, and the chart,
binary, and generated compatibility contract agree on schema 65. Bundle ceilings
were not increased. Collapsed navigation now portals its flyout outside the
sidebar's scroll clipping boundary.
The project-members card's route-layer imports were also moved into shared RBAC
components, preserving one implementation and restoring dependency direction.

Integrated branch ledger:

| Merge order | Branch | Base | Plan | Contents |
|---|---|---|---|---|
| 1 | `advisor/p0-ui-slice` | `59619920` | 023 §1–3, 024 §1–2 | Delivery overview reports failures instead of zeros; Apps/baselines error states; register-wizard draft survives refresh; Helm upgrade modal wired; branding/banners rendered |
| 2 | `advisor/021-design-system` | p0 | 021 | `ResourceMasthead`; accessible `Tabs`; `<th scope>` + sort buttons; one `StatusBadge`; Card/Switch/MetricCard/Field variants; `EmptyState` requires an action; brand + chart tokens; lint rules + counting ratchets; `frontend/docs/design-system.md` |
| 3 | `advisor/018-navigation-model` | p0 | 018 | Multi-open sidebar; regrouped global nav; delivery estate layout; one label registry → breadcrumbs + `document.title`; palette derived from nav; SSO tab removed; monitoring stacks re-homed; orphan routes linked/deleted |
| 4 | `advisor/019-global-shell` | 021+018 | 019 | Always-mounted cluster switcher (pinned/recent, ⌘J); header kubeconfig + Import YAML; uncrowded cluster topbar; collapsed rail flyout; unique nav icons; **migration 063** (`pinned_clusters`), schema version 63 |
| 5 | `advisor/022-page-migration` | 021+018, contains 019 | 022 | Mastheads, tab bars, raw h1/button/card/switch/tile migrations; five raw tables → `DataTable`; god-files split (node 1370→229, apps 1239→431, target 1182→401, security 1160→245); `nodeActionPending` bug fixed; seven create modals → `*/new` routes; create-as-route rule documented |
| 6 | `advisor/024-parity-quick-wins` | 022 | 024 §3–8 | Clone / Download YAML (workloads table); inline labels/annotations editor; project members card (from project RBAC bindings); typed probes and typed secrets; Home provider column, "showing N of M", welcome banner; **migration 064** (`rows_per_page`, `date_format`), schema version 64 |
| 7 | `advisor/023-forms-and-url-state` | 022 | 023 §4–9 | Unsaved-changes guard; validators + `FormErrorSummary` ratchet; mutation-feedback ratchet; router-native Back; URL-synced list filters; extension `$name` route; `aria-expanded`; ten-page error-state harness (caught two more empty-on-error bugs) |

Every branch passed, re-run by the reviewer: type-check, lint (0 warnings),
vitest, `scripts/check-complexity-budget.mjs`, `scripts/code-health-inventory.mjs`,
and (where backend changed) `check-migrations.sh`, `make sqlc-check`, `go build`,
`go vet`, `go test`. Executors ran the serial route-smoke + axe crawl on a
dedicated port; final counts 292–294 routes green.

## 2. Integration and promotion

```bash
git checkout advisor/ui-overhaul-integration
# Plan 020 is already merged. Before opening/pushing the integration PR:
# Resolve the recorded Local CI Charlie-suite failures, then run both gates.
make local-ci-pr-representative
make local-ci-pr
```

The completed merges conflicted only on the generated code-health inventory,
which was regenerated. Migrations 063, 064, and 065 are additive preference
changes. Integration promotion to `main`, deployment, and protected/external
release qualifications remain separate; local static/browser checks do not
replace them.

## 3. What is still open

| Plan | Status | Needs |
|---|---|---|
| [020 — Explorer nav: discovery + CRD groups](./020-cluster-explorer-nav-discovery-and-crd-groups.md) | DONE — INTEGRATED | Discovery, Gateway regrouping, CRD subgroups, stars, counts, and schema 65; [validation passed](./ui-overhaul-validation-2026-09-22.md). |
| [025 — Guided form depth + table parity](./025-guided-form-depth-and-table-parity.md) | PARTIAL — VIA 026 | Indexed guided fields, namespace grouping, bounded bulk operations and common related-resource links are implemented. Advanced affinity/Ingress unions and the design-only `questions.yaml` spike remain; [current ledger](./026-platform-robustness-and-workflow-closure.md). |
| [026 — Platform robustness and workflow closure](./026-platform-robustness-and-workflow-closure.md) | IN PROGRESS | Extend negative browser workflows, audit other legacy selectors and complete advanced guided fields; resolve representative CI, then full matrix and authorized live qualification. |

Follow-ups discovered during execution (originally recorded in 017 §7):

- **Global delivery lists implemented in Plan 026.** Project-authorized Sources / Bundles / Targets / Rollouts / Deployments use canonical APIs, not first-cluster redirects.
- **Rollout URLs are not project-agnostic.** The rollout GET requires `project_id` and the payload has none; deep links without `?project=` hit a "select a project" gate. API decision needed.
- **Clone / Download YAML implemented in Plan 026** for shared built-in families and custom-resource instances, with explicit non-clonable infrastructure exceptions.
- **Native RBAC grant UI implemented in Plan 026** on the canonical API with scoped permissions and typed confirmation.
- **Project members card corrected:** subject names are permission-aware; denied binding reads do not become a false zero.
- Deliberately unconverted in 022: two collapsible `Section`s, four row-shaped tiles, six auth/markdown `<h1>` exemptions, the shared delivery button constants; button ratchet stopped at 196 (target was 154).
- Descoped from 023: the ESLint mutation rule became a counting ratchet (28 pre-existing sites); extension not-found renders inline rather than throwing; the registries list has no filter to persist.

## 4. Rules learned for anyone executing here

- `scripts/check-complexity-budget.mjs` requires an **exact** line match per baselined unit. Never let a unit grow (extract to a new file); when it shrinks, `--write-baseline`, confirm every number went down, commit separately. Never run blanket `prettier --write` on ceilinged files.
- `user_preferences` is an explicit-column table: a new preference needs a one-line `ADD COLUMN … NOT NULL DEFAULT … CHECK (…)` migration (down file carries the three contract headers), the query updated, `make sqlc-generate && make sqlc-check`, the schema-version bump, and handler mapping with a nil→default guard.
- `TabsList` is `role="tablist"`; navigation strips of links must be `<nav>` with `tabLinkClassName(active)`, never tabs.
- Importing a route's `Route.options.component` hangs under vitest; export testable page components from adjacent `-page.tsx` files. Do not export them from route entrypoints: this defeats automatic component splitting and breaks eager-bundle budgets.
- Adding a TanStack file route needs a vite build to regenerate `routeTree.gen.ts` and an `EXPECTED_ROUTE_COUNT` bump in `frontend/scripts/generate-route-manifest.mjs`.
- Playwright: use a dedicated `PLAYWRIGHT_PORT` per worktree and `--workers=1`; the default reuses another worktree's preview server and parallel runs are flaky on this host. Serve an immutable build snapshot if other verification jobs may rebuild `dist/` during the crawl.
- Do not overlap host-browser crawls with Docker-based Local CI creating/removing networks: Chromium can report `ERR_NETWORK_CHANGED`. Keep failure evidence and rerun in an isolated environment, without relaxing assertions or adding retries.
- `go build` in a worktree: `GOTMPDIR=<outside /tmp> go build -buildvcs=false ./...`.
- Fresh screenshots without a live server: `cd frontend && SMOKE_GALLERY=1 npx playwright test --project=route-smoke` → `frontend/gallery/*.png` (git-ignored; run the desktop project alone or mobile overwrites it).
