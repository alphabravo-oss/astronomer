# Plan 020: Cluster explorer navigation that describes the cluster — discovery-gated items, Gateway API folded into Service Discovery, dynamic "More Resources" CRD groups, starred resource types

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/components/layout/sidebar-navigation.ts frontend/src/components/layout/sidebar.tsx frontend/src/components/layout/use-sidebar-resource-counts.ts frontend/src/components/clusters/custom-resources-page.tsx frontend/src/lib/api/resources.ts frontend/src/lib/api/user-preferences.ts`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: L (3 days)
- **Risk**: MED (nav becomes data-driven; an extra discovery call per cluster)
- **Depends on**: 018 (multi-open groups), 019 (icon uniqueness; header switcher so the sidebar has room)
- **Category**: direction (Rancher parity)
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

The cluster side nav lists 57 static items in 9 groups (Rancher: 31 in 5). "Gateway API" is a permanent 8-row group even on clusters without those CRDs; Gatekeeper, Service Mesh, Image Scans and others render regardless of whether anything backs them. Meanwhile the thing operators most often need from a Rancher-style explorer — cert-manager Certificates, Istio VirtualServices, Argo Applications, Crossplane claims — is only reachable by going Custom Resources → find the group in a table → drill. Rancher builds one nav subgroup per in-use CRD API group under "More Resources" and lets users star types. The discovery data already exists in Astronomer; this plan turns it into navigation.

## Current state

Cluster nav, `frontend/src/components/layout/sidebar-navigation.ts:286-659` — `getClusterNavGroups(clusterId, opts)` with `opts: { isLocal?, veleroInstalled?, grafanaAvailable? }`. Groups and counts: Cluster 15 (9 + 6 `agentRequiredItems` at 299–350), Observability 5, Workloads 6, Service Discovery 3, Gateway API 8 (499–518), Storage 5, Policy 5, RBAC 5, More Resources 5 (626–657: Custom Resources, CRDs, Endpoints, ReplicaSets, Mirrored Resources).

Gateway API group today (`:498-518`):

```ts
    {
      label: "Gateway API",
      items: [
        { label: "Gateways", href: `${base}/gateways`, icon: Globe },
        { label: "HTTPRoutes", href: `${base}/httproutes`, icon: Network },
        { label: "GatewayClasses", href: `${base}/gatewayclasses`, icon: Layers },
        { label: "GRPCRoutes", href: `${base}/grpcroutes`, icon: Network },
        { label: "TLSRoutes", href: `${base}/tlsroutes`, icon: Network },
        { label: "TCPRoutes", href: `${base}/tcproutes`, icon: Network },
        { label: "UDPRoutes", href: `${base}/udproutes`, icon: Network },
        { label: "ReferenceGrants", href: `${base}/referencegrants`, icon: KeyRound },
      ],
    },
```

`NavItem` type (`sidebar-navigation.ts:52-69`) has `permission`, `superuserOnly`, `featureFlag`, `optIn`, `requiresCharlieActivated`, `countKey`, `exact` — no schema/discovery predicate. `filterNavGroups` (661–685) filters by feature flag, Charlie, superuser, permission only.

Sidebar composes groups in `components/layout/sidebar.tsx:96-116`:

```ts
  const navGroups = useMemo(() => {
    const baseGroups = isClusterContext
      ? getClusterNavGroups(clusterId!, { isLocal: cluster?.isLocal, veleroInstalled: !!veleroStatus?.installed, grafanaAvailable: monitoringStatus?.grafanaAvailable === true })
      : globalNavGroups;
    const groups = isClusterContext ? baseGroups : withFavoriteNavigation(baseGroups, preferences.favorites);
    return filterNavGroups(groups, user, featureFlags, charlieActivated);
  }, [...]);
