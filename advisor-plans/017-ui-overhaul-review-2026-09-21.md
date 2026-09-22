# 017 — UI overhaul review vs Rancher dashboard (2026-09-21)

**Audited at:** `59619920` on `main`, 2026-09-21. Clean tree.
**Benchmark:** `rancher/dashboard` at `708d60f` (cloned to `/root/astronomer-all/rancher-dashboard`, 2026-09-21).
**Scope:** `frontend/src` only — information architecture, visual consistency, Rancher functional parity, and UX/functional defects. Backend audited only where a UI gap needed an API check.
**Method:** four parallel read-only lanes, every headline finding re-verified by hand against the source, plus a fresh 136-route screenshot gallery rendered from the stubbed preview server (`SMOKE_GALLERY=1 npx playwright test --project=route-smoke`; output in `frontend/gallery/`, git-ignored).
**Plans:** 018–025 in this directory. Execute in the order in `README.md`.

---

## 1. Bottom line

The product does not need a rewrite. The token layer, the primitives, the namespace/project scope, the explorer, the YAML dry-run/diff flow, bulk delete, custom-role editing, principal search and server-side preferences all exist and are in several places ahead of Rancher. Of the 24 UI findings in the September 10 review, 21 are already fixed.

What still separates it from Rancher is **enforcement and shape**, not capability:

1. **Navigation is not Rancher-shaped.** No global cluster entry point (the switcher only mounts inside a cluster); a one-open accordion that collapses on every navigation and starves counts; two single-item groups; an "Administration" grab bag; the delivery estate is a dead end once you click into it; 8 destinations have three different names across nav, H1 and breadcrumb; every browser tab is titled the same; the command palette covers 7 of 18 global destinations; 7 built pages are reachable only by URL.
2. **The design system is optional.** `Card` has 2 adopters against 120 hand-rolled card recipes; 480 raw `<button>` elements bypass `ActionButton`; 52 of 129 index routes hand-roll their H1; 6 status-badge implementations; 10 hand-rolled tab bars; the shared `Tabs` primitive is the *least* accessible tab implementation in the repo; no `<th scope>` anywhere; the ratchet tests that exist guard one file each.
3. **Three high-consequence truth bugs.** The delivery overview renders API failure as "0 drifted, 0 rollouts, no warnings"; cluster Apps and anomaly baselines render errors as "nothing installed / no baselines"; the register-cluster wizard loses its draft id on refresh and then reports the operator's own draft name as taken.
4. **Parity gaps are mostly UI-only over finished APIs.** A complete Helm upgrade modal is never imported; branding and banner settings are saved and never rendered; Import YAML, kubeconfig download, group-by-namespace, clone/download YAML, and inline label editing are missing from the header/tables; guided forms edit only index `[0]` of every Kubernetes array.

### What is ahead of Rancher (do not regress)
Guided↔YAML toggle with server dry-run and line diff before save (`components/ui/yaml-view-dialog.tsx`, `yaml-apply-preview.tsx`); registry secrets with live test; per-object authorization and partial-failure tables on bulk delete; 15-minute audited direct kubeconfig; cluster badge/colour; principal search that reifies external identities; delivery approvals/maintenance windows/rollback; namespace scope that fails closed for restricted users.

---

## 2. Priority table

Leverage = impact ÷ effort, discounted by confidence and fix risk. All findings HIGH confidence unless noted. Plan column says where the fix lives.

