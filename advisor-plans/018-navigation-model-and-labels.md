# Plan 018: Make navigation Rancher-shaped — one label registry, multi-open sidebar, delivery layout, palette derived from nav, no orphan routes

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/components/layout frontend/src/components/settings frontend/src/lib/breadcrumbs.ts frontend/src/routes/dashboard/delivery frontend/src/routes/dashboard/settings/general frontend/src/routes/dashboard/backups frontend/src/routes/dashboard/workloads`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: L (2–3 days)
- **Risk**: MED (touches the shell on every route; label changes can break string-asserting tests)
- **Depends on**: none
- **Category**: direction / tech-debt (information architecture)
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

Astronomer's sidebar is a one-open accordion that snaps back to "Platform" on every navigation, so an operator comparing Deployments to Services must collapse one group to see the other, and resource counts are fetched for only one group at a time. Two global groups contain a single item. The delivery estate — the product's headline capability — is a dead end: click any sub-page and the sidebar forgets you are in Delivery. Eight destinations have three different names across nav label, page title and breadcrumb; every browser tab is titled identically; the command palette covers 7 of 18 global destinations; seven built pages are reachable only by typing a URL. Rancher avoids all of this with one registry per product that feeds the side nav, header, title and search. This plan gives Astronomer the same single source of truth.

## Current state

Files (all under `frontend/src/`):

- `components/layout/sidebar-navigation.ts` — `globalNavGroups` (lines 107–266), `withFavoriteNavigation` (268–283), `getClusterNavGroups` (286–659), `filterNavGroups` (661–685), and the accordion helpers `activeNavGroupLabel` / `defaultOpenNavGroupLabel` / `toggleOpenNavGroupLabel` (75–104).
- `components/layout/sidebar.tsx` — renders groups; holds the single-open state.
- `components/layout/sidebar-navigation-view.tsx` — `SidebarGroup` and `InstalledToolLinks`.
- `components/layout/command-palette.tsx` — hardcoded `pages` (41–49) and `clusterPages` (54–132); settings already derived from the registry (250–254).
- `components/layout/topbar.tsx` — breadcrumbs via `generateBreadcrumbs` (line 42 import; rendered 182–202).
- `lib/breadcrumbs.ts` — hand-kept `routeLabels` dictionary.
- `components/settings/settings-navigation.ts` — `SETTINGS_NAVIGATION` registry (46–219), `visibleSettingsNavigation` (225–244).
- `routes/dashboard/clusters/$id/delivery/route.tsx` — the per-cluster delivery layout with a 9-tab strip (the template for the missing estate layout).
- `routes/dashboard/delivery/index.tsx` — estate overview; its 7 siblings (`bundles`, `deployments`, `rollouts`, `sources`, `targets`, `override-sets`, `configuration-templates`) have **no** `route.tsx` layout.
- `routes/dashboard/settings/general/index.tsx` — tab list including an `sso` tab that duplicates `routes/dashboard/settings/auth/`.
- `routes/dashboard/backups/index.tsx` — redirects to `/dashboard/settings/backup`, stranding four children.

Single-open accordion, `sidebar.tsx:120-141`:

```ts
  const [openGroup, setOpenGroup] = useState<string | null>(() =>
    defaultOpenNavGroupLabel(navGroups, pathname),
  );
  const openGroups = useMemo(
    () => new Set(openGroup ? [openGroup] : []),
    [openGroup],
  );
  ...
  const [groupScope, setGroupScope] = useState({ navGroups, pathname });
  if (groupScope.navGroups !== navGroups || groupScope.pathname !== pathname) {
    setGroupScope({ navGroups, pathname });
    setOpenGroup(defaultOpenNavGroupLabel(navGroups, pathname));
  }
