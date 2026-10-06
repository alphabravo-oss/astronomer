# Plan 031 — UI refinement and design-system depth

> Execute phase by phase. Complete each phase's verification before advancing. Preserve unrelated working-tree changes. Do not reimplement Plans 018–025 or 027; this plan builds on the merged shell, navigation and DataTable.

## Status and baseline

- Status: IMPLEMENTED on `feat/031-ui-refinement` (tip 1ac972c0) with the residuals listed in [031-evidence/after.md](./031-evidence/after.md): image search/presence blocked on a backend filter, one unfixable dev-tool dependency advisory failing `npm audit`, no hand QA. Measurements, verification and open items are in that file.
- Priority: P1 for visible defects (topbar wrap, table truncation, contradictory status, jargon leaks). P2 for primitives, table depth and density.
- Effort: L overall. Each phase is S–M and independently mergeable.
- Risk: MEDIUM. A new primitive dependency touches every overlay; CSP, bundle budget, focus handling and test selectors must stay green.
- Category: design system, UX polish, tables, consistency.
- Planned at: Astronomer `main` @ `f87b189f`, 2026-10-06.
- Source evidence: fresh desktop route crawl (`SMOKE_GALLERY=1 npx playwright test --project=route-smoke`, 155/155 passed, screenshots in `frontend/gallery/`) plus source measurements below.
- Dependencies: Plan 016 keeps release authority. Plan 026 residue (affinity/Ingress unions, `questions.yaml`) stays in 026.

## Why this matters

Page coverage is close to Rancher. What still reads as "less mature" is polish: a header that wraps at a common laptop width, tables that truncate while empty, status badges that disagree, native browser tooltips, spinners instead of skeletons, and pages that each invent their own header. These are cross-cutting, so fixing them in the shared kit fixes every page at once.

## Baseline measurements (2026-10-06)

Recompute these in Phase 0 and record them; Phase 9 must show the target column met.

| Signal (frontend `src/`, excluding tests and `components/ui/`) | Baseline | Target |
|---|---|---|
| Native `title=` tooltips | 917 | 0 outside `components/ui/` (aria-only exceptions allowlisted) |
| Raw `<button` elements | 361 | 0 outside `components/ui/` |
| Spinner / "Loading..." placeholders | 197 | List, card and detail loads use Skeleton |
| Skeleton component usages | 0 | ≥ 1 per DataTable, MetricCard, ResourceMasthead |
| Tailwind arbitrary values `x-[...]` | 173 | ≤ 60, remaining ones justified |
| Raw palette colors (`bg-red-500` style) | 15 | 0 |
| Hex colors in TSX | 9 | 0 outside chart/brand config |
| `useState` in `routes/` | 506 | No growth; down in every file touched |
| Route files > 750 lines | 10 | Every file touched in this plan shrinks |

Measurement script (run from `frontend/`, under bash):

```bash
g(){ grep -rE "$1" --include='*.tsx' src | grep -vE 'test|components/ui/'; }
g 'title=(\{|")' | wc -l
g '<button' | wc -l
g 'Loading\.\.\.|Loader2' | wc -l
grep -rl Skeleton --include='*.tsx' src | grep -v test | wc -l
grep -roE '[a-z]+-\[[^]]+\]' --include='*.tsx' src | grep -v test | wc -l
g '(bg|text|border|ring)-(red|green|blue|yellow|amber|emerald|orange|slate|gray|zinc|sky|indigo|purple|rose|violet)-[0-9]' | wc -l
grep -roE '#[0-9a-fA-F]{6}' --include='*.tsx' src | grep -v test | wc -l
grep -ro useState --include='*.tsx' src/routes | wc -l
```

## Product and implementation constraints