| # | Finding | Category | Impact | Effort | Risk | Plan |
|---|---|---|---|---|---|---|
| 1 | Delivery overview renders API failure as all-clear zeros (`routes/dashboard/delivery/index.tsx:642,651,733-751,778`) | bug | critical | S | LOW | 023 |
| 2 | Apps / Recommended / Anomaly-baselines render errors as empty states (`clusters/$id/apps/index.tsx:714-728,1153-1162`; `clusters/$id/index.tsx:934-959`) | bug | high | S | LOW | 023 |
| 3 | Register wizard loses `draftClusterId` on refresh → Back exits, own name "taken" (`clusters/register/index.tsx:29,140,151`) | bug | high | S | LOW | 023 |
| 4 | Helm upgrade modal built, never imported (`catalog/-upgrade-chart-modal.tsx`; `-installed-tab.tsx:101`) | parity | high | S | LOW | 024 |
| 5 | Branding + banners saved, never rendered (`lib/api/public-settings.ts:31,35` 0 consumers; `sidebar.tsx:171`; `login/index.tsx:163,223`) | parity | high | M | LOW | 024 |
| 6 | No global cluster entry: switcher only inside a cluster; no pins/recents (`topbar.tsx:211`; `sidebar.tsx:199`) | IA | high | L | MED | 019 |
| 7 | Single-open sidebar accordion resets per navigation and gates counts to one group (`sidebar.tsx:120-141`; `use-sidebar-resource-counts.ts:57-93`) | IA | high | M | LOW | 018 |
| 8 | Delivery estate sub-pages have no layout/tab strip; nav group collapses on entry (`exact: true` at `sidebar-navigation.ts:196`; no `routes/dashboard/delivery/route.tsx`) | IA | high | M | LOW | 018 |
| 9 | Nav label ≠ H1 ≠ breadcrumb for 8 destinations; `fleet` label; slug mismatches (`lib/breadcrumbs.ts:18,41-43`; `settings/auth/index.tsx:158` "Identity Broker") | IA | med | M | LOW | 018 |
| 10 | `document.title` never set (0 hits; `index.html` static title) | IA | med | S | LOW | 018 |
| 11 | Command palette hardcodes 7 global + 12 cluster pages; drifts from nav (`command-palette.tsx:41-49,54-132`) | IA | med | S | LOW | 018 |
| 12 | 7 orphan routes (native-rbac, backup-drill, backups/* children, cluster network-policies dup, cluster workloads overview) | IA | med | M | LOW–MED | 018 |
| 13 | Identity split across RBAC / Settings→Auth / Settings→General "SSO Providers" tab / native-rbac | IA | med | L | MED | 018 |
| 14 | Four overlapping Helm surfaces (Catalog, Cluster Tools, cluster Tools, cluster Apps) | IA | med | L | MED | 018 (rule) |
| 15 | No unsaved-changes guard anywhere (0 `useBlocker`/`beforeunload`) | UX | high | M | LOW | 023 |
| 16 | 26/47 forms have no validators; 32/47 no `FormErrorSummary` | UX | med | M | LOW | 023 |
| 17 | `settings/widgets` 5 mutations with no `onError` (`widgets/index.tsx:145-170`) | bug | med | S | LOW | 023 |
| 18 | `history.back()` after delete / as Back on deep-linked detail (`resource-detail.tsx:189,228`; `admin/users/$id:153`) | bug | med | S | LOW | 023 |
| 19 | Apps section tab not in URL; CTA clicks `nav button:nth-of-type(2)` (`apps/index.tsx:229,749`) | bug | med | S | LOW | 023 |
| 20 | Extension sidebar links target non-existent `/dashboard/extensions/$name` (`ExtensionNavItems.tsx:19`) | bug | med | M | LOW | 023 |
| 21 | List search/filter state local-only on 8 lists; 3 routes bypass router with `replaceState` (`search/-page.tsx:140`) | UX | med | M | MED | 023 |
| 22 | Shared `Tabs` has no tab ARIA while 3 hand-rolled bars do (`ui/tabs.tsx:13-53` vs `resource-detail.tsx:233-258`) | a11y | med | S | LOW | 021 |
| 23 | No `<th scope="col">` on any table; sortable `<th>` not a button (`ui/table.tsx:54-63`; `data-table-semantic-view.tsx:124-136`) | a11y | med | S | LOW | 021 |
| 24 | No detail-page masthead primitive: 5 divergent mastheads | visual | high | M+L | MED | 021, 022 |
| 25 | `Card` 2 adopters vs 120 hand-rolled surfaces, 13 padding/radius combos | visual | high | M | LOW | 021, 022 |
| 26 | 480 raw `<button>` in 157 files; ratchet test guards 1 file | visual | high | L | MED | 022 |
| 27 | 6 status-badge implementations; `StatusBadge` uses `text-[10px]` not the `text-2xs` token | visual | med | M | LOW | 021 |
| 28 | 10 hand-rolled tab bars + a 7th tab system in `resource-detail-tabs.tsx` | visual | med | M | LOW–MED | 022 |
| 29 | 52/129 index routes hand-roll page header; 28 raw `<h1>` at 4 sizes | visual | med | M | LOW | 022 |
| 30 | 9 local `Field` copies; 12 local stat-tile components; 7 hand-rolled switch thumbs; 7 direct `ui/table` imports | visual | med | S–M | LOW | 021, 022 |
| 31 | 24/48 `EmptyState` with no action; 146 spinners vs 44 `QueryStates` routes; recharts hex in image-scans | visual | med | S | LOW | 021 |
| 32 | Create/edit split modal-vs-route inside backups and settings | visual | med | M | MED | 022 |
| 33 | Nav icon collisions (`Network` ×6); collapsed rail shows 57 unlabeled icons | IA/visual | med | S | LOW | 019, 021 |
| 34 | Cluster nav: 57 static items vs Rancher 31; Gateway API always shown; no discovery gating; "More Resources" static | IA | med | M–L | MED | 020 |
| 35 | No dynamic CRD groups / starred types in cluster nav | parity | high | L | MED | 020 |
| 36 | Guided forms edit only `[0]` of containers/rules/volumes/env/ports/paths (`guided-resource-model.ts:110`) | parity | high | L | MED | 025 |
| 37 | No header Import YAML (dialog exists, kind-bound) | parity | high | S | LOW | 019 |
| 38 | Kubeconfig download only on cluster overview; no copy | parity | med | S | LOW | 019 |
| 39 | No group-by-namespace in tables (0 `getGroupedRowModel`) | parity | med | M | MED | 025 |
| 40 | Bulk actions = delete only | parity | med | M | MED | 025 |
| 41 | No Clone / Download YAML row actions | parity | med | S | LOW | 024 |
| 42 | Labels/annotations read-only outside YAML editor | parity | med | S | LOW | 024 |
| 43 | Project members: count only, no manage | parity | med | M | LOW | 024 |
| 44 | Probe fields = HTTP path only (invalid without port) | parity | med | S | LOW | 024 |
| 45 | Secret create: no TLS/basic/ssh sub-forms | parity | low–med | S | LOW | 024 |
| 46 | Prefs lack rows-per-page, date format, pinned clusters | parity | med | S | LOW | 019, 024 |
| 47 | Home lacks provider column, "showing N of M", banner slot | parity | low–med | M | LOW | 024 |
| 48 | Tolerations/affinity YAML-only | parity | med | M | LOW | 025 |
| 49 | Chart install ignores `questions.yaml` (backend + UI) | parity | med | L | LOW | 025 (spike) |
| 50 | Related-resources tab hand-coded for 3 relationships | parity | med | M | LOW | 025 |
| 51 | Cert-expiry surface (needs agent collection) — MED confidence | parity | med | L | LOW | deferred |
| 52 | Longhorn/NeuVector post-install UI | parity | low | L | LOW | deferred |
| 53 | Oversized route files (nodes 1370, apps 1242, targets 1182, security 1160) with a live coupling bug (`nodeActionPending`) | debt | med | L | MED | 022, 023 |
| 54 | Zero tests on register / drain / uninstall / rollout-launch routes | tests | high | L | LOW | 023 (harness), rest deferred |

---

## 3. What the screenshots show (desktop, stubbed data)

- **Cluster-context topbar is over-full.** Breadcrumbs crush to `D… > ( > s. > D. > S.` on a resource detail because search, project scope, namespace scope, Shell, ⌘K, theme, bell and user menu all compete at 1280 px. Rancher puts the cluster in a badge on the left and drops breadcrumbs. → 019.
- **Two search entry points** (topbar "Search resources… /" and the ⌘K chip) on every page. → 019.
- **Node detail has no H1** (only a status pill and "Roles: Age: Never") and two buttons both labelled YAML. → 021/022 masthead.
- **Global Workloads page** uses a different toolbar style (monospace placeholders, "0 results from 0 clusters", a "Tip: hit Cmd+K" footer) than every other list. → 018 (retire/relabel as Search) and 022.
- **Settings › General** has an H1 of "Settings", a "General" tab inside the General section, and an "SSO Providers" tab duplicating Settings › Authentication; three navigation layers (sidebar › sub-nav › tabs) are visible at once. → 018.
- **Security** sidebar label is "Security Policies" but that is one of three tabs on a page titled "Security"; a large PSA explainer pushes the table below the fold. → 018 labels; 022 collapsible explainer.
- **Rollout detail** is fully blocked by "Select a project" even though the rollout id is in the URL. → 023.
- **Cluster overview** meta line mixes a status pill, a "created" pill, provider/version/environment text and an "Access: Admin" pill at four different visual weights; "Baseline Tools 0/0", "0 reports · last Never". → 021 masthead + 022.
- **Global nav** shows "Continuous Delivery" and "Security" as single-item accordions. → 018.
- **Cluster nav "Cluster" group** is 14 items before Workloads is reachable. → 020.
- **Density is now right** (1800 px container, tables use width) — the September finding is closed.

---

## 4. Direction (options, not ranked against bugs)

1. **One declarative navigation registry** feeding sidebar, palette, breadcrumbs, `document.title`, favorites and settings sub-nav (Rancher's `type-map.js` + `config/product/*.js` shape). 018 does the mechanical half (labels, palette derivation); a follow-up spike could make sub-navigation (left rail vs tab strip vs `?tab=`) one primitive. Trade-off: touches every route family; do incrementally behind the registry.
2. **Discovery-driven cluster nav.** Show only kinds the cluster serves, group in-use CRDs by API group, let users star types (Rancher "Starred" + "More Resources"). 020 plans it. Trade-off: one extra discovery call per cluster load; cache and gate like counts.
3. **Tool workspaces instead of per-vendor products.** For an installed tool (Longhorn, NeuVector, Istio) pin its CRD lists and an external deep link rather than building Rancher-style product modules. Cheap, grounded in the CRD explorer that exists. Not planned; record as the agreed direction if the user wants it.
4. **`questions.yaml` chart forms.** Largest apps-area delta; needs ingest-side storage. 025 lists it as a spike, not a build.

---

## 5. Considered and rejected

- **Duplicate workload detail page** — not present; `routes/dashboard/workloads/index.tsx` is a 20-line alias of the search page. Closed.
- **Raw `<form>` in 35 routes** — false positive; the matches are TanStack `<form.Field>`. Every route form uses `useAppForm` + `FormShell`.
- **Routes with `useQuery` and no error handling** — 0 at route level; the real defect is in sub-components (findings 1–2).
- **Modal focus trap** — `OverlayShell` already traps focus, restores it, honours `data-initial-focus`, closes on Escape.
- **Mutation feedback in general** — 45/57 mutation files have `onError`; only `settings/widgets` is silent.
- **Live/SSE coverage** — 41 consumers with `liveFallback` polling; node detail is intentionally refetch-driven with an `aria-live` region.
- **Project page titled "Project" / "Widgets: Missing path parameter id"** — stub fixture lacks `id`/`name`; not a product bug. A `project.id` guard before `renderForProject` is a one-liner folded into 023 step 2.
- **Rancher workspace switcher, `ConfigBadge`, `EtcdInfoBanner`** — provisioning-time concepts; out of scope by product boundary.
- **Provider brand tints in `provider-badge.tsx`** — defensible vendor colours; 021 adds `--brand-*` tokens rather than deleting them.
- **Hard-coded palette classes** — only 52 across 7 files; the token layer is healthy. Folded into 021 as an allowlisted lint rule.

## 6. Not audited

Backend handlers beyond API-existence checks; Charlie UI; extension sandbox runtime; dark-mode screenshots (gallery is light-only; tokens are theme-safe by construction); mobile beyond the September baselines; performance under 2,000 clusters (Plan 016 Phase 5 owns it).

## 7. Follow-ups discovered during execution (2026-09-21)

- **No cross-cluster delivery lists.** `routes/dashboard/delivery/{deployments,rollouts,sources,bundles,targets}/index.tsx` are `RedirectDeliveryList` wrappers (`components/delivery/shared.tsx:140-160`): with one resolvable cluster they redirect into that cluster's delivery workspace; with several and no `?project=` they bounce back to Estate. Plan 018 therefore lists only Estate, Templates and Overrides in the Continuous Delivery group. A future plan should build real estate-wide lists for those five kinds (Rancher: GitRepos / Clusters / Cluster Groups are real lists) or make the redirect carry project context.
- **Native RBAC passthrough has an API client and no UI.** `lib/api/native-rbac.ts` (per-CRD additive grants) has zero consumers; the only route was a redirect stub, now deleted. Candidate for Plan 024/025 scope or the RBAC page.
- **Executor gotchas** (recorded in `frontend/docs/design-system.md` by Plan 021): `scripts/check-complexity-budget.mjs` requires an exact line match per baselined unit; route components imported via `Route.options.component` hang under vitest.
- **Rollout URLs are not project-agnostic.** `GET /delivery/rollouts/{id}` requires `project_id` and the `DeliveryRollout` payload carries none, so a deep link to `/dashboard/delivery/rollouts/<id>` without `?project=` can only show a "select a project" gate. Fix belongs to the API (rollout-by-id lookup or `project_id` in the payload); Plan 023 descoped the UI seeding (2026-09-22).
- **Clone / Download YAML landed only on the workloads table.** The other five row-action builders (`generic-resource-table.tsx`, `resource-core-tables.tsx`, `resource-network-tables.tsx`, `resource-storage-tables.tsx`, `resource-gateway-tables.tsx`) still lack them — small follow-up once Plan 024 lands.