```

Counts gated on the open set, `components/layout/use-sidebar-resource-counts.ts:57,63,69,75,81,87,93` — each query has `enabled: scopeReady && openGroups.has("<Group>")`. Because `openGroups` holds at most one label, at most one group's counts ever load. This gating is correct and must be kept; only the size of the set changes.

Global nav today, `sidebar-navigation.ts:107-266` (abridged):

```
Platform:            Overview, Charlie, Clusters, Workloads, Cluster Agents, Onboarding Bundles
Observability:       Shared metrics (/monitoring), Shared stacks (/settings/monitoring), Alerting, Logging
Continuous Delivery: Estate (/delivery, exact: true)          ← single item
Integrations:        Cluster Tools (/tools), Extensions
Security:            Security Policies (/security)            ← single item
Administration:      Projects, RBAC, Audit Log, Settings (superuserOnly)
```

`exact: true` on Estate (`sidebar-navigation.ts:196`) means `activeNavGroupLabel` (line 79–81: `item.exact ? pathname === item.href : pathname.startsWith(item.href)`) matches nothing on `/dashboard/delivery/targets`, so the group collapses and "Platform" (the `defaultOpen` group) opens.

Breadcrumb dictionary, `lib/breadcrumbs.ts:1-59`, contains a dead `fleet: "Fleet Operations"` (line 18) and three slugs that do not match the real hrefs (`"persistent-volumes"`, `"persistent-volume-claims"`, `"storage-classes"` at 41–43; the nav uses `persistentvolumes`, `persistentvolumeclaims`, `storageclasses` at `sidebar-navigation.ts:525,530,537`).

Name drift (nav label → page H1 → breadcrumb):

| href | nav label | H1 | breadcrumb |
|---|---|---|---|
| `/settings/auth` | Authentication (`settings-navigation.ts:88`) | "Identity Broker" (`settings/auth/index.tsx:158`) | Auth |
| `/settings/monitoring` | Shared stacks (`sidebar-navigation.ts:168`) | — | Monitoring |
| `/delivery` | Estate | Estate | Continuous Delivery |
| `/cluster-templates` | Onboarding Bundles | — | Cluster Templates |
| `/security` | Security Policies | Security (`security/index.tsx:468`) | Security |
| `/agents` | Cluster Agents | Cluster Agents | Agents |
| `/tools` | Cluster Tools | Cluster Tools | Tools |
| `/monitoring` | Shared metrics | Shared metrics | Monitoring |

`document.title`: `grep -rn "document.title" frontend/src` → 0 hits; `frontend/index.html` sets one static title.

Command palette, `command-palette.tsx:41-49`:

```ts
const pages = [
  { name: "Dashboard", href: "/dashboard", icon: LayoutDashboard },
  { name: "Clusters", href: "/dashboard/clusters", icon: Server },
  { name: "Projects", href: "/dashboard/projects", icon: Folder },
  { name: "Shared metrics", href: "/dashboard/monitoring", icon: BarChart3 },
  { name: "Continuous Delivery", href: "/dashboard/delivery", icon: Rocket },
  { name: "RBAC", href: "/dashboard/rbac", icon: Shield },
  { name: "Settings", href: "/dashboard/settings", icon: Settings },
] as const;
```

and a 12-entry `clusterPages` array (54–132). Settings, by contrast, are derived at 250–254 from `visibleSettingsNavigation(SETTINGS_NAVIGATION, …)` — copy that pattern.

Per-cluster delivery layout (the template), `routes/dashboard/clusters/$id/delivery/route.tsx:21-51` defines `const tabs = [{ key, label, icon, segment }, …]` and `:68-76` picks the active key by longest matching segment. There is no equivalent `routes/dashboard/delivery/route.tsx`.

Settings › General tabs, `routes/dashboard/settings/general/index.tsx:20-25`:

```ts
const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: "sso", label: "SSO Providers", icon: Shield },
  { key: "general", label: "General", icon: Settings },
  { key: "tokens", label: "API Tokens", icon: Key },
  ...