- Astronomer adopts existing clusters and uses Flux. No provisioning, no Fleet.
- React 19, TanStack Router/Query/Table/Form, Tailwind v4 CSS-first tokens in `src/styles/globals.css`. All new colors are tokens; no palette or hex literals.
- `TabsList` is `role="tablist"`; route-layout strips stay `<nav>` of links.
- `user_preferences` is explicit columns. New persisted preferences need a migration, sqlc regen and handler mapping (see Plan 020 starred types for the pattern).
- Complexity budget is exact-match: shrink a baselined unit, then `node scripts/check-complexity-budget.mjs --write-baseline`. Never grow.
- `Route.options.component` hangs under vitest; export page components by name.
- Adding a TanStack file route needs a vite build to regenerate `routeTree.gen.ts`.
- Playwright: set `PLAYWRIGHT_PORT` per worktree; use `--workers=1` if smoke flakes. If browsers are missing, `npx playwright install chromium`.

## Commands and conventions

```bash
cd frontend
npm run type-check && npm run lint && npm test
SMOKE_GALLERY=1 PLAYWRIGHT_PORT=3177 npx playwright test --project=route-smoke --workers=2
npx playwright test --project=chromium          # tier-1 e2e incl. axe
node scripts/check-bundle-budget.mjs && node scripts/check-csp-build.mjs
cd .. && node scripts/check-complexity-budget.mjs
make verify-enterprise VERIFY_SCOPE=frontend
```

Branch per phase: `feat/031-<phase-slug>`, stacked in phase order. Run Prettier on touched files.

---

## Phase 0 — Baseline and decision record

Tasks:

1. Run the measurement script; paste results into `advisor-plans/031-evidence/baseline.md`.
2. Regenerate the desktop gallery and keep these as "before" references: `dashboard`, `dashboard_clusters`, `dashboard_clusters_c-smoke-1`, `dashboard_clusters_c-smoke-1_workloads`, `dashboard_clusters_c-smoke-1_deployments_default_smoke-app`, `dashboard_clusters_c-smoke-1_apps`, `dashboard_delivery`, `dashboard_settings_webhooks_new`.
3. Record the primitive-library decision (Phase 2) in the evidence folder: **Radix UI via the single `radix-ui` package**, wrapped inside `components/ui/`. Rationale: mature collision-aware positioning, focus management, React 19 support, per-primitive tree shaking, no styling opinions. Base UI is the fallback if Radix fails the CSP check.
4. Spike: add `radix-ui`, render one Tooltip and one Popover, run `check-csp-build.mjs` and `check-bundle-budget.mjs`. Record the bundle delta.

Acceptance: baseline file committed; spike passes CSP; bundle delta recorded and within budget (or budget change justified in the evidence file).

STOP if Radix requires inline styles the CSP rejects and Base UI does too. Report back instead of loosening CSP.

## Phase 1 — Visible defect fixes (P1, no new dependency)

### 1.1 Topbar must not wrap at 1280px

Files: `src/components/layout/topbar.tsx`, `src/components/layout/header-cluster-actions.tsx`, `src/components/layout/command-palette.tsx`.

- Remove the "Go to page" button; Cmd+K and the search field already open the palette. Make the search field's click open the palette with page results visible (keep `/` and `Cmd+K` hints).
- Collapse Shell, Kubeconfig and Import into icon-only `ActionButton`s with accessible labels, or into one "Cluster actions" menu below 1440px.
- Header becomes a single row: scope selectors (left), search (flex, min 240px), icon actions, theme, notifications, account.
- Acceptance: at 1280×720 and 1366×768, cluster-scope pages render the header on one row with search ≥ 240px wide. Add a Playwright assertion that the header height equals the global-scope header height.

### 1.2 Clusters table truncates with empty space

Files: `src/routes/dashboard/clusters/-columns.tsx`, `src/components/ui/data-table.tsx`.

- Give columns content-based `size`/`minSize`; make Name the flexible column (`grow`). Use the short header labels "K8s", "Heartbeat" and show full names in the header tooltip.
- Acceptance: at 1280px the single-row smoke cluster shows the full name "Smoke East", the distribution and the heartbeat without ellipsis.

### 1.3 One status on the cluster overview

File: `src/routes/dashboard/clusters/$id/-page.tsx`.