```

Favorites apply **only** outside cluster context (`withFavoriteNavigation` at `sidebar-navigation.ts:268-283` maps `favorites: string[]` of global hrefs to a "Favorites" group). `favorites` is validated server-side against a fixed enum of global routes (`docs/openapi.yaml:4181-4200`; `internal/userpreferences/preferences.go` `Validate()`).

Counts: `components/layout/use-sidebar-resource-counts.ts` — one query per group, `enabled: scopeReady && openGroups.has("<Group label>")`.

CRD discovery: `components/clusters/custom-resources-page.tsx` lists CRDs with a "Group" column (~lines 144–148) via `lib/api/resources.ts` (find the function that returns the CRD list; it is the same data the `routes/dashboard/clusters/$id/custom-resources/$.tsx` splat route drills into with `custom-resources/<group>/<version>/<plural>`).

Rancher reference: `rancher-dashboard/shell/config/product/explorer.js:79-121` (5 groups; `GATEWAY_API.HTTP_ROUTE` and `GATEWAY_API.GATEWAY` live inside `serviceDiscovery` at 100–101); every product/type is gated by `ifHaveType` / `ifHaveGroup` / `ifFeature` (`explorer.js:62-77`, `gatekeeper.js:22`, `istio.js:17-18`); `shell/store/type-map.js:758` (`starred` group) and `:761` (`inUse::<groupLabel>` subgroups); friendly group names at `explorer.js:158-174` (`cert-manager.io` → "Cert Manager", `monitoring.coreos.com` → "Monitoring", `longhorn.io` → "Longhorn", etc.).

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Frontend gate | `cd frontend && npm run type-check && npm run lint && npm test` | exit 0 |
| Route smoke | `cd frontend && npm run test:e2e:smoke` | pass |
| Gallery | `cd frontend && SMOKE_GALLERY=1 npx playwright test --project=route-smoke` | PNGs |
| If you touch `docs/openapi.yaml` | `make openapi-generate` then `cd frontend && npm run openapi:check` | exit 0 |

## Scope

**In scope**:
- `frontend/src/components/layout/sidebar-navigation.ts` (+ test), `sidebar.tsx`, `use-sidebar-resource-counts.ts`
- `frontend/src/components/layout/use-cluster-discovery-nav.ts` (create)
- `frontend/src/lib/api/resources.ts` only if a typed accessor for the CRD list is missing
- `frontend/src/lib/api/user-preferences.ts`, `docs/openapi.yaml` `UserPreferences.starred_types` (new field), `internal/userpreferences/preferences.go` (+ test)
- `frontend/tests/e2e-smoke` stub overrides for the CRD list if the crawl needs deterministic groups

**Out of scope**:
- The custom-resources pages themselves; the `$resource` list/detail pages.
- Global nav (018), header (019).
- Any change to which routes exist. Hiding a nav row never removes its route.

## Git workflow

- Branch: `advisor/020-explorer-nav`
- Conventional commits.

## Steps

### Step 1: Discovery hook

Create `components/layout/use-cluster-discovery-nav.ts` exporting `useClusterDiscovery(clusterId)` that returns `{ groups: Set<string>, kinds: Set<string>, crdsByGroup: Map<string, Array<{ group, version, plural, kind }>>, isLoading }` from the same API the custom-resources page uses. `staleTime: 5 * 60_000`. Use the query-key factory (`lib/query-keys.ts`) — inline `queryKey` arrays fail lint.

**Verify**: unit test with a mocked API: two CRDs in `cert-manager.io` and one in `monitoring.coreos.com` produce `groups.size === 2` and the right map.

### Step 2: `ifHaveGroup` / `ifHaveKind` on `NavItem`

- Add to `NavItem`: `ifHaveGroup?: string` (API group, e.g. `"gateway.networking.k8s.io"`), `ifHaveKind?: string`.
- Add a second filter stage `filterNavGroupsByDiscovery(groups, discovery)` in `sidebar-navigation.ts` that drops items whose predicate is unmet; when `discovery.isLoading`, keep items (avoid nav flicker) — items disappear once discovery resolves.
- Annotate: all Gateway API items `ifHaveGroup: "gateway.networking.k8s.io"`; Gatekeeper `ifHaveGroup: "constraints.gatekeeper.sh"`; Service Mesh `ifHaveGroup: "networking.istio.io"` (keep the route reachable); Image Scans `ifHaveGroup: "aquasecurity.github.io"` (Trivy Operator) — check what the image-scans page actually reads and use that group; if it reads an Astronomer-side API rather than a CRD, leave it ungated.
- Wire the hook into `sidebar.tsx` `navGroups` memo after `filterNavGroups`.

**Verify**: `npm test -- sidebar-navigation`: with an empty discovery set, no Gateway API items render; with `gateway.networking.k8s.io` present, they do.

### Step 3: Fold Gateway API into Service Discovery; trim the Cluster group

- Move `Gateways` and `HTTPRoutes` into the Service Discovery group (after Ingresses), Rancher-style. Move the remaining six Gateway API items (GatewayClasses, GRPC/TLS/TCP/UDP routes, ReferenceGrants) into "More Resources" as a static sub-block titled "Gateway API" (see step 4 for sub-blocks). Delete the standalone group.
- Cluster group: keep Overview, Nodes, Namespaces, Events, Members (if a members page exists — it does not today; skip), Tools, Apps, Delivery. Move Adoption, Shell, Control-plane DR, Registries, Snapshots, Network & Access, Image Scans, Service Mesh into a new group **"Cluster Management"** placed last before More Resources. (Shell also has a header launcher from 019; keep the nav row.)

**Verify**: `npm test`; gallery `dashboard_clusters_c-smoke-1.png`: Cluster group shows ≤ 8 rows; a "Cluster Management" group exists.

### Step 4: Dynamic "More Resources" CRD groups

- Extend `NavGroup` with `subgroups?: Array<{ label: string; items: NavItem[] }>`; `SidebarGroup` renders subgroups as a small uppercase caption + items (no second-level collapse; keep it flat like Rancher's `inUse::` groups which are collapsible only at the parent).
- Build subgroups from `crdsByGroup`: label = friendly name map (port the table from `rancher-dashboard/shell/config/product/explorer.js:158-174` into `lib/crd-group-labels.ts`, falling back to the raw group), items = one per CRD kind, `href = ${base}/custom-resources/${group}/${version}/${plural}` (the existing splat route), icon `Puzzle`, `countKey` = `crd:${group}/${plural}`.
- Sort subgroups alphabetically; cap at 40 kinds total with a trailing "All custom resources →" item linking to `${base}/custom-resources` when truncated.
- Keep the five static items (Custom Resources, CRDs, Endpoints, ReplicaSets, Mirrored Resources) as the first sub-block "Built-in".
- Counts: in `use-sidebar-resource-counts.ts` add one query for CRD counts, enabled when `openGroups.has("More Resources")`, that requests counts for the listed kinds (if the API only counts one kind per call, cap to the first 15 visible kinds and skip the rest — do not fan out unbounded requests).

**Verify**: unit test: three CRDs across two groups → two subgroups with friendly labels. Gallery with a stub CRD list (add to `tests/e2e-smoke/stub-overrides.ts` if the crawl's default stub has none) shows "Cert Manager › Certificates" under More Resources.

### Step 5: Starred resource types (cluster context)

> **Note (from Plan 019 execution):** `user_preferences` is an explicit-column table. `starred_types` needs a migration `NNN_user_preferences_starred_types.{up,down}.sql` mirroring the `favorites`/`pinned_clusters` columns (one-line `ADD COLUMN starred_types jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (...)`), the column added to `internal/db/queries/user_preferences.sql`, `make sqlc-generate && make sqlc-check`, and handler mapping in `internal/handler/user_preferences.go`. Add those files to Scope and `./scripts/check-migrations.sh && make sqlc-check` to the gate.


- `docs/openapi.yaml` `UserPreferences`: add `starred_types: { type: array, maxItems: 20, uniqueItems: true, items: { type: string, pattern: "^[a-z0-9./-]+$" } }` (values are `${group}/${plural}` or `core/${plural}`; not required). Backend `Validate()`: max 20, unique, pattern. `make openapi-generate`.
- In cluster context, `withFavoriteNavigation` equivalent: `withStarredTypes(groups, starred, base)` prepends a "Starred" group whose items are the matching cluster nav items (match by href suffix `/<plural>` or `/custom-resources/<group>/<version>/<plural>`), icon `Star`.
- Add a star toggle to each cluster nav row on hover/focus (`aria-pressed`, `aria-label="Star <label>"`) in `sidebar-navigation-view.tsx`, calling the preferences mutator. Keep it keyboard-reachable (a real `<button>` inside the row, not a click handler on the link).

**Verify**: backend test for `starred_types`; `npm test -- sidebar-navigation`: starring `apps/deployments` yields a Starred group first with Deployments.

### Step 6: Retire `SearchableClusterSwitcher` (deprecated in 019)

Delete the export and its test cases if 019 has landed and nothing imports it (`grep -rn SearchableClusterSwitcher frontend/src` → only its definition).

### Step 7: Gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
go test ./internal/userpreferences/... && go vet ./internal/...
node scripts/check-complexity-budget.mjs
```

