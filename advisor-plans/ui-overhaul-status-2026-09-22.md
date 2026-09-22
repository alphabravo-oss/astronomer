# UI overhaul — status and plan (2026-09-22)

Single-page status for the Rancher-parity UI overhaul. The audit is
[017](./017-ui-overhaul-review-2026-09-21.md); the execution plans are 018–025;
the index with per-plan status rows is [README.md](./README.md).

## 1. Where things stand

Seven branches, every one reviewed and approved. **Nothing is merged; `main` is
untouched at `59619920`.** The plan files themselves (017–025, this file, the
README changes) are still uncommitted in the main checkout.

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

## 2. How to merge

```bash
git checkout main
git merge --no-ff advisor/p0-ui-slice
git merge --no-ff advisor/021-design-system
git merge --no-ff advisor/018-navigation-model
git merge --no-ff advisor/019-global-shell
git merge --no-ff advisor/022-page-migration      # already contains 019
git merge --no-ff advisor/024-parity-quick-wins
git merge --no-ff advisor/023-forms-and-url-state  # conflicts only on the generated code-health doc
node scripts/code-health-inventory.mjs --write     # then re-run --check --verify-doc
node scripts/check-complexity-budget.mjs           # must pass; if it reports "below stale ceiling", --write-baseline and commit
cd frontend && npm run type-check && npm run lint && npm test
PLAYWRIGHT_PORT=3999 npx playwright test --project=route-smoke --project=route-smoke-mobile --workers=1
```

Dry-run merges done during review: 018 onto 021 clean; 024 onto 023 conflicts
only on `docs/rancher-quality-phase0-code-health-inventory.md` (generated).
Two migrations land (063, 064); `internal/db/schema_version.go` ends at 64.

Also commit the plan files: `git add advisor-plans && git commit -m "docs(advisor): UI overhaul review, plans 018–025, status"`.

## 3. What is still open

| Plan | Status | Needs |
|---|---|---|
| [020 — Explorer nav: discovery + CRD groups](./020-cluster-explorer-nav-discovery-and-crd-groups.md) | TODO — READY | `ifHaveGroup` gating, Gateway API folded into Service Discovery, dynamic "More Resources" CRD subgroups, starred types. Needs a `starred_types` migration (same recipe as 063/064 — noted in the plan). |
| [025 — Guided form depth + table parity](./025-guided-form-depth-and-table-parity.md) | TODO (after 024) | `ArrayField` for containers/rules/volumes/env/ports/paths/tolerations/affinity; group-by-namespace; bulk restart/scale; generic related resources; `questions.yaml` ADR spike. |

Follow-ups discovered during execution (all recorded in 017 §7):

- **Cross-cluster delivery lists do not exist.** `delivery/{deployments,rollouts,sources,bundles,targets}` are redirect wrappers, so the Continuous Delivery nav group lists only Estate / Templates / Overrides.
- **Rollout URLs are not project-agnostic.** The rollout GET requires `project_id` and the payload has none; deep links without `?project=` hit a "select a project" gate. API decision needed.
- **Clone / Download YAML** landed only on the workloads table; the five other row-action builders still lack them.
- **Native RBAC passthrough** has an API client and no UI (the only route was a dead redirect, now deleted).
- **Project members card** resolves names through the user-list helper; a project viewer without user-list permission may see a 403 (mirrors the RBAC page).
- Deliberately unconverted in 022: two collapsible `Section`s, four row-shaped tiles, six auth/markdown `<h1>` exemptions, the shared delivery button constants; button ratchet stopped at 196 (target was 154).
- Descoped from 023: the ESLint mutation rule became a counting ratchet (28 pre-existing sites); extension not-found renders inline rather than throwing; the registries list has no filter to persist.

## 4. Rules learned for anyone executing here

- `scripts/check-complexity-budget.mjs` requires an **exact** line match per baselined unit. Never let a unit grow (extract to a new file); when it shrinks, `--write-baseline`, confirm every number went down, commit separately. Never run blanket `prettier --write` on ceilinged files.
- `user_preferences` is an explicit-column table: a new preference needs a one-line `ADD COLUMN … NOT NULL DEFAULT … CHECK (…)` migration (down file carries the three contract headers), the query updated, `make sqlc-generate && make sqlc-check`, the schema-version bump, and handler mapping with a nil→default guard.
- `TabsList` is `role="tablist"`; navigation strips of links must be `<nav>` with `tabLinkClassName(active)`, never tabs.
- Importing a route's `Route.options.component` hangs under vitest; export page components and import by name.
- Adding a TanStack file route needs a vite build to regenerate `routeTree.gen.ts` and an `EXPECTED_ROUTE_COUNT` bump in `frontend/scripts/generate-route-manifest.mjs`.
- Playwright: use a dedicated `PLAYWRIGHT_PORT` per worktree and `--workers=1`; the default reuses another worktree's preview server and parallel runs are flaky on this host.
- `go build` in a worktree: `GOTMPDIR=<outside /tmp> go build -buildvcs=false ./...`.
- Fresh screenshots without a live server: `cd frontend && SMOKE_GALLERY=1 npx playwright test --project=route-smoke` → `frontend/gallery/*.png` (git-ignored; run the desktop project alone or mobile overwrites it).