- The header shows "Unknown", "Active" and "created" at once. Derive one effective status (connection + lifecycle), show it as one `StatusBadge`, and move lifecycle detail into the metadata line.
- CPU/Memory cards show a percentage with "No data" underneath. Show the percentage with its sample time, or show "No data" with no number, never both.
- Acceptance: unit test for the status derivation covering connected/active, disconnected, unknown agent, decommissioning.

### 1.4 Copy pass for internal jargon

- Replace `anomaly_baseline_recompute task` text (`clusters/$id/-page.tsx`) with user language, e.g. "Baselines appear after 24 hours of metrics."
- Replace "Managed by Tools pivot" (`clusters/$id/apps/-page.tsx`) with "Releases installed by Cluster Tools are marked Managed by Tools."
- Grep for snake_case identifiers and task names inside JSX text and fix every hit:

```bash
grep -rnE '>[^<{]*\b[a-z]+_[a-z_]+\b[^<]*<' --include='*.tsx' src | grep -v test
```

### 1.5 Empty and zero states

- Estate (`routes/dashboard/delivery/-page.tsx`): when every KPI is 0 and there are no adopted clusters, replace the 8-card grid with one empty state that has a primary action ("Adopt a cluster").
- Shorten empty-state copy to one sentence plus actions everywhere you touch.

Phase 1 acceptance: gallery "after" shots for the 8 reference pages; route-smoke green; complexity budget not grown.

## Phase 2 — Interactive primitives in `components/ui/`

Wrap Radix in house components so pages never import `radix-ui` directly. Add an ESLint `no-restricted-imports` rule allowing `radix-ui` only under `src/components/ui/`.

| New / replaced primitive | Replaces | Notes |
|---|---|---|
| `tooltip.tsx` (`Tooltip`, `TooltipProvider`) | native `title=` | 300ms delay, keyboard focus shows it, works on touch via long-press fallback to aria-label |
| `popover.tsx` | `layout/use-header-popover.ts` hand positioning | Collision-aware, portal, Escape closes |
| `dropdown-menu.tsx` | `action-menu.tsx` internals | Keep the `ActionMenu` public API (`items`, `disabledReason`, `separator`) so call sites don't change; add keyboard type-ahead |
| `combobox.tsx` | native `Select` for long lists | Searchable, async options, virtualized past 100 items via `@tanstack/react-virtual`; use for namespace, cluster, chart, storage class, service account pickers |
| `dialog.tsx` / `sheet.tsx` | `overlay-shell.tsx` internals | Keep `ModalShell`/`DrawerShell` APIs; swap internals for Radix Dialog focus trap and scroll lock |
| `skeleton.tsx` | spinners | `Skeleton`, `SkeletonText`, `SkeletonTableRows`, `SkeletonCard` |
| `kbd.tsx`, `separator.tsx` | ad-hoc spans/hr | Small, used by palette and menus |

Tasks:

1. Implement each primitive with tests (keyboard, focus return, Escape, portal under `overflow-hidden`).
2. Swap internals of `ActionMenu`, `ModalShell`, `DrawerShell`, header popovers without changing their props. Existing tests must pass unchanged; update selectors only where role names legitimately change.
3. Add `Tooltip` support to `ActionButton` and icon buttons via a `tooltip` prop.
4. Wire `Skeleton` into `DataTable` loading, `MetricCard` loading and `ResourceMasthead` loading.

Acceptance: axe passes on tier-1 e2e; no visual change other than tooltips/skeletons on reference pages; bundle and CSP checks green.

## Phase 3 — Migration sweep and lint ratchets

Tasks:

1. Codemod or hand-migrate:
   - `title=` on interactive elements → `<Tooltip>` or `tooltip` prop. `title=` on truncated text → `TableText` truncation with tooltip.
   - Raw `<button>` → `Button`/`ActionButton`.
   - Spinner/"Loading..." in list, card and detail loads → Skeleton. Keep spinners only inside buttons during mutations.
   - Palette colors and hex → status/brand tokens; add tokens to `globals.css` if a meaning is missing.
2. Add ESLint `no-restricted-syntax` rules (scoped to `src/**` excluding `src/components/ui/**`):
   - JSX `<button>` element.
   - JSX attribute `title` on intrinsic elements (allowlist `svg > title`, `iframe`).
   - Class strings matching raw palette colors or `#hex`.