## Test plan

- `use-cluster-discovery-nav.test.ts`: mapping from CRD list to groups/kinds.
- `sidebar-navigation.test.ts`: discovery filtering; loading keeps items; Gateway fold; subgroup construction and cap; starred group.
- `preferences_test.go`: `starred_types` validation.
- Smoke crawl: stub CRD list renders subgroups.

## Done criteria

- [ ] Gates exit 0
- [ ] `grep -n 'label: "Gateway API"' frontend/src/components/layout/sidebar-navigation.ts` → 0 (as a top-level group)
- [ ] `grep -n "ifHaveGroup" frontend/src/components/layout/sidebar-navigation.ts` → ≥ 8 hits
- [ ] `grep -n "starred_types" docs/openapi.yaml internal/userpreferences/preferences.go` → hits
- [ ] Cluster nav with empty discovery has ≤ 40 items (test asserts a count)
- [ ] `git status` limited to in-scope + generated files

## STOP conditions

- The CRD discovery endpoint does not expose API group/version/plural for served CRDs (only names) — STOP; a backend change is needed first.
- Fetching counts for CRD kinds requires one request per kind and the sidebar would issue > 15 — cap as specified; if product wants all, STOP and report the cost.
- Hiding Image Scans / Service Mesh by CRD presence would hide pages that read Astronomer-side data (not CRDs) — leave those ungated and note it.

## Maintenance notes

- New cluster nav rows must declare `ifHaveGroup`/`ifHaveKind` unless they read Astronomer-side APIs. Reviewers should ask.
- The friendly-name table will need entries as customers bring new operators; keep it alphabetical.
- Plan 019's kind-less Import YAML can now resolve CRD kinds via `useClusterDiscovery` (`createPathForManifest` fallback) — small follow-up.
