# Plan 019: Rancher-grade shell — always-mounted cluster switcher with pinned/recent clusters, header kubeconfig and Import YAML, a usable collapsed rail, and an uncrowded cluster-context topbar

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/components/layout frontend/src/components/resources/create-resource-dialog.tsx frontend/src/lib/api/user-preferences.ts frontend/src/lib/hooks/kubernetes-proxy.ts internal/userpreferences internal/handler/user_preferences.go docs/openapi.yaml`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: L (2–3 days; one small backend change)
- **Risk**: MED (shell on every route; OpenAPI schema change with generated clients)
- **Depends on**: 018 (multi-open sidebar `openGroups` set; header-less Home group)
- **Category**: direction (Rancher parity, IA)
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

The core loop of a multi-cluster console is "which cluster am I on, take me to another one". Today the searchable cluster switcher exists but is mounted only inside a cluster (`topbar.tsx:211`, `sidebar.tsx:199`), so from Overview, Delivery, Alerting, RBAC or Settings there is no cluster affordance at all, and there are no pinned or recent clusters anywhere. Meanwhile the cluster-context topbar packs breadcrumbs, global search, project scope, namespace scope, Shell, ⌘K, theme, notifications and the user menu into 1280 px, and breadcrumbs crush to `D… › ( › s. › D. › S.` (see `frontend/gallery/dashboard_clusters_c-smoke-1_deployments_default_smoke-app.png` after a gallery run). Kubeconfig download lives only on the cluster overview page; "paste this manifest into the cluster" requires first navigating to the list page of the right kind even though the dialog already applies multi-kind documents. Rancher exposes all of these from the header on every page.

## Current state

Topbar layout, `frontend/src/components/layout/topbar.tsx:171-230`:

```tsx
    <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-border bg-background/80 px-3 backdrop-blur-lg sm:px-6">
      <button ... aria-label="Open navigation" className="... lg:hidden"><Menu/></button>
      {/* Left: Breadcrumbs */}
      <nav className="flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden text-sm">
        {breadcrumbs.map(...)}
      </nav>
      {/* Center: Cross-cluster Global Search (Phase A3) */}
      <div className="hidden md:flex flex-1 justify-center px-6"><GlobalSearch /></div>
      {/* Right: Actions */}
      <div className="flex items-center gap-2">
        {currentClusterId ? (<ClusterScopeControls clusterId={currentClusterId} />) : null}
        <ClusterShellLauncher clusterId={activeClusterId} ... />
        {/* Command Palette Trigger */}
        <button onClick={() => setCommandPaletteOpen(true)} ...><Command/><kbd>K</kbd></button>
        {/* Theme Toggle */} ...
        {/* Notifications */} ...
```

`currentClusterId` comes from `clusterIdFromPath(pathname)` (`topbar.tsx:68`); `activeClusterId = currentClusterId ?? rememberedClusterId` (`:72`) where `rememberedClusterId` is `useClusterScopeStore((s) => s.lastClusterId)`.

Cluster switcher, `frontend/src/components/layout/cluster-scope-controls.tsx:89-130`:

```tsx
export function SearchableClusterSwitcher({ clusterId, fallbackName }: { clusterId: string; fallbackName: string }) {
  ...
  const query = useClusterSearch(debouncedTerm, open);
  const clusters = query.data?.pages.flatMap((page) => page.data) ?? [];
  const subRoute = pathname.slice(`/dashboard/clusters/${clusterId}`.length);
  const select = (next: Cluster) => {
    const nextPath = `/dashboard/clusters/${next.id}${subRoute}`;
    void navigate({ to: withClusterScopeSelection(nextPath, search, scopes[next.id] ?? null, projectScopes[next.id] ?? null) });
    close();
  };
```

It is rendered only in `sidebar.tsx:199-216` inside `{isClusterContext && !collapsed && (...)}`. `ClusterOption` (`cluster-scope-controls.tsx:65`) renders one row.

Collapsed sidebar, `components/layout/sidebar-navigation-view.tsx:123-153`: when `collapsed`, every group renders all its items as icon-only links, ignoring `isOpen`, with only a `title` attribute — 57 icons in cluster context with heavy icon reuse (`Network` ×6 at `sidebar-navigation.ts:502,508-511`).

Kubeconfig buttons, `routes/dashboard/clusters/$id/index.tsx:20-23,245-270`: `useDownloadProxyKubeconfig` / `useDownloadDirectKubeconfig` from `@/lib/hooks/kubernetes-proxy`, rendered as two `ActionButton`s in the `PageHeader` `actions` slot; the direct one is disabled when `!directPermission.canWrite || !cluster.apiServerUrl || cluster.isLocal` with a `disabledReason`. No other page offers kubeconfig; no copy-to-clipboard variant exists.

Create/Import dialog, `components/resources/create-resource-dialog.tsx:30-41,143-197`:

```ts
interface CreateResourceDialogProps {
  open: boolean; onClose: () => void; clusterId: string;
  /** Resource type key from k8sTemplates (e.g. "deployment", "service") */
  templateKey: string;
  title: string; apiPath?: string; resourceType?: ResourceType;
}
...
  const [mode, setMode] = useState<"guided" | "yaml">("guided");
  const [yamlContent, setYamlContent] = useState(k8sTemplates[templateKey] || "");
```

It already parses multi-document YAML (`yaml.loadAll` at line ~216, 50-doc cap) and derives the API path per document via `createPathForManifest` (line 117) using `KIND_TO_PLURAL` (64+). It is mounted from 7 list surfaces, each with a fixed `templateKey`; nowhere kind-less.

User preferences, `frontend/src/lib/api/user-preferences.ts:7-21` (typed from OpenAPI) and `docs/openapi.yaml:4155-4200`:

```yaml
    UserPreferences:
      type: object
      additionalProperties: false
      required: [theme, table_density, landing_route, time_format, favorites]
      properties:
        theme: {enum: [light, dark, system]}
        table_density: {enum: [compact, comfortable]}
        landing_route: {enum: [/dashboard, /dashboard/clusters, ...]}
        time_format: {enum: [locale, 12h, 24h]}
        favorites: {type: array, maxItems: 12, uniqueItems: true, items: {enum: [...]}}
```

Backend: `internal/userpreferences/preferences.go` — struct (lines ~50–57), `Defaults()` (59), `Validate()` (67–90, enforces `MaxFavorites = 12` and uniqueness); handler `internal/handler/user_preferences.go` (`GetUserPreferences` 63, `PutUserPreferences` 93). Frontend types are generated: `npm run openapi:generate` in `frontend/` regenerates `src/types/openapi.generated.ts` and `src/lib/api/generated/client.ts`; `make openapi-generate` at repo root regenerates everything (embedded spec, Go SDK, route inventory).

Rancher reference: `rancher-dashboard/shell/components/nav/TopLevelMenu.vue:99-160` (switcher trigger "N clusters"), `:244-290` (pinned / recent / all shelves), `:8,467` (`PINNED_CLUSTERS`, `RECENT_CLUSTERS` prefs); `shell/components/nav/Header.vue:618` (`ClusterBadge` in the header), `:714` (`openImport()`), `:742` (download kubeconfig), `:757` (copy kubeconfig). Rancher shows no breadcrumbs in cluster context; the cluster badge on the left is the context.

Conventions: `ActionButton` (`@/components/ui/action-button`) for buttons; `ModalShell` for dialogs; `useDismissable` (local to `cluster-scope-controls.tsx:31`) for popovers; toasts via `@/lib/toast` (`toastSuccess`, `toastApiError`); tests colocated (`cluster-scope-controls.test.tsx` is the pattern for switcher tests).

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Frontend gate | `cd frontend && npm run type-check && npm run lint && npm test` | exit 0 |
| Regenerate API types after editing `docs/openapi.yaml` | `make openapi-generate` (repo root) | exit 0; commit the generated diffs |
| Backend tests | `go test ./internal/userpreferences/... ./internal/handler/ -run Preferences` | pass |
| Backend vet | `go vet ./internal/... ./cmd/...` | exit 0 |
| Route smoke | `cd frontend && npm run test:e2e:smoke` | pass |
| Gallery | `cd frontend && SMOKE_GALLERY=1 npx playwright test --project=route-smoke` | PNGs in `frontend/gallery/` |

## Scope

**In scope**:
- `frontend/src/components/layout/topbar.tsx`, `cluster-scope-controls.tsx` (+ test), `sidebar.tsx`, `sidebar-navigation-view.tsx`, `global-search.tsx`
- `frontend/src/components/layout/cluster-switcher-menu.tsx` (create), `header-cluster-actions.tsx` (create)
- `frontend/src/components/resources/create-resource-dialog.tsx` (make `templateKey` optional)
- `frontend/src/lib/hooks/kubernetes-proxy.ts` (extract a `useClusterKubeconfig` hook) and `frontend/src/routes/dashboard/clusters/$id/index.tsx` (consume it)
- `frontend/src/lib/api/user-preferences.ts`, `frontend/src/lib/user-preferences.ts` (hook), `frontend/src/lib/cluster-scope.ts` (recent clusters)
- `docs/openapi.yaml` `UserPreferences` schema; `internal/userpreferences/preferences.go` (+ test); `internal/handler/user_preferences.go` only if field mapping is manual there
- Generated files produced by `make openapi-generate`

**Out of scope**:
- Nav group contents (018/020). Cluster page bodies. The notification centre and user menu.
- Any new backend endpoint. Only the preferences schema gains fields.
- Kubeconfig minting rules (`clusters_direct_kubeconfig.go`) — reuse the existing hooks exactly; do not change TTL, audit, or permission checks.

## Git workflow

- Branch: `advisor/019-global-shell`
- Conventional commits; the OpenAPI change and its generated output in one commit (`feat(api): add pinned_clusters user preference`).

## Steps

### Step 1: `pinned_clusters` preference (backend + contract)

> **Reconciled 2026-09-21 (executor STOP):** `user_preferences` is an explicit-column table (`internal/db/migrations/033_user_preferences.up.sql`), not a JSON document. A new preference needs: an up/down migration `063_user_preferences_pinned_clusters.{up,down}.sql` mirroring the `favorites` column (`jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(...) = 'array' AND jsonb_array_length(...) <= 20)`, on one line so `scripts/check-migrations.sh` sees the DEFAULT); the column added to `internal/db/queries/user_preferences.sql` INSERT/UPDATE; `make sqlc-generate` + `make sqlc-check`; and `internal/handler/user_preferences.go` mapping the column in `preferencesFromRow` and `PutUserPreferences` exactly like `Favorites`. These files are added to Scope. Gate additions: `./scripts/check-migrations.sh && make sqlc-check`.


- `docs/openapi.yaml` `UserPreferences`: add
  ```yaml
        pinned_clusters:
          type: array
          maxItems: 20
          uniqueItems: true
          items: { type: string, format: uuid }
  ```
  Do **not** add it to `required` (older clients omit it). Keep `additionalProperties: false`.
- `internal/userpreferences/preferences.go`: add `PinnedClusters []string \`json:"pinned_clusters"\`` to the struct; default `[]string{}`; in `Validate()` enforce `len <= 20`, uniqueness, and that each entry parses as a UUID (`github.com/google/uuid` is already imported by the handler). Do not verify cluster existence (a pinned cluster may be decommissioned; the UI filters).
- Extend `internal/userpreferences` tests with: too many pins → error; duplicate → error; non-uuid → error; empty → ok.
- `make openapi-generate`; commit generated changes.
- `frontend/src/lib/api/user-preferences.ts`: add `pinned_clusters: []` to `defaultUserPreferences`.

**Verify**: `go test ./internal/userpreferences/...` pass; `cd frontend && npm run openapi:check` exit 0; `npm run type-check` exit 0.

### Step 2: Recent clusters (client-only)

- In `frontend/src/lib/cluster-scope.ts` (the zustand store that already holds `lastClusterId`), add `recentClusterIds: string[]` (max 5, most-recent-first, deduped) updated wherever `lastClusterId` is set. Persist with the store's existing persistence mechanism (inspect the file: if it uses `persist`, add the key to the partialize list; if it does not persist, add a `localStorage` read/write wrapped in try/catch, key `astronomer.recentClusters`).

**Verify**: unit test in `lib/__tests__/cluster-scope.test.ts` (create if absent): setting `lastClusterId` three times yields `recentClusterIds` in reverse order, capped at 5.

### Step 3: `ClusterSwitcherMenu` — always mounted in the topbar

Create `components/layout/cluster-switcher-menu.tsx`:

- Trigger: an `ActionButton`-styled chip at the **left** of the topbar (right after the hamburger), showing a `Server` icon and either the current cluster's display name (with the cluster's `badgeColor` dot when set — the field exists on `Cluster`) or "Clusters" when not in cluster context. Keyboard shortcut `Ctrl/⌘+J` opens it (Rancher's shortcut; do not collide with `⌘K`).
- Popover body (reuse `useDismissable`, the search input, `useClusterSearch` and `ClusterOption` from `cluster-scope-controls.tsx` — export `ClusterOption` and `useDismissable`, do not copy them):
  1. **Pinned** shelf: clusters whose ids are in `preferences.pinned_clusters` (fetch by id via `useCluster` for each, or filter the first search page; prefer a single `useClusters({ ids })` if the API supports an id filter — check `lib/api/clusters.ts`; if not, fall back to per-id `useCluster` capped at 20).
  2. **Recent** shelf: `recentClusterIds` minus pinned.
  3. **All / search results**: the existing infinite search list.
  Each row has a pin toggle (star icon, `aria-pressed`) that updates `pinned_clusters` through the existing `useUserPreferences().update` (inspect `lib/user-preferences.ts` for the mutator name).
- Selecting a cluster: if currently in cluster context, preserve the sub-route exactly as `SearchableClusterSwitcher.select` does (`cluster-scope-controls.tsx:114-130`); otherwise navigate to `/dashboard/clusters/$id`.
- Replace the sidebar's `SearchableClusterSwitcher` block (`sidebar.tsx:199-216`) with nothing — the sidebar in cluster context keeps only "← All Clusters" and the version line. Keep `SearchableClusterSwitcher` exported for one release, marked `@deprecated`, then delete in 020.

**Verify**: `npm test -- cluster-switcher-menu` (new test modelled on `cluster-scope-controls.test.tsx`): renders pinned shelf from preferences; pin toggle calls the preferences mutator with the id added; selecting from a non-cluster route navigates to the overview. Gallery `dashboard.png` shows the chip at top-left.

### Step 4: Header cluster actions — kubeconfig + Import YAML

- Extract `useClusterKubeconfig(clusterId)` into `lib/hooks/kubernetes-proxy.ts` returning `{ downloadProxy, downloadDirect, copyProxy, proxyPending, directPending, directDisabledReason }`, moving the disabled-reason ladder from `clusters/$id/index.tsx:258-270` into the hook (it needs `cluster` and `directPermission`; accept them as arguments or fetch inside — keep whichever the file already uses). `copyProxy` writes the same blob text to `navigator.clipboard.writeText` and toasts "Kubeconfig copied (expires in 1 hour)".
- Create `components/layout/header-cluster-actions.tsx`: when `currentClusterId` is set, render next to `ClusterShellLauncher`:
  - a kubeconfig dropdown (`ActionMenu` from `@/components/ui/action-menu`) with "Download proxy kubeconfig", "Copy proxy kubeconfig", "Download direct kubeconfig" (disabled with reason);
  - an "Import YAML" button (`Upload` icon) that mounts `CreateResourceDialog` with `templateKey` omitted.
- `create-resource-dialog.tsx`: make `templateKey?: string`; when absent, initial `mode` is `"yaml"`, the guided/yaml toggle is hidden, `yamlContent` starts as `"# Paste one or more Kubernetes manifests, separated by ---\n"`, `resolvedResourceType` is `undefined`, and `useResourceSchema` is disabled. `createPathForManifest` must already handle any kind in `KIND_TO_PLURAL`; for unknown kinds it should surface a per-document error row ("Unsupported kind X") rather than throw — check the existing error path and keep it.
- Update `routes/dashboard/clusters/$id/index.tsx` to use `useClusterKubeconfig` (keep its two page-header buttons; they are still the discoverable entry for new users).

**Verify**: `npm test -- create-resource-dialog` — add a case: with no `templateKey` the dialog opens in YAML mode and applying two documents of different kinds issues two batch items. Gallery `dashboard_clusters_c-smoke-1_deployments.png` shows the kubeconfig and Import controls in the header.

### Step 5: Uncrowd the cluster-context topbar

- In cluster context (`currentClusterId` set), **hide the breadcrumb `<nav>`** (the switcher chip is the context; the page `PageHeader` is the title) and render `ClusterScopeControls` (project + namespace) immediately right of the chip, on the left side.
- Remove the separate `⌘K` chip button (`topbar.tsx:222-230`); instead render the `<kbd>⌘K</kbd>` hint inside `GlobalSearch`'s input (it already shows `/`). The command palette keyboard shortcut is unaffected.
- Outside cluster context keep breadcrumbs as they are.
- Set `document.title` unaffected (018 computes it from breadcrumbs regardless of rendering).

**Verify**: gallery `dashboard_clusters_c-smoke-1_deployments_default_smoke-app.png` shows: `[☰] [● Smoke East ▾] [All projects ▾] [All namespaces ▾]  …  [Search ⌘K] [Shell] [kubeconfig ▾] [Import] [theme] [bell] [user]` with no truncated crumbs. Route smoke passes.

### Step 6: Usable collapsed rail

In `sidebar-navigation-view.tsx` collapsed branch (`123-153`):

- Render **one icon per group** (use the first item's icon for groups without a dedicated icon; add an optional `icon` to `NavGroup` and set one per group in `sidebar-navigation.ts`), with `title={group.label}` and `aria-label={group.label}`.
- Clicking/hovering (use `onClick` + `onFocus`; keyboard must work) opens a flyout panel (`position: absolute; left: 100%`) listing the group's items as normal `nav-item` links. Close on Escape, outside click, and selection (`useDismissable`).
- Active state: highlight the group icon whose items contain the active route.

**Verify**: `npm test -- sidebar-navigation-view` (new): collapsed mode renders N group buttons, not N×items links; activating a group button shows its items. Gallery with `sidebarCollapsed` forced true (set the UI store in a small Playwright step or a unit render) shows ≤ 10 icons in cluster context.

### Step 7: Nav icon uniqueness

In `sidebar-navigation.ts` give each cluster nav item a distinct lucide icon within its group. Suggested for Gateway API (if the group still exists after 020; if 020 has landed, apply to wherever those items live): Gateways `Globe`, GatewayClasses `Layers`, HTTPRoutes `Route`, GRPCRoutes `Waypoints`, TLSRoutes `Lock`, TCPRoutes `Cable`, UDPRoutes `Radio`, ReferenceGrants `KeyRound`. Add a test to `sidebar-navigation.test.ts`: within any single group, no two items share an icon.

**Verify**: `npm test -- sidebar-navigation` pass.

### Step 8: Gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
go vet ./internal/... ./cmd/... && go test ./internal/userpreferences/... ./internal/handler/ -run Preferences
node scripts/check-complexity-budget.mjs
```

## Test plan

- Backend: `preferences_test.go` cases for `pinned_clusters` limits.
- `cluster-switcher-menu.test.tsx`: shelves, pin toggle, navigation from global and cluster contexts (sub-route preserved).
- `create-resource-dialog.test.tsx`: kind-less import mode.
- `sidebar-navigation-view.test.tsx`: collapsed rail flyout.
- `sidebar-navigation.test.ts`: icon uniqueness per group.
- Playwright: extend `tests/e2e/critical-workflows-keyboard.spec.ts` (or the closest existing keyboard spec) with: `Ctrl+J` opens the switcher, arrow to a cluster, Enter navigates.

## Done criteria

- [ ] All gate commands exit 0
- [ ] `grep -n "pinned_clusters" docs/openapi.yaml frontend/src/types/openapi.generated.ts internal/userpreferences/preferences.go` → ≥1 hit each
- [ ] `grep -n "SearchableClusterSwitcher" frontend/src/components/layout/sidebar.tsx` → 0
- [ ] `grep -n "templateKey: string" frontend/src/components/resources/create-resource-dialog.tsx` → 0 (it is optional now)
- [ ] `grep -c "useDownloadProxyKubeconfig" frontend/src/routes/dashboard/clusters/\$id/index.tsx` → 0 (uses `useClusterKubeconfig`)
- [ ] Collapsed sidebar in cluster context renders one control per group (test asserts)
- [ ] `git status` shows only in-scope files (plus generated OpenAPI outputs)
- [ ] `advisor-plans/README.md` row updated

## STOP conditions

- "Current state" excerpts don't match live code.
- `make openapi-generate` produces diffs outside `frontend/src/types`, `frontend/src/lib/api/generated`, `internal/apispec` (or wherever the embedded spec lives), `docs/generated-route-inventory.json`, and the Go SDK — investigate before committing; if the generator rewrites unrelated schemas, STOP.
- The clusters list API has no way to fetch pinned clusters without N requests and N > 20 is possible — cap at 20 (schema) and proceed; if the cap is rejected by product, STOP.
- Removing the breadcrumb nav in cluster context breaks an existing Playwright assertion that cannot be updated in scope — STOP and report the spec name.

## Maintenance notes

- Reviewers: the direct-kubeconfig `disabledReason` ladder must remain identical to today's (`clusters/$id/index.tsx:263-270`) — it encodes a security decision (no direct access for local clusters or without a verified endpoint).
- The kind-less Import dialog relies on `KIND_TO_PLURAL`; new CRD kinds imported via the header will report "Unsupported kind" until discovery-based path resolution lands (020 can extend `createPathForManifest` to consult the cluster's discovery list).
- Recent clusters are per-browser; pinned are per-user server-side. Do not "upgrade" recents to server-side without a product decision — it would leak cross-device activity.