3. Arbitrary values: replace repeated ones with tokens (spacing, widths, z-index already tokenized). Leave one-offs with a `// ui: <reason>` comment.

Migrate in batches by folder (settings, clusters, delivery, resources, security, monitoring, rest) so each PR stays reviewable. Each batch reruns route-smoke.

Acceptance: measurement script hits the Phase 3 targets in the baseline table; lint rules on and passing; no `eslint-disable` added for these rules without a reason comment.

## Phase 4 — Page anatomy consistency

Files: `src/components/ui/page.tsx` (`PageShell`, `PageHeader`, `PageSection`, `ResourceMasthead`).

1. `PageHeader` gets fixed slots: `eyebrow` (scope line, optional), `title`, `description` (max one sentence), `status`, `actions`, `tabs` (route nav). Remove page-level icons next to titles.
2. One content width and padding from `PageShell`. Remove per-page padding overrides; the Apps page currently uses different padding.
3. Move scope notices (e.g. the Apps "Installed releases are cluster-wide..." line above the title) into an `InfoCallout` under the header, or into the description.
4. Audit every route using `PageHeader`/`PageShell` (≈112 files) and every route not using them. Every top-level route must use `PageShell` + `PageHeader`; detail routes use `ResourceMasthead`.
5. Add a vitest that renders the route manifest pages under stubs and fails if a page lacks a `PageHeader` or `ResourceMasthead` heading (use the existing route manifest generator output).

Acceptance: reference screenshots show identical header geometry across list, settings and delivery pages; the new test passes.

## Phase 5 — Resource detail masthead depth

Files: `src/components/ui/page.tsx` (`ResourceMasthead`), `src/components/resources/resource-detail.tsx`, `resource-detail-tabs.tsx`.

1. Masthead shows: kind badge, name, namespace link, effective status, age (relative, exact on tooltip), owner reference link, created-by (Helm release / Flux Kustomization / Tool owner if known).
2. Labels and annotations as chips, collapsed past 6 with "+N more"; click copies `key=value`.
3. Conditions strip: one chip per condition, colored by status, reason/message in tooltip.
4. Action bar: Edit as form (where a guided form exists), Edit YAML, Clone, Download YAML, Delete. Respect RBAC and owner rules (Flux-managed resources show "Managed by Flux" and disable direct edit as today).
5. Overview tab must never render bare "No data." Show the masthead plus a typed empty state.
6. Pods masthead adds container summary: ready/total, restarts, last termination reason.

Acceptance: unit tests for owner-link resolution and condition rendering; deployment, pod, node, CRD detail screenshots updated.

## Phase 6 — DataTable depth (TanStack Table)

Files: `src/components/ui/data-table-features.ts`, `data-table.tsx`, `data-table-toolbar.tsx`, `use-data-table-state.ts`, `use-data-table-controller.ts`.

1. **Column pinning**: add `columnPinningFeature`. Default-pin the selection column and the name column left, the actions column right. Sticky cells use `--z-sticky` and a token background.
2. **Content sizing**: columns declare `size`/`minSize`/`maxSize`; one column per table may declare `grow: true` and absorbs remaining width. Remove truncation on tables that fit.
3. **Column ordering**: add `columnOrderingFeature`; drag in the Columns menu (keyboard reorder with up/down buttons for accessibility).
4. **Filter chips**: render active faceted/column filters as removable chips below the toolbar with "Clear all".
5. **Expandable rows**: add `rowExpandingFeature` with a `renderSubRow` prop. First users: pods → containers (state, restarts, last termination), deployments → ReplicaSets.
6. **Density**: `compact | comfortable` from a CSS variable (Phase 7) applied to row padding.
7. **Keyboard navigation**: `j/k` or arrow keys move row focus, `Enter` opens, `x` toggles selection, `/` focuses table search. Disabled while a text input has focus.
8. **CSV export**: toolbar action exporting the filtered, visible columns of loaded rows (client-side). Server-paged tables export the current page and say so.
9. **Saved views**: named bundles of filters, sort, visible columns, order and pinning.
   - URL carries the active state (existing `validateSearch` pattern) so views are shareable.
   - Persist named views per `persistKey` server-side: new `user_table_views` table (`user_id`, `table_key`, `name`, `state jsonb`, timestamps), sqlc queries, CRUD handlers, OpenAPI entries, then `make openapi-generate`. Cap at 20 views per table per user.
   - Toolbar "Views" menu: apply, save current, rename, delete, set default.
