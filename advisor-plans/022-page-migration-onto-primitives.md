# Plan 022: Migrate the worst-offending pages onto the primitives, split the god-files, and settle the modal-vs-route rule

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/routes frontend/src/components/resources frontend/src/components/clusters frontend/src/components/ui/__tests__`
> Plan 021 is a prerequisite and *will* have changed `components/ui`; that is
> expected. For the route files listed under "Scope", compare the "Current
> state" excerpts against the live code; on a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: L (4–5 days; split across PRs per route family)
- **Risk**: MED (high-traffic pages; purely presentational but wide)
- **Depends on**: 021 (primitives and ratchets), 018 (delivery layout removes one hand-rolled header)
- **Category**: tech-debt (design system adoption)
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

Plan 021 makes the primitives complete and adds ratchets that stop new drift. The existing drift still has to be paid down where users see it most: the five hand-rolled detail mastheads (the node page has no visible H1), 10 hand-rolled tab bars, 28 raw `<h1>` at four sizes, the top raw-`<button>` files (account security 17, delivery target 12, topbar 12, settings widgets 10), 9 local `Field` copies, 12 local stat-tile components, 7 hand-rolled switch thumbs, and the 1,000+ line route files where all of these accumulate because they are too big to review. Rancher never has to decide per family whether "New X" is a modal or a page because `CruResource` is the only shell; Astronomer's backups and settings families are split internally, which is the most confusing case.

## Current state

Detail mastheads to replace with `ResourceMasthead` (added by 021 to `components/ui/page.tsx`):

1. `components/resources/resource-detail.tsx:186-205` — back button (`window.history.back()` at 189; Plan 023 replaces it with a router navigate — coordinate: do the masthead here, keep whatever `backTo` 023 computes), `font-mono text-xl` h1, "Kind: / Namespace: / Age:" strip.
2. `routes/dashboard/clusters/$id/nodes/$nodeName/index.tsx:705-725` — back arrow, `text-xl font-semibold font-mono` h1, `StatusBadge`, "Unschedulable" pill, `Roles: … Age: …` strip. Two YAML buttons exist in the action row (a `<> YAML` view and a download `YAML`) — label them "View YAML" and "Download YAML".
3. `routes/dashboard/clusters/$id/delivery/route.tsx:87-99` — eyebrow + h1 + description (use `PageHeader`, not masthead).
4. `routes/dashboard/projects/$id/route.tsx:80-95` — eyebrow + icon + h1 (use `PageHeader` with `eyebrow="Project"`, `title={project.name}`; keep the `Loader2` while loading via `title={project ? name : <Skeleton/>}`).
5. `routes/dashboard/clusters/$id/index.tsx:235-270` — `PageHeader` whose `title` embeds badges; convert to `ResourceMasthead` with `status={<StatusBadge …/>}`, `meta=[{Distribution},{Version},{Environment}]`, and the "Access: Admin" pill in `status`.

Hand-rolled tab bars (replace with `TabStrip` from `components/ui/tabs.tsx`, wired to `useTabParam` from `lib/use-tab-param.ts` where the page already keeps the tab in `?tab=`):
`routes/dashboard/clusters/$id/nodes/$nodeName/index.tsx:784`, `routes/dashboard/clusters/$id/apps/index.tsx` (section switcher; 023 also fixes its URL sync — do them together), `routes/dashboard/projects/$id/route.tsx`, `routes/dashboard/clusters/$id/delivery/route.tsx` (route-layout strip: render `TabsList`/`TabsTrigger` inside `RouterLink`s rather than `TabStrip`), `routes/dashboard/security/index.tsx`, `routes/dashboard/settings/cluster-groups/index.tsx`, `routes/dashboard/charlie/index.tsx:119-147`, `routes/dashboard/settings/charlie/index.tsx`, `components/resources/resource-detail.tsx:233-258`, `components/resources/namespace-detail-page.tsx`. Also collapse the parallel tab chrome in `components/resources/resource-detail-tabs.tsx` (`ResourceDetailTabPanel`) onto `TabsContent`, keeping its panel-content logic.

Raw `<h1>` outside `components/ui` (28): including `routes/dashboard/charlie/index.tsx:99`, `clusters/$id/workloads/index.tsx:80`, `settings/vault/index.tsx:149`, `settings/operations/index.tsx:159`, `settings/widgets/index.tsx:314` (`text-2xl`); `clusters/$id/resources/index.tsx:507`, `clusters/$id/network-access/index.tsx:262`, `components/clusters/custom-resources-page.tsx:232`, `components/resources/resource-list-page.tsx:630` (`text-xl`); `routes/auth/login/index.tsx:173` (`text-4xl`, keep with a lint disable and reason), `routes/auth/login/reset-password/index.tsx:95`. Run `grep -rn "<h1" frontend/src/routes frontend/src/components | grep -v components/ui/` for the full list.

Raw `<button>` top files: `routes/dashboard/account/security/index.tsx` (17), `routes/dashboard/delivery/targets/$targetId/index.tsx` (12; plus local `primaryButton`/`secondaryButton` class constants at ~820–829), `routes/dashboard/delivery/targets/index.tsx` (`:442-451` same constants), `components/layout/topbar.tsx` (12), `routes/dashboard/settings/widgets/index.tsx` (10), `routes/dashboard/delivery/sources/index.tsx` (10; `secondaryButton` at ~490). `ActionButton` (`components/ui/action-button.tsx:18-29`) has intents, sizes, `loading`, `disabledReason`; it forces `type="button"` — submit buttons must pass `type="submit"` explicitly.

Local duplicates:
- `Field`: `routes/dashboard/delivery/targets/index.tsx:458`, `delivery/sources/index.tsx:758`, `delivery/bundles/$bundleId/index.tsx:579`, `delivery/targets/$targetId/index.tsx:1157`, `clusters/register/index.tsx:528`, `settings/read-audit/index.tsx:350` (divergent uppercase labels), plus two already-shared ones (`components/charlie/settings/shared.tsx:71`, `components/resources/guided-resource-fields.tsx:25`) — consolidate on the `Field` exported by 021 from `components/form/fields.tsx`.
- Stat tiles: `MetricTile` (`routes/dashboard/index.tsx:414` and `routes/dashboard/rbac/-effective-tab.tsx:283`), `SummaryTile` (`agents/index.tsx:399`), `SummaryCard` (`projects/$id/index.tsx:118`, `components/resources/pod-resource-overview.tsx:776`), `FactCard` (`admin/users/$id/index.tsx:310`), `Stat` (`settings/backup/index.tsx:81`, `components/resources/namespace-detail-page.tsx:528`), `Metric` (`delivery/targets/$targetId/index.tsx:1147`), `SummaryRow` (`settings/smtp/index.tsx:337`), `DetailRow` (`cluster-templates/$id/index.tsx:299`), `SummaryStrip` (`security/scans/$scanId/index.tsx:212`), `MetricLink` (`delivery/index.tsx:896`) → `MetricCard`.
- Local `Section` wrappers: `settings/platform/index.tsx:38`, `clusters/$id/resources/index.tsx:105`, `settings/auth/settings/index.tsx:417`, `components/projects/cluster-templates/template-form.tsx:357` → `PageSection`.
- Switch thumbs: `settings/webhooks/$id/index.tsx:234`, `settings/auth/settings/index.tsx:555`, `components/clusters/snapshot-tables.tsx:148`, `components/auth/connector-form.tsx:451` (`h-4`); `logging/-pipelines-tab.tsx:140`, `settings/webhooks/index.tsx:44`, `logging/-outputs-tab.tsx:187` (`h-3.5`) → `<Switch size=…/>`.

Card literals: 120 `rounded-* border border-border bg-card p-N` sites; migrate to `<Card padding=… radius=…>` in the files touched by this plan and lower the 021 baseline accordingly.

Raw tables on daily lists (migrate to `DataTable` and append to `operationalGrids` in `components/ui/__tests__/table-adoption.test.ts:6-9`): `routes/dashboard/settings/operations/index.tsx` (DLQ/outbox; no sort/search), `routes/dashboard/clusters/$id/resources/index.tsx`, `routes/dashboard/settings/read-audit/index.tsx`, `routes/dashboard/clusters/$id/network-access/index.tsx`, `routes/dashboard/settings/compliance/baselines/index.tsx`. Global Workloads (`routes/dashboard/search/-page.tsx`) uses a bespoke toolbar (monospace placeholders, "0 results from 0 clusters") — restyle its inputs with `Input`/`Select` and `DataTableToolbar` conventions.

God-files: `nodes/$nodeName/index.tsx` (1370; 10 `useState` at ~400–412; one shared `nodeActionPending` flag at 412 disables all eight actions together — a live UX bug), `clusters/$id/apps/index.tsx` (1242; three sections + 4 inline views), `delivery/targets/$targetId/index.tsx` (1182; `TargetEditDialog` at 571, `LaunchDialog` at 836), `security/index.tsx` (1160; `AssignTemplateModal` 647–784, `PSATemplateModal` 792–1151), `clusters/$id/index.tsx` (1011). The co-located `-modal.tsx` / `-tab.tsx` convention already exists (`routes/dashboard/alerting/-rule-modal.tsx`, `routes/dashboard/catalog/-installed-tab.tsx`).

Modal-vs-route inconsistency: backups uses full routes for schedule/storage create (`backups/schedules/new`, `backups/storage/new`) but a modal for restore (`components/backups/restore-modal.tsx`); settings uses routes for `webhooks/new`, `quotas/new`, `gitops/new`, `auth/connectors/new` but modals elsewhere in the same section. Plan 018 step 8 may have converted the two backups routes to redirects — check before acting.

Rancher reference: one detail shell `shell/components/ResourceDetail/index.vue` + `Masthead/` (31 adopters); one create/edit shell `shell/components/CruResource.vue` (60 adopters); one list `ResourceTable.vue` (68 adopters).

## Commands you will need

| Purpose | Command (in `frontend/`) | Expected |
|---|---|---|
| Gate | `npm run type-check && npm run lint && npm test` | exit 0 |
| Smoke + axe | `npm run test:e2e:smoke` | pass |
| Gallery | `SMOKE_GALLERY=1 npx playwright test --project=route-smoke` | PNGs |
| Complexity budget (repo root) | `node scripts/check-complexity-budget.mjs` | exit 0; after splitting a file, `node scripts/check-complexity-budget.mjs --write-baseline` and review the diff |
| Code-health inventory (repo root) | `node scripts/code-health-inventory.mjs --check --verify-doc` | exit 0 (refresh with its `--write` flag if line counts moved; commit as `chore(frontend): refresh code health inventory`) |

## Scope

**In scope**: every file named in "Current state", the ratchet allowlists/baselines in `components/ui/__tests__/`, new co-located `-tab.tsx`/`-modal.tsx` files next to the god-files, `docs/architecture/complexity-baseline.json` (only via the generator).

**Out of scope**: `components/ui/*` (021 owns it; if a variant is missing, STOP and report rather than extending here), nav/layout (018–020), behaviour changes (023 owns error states, URL state, guards — coordinate on the two shared files: `apps/index.tsx` and `resource-detail.tsx`), backend.

## Git workflow

- Branch per family: `advisor/022-migrate-<family>` (mastheads, tabs, buttons, tiles, tables, god-files). Each PR lowers a ratchet baseline and must not raise any.
- Conventional commits, e.g. `refactor(ui): adopt ResourceMasthead on node detail`.

## Steps

### Step 1: Mastheads (5 files)

Replace the five hand-rolled headers with `ResourceMasthead` / `PageHeader` as specified above. Node page: `title={nodeName}` (mono), `status={<StatusBadge status=…/>}` plus the Unschedulable pill, `meta=[{label:"Roles"},{label:"Age"}]`, `backTo` = the nodes list. Rename the two YAML buttons.

**Verify**: gallery `dashboard_clusters_c-smoke-1_nodes_node-smoke-1.png` shows an H1 with the node name; `grep -c "<h1" <each file>` → 0.

### Step 2: Tabs (10 bars + `resource-detail-tabs`)

Migrate each hand-rolled bar to `TabStrip` (with `useTabParam` when the page already URL-syncs). For `apps/index.tsx`, land this with 023's URL fix in the same PR. Delete the tab-chrome half of `resource-detail-tabs.tsx`.

**Verify**: `grep -rn 'border-b-2' frontend/src/routes frontend/src/components | grep -v components/ui/` → 0; smoke axe passes; each migrated page's tabs are keyboard-arrowable (spot-check via `tests/e2e/critical-workflows-keyboard.spec.ts` — add one assertion for the node page).

### Step 3: Page headers (28 raw h1)

Convert to `PageHeader` (or `ResourceMasthead` for detail pages). Keep the two auth pages with `// eslint-disable-next-line no-restricted-syntax -- marketing hero, not a page header`. Then remove the h1 lint-rule exemption if 021 had to add any beyond those two.

**Verify**: `npm run lint` → 0 with the h1 rule enabled repo-wide.

### Step 4: Buttons (top 6 files → ≈ 75 buttons) and card literals in those files

Migrate to `ActionButton`; delete `primaryButton`/`secondaryButton` constants; pass `type="submit"` where a button submits a `FormShell`. Replace `border border-border bg-card` literals in these files with `<Card>`. Append each file to `actionSurfaces` in `design-system-adoption.test.ts` and lower the `<button` and card baselines by the amounts removed.

**Verify**: `npm test -- adoption` passes with lowered baselines; every migrated file has `grep -c "<button" → 0`.

### Step 5: Local duplicates (Field ×6, stat tiles ×12, Section ×4, Switch ×7)

Replace with the shared primitives; delete the local components. For `read-audit`'s uppercase-label `Field`, adopt the shared visual (product decision: labels are `text-sm font-medium` everywhere).

**Verify**: `grep -rn "^function Field\|^const Field\|function MetricTile\|function SummaryTile\|function SummaryCard\|function FactCard\|^function Stat\b\|function SummaryRow\|function DetailRow\|function SummaryStrip\|function MetricLink" frontend/src/routes frontend/src/components | grep -v components/ui | grep -v components/form` → 0; `grep -rn "rounded-full bg-white transition-transform" frontend/src | grep -v components/ui` → 0.

### Step 6: Raw tables (5 lists) + global Workloads toolbar

Migrate to `DataTable` with search + sort; append to `operationalGrids`. Restyle the search-page toolbar.

**Verify**: `npm test -- table-adoption` pass; gallery `dashboard_settings_operations.png` shows a search box and sortable headers.

### Step 7: Split the god-files

For each of the four ≥1,100-line routes:
- Extract inline modals to `-<name>-modal.tsx` and tab bodies to `-<name>-tab.tsx` siblings (existing convention).
- `nodes/$nodeName`: replace `nodeActionPending` with per-action pending derived from the mutation's `variables` (e.g. `nodeOperation.isPending && nodeOperation.variables?.action === "drain"`), so draining does not disable label/taint controls. Add a unit test for this.
- `apps/index.tsx`: promote Installed / Browse / Repositories to child routes under `clusters/$id/apps/` **only if** 023 has not already made the section a URL param; if it has, keep one route and extract the three views to `-*-tab.tsx`. Either way the deep link `?section=browse&install=<chart>` must keep working (there is an auto-open effect at ~226–231).
- After each split: `node scripts/check-complexity-budget.mjs --write-baseline`, review the diff (only the split files should move), commit the baseline with the split.

**Verify**: `wc -l` of each original file ≤ 600; complexity and code-health checks exit 0.

### Step 8: Modal-vs-route rule

Adopt the rule (write it into `frontend/docs/design-system.md` from 021): **create and edit are routes; modals are for confirmations, single-field actions, and pickers.** Then fix the two internally inconsistent families:
- Backups: if 018 turned `backups/schedules/new` and `backups/storage/new` into redirects, convert the inline creates in `settings/backup/index.tsx` into `settings/backup/schedules/new` and `settings/backup/destinations/new` routes; if 018 kept them as routes, leave them and convert `restore-modal.tsx` into `settings/backup/restores/new` (a route) reusing the same form component. Pick the option that results in *all three* creates being routes.
- Settings: leave existing `*/new` routes; convert any modal-based create in the same section (grep `ModalShell` in `routes/dashboard/settings/**` for creates) to a `*/new` route. Do not convert edit-in-place toggles.
- Do not touch alerting/catalog modals in this plan (they are consistent within their family).

**Verify**: `find frontend/src/routes/dashboard/settings -path "*new/index.tsx" | wc -l` increased by the number converted; smoke crawl passes with the updated manifest count.

### Step 9: Gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
cd .. && node scripts/check-complexity-budget.mjs && node scripts/code-health-inventory.mjs --check --verify-doc
```