```

Orphans (zero inbound `to=`/`href=` references outside their own directory and `routeTree.gen.ts`):

- `routes/dashboard/settings/native-rbac/index.tsx`
- `routes/dashboard/settings/backup-drill/index.tsx` (the hooks/API are used; the page is not linked)
- `routes/dashboard/backups/schedules/new`, `backups/storage/new`, `backups/runs/$runId`, `backups/restores/$restoreId` (parent redirects away)
- `routes/dashboard/clusters/$id/network-policies/index.tsx` (nav points at `${base}/networkpolicies`, a different page served by the `$resource` catch-all — `sidebar-navigation.ts:559-561`)
- `routes/dashboard/clusters/$id/workloads/index.tsx` (a per-cluster workload overview with no nav entry)

Rancher reference (for shape, not for copying code): global menu = Home / cluster switcher with pinned+recent / **Multi-Cluster** (Cluster Management, Continuous Delivery) / **Configuration** (Users & Authentication, Extensions, Global Settings) — `rancher-dashboard/shell/components/nav/TopLevelMenu.vue:66-90,244-290,1452-1517`; product membership by `category: 'configuration'` in `shell/config/product/auth.js:36`, `settings.js:29`, `uiplugins.js:15`. Continuous Delivery declares its children as first-class nav rows: `shell/config/product/fleet.js:96-103`. Rancher sets `document.title` from breadcrumbs: `shell/utils/title.ts:2`.

Conventions: TanStack file routes; `RouterLink` from `@tanstack/react-router`; lucide icons; tests in vitest next to the file (`components/layout/sidebar-navigation.test.ts` is the pattern for nav helpers). Commit style: conventional commits, e.g. `fix(ui): keep complexity budget stable for yaml panel`.

## Commands you will need

| Purpose | Command (run in `frontend/`) | Expected |
|---|---|---|
| Install | `npm ci` | exit 0 (Node 22 works despite the `engines` field; do not upgrade Node for this plan) |
| Typecheck | `npm run type-check` | exit 0 |
| Lint | `npm run lint` | exit 0, 0 warnings |
| Unit tests | `npm test` | all pass |
| Route smoke (every route renders) | `npm run test:e2e:smoke` | all pass; the route-manifest generator asserts the route count — update its expected count when you add/remove routes (it prints the file to edit) |
| Screenshot gallery for eyeballing | `SMOKE_GALLERY=1 npx playwright test --project=route-smoke` | writes `frontend/gallery/*.png` |
| Complexity budget (repo root) | `node scripts/check-complexity-budget.mjs` | exit 0 |

## Scope

**In scope**:
- `frontend/src/components/layout/sidebar-navigation.ts`, `sidebar.tsx`, `sidebar-navigation-view.tsx`, `sidebar-navigation.test.ts`, `use-sidebar-resource-counts.ts`
- `frontend/src/components/layout/command-palette.tsx`
- `frontend/src/components/layout/topbar.tsx` (breadcrumb source + title hook only)
- `frontend/src/lib/breadcrumbs.ts` and its test
- `frontend/src/components/settings/settings-navigation.ts`
- `frontend/src/routes/dashboard/delivery/route.tsx` (create)
- `frontend/src/routes/dashboard/settings/general/index.tsx` and `-sso-tab.tsx` (remove the SSO tab)
- `frontend/src/routes/dashboard/settings/auth/index.tsx` (H1 only)
- `frontend/src/routes/dashboard/backups/**` (redirect stubs)
- `frontend/src/routes/dashboard/settings/native-rbac/index.tsx` (delete or link — see step 8)
- `frontend/src/lib/api/user-preferences.ts` (`favoriteNavigationOptions` labels only)
- `frontend/tests/e2e-smoke/route-manifest` expected count (generator-owned file it names)

**Out of scope** (do NOT touch):
- Topbar right-hand controls, cluster switcher, kubeconfig, import — Plan 019.
- Cluster-context nav contents / discovery gating — Plan 020.
- Any page body, table, or form. Only labels, layouts and nav data change here.
- Backend, OpenAPI, `landing_route`/`favorites` enums in `docs/openapi.yaml` (route paths do not change in this plan, so the enums stay valid).

## Git workflow

- Branch: `advisor/018-navigation-model`
- One commit per step; conventional commits (`feat(ui): …`, `fix(ui): …`, `refactor(ui): …`).
- Do not push or open a PR unless told to.

## Steps

### Step 1: Multi-open sidebar with persisted open groups

In `sidebar.tsx` replace `openGroup: string | null` with `openGroups: Set<string>`:

- Initial value: `new Set([...persisted, defaultOpenNavGroupLabel(navGroups, pathname)].filter(Boolean))` where `persisted` is read from `localStorage` key `astronomer.sidebar.openGroups.<global|cluster>` (two keys: one for global context, one for cluster context). Wrap all storage access in try/catch.
- On pathname/navGroups change: **union in** the active group (`activeNavGroupLabel`) instead of replacing the set. Never remove groups on navigation.
- `onToggle` adds/removes the label and persists.
- Pass `isOpen={openGroups.has(group.label)}` to `SidebarGroup` and the `InstalledToolLinks` block; pass the full `openGroups` to `useSidebarResourceCounts` (it already takes a `Set<string>`).
- In `sidebar-navigation.ts` keep `activeNavGroupLabel` and `defaultOpenNavGroupLabel`; delete `toggleOpenNavGroupLabel` (single-open semantics) and update `sidebar-navigation.test.ts` accordingly (drop the "accordion" describe; add a test that `activeNavGroupLabel` still finds the group).

**Verify**: `npm test -- sidebar-navigation` → pass. Manually (or via gallery): open `/dashboard/clusters/<id>/deployments`, expand "Service Discovery"; navigate to `/services` — both "Workloads" and "Service Discovery" stay open and both show counts.

### Step 2: Regroup the global nav

Edit `globalNavGroups` in `sidebar-navigation.ts` to this shape (keep every existing `permission`, `featureFlag`, `optIn`, `superuserOnly`, `requiresCharlieActivated` field on the moved items exactly as it is today; only `label`/grouping change):

```
Home group (no header — render items with group label "" flat; see below):
  Overview (/dashboard, exact), Clusters (/dashboard/clusters), Search (/dashboard/search), Charlie (unchanged gates)
Continuous Delivery:
  Estate (/dashboard/delivery, exact),
  Deployments (/dashboard/delivery/deployments), Rollouts (/dashboard/delivery/rollouts),
  Sources (/dashboard/delivery/sources), Bundles (/dashboard/delivery/bundles),
  Targets (/dashboard/delivery/targets), Templates (/dashboard/delivery/configuration-templates),
  Overrides (/dashboard/delivery/override-sets)
  — all with permission { resource: "delivery_targets", verb: "list" } (same as Estate today)
Observability:
  Metrics (/dashboard/monitoring), Alerting, Logging, Shared stacks (/dashboard/settings/monitoring)
Security:
  Security (/dashboard/security), Cluster Agents (/dashboard/agents)   ← agents is fleet health, keep gates
Users & Access:
  RBAC (/dashboard/rbac), Projects (/dashboard/projects), Audit Log (/dashboard/audit)
Configuration:
  Onboarding templates (/dashboard/cluster-templates), Cluster Tools (/dashboard/tools),
  Extensions (unchanged gates), Settings (superuserOnly)
```

- Remove the global "Workloads" item; `/dashboard/workloads` stays a valid route (it is a `landing_route` enum value) and is now reached via Search. Update `favoriteNavigationOptions` in `lib/api/user-preferences.ts:44` label from "Workloads" to "Search: Workloads" (href unchanged).
- Support header-less groups: add `hideLabel?: boolean` to `NavGroup`; `SidebarGroup` renders items without the collapsible header when set, and such groups are always open (skip them in the open-set logic).
- Retire `defaultOpen` on Platform; the header-less Home group is always visible.

**Verify**: `npm run type-check && npm test -- sidebar` → pass. `grep -n '"Administration"\|"Integrations"\|"Platform"' frontend/src/components/layout/sidebar-navigation.ts` → no matches.

### Step 3: Delivery estate layout route

Create `routes/dashboard/delivery/route.tsx` modelled on `routes/dashboard/clusters/$id/delivery/route.tsx:21-76,78-99`:

- `createFileRoute("/dashboard/delivery")({ component: DeliveryEstateLayout })`.
- Tab list = the seven Continuous Delivery items from step 2 plus "Estate" (segment `""`), same `{ key, label, icon, segment }` shape and the same longest-segment active-key logic.
- Render `PageHeader` (from `@/components/ui/page`) with `eyebrow="Continuous Delivery"`, `title` = active tab label, then the tab strip (use `TabsList`/`TabsTrigger` from `@/components/ui/tabs` wrapped in `RouterLink`s), then `<Outlet />`.
- Remove the hand-rolled eyebrow/H1 from `routes/dashboard/delivery/index.tsx` (lines around 300–310 render "CONTINUOUS DELIVERY / Estate") so the page does not show two headers. Keep everything else on that page.
- Remove the `-modal`/card grid links at `delivery/index.tsx:985-1025` only if they are pure navigation cards duplicating the tab strip; if they carry counts, keep them.

**Verify**: `npm run test:e2e:smoke` → pass (route count +1; update the generator's expected count). Gallery: `dashboard_delivery_targets.png` shows the eyebrow, tab strip with Targets active, and the sidebar "Continuous Delivery" group open with Targets highlighted.

### Step 4: One label registry; breadcrumbs derived from it

- In `sidebar-navigation.ts` export `export function navLabelForHref(href: string): string | undefined` that searches `globalNavGroups` and `SETTINGS_NAVIGATION` (import from `@/components/settings/settings-navigation`) for an exact `href` match and returns its `label`/`title`.
- In `lib/breadcrumbs.ts`:
  - `generateBreadcrumbs` first tries `navLabelForHref(path)` for the accumulated path; falls back to `breadcrumbLabel(segment)`.
  - Delete `fleet: "Fleet Operations"` (line 18).
  - Fix the three slugs at 41–43 to `persistentvolumes`, `persistentvolumeclaims`, `storageclasses` (labels unchanged).
  - Add a test in the existing breadcrumbs test file: for every item in `globalNavGroups` (flattened) and `SETTINGS_NAVIGATION` (flattened), `generateBreadcrumbs(item.href).at(-1).label === item.label`.
- Rename to reconcile the table in "Current state":
  - `settings/auth/index.tsx:158` `title="Identity Broker"` → `title="Authentication"`.
  - `settings-navigation.ts:161` "Shared observability stacks" → "Shared stacks".
  - Keep "Estate" as both nav label and H1; the breadcrumb now reads "Continuous Delivery › Estate" because the layout route contributes the group name (step 3).
- Add a vitest that asserts every `href` in `globalNavGroups` + `SETTINGS_NAVIGATION` resolves to a route id in `src/routeTree.gen.ts` (read the file, regex for `'/dashboard/...'` route ids).

**Verify**: `npm test -- breadcrumbs sidebar-navigation` → pass, including the two new tests.

### Step 5: `document.title` per route

- Add `lib/use-document-title.ts`: `useDocumentTitle(parts: string[])` sets `document.title = [...parts.filter(Boolean), "Astronomer"].join(" · ")` in an effect and restores the previous title on unmount.
- Call it in `Topbar` (`topbar.tsx`, where `breadcrumbs` is already computed) with `breadcrumbs.map(c => c.label).slice(1)` (drop the leading "Dashboard").
- Unit test with `@testing-library/react` `renderHook`: title becomes `"Clusters · Smoke East · Astronomer"` for a two-crumb input.

**Verify**: `npm test -- use-document-title` → pass. Gallery run: `page.title()` differs across routes (add an assertion to `tests/e2e-smoke/route-crawl.spec.ts`: `expect(await page.title()).not.toBe("Astronomer - Kubernetes Multi-Cluster Management")` for `kind === "app"` entries).

### Step 6: Command palette derived from the nav registry

In `command-palette.tsx`:

- Delete the `pages` constant (41–49). Replace its consumer with `filterNavGroups(globalNavGroups, user, featureFlags, charlieActivated).flatMap(g => g.items)` (the sidebar already computes the same; `useCharlieActivated` is exported from `@/lib/hooks/clusters`).
- Delete `clusterPages` (54–132). Replace with `getClusterNavGroups(currentClusterId, {}).flatMap(g => g.items)` filtered through `filterNavGroups`, when `currentClusterId` is set. Use each item's `label` as the command name and its group label as the description.
- Keep the existing settings derivation (250–254) and the resource-search group untouched.
- Test: every `href` in `globalNavGroups` appears in the rendered palette for a superuser (render with a superuser in `useAuthStore`; search for each label; expect a match). Model on `components/layout/cluster-scope-controls.test.tsx` for store setup.

**Verify**: `npm test -- command-palette` → pass. `grep -c "clusterPages\|const pages" frontend/src/components/layout/command-palette.tsx` → 0.

### Step 7: Remove the duplicate SSO tab; retitle Settings › General

- In `routes/dashboard/settings/general/index.tsx` remove the `sso` entry from `tabs` and `TabKey`; make `general` the fallback tab. Delete `-sso-tab.tsx` if nothing else imports it (`grep -rn "sso-tab" frontend/src` must return 0 after the edit).
- Add a one-line `StatePanel` (tone `info`) at the top of the General tab: "Single sign-on providers are configured under Settings › Authentication." with `actionHref="/dashboard/settings/auth"`.
- If `settings/general/index.tsx` renders `PageHeader title="Settings"`, change to `title="General"` so H1 = nav label.

**Verify**: `npm run type-check` → 0 errors; gallery `dashboard_settings_general.png` shows tabs General / API Tokens / Audit Log / Support and H1 "General".

### Step 8: Orphan routes

- `routes/dashboard/clusters/$id/workloads/index.tsx`: add `{ label: "Overview", href: \`${base}/workloads\`, icon: LayoutDashboard, exact: true }` as the **first** item of the cluster "Workloads" group in `getClusterNavGroups` (mirrors Rancher `WORKLOAD_DASHBOARD`, `shell/config/product/explorer.js:113`).
- `routes/dashboard/settings/backup-drill/index.tsx`: add to `SETTINGS_NAVIGATION` › Reliability as `{ href: "/dashboard/settings/backup-drill", title: "Backup drills", description: "Restore rehearsals and drill history.", icon: <pick an unused lucide icon> }`.
- `routes/dashboard/settings/native-rbac/index.tsx`: read the file. If it is a distinct capability (native ClusterRole passthrough policies), add it under `SETTINGS_NAVIGATION` › Identity & access as "Native RBAC passthrough". If it duplicates `/dashboard/rbac`, delete the route file and update the route-manifest expected count. Record which you did in the commit message.
- `routes/dashboard/backups/schedules/new`, `backups/storage/new`: read them; if `settings/backup/index.tsx` already offers the same create flows inline, replace each with a `beforeLoad: () => { throw redirect({ to: "/dashboard/settings/backup" }) }` stub; otherwise link them from `settings/backup/index.tsx` with `ActionButton`s ("New schedule", "New destination"). `backups/runs/$runId` and `backups/restores/$restoreId`: link from the runs/restores tables in `settings/backup/index.tsx` (row click → detail).
- `routes/dashboard/clusters/$id/network-policies/index.tsx` vs `${base}/networkpolicies`: open both. Keep the purpose-built page if it offers more than the generic list (apply templates, drift); point the Policy nav item at it (`sidebar-navigation.ts:560`). Otherwise delete it and update the manifest count.

**Verify**: for each retained route, `grep -rn "<href>" frontend/src --include=*.tsx --include=*.ts | grep -v routeTree | grep -v "<its own dir>"` → ≥1 inbound reference. `npm run test:e2e:smoke` → pass with the corrected manifest count.

### Step 9: Re-home `settings/monitoring` under Observability

- Move `routes/dashboard/settings/monitoring/index.tsx` to `routes/dashboard/monitoring/stacks/index.tsx` (it is a 7-line delegating route; the component moves unchanged).
- Leave a redirect stub at the old path: `beforeLoad: () => { throw redirect({ to: "/dashboard/monitoring/stacks" }) }`.
- Update the "Shared stacks" href in `sidebar-navigation.ts`, remove the entry from `SETTINGS_NAVIGATION` › Reliability, update `routes/dashboard/route.tsx:288` feature-prefix list (`/dashboard/settings/monitoring` → `/dashboard/monitoring/stacks`), and delete the explanatory comment at `sidebar-navigation.ts:162-166`.
- Also remove the duplicate Extensions entry from `SETTINGS_NAVIGATION` (it stays in the global nav under Configuration).

**Verify**: gallery `dashboard_monitoring_stacks.png` renders without the settings sub-navigation column; old URL redirects (smoke crawl of the stub passes).

### Step 10: Final gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
cd .. && node scripts/check-complexity-budget.mjs
```

## Test plan

- `sidebar-navigation.test.ts`: multi-open set semantics; `navLabelForHref`; every nav href resolves to a generated route id; no duplicate hrefs across groups.
- `breadcrumbs.test.ts`: last crumb label equals nav label for every registry href; slug fixes.
- `use-document-title.test.ts`: title format and restore on unmount.
- `command-palette.test.tsx`: every global nav href reachable by label search.
- `route-crawl.spec.ts`: per-route title assertion.

## Done criteria

- [ ] `npm run type-check`, `npm run lint`, `npm test`, `npm run test:e2e:smoke` all exit 0
- [ ] `grep -rn "document.title" frontend/src/lib/use-document-title.ts` → 1 hit; every crawled app route has a distinct title
- [ ] `grep -c "toggleOpenNavGroupLabel" frontend/src/components/layout/*.ts*` → 0
- [ ] `frontend/src/routes/dashboard/delivery/route.tsx` exists and renders a tab strip
- [ ] `grep -n "Fleet Operations\|persistent-volumes" frontend/src/lib/breadcrumbs.ts` → 0
- [ ] `grep -n '"sso"' frontend/src/routes/dashboard/settings/general/index.tsx` → 0
- [ ] Every route listed in step 8 has ≥1 inbound link or is a redirect stub
- [ ] No files outside the in-scope list modified (`git status`)
- [ ] `advisor-plans/README.md` status row updated

## STOP conditions

- The "Current state" excerpts do not match the live code.
- `landing_route` or `favorites` validation in `internal/userpreferences/preferences.go` rejects an existing href after your change (it must not: no hrefs change in this plan except the `settings/monitoring` move, which is not in either enum — verify with `grep -n "settings/monitoring" docs/openapi.yaml`; if it *is* there, STOP).
- Removing `native-rbac` or `clusters/$id/network-policies` would delete functionality with no equivalent elsewhere — STOP and report instead of deleting.
- The route-manifest generator's expected count cannot be reconciled with the routes you added/removed.

## Maintenance notes

- Adding a nav destination now means one entry in `globalNavGroups` or `SETTINGS_NAVIGATION`; the palette, breadcrumbs, title, and the route-resolution test pick it up automatically. Reviewers should reject PRs that add labels anywhere else.
- Plan 019 will mount a cluster switcher in the header; Plan 020 will make cluster groups dynamic. Both consume `openGroups: Set<string>` from step 1.
- Deferred: a single `SubNavigation` primitive unifying the settings left rail, the delivery tab strip and `?tab=` pages (design spike, see 017 §4.1).