10. **Server-side mode**: `manualPagination/manualSorting/manualFiltering` path in `useDataTableController` for audit log, events and pods; wire to existing paged endpoints only (do not invent query params the API lacks; STOP and list gaps instead).

Acceptance: unit tests per feature (pinning order, grow sizing math, chip removal, view round-trip through URL, CSV output escaping); e2e: save a view on Pods, reload, it applies; keyboard row navigation e2e with axe clean.

## Phase 6b — Per-table column tuning (generic tables)

Finding: `Column<T>` only offers `width?: string`; 76 of ~91 DataTable users declare no width, so every column gets an equal share. Long names and image refs are cut off while counts, ages and badges get far more space than they need.

Tasks:

1. **Column kinds (in P6.2).** Add `kind` to `Column<T>`: `name | text | status | badge | count | percent | age | date | version | id | bytes | actions | select`. Each kind sets default `size/minSize/maxSize`, alignment (numeric kinds right-aligned, tabular figures), and overflow policy (name grows and truncates with a Tooltip; text wraps to 2 lines then truncates; status/badge/count/age/version never truncate; id is mono with middle-ellipsis and copy; actions is fixed and pinned right). Explicit width/size/align/grow overrides the kind. No kind and no width keeps today's behavior.
2. **Headers never clip.** Header min width accounts for label plus sort icon; short header labels with the full name in a tooltip.
3. **Inventory.** Produce `031-evidence/table-inventory.md`: every DataTable user (≈94 files), its columns, the proposed `kind` per column, and the worst current symptom (clipped, oversized, wrapped).
4. **Sweep by domain**, one PR per batch: clusters/nodes, workloads/pods/events, networking/storage/policy/RBAC resources, delivery/Flux, apps/catalog/tools, monitoring/alerting/logging, security/scans/registries, settings/admin/audit/backups/projects. Assign a `kind` to every column; set `grow` on exactly one text-like column per table; add `minSize` for columns holding long identifiers (image refs, namespaced names, URLs).
5. **Per-table special cases** the generic kinds cannot express: multi-line cells (name + subtitle), composite cells (status + reason), image references (registry/repo:tag with tag always visible), label/selector chips (cap to 2 plus "+N"), resource quantities (CPU/memory with unit, right-aligned), timestamps (relative with exact tooltip).
6. **Guard.** Vitest that renders each table's column list through the kind resolver and fails when a column has neither `kind` nor explicit width (allowlist shrinks to zero by the end of the sweep).
7. **Visual check.** Add the 25 densest tables to the visual-regression set at 1280px.

Acceptance: no clipped values and no column wider than 3x its content in the inventory's 25 densest tables at 1280px; guard test passes with an empty allowlist; screenshots before and after in `031-evidence/tables/`.

## Phase 7 — Tokens: density and type scale

File: `src/styles/globals.css`, `src/lib/user-preferences`.

1. Add semantic type tokens: `--text-body` (default `text-sm`), `--text-meta` (`text-xs`), `--text-micro` (`text-2xs`), headings `--text-page-title`, `--text-section-title`. Map them in `@theme`.
2. Add density tokens: `--row-py`, `--control-h`, `--card-p`, `--gap-section`, with `[data-density="compact"]` overrides on `<html>`.
3. Migrate `components/ui/` to the tokens first, then pages touched in earlier phases. Body copy defaults to `text-sm`; `text-xs` is metadata only.
4. Preference: `density` column on `user_preferences` (migration + sqlc + handler + OpenAPI), selector in Account → Preferences, applied as `data-density` before first paint (read from the cached preferences query).