## Test plan

- Ratchet tests updated per step (allowlists grow, baselines fall).
- Node page: unit test that per-action pending isolates buttons.
- Keyboard spec: one tab-arrow assertion on a migrated page.
- Gallery review of every migrated page (attach before/after PNGs to each PR).

## Done criteria

- [ ] Gates exit 0
- [ ] `grep -rn "<h1" frontend/src/routes frontend/src/components | grep -v components/ui/ | grep -v "eslint-disable"` → 0
- [ ] `<button` baseline in `design-system-adoption.test.ts` ≤ 60% of the value 021 measured
- [ ] Card literal baseline ≤ 50% of the 021 value
- [ ] No route file > 600 lines among the four named
- [ ] `grep -c "nodeActionPending" frontend/src/routes/dashboard/clusters/\$id/nodes/\$nodeName/index.tsx` → 0
- [ ] All create flows in backups and settings are routes
- [ ] README row updated

## STOP conditions

- A required variant is missing from a 021 primitive — STOP; do not extend `components/ui` here.
- Splitting `apps/index.tsx` breaks the `?install=` deep link auto-open — STOP if you cannot preserve it with the existing effect.
- Converting a modal to a route changes a URL that appears in `docs/openapi.yaml` `landing_route` enums or in `frontend/tests/e2e-live` — STOP.
- The complexity baseline diff shows movement in files you did not split.

## Maintenance notes

- Reviewers: any PR that adds a raw `<button>`, `<h1>`, `<table>`, local `Field`/tile, or card literal under `src/routes` must fail lint or a ratchet; if it passes, the ratchet is broken — fix the ratchet, not the PR.
- Remaining after this plan: ~50 medium-traffic files with raw buttons/cards; migrate opportunistically, always lowering the baseline in the same PR.