Acceptance: toggling density changes row height in DataTable and card padding without reload; both densities pass axe contrast; light and dark reference shots for both densities.

## Phase 8 — Component gallery and visual regression

1. Add a dev-only route `/dashboard/dev/ui` (excluded from production nav, gated by build flag) that renders every `components/ui/` primitive in all variants and states, in light and dark side by side.
2. Add it to the route-smoke crawl so `SMOKE_GALLERY=1` captures it.
3. Add Playwright `toHaveScreenshot` comparisons for the gallery route and the 8 reference pages, desktop only, with a small `maxDiffPixelRatio`. Commit baselines under `frontend/tests/__screenshots__/`.

Acceptance: gallery route renders with no console errors; visual tests green; documented update command in `frontend/docs/`.

## Phase 9 — Large-file reduction (opportunistic, budget-driven)

Targets (current lines): `routes/dashboard/charlie/index.tsx` (1048), `components/resources/resource-gateway-tables.tsx` (961), `routes/dashboard/delivery/-page.tsx` (958), `routes/dashboard/clusters/$id/image-scans/index.tsx` (920), `components/resources/resource-core-tables.tsx` (826), `routes/dashboard/clusters/$id/-page.tsx` (825), `components/resources/resource-list-columns.tsx` (804), `routes/dashboard/settings/backup/-page.tsx` (803), `components/resources/pod-resource-overview.tsx` (801), `routes/dashboard/clusters/$id/registries/index.tsx` (775).

1. Split each into a route shell plus section components colocated with `-` prefixed files.
2. Move form state from chains of `useState` to TanStack Form (`useAppForm` pattern already used in 45 files).
3. Column definitions move to their own `-columns.tsx` file per table.
4. Refresh the complexity baseline after each shrink.

Acceptance: every file above is under 600 lines; `useState` count in `routes/` lower than baseline; no behavior change (existing tests pass untouched).

## Phase 10 — Multi-cluster and live polish (differentiators)

1. **Live updates**: inventory pages still polling via `refetchInterval` (52 files) move to `lib/live` hooks where the backend already streams that resource. Keep polling only where no stream exists; list those in the evidence file.
2. **Cross-cluster workload search**: from global Search, query workloads/pods/images across authorized clusters using existing search endpoints; results grouped by cluster with status. STOP and document if the API cannot fan out.
3. **Image presence view**: on an image-scan finding, "Where is this image running?" across clusters (reuse search).

Acceptance: e2e for one live-updating list (stubbed stream event changes a row without refetch); search shows grouped multi-cluster results under stubs.

## Phase 11 — Integration and acceptance

1. Rerun the measurement script; record "after" next to baseline in `031-evidence/after.md`. Every target met or explicitly waived with reason.
2. Full gate: type-check, lint, vitest, tier-1 e2e, route-smoke desktop and mobile (run separately), visual tests, bundle, CSP, complexity budget, `make verify-enterprise VERIFY_SCOPE=frontend` (plus `backend` for Phases 6 and 7 migrations).
3. Manual QA at 1280, 1440, 1920 and mobile 390px in light and dark, both densities, keyboard-only pass over clusters list, pod detail, apps install, settings form.
4. Update `advisor-plans/README.md` status and the frontend code-health inventory.

## Done criteria

- Header single-row at 1280px in every scope.
- Zero native tooltips, raw buttons, palette or hex colors outside `components/ui/`, enforced by lint.
- Every list, card and detail load uses skeletons.
- Every page uses `PageShell` + `PageHeader` or `ResourceMasthead`, enforced by test.
- Every DataTable column has a semantic `kind` or explicit size; nothing clipped and nothing oversized in the 25 densest tables at 1280px.
- DataTable supports pinning, grow sizing, ordering, chips, expansion, density, keyboard nav, CSV export and saved views; at least Pods, Deployments, Nodes, Clusters, Events and Audit use them.
- Density preference persisted and applied before first paint.
- Component gallery route and visual regression in CI.
- Ten largest UI files under 600 lines.

## STOP conditions

- CSP must be loosened for any primitive library.
- Bundle budget would grow more than 10% without a matching removal.
- A phase needs a backend endpoint or query parameter that does not exist (Phases 6.10, 10.2, 10.3). List the gap; do not invent the API.
- A primitive swap changes the public props of `ActionMenu`, `ModalShell`, `DrawerShell` or `DataTable` beyond additive options.
- Permission or owner rules (Flux-managed, Tools-managed) would be bypassed by a new action.

## Task checklist

- [x] P0.1 Baseline measurements recorded
- [x] P0.2 Before screenshots captured
- [x] P0.3 Primitive library decision recorded
- [x] P0.4 Radix spike passes CSP and bundle budget
- [x] P1.1 Topbar single row at 1280px
- [x] P1.2 Clusters table sizing fixed
- [x] P1.3 Single effective cluster status; metric cards consistent
- [x] P1.4 Jargon copy pass
- [x] P1.5 Zero-state collapse on Estate; empty-state copy trimmed
- [x] P2.1 Tooltip
- [x] P2.2 Popover replaces header popover positioning
- [x] P2.3 DropdownMenu behind ActionMenu
- [x] P2.4 Combobox (namespace, cluster, chart, storage class, service account)
- [x] P2.5 Dialog/Sheet behind ModalShell/DrawerShell
- [x] P2.6 Skeleton set wired into DataTable, MetricCard, ResourceMasthead
- [x] P2.7 Kbd, Separator
- [x] P2.8 `radix-ui` import restricted to `components/ui/`
- [x] P3.1 Tooltip migration (917 → 0)
- [x] P3.2 Button migration (361 → 0)
- [x] P3.3 Spinner → Skeleton migration
- [x] P3.4 Palette/hex → tokens
- [x] P3.5 Arbitrary values ≤ 60
- [x] P3.6 Lint ratchets enabled
- [x] P4.1 PageHeader slots
- [x] P4.2 Single content width/padding
- [x] P4.3 Scope notices moved to callouts
- [x] P4.4 All routes on PageShell/PageHeader/ResourceMasthead
- [x] P4.5 Page-anatomy test
- [x] P5.1 Masthead metadata (status, age, owner, managed-by)
- [x] P5.2 Labels/annotations chips
- [x] P5.3 Conditions strip
- [x] P5.4 Detail action bar with RBAC/owner rules
- [x] P5.5 No bare "No data." on Overview
- [x] P5.6 Pod container summary
- [x] P6.1 Column pinning
- [x] P6.2 Content sizing with grow column
- [x] P6.3 Column ordering
- [x] P6.4 Filter chips
- [~] P6.5 Expandable rows (pods done; deployments need ReplicaSets in the list payload)
- [x] P6.6 Row density
- [x] P6.7 Keyboard row navigation
- [x] P6.8 CSV export
- [x] P6.9 Saved views (URL + `user_table_views` API)
- [~] P6.10 Server-side mode for audit, events, pods (DataTable support done; audit/alerting endpoints lack sort/filter params)
- [x] P6b.1 Column kinds and header sizing in DataTable
- [x] P6b.2 Table inventory document
- [x] P6b.3 Domain sweeps (8 batches) assign a kind to every column
- [x] P6b.4 Special-case cells (composite, image refs, chips, quantities, timestamps)
- [x] P6b.5 Guard test with empty allowlist
- [x] P6b.6 Densest 25 tables in visual regression
- [x] P7.1 Type tokens
- [x] P7.2 Density tokens
- [x] P7.3 Kit and touched pages migrated to tokens
- [x] P7.4 Density preference persisted, pre-paint
- [x] P8.1 Dev component gallery route
- [x] P8.2 Gallery in route-smoke crawl
- [x] P8.3 Visual regression baselines
- [x] P9 Ten largest files under 600 lines
- [x] P10.1 Polling → live streams where available
- [~] P10.2 Cross-cluster workload search (workloads and pods grouped by cluster; image search blocked on backend filter)
- [ ] P10.3 Image presence across clusters (blocked: backend has no image filter; see after.md)
- [~] P11 Integration gate, after-measurements, manual QA, README update (automated gate done; manual QA not performed; npm audit blocked by braces advisory)
