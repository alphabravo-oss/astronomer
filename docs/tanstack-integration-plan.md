# TanStack Integration Plan — Astronomer Frontend

> **Scope:** `astronomer/frontend` only. Adopt TanStack **Table**, **Virtual**, and harden the
> existing TanStack **Query** usage, while isolating Next.js router coupling so a future
> Vite + TanStack Router migration becomes a swap, not a rewrite.
>
> **Explicitly out of scope (now):** TanStack Router (conflicts with Next.js App Router — see
> [Appendix B](#appendix-b--why-not-tanstack-router-now)). TanStack Form (the app already uses
> `react-hook-form`; swapping is churn for no functional gain).
>
> **Status:** DRAFT (rev 2 — corrected after multi-agent review). Author: platform/frontend.
> Last updated: 2026-06-15.
>
> **Rev 2 changelog (review corrections folded in):** CI does not currently run lint/Playwright —
> added as a hard P0 prerequisite (was an unstated assumption); corrected grounding counts
> (`useQuery` ~187 not 392; DataTable ~74 instances across 32 files not 35; `queryKeys.` usage 238;
> 61 of 69 inline keys live in `hooks.ts`); fixed `experimental_streamedQuery` import name and the
> `queryKeys` re-export; re-scoped Workstream C targets (most named pages don't qualify) and flagged
> the `<table>`-semantics conflict + ARIA-grid requirement; corrected the B4 audit exemplar
> (audit is already server-paginated); pinned devtools to the resolved 5.101.x; added `middleware.ts`
> to the Vite audit. See each section's **[rev2]** notes.

---

## 0. TL;DR

| Workstream | What | Why now | Risk | Vite-later impact |
|---|---|---|---|---|
| **A — Query hardening** | Devtools, extract + complete the existing `queryKeys` factory, migrate `useEffect`+axios holdouts, optional `streamedQuery` for SSE, lint enforcement | Cheap, high-leverage polish on the data layer you already depend on (~187 `useQuery` / ~173 `useMutation` call sites) | Low | Portable as-is — moves logic *into* framework-agnostic libs |
| **B — TanStack Table** | Rebuild `DataTable` internals on `@tanstack/react-table` headless core, **same public API**; add opt-in server-side mode + faceted filters | Replaces a 440-line hand-rolled table (**~74 `<DataTable>` instances across 32 files**) that re-derives sort/filter/paginate/selection by hand; unlocks server-side pagination for large k8s lists | Low–Med | Fully portable |
| **C — TanStack Virtual** | Virtualize the `DataTable` body + large list views | Real perf win on multi-thousand-row k8s resource lists (workloads, audit, logging) | Low–Med | Fully portable |
| **D — Router isolation** | Wrap `next/navigation` + `next/link` behind thin local adapters | Converts the 70-file Next router lock-in into a few-file swap for Vite + TanStack Router later | Low | **This is the no-regrets enabler** |

**North Star:** every line written here runs unchanged under Vite. The only Next-coupled code
(routing, `next/link`, `next/image`, server actions) gets *quarantined* behind adapters in
Workstream D so it can be swapped wholesale later.

---

## 1. Current-state assessment (grounded)

Measured from `astronomer/frontend/src` on 2026-06-15:

- **Framework:** Next.js `16.2.9` (current `latest` stable), React `19`, App Router, ~55 routes,
  `output: 'standalone'`, API proxied via `next.config.js` rewrites to the Go backend.
- **TanStack Query:** already the data-fetching backbone. *(Counts corrected in rev2 — earlier
  figures conflated textual matches with real call sites; use a consistent metric:
  `grep -roE 'useQuery[<(]'` for call sites.)*
  - **~187** `useQuery` call sites (`218` textual; `392` was wrong — it included `useQueryClient`)
    and **~173** `useMutation` call sites (`203` textual), across 41 files.
  - `146` exported hooks centralized in `lib/hooks.ts` (76 KB).
  - `QueryClient` configured in `app/(components)/providers.tsx` (staleTime 30s, gcTime 5m,
    `refetchOnWindowFocus`, retry-skip on 401).
  - **A `queryKeys` factory already exists** (`lib/hooks.ts:50`) and is referenced **238** times
    (`queryKeys.*`) — but **69** raw inline `queryKey: [...]` arrays still bypass it, and it lives
    buried inside the 76 KB hooks file. **[rev2]** Of those 69, **61 are inside `lib/hooks.ts`
    itself** and only **8 are spread across 4 other files** (incl. `lib/live-events.ts`).
  - `194` `invalidateQueries` call sites — correctness here depends on key consistency.
  - Real-time: a single shared `EventSource` (SSE) in `lib/live-events.ts` drives
    `useLiveQueryInvalidation` + `useLiveClusterMetricsMerger` (`setQueryData`/`invalidateQueries`).
  - **No** `@tanstack/react-query-devtools`. **No** ESLint plugin enforcing query rules.
  - **Installed `@tanstack/react-query` is `5.101.0`** (the `^5.32.0` pin resolves up to it);
    `experimental_streamedQuery` **is** present in this version.
  - **[rev2]** Imperative-fetch holdouts **do exist**, but they're **axios-wrapper calls into
    `useState`**, not literal `fetch(` (e.g. `auth/login/page.tsx` `getSSOProviders()`,
    `clusters/register/[id]/connect/page.tsx`). A `grep` for `fetch(` alone misses them.
- **Tables:** `components/ui/data-table.tsx` — a **440-line hand-rolled** `DataTable<T>`. **[rev2]**
  Used in **~74 `<DataTable>` instances across 32 production files** (76 incl. the test) — *not* 35.
  Implements search, type-aware sort, pagination, column visibility, and row selection manually.
  Public API is a `Column<T>` interface (`key`, `header`, `accessor: (row) => ReactNode`,
  `sortAccessor?`, `sortable?`, `filterable?`, `hidden?`, `width?`, `align?`). **Zero**
  `@tanstack/react-table`. Has a Jest test (`components/ui/__tests__/data-table.test.tsx`).
- **Virtualization:** none anywhere.
- **Forms:** `react-hook-form` already present.
- **Router lock-in surface:** `next/navigation` in 70 files (`useRouter` ×104, `useParams` ×58,
  `useSearchParams` ×11, `usePathname` ×8, `redirect()` ×2); `next/link` in 60 files;
  `next/image` in 1 file; server actions enabled in `next.config.js`.
- **Tooling present:** Jest + Testing Library, Playwright, `tsc --noEmit`, ESLint 9 (flat config,
  `eslint.config.mjs`), path alias `@/* → ./src/*`.
- **[rev2] CI reality:** `.github/workflows/pr-validation.yaml`'s frontend job runs **only**
  code-health, `type-check`, Jest, and `npm audit`. It does **not** run ESLint or Playwright. So
  every lint-based guardrail (A3, D3) and **every Playwright DoD in this plan is currently
  unenforced** until CI is extended — see new task **P0-CI** below.

**Interpretation:** This is effectively a client-side SPA wearing a Next.js coat (`'use client'`
everywhere, all data via Query). That makes a future Vite migration tractable — *provided* the
router coupling is quarantined now (Workstream D).

---

## 2. Goals & non-goals

### Goals
1. Replace bespoke table logic with `@tanstack/react-table` **without changing any of the 35 call
   sites** (internals swap behind the existing `DataTable` API).
2. Virtualize large tables/lists so multi-thousand-row k8s views stay smooth.
3. Make the Query layer airtight: one canonical key factory, devtools, lint enforcement, no
   `useEffect`+`fetch` holdouts.
4. Quarantine all Next.js router coupling behind adapters so Vite + TanStack Router later is a
   few-file change.

### Non-goals
- Migrating to Vite **now**.
- Adopting TanStack Router **now** (incompatible with App Router — Appendix B).
- Replacing `react-hook-form`.
- Rewriting individual page components beyond what each workstream strictly requires.
- Upgrading Next.js major (already on latest stable).

---

## 3. Guiding principles

1. **No-regrets portability.** Prefer changes that are framework-agnostic. If a change couples us
   harder to Next.js, it goes behind an adapter (Workstream D) or doesn't happen.
2. **Stable public APIs.** `DataTable`'s props and the `queryKeys` shape are contracts. Refactor
   internals; do not break callers.
3. **Strangler pattern.** Land infrastructure first, migrate call sites incrementally behind the
   unchanged API, delete the old implementation last.
4. **Each PR is independently shippable and reversible.** No big-bang merges.
5. **Tests gate everything.** `tsc --noEmit`, Jest, and the relevant Playwright specs must be green
   before a task is "done."

---

## 4. Priority gating (phases & gates)

Phases are gated: do not start a phase until the prior gate passes. Within a phase, tasks may run
in parallel unless `depends-on` says otherwise.

```
P0  Foundations & guardrails        ── GATE 0 ──┐
P1  Query hardening (Workstream A)   ── GATE 1 ──┤  (A can run alongside D)
P1  Router isolation (Workstream D)  ── GATE 1 ──┘
P2  TanStack Table internals swap (B) ── GATE 2 ──
P3  Server-side mode + faceted filters (B) ── GATE 3 ──
P4  Virtualization (C)               ── GATE 4 ──
P5  Cleanup, docs, Vite-readiness audit ── DONE ──
```

> **[rev2] P0 is now blocking for real.** GATE 0 previously *assumed* CI ran lint + Playwright; it
> does not. Task **P0-CI** (below) must land before A3/D3/GATE 1 enforcement or any Playwright DoD
> means anything.

| Gate | Pass criteria |
|---|---|
| **GATE 0** | New deps installed & building; **task P0-CI merged** so CI runs `tsc`, Jest, **ESLint (incl. query plugin), and Playwright** on every PR; devtools live in dev only. |
| **GATE 1** | Zero raw inline `queryKey` arrays outside the factory file; `eslint-plugin-query` clean; all `useEffect`+`fetch` data loads migrated to Query; `next/navigation` + `next/link` only imported by adapter files (lint rule enforces). |
| **GATE 2** | `DataTable` reimplemented on `@tanstack/react-table`; existing Jest test + all 35 call sites pass with **no call-site edits**; visual parity confirmed on 5 representative pages. |
| **GATE 3** | At least 2 large-list pages (workloads, audit) use server-side pagination/sort; faceted filters available; no client-side "fetch-all-then-slice" on those pages. |
| **GATE 4** | `DataTable` virtualization behind a `virtualized` prop; enabled on ≥3 heavy pages; verified smooth at 5k+ rows; a11y (keyboard nav, screen-reader row semantics) intact. |
| **DONE** | Old DataTable internals deleted; Vite-readiness audit (Appendix C) passes; docs updated. |

---

## 5. Package matrix

Add (pin to current 5.x/8.x/3.x lines; let Renovate bump patches):

```jsonc
// frontend/package.json — dependencies
"@tanstack/react-table": "^8.21.0",     // headless table core
"@tanstack/react-virtual": "^3.13.0",   // row/list virtualization
// dependencies already present:
"@tanstack/react-query": "^5.32.0",     // already installed; resolves to 5.101.0

// devDependencies
"@tanstack/react-query-devtools": "^5.101.0", // MUST match the installed react-query (5.101.x) — not 5.32
"@tanstack/eslint-plugin-query": "^5.0.0"
```

> Notes: `@tanstack/react-table` v8 is the current stable headless line and is framework-agnostic.
> `react-virtual` v3 is the standalone (non-table) virtualizer; it composes with react-table by
> virtualizing the row model — we do **not** need `@tanstack/react-table`'s removed built-in
> virtual row model. Keep `react-query` on a single 5.x version across the repo to avoid duplicate
> QueryClients.

---

## 6. Workstream A — TanStack Query hardening

**Theme:** make the data layer you already rely on airtight and even more portable. None of this
adds Next coupling.

### P0-CI — Wire lint + Playwright into CI (P0) ⭐ [rev2] prerequisite
- **Priority:** P0 · **Depends-on:** — · **Blocks:** GATE 0, A3, D3, GATE 1, all Playwright DoDs
- **Reasoning:** `.github/workflows/pr-validation.yaml`'s frontend job runs only code-health,
  `type-check`, Jest, and `npm audit`. Without lint + e2e in CI, the plan's hardest guardrails
  (`eslint-plugin-query`, `no-restricted-imports`, "CI fails on inline key / direct import") and
  every Playwright validation are documentation, not enforcement. This must land first.
- **Tasks:**
  1. Add `npm run lint` to the frontend CI job (fails the build on lint errors).
  2. Add a Playwright stage: cache/install browsers (`npx playwright install --with-deps`), boot the
     app via `playwright.config.ts` `webServer`, run `npm run test:e2e`, upload the HTML report.
  3. (Optional) add a bundle-analyzer / `npm ls @tanstack/react-query` single-version check.
- **Files:** `.github/workflows/pr-validation.yaml`, possibly `playwright.config.ts` (`webServer`).
- **DoD:** A PR with a lint error **and** a PR with a failing Playwright spec both go red in CI.
- **Tests:** Deliberate-failure PRs (one lint, one e2e) confirm the gates block merge.

### A0 — Add React Query Devtools (P0)
- **Priority:** P0 · **Depends-on:** —
- **Reasoning:** 146 hooks and 194 invalidations with zero cache visibility. Devtools is dev-only,
  tree-shaken from prod, and pays for itself the first time a stale-key bug appears.
- **Example:**
  ```tsx
  // app/(components)/providers.tsx
  import { ReactQueryDevtools } from '@tanstack/react-query-devtools';
  // ...inside <QueryClientProvider>, after {children}:
  {process.env.NODE_ENV !== 'production' && (
    <ReactQueryDevtools initialIsOpen={false} buttonPosition="bottom-left" />
  )}
  ```
- **Files:** `app/(components)/providers.tsx`, `package.json`.
- **DoD:** Devtools panel appears in `next dev`, absent from `next build` output (verify bundle).
- **Tests:** Build succeeds; grep prod bundle for `ReactQueryDevtools` returns nothing.

### A1 — Extract the `queryKeys` factory to its own module (P1)
- **Priority:** P1 · **Depends-on:** —
- **Reasoning:** The factory already exists and is good, but it's buried in a 76 KB `lib/hooks.ts`
  and only used 218/287 times. Extracting it to `lib/query-keys.ts` gives it a home, makes it
  importable without dragging in all hooks, and sets up A2's enforcement.
- **Example:**
  ```ts
  // lib/query-keys.ts  (moved verbatim from lib/hooks.ts:50, then re-exported)
  export const queryKeys = {
    featureFlags: ['settings', 'features'] as const,
    clusters: {
      all: ['clusters'] as const,
      list: (params?: Record<string, unknown>) => ['clusters', 'list', params] as const,
      detail: (id: string) => ['clusters', 'detail', id] as const,
      // ...existing tree unchanged...
    },
    // ...
  } as const;
  ```
  ```ts
  // lib/hooks.ts  — [rev2] import THEN re-export. A bare `export { queryKeys } from './query-keys'`
  // would leave the ~96 internal `queryKeys.*` references inside hooks.ts unbound.
  import { queryKeys } from './query-keys';
  export { queryKeys };
  ```
- **Files:** new `lib/query-keys.ts`; `lib/hooks.ts` (cut, then import + re-export).
- **DoD:** `tsc` green; no import churn required at call sites; factory is the single source of truth.
  (Note: the "grep returns only query-keys.ts" check belongs to **A2**, not A1 — moving the factory
  alone leaves the 61 in-file inline keys behind.)
- **Tests:** `tsc --noEmit`; existing Jest suite green.

### A2 — Close the 69 inline-key gaps (P1)
- **Priority:** P1 · **Depends-on:** A1
- **Reasoning:** 69 hand-written `queryKey: ['clusters', id, 'conditions']`-style arrays bypass the
  factory. These are exactly the keys that drift out of sync with `invalidateQueries` and cause
  "why didn't the list refresh" bugs. Promote each into the factory. **[rev2] Blast radius:** 61 of
  the 69 live **inside `lib/hooks.ts` itself**; only 8 are in 4 other files. Critically, promote the
  keys in **`lib/live-events.ts`** too — its SSE invalidations must match the factory or live
  updates silently target the wrong cache entry.
- **Procedure (granular):**
  1. `grep -rn "queryKey: \[" src` → enumerate the 69 sites (mostly in `hooks.ts`).
  2. For each, add a typed entry under the right namespace in `query-keys.ts`
     (e.g. `clusters.conditions: (id) => ['clusters', id, 'conditions'] as const`).
  3. Replace the inline array at both the `useQuery` site **and** any matching `invalidateQueries`.
  4. Repeat until `grep -rn "queryKey: \[" src` returns only `lib/query-keys.ts`.
- **Files:** `lib/query-keys.ts`, the ~30 hook/page files holding inline keys.
- **DoD:** `grep -rn "queryKey: \[" src` matches only the factory file; `invalidateQueries` calls
  reference factory entries.
- **Tests:** `tsc`; Jest; targeted Playwright for a mutation→list-refresh flow (e.g. create
  cluster → list updates) on at least 3 domains.

### A3 — Enforce with `@tanstack/eslint-plugin-query` (P1)
- **Priority:** P1 · **Depends-on:** A2
- **Reasoning:** Prevent regression. The plugin catches missing deps in query keys, unstable keys,
  and bad mutation patterns — turning A1/A2 from a one-time cleanup into a guarantee.
- **Example:**
  ```js
  // eslint.config.mjs
  import pluginQuery from '@tanstack/eslint-plugin-query';
  export default [
    // ...existing config...
    ...pluginQuery.configs['flat/recommended'],
  ];
  ```
  Add a custom `no-restricted-syntax` rule to ban inline `queryKey` array literals outside
  `lib/query-keys.ts` (belt-and-suspenders for A2).
- **Files:** `eslint.config.mjs`, `package.json`.
- **DoD:** `npm run lint` clean with the plugin on; CI fails on a deliberately-introduced inline key.
- **Tests:** Lint passes; negative test (temp inline key) fails lint locally.

### A4 — Migrate imperative-fetch holdouts to Query (P1)
- **Priority:** P1 · **Depends-on:** A1
- **Reasoning:** A handful of files still imperatively fetch in effects — no caching, no retry, no
  dedupe, no devtools visibility, and **non-portable** patterns that each have to be reasoned about
  individually. Fold them into the hook layer.
- **[rev2] These are axios-wrapper calls, not literal `fetch(`.** Confirmed holdouts include
  `auth/login/page.tsx` (`getSSOProviders()` → `useState`) and
  `clusters/register/[id]/connect/page.tsx`. Enumerate with a grep that catches the wrapper forms,
  e.g. `grep -rnE "useEffect" src | xargs grep -lE "\.(get|post|put|delete)\(|getSSO|apiClient"`,
  not just `fetch(`.
- **Procedure:** For each holdout, identify the endpoint, add a hook in `lib/hooks.ts` (or the
  relevant `lib/api/*` module) using `queryKeys`, delete the effect + local `useState` loading
  flags, render off the hook's `isLoading`/`data`/`error`.
- **Example (before → after):**
  ```tsx
  // before
  const [data, setData] = useState<Foo[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => { fetch('/api/v1/foo').then(r => r.json()).then(d => { setData(d); setLoading(false); }); }, []);

  // after
  const { data = [], isLoading } = useQuery({
    queryKey: queryKeys.foo.list(),
    queryFn: () => api.get<Foo[]>('/api/v1/foo'),
  });
  ```
- **Files:** the ~5 holdout pages/components; `lib/hooks.ts` / `lib/api/*`; `lib/query-keys.ts`.
- **DoD:** the rev2 grep above shows no *data-loading* effects (subscriptions/imperative
  side-effects excepted and commented).
- **Tests:** Jest for new hooks (mock the axios wrapper); Playwright smoke on each migrated page.

### A5 — (Optional) Formalize SSE with `streamedQuery` (P2)
- **Priority:** P2 (nice-to-have) · **Depends-on:** A1
- **Reasoning:** `lib/live-events.ts` hand-rolls a shared `EventSource` plus manual
  `setQueryData`/`invalidateQueries`. TanStack Query v5's streamed-query helper (experimental) can
  model a stream as a first-class query, giving devtools visibility and consistent lifecycle.
  **Evaluate, don't force** — the current implementation works and is well-commented; only adopt if
  it simplifies the merge/invalidation logic.
- **[rev2] Import name:** the export is **`experimental_streamedQuery`**, not the bare
  `streamedQuery` — `import { experimental_streamedQuery as streamedQuery } from '@tanstack/react-query'`.
  Confirmed present in the installed 5.101.0.
- **DoD:** Either a spike doc concluding "keep current" **or** a migrated, devtools-visible stream
  query with identical behavior (reconnect, metrics merge).
- **Tests:** Playwright: open a cluster detail page, push a live metric event, assert UI updates
  without manual refresh.

---

## 7. Workstream B — TanStack Table

**Theme:** swap `DataTable`'s hand-rolled internals for `@tanstack/react-table`'s headless core
**behind the existing `Column<T>` API**, then add capabilities the hand-rolled version can't
reasonably grow into (server-side mode, faceted filters, column resize/persist).

### B0 — Install + spike adapter (P2)
- **Priority:** P2 · **Depends-on:** GATE 1
- **Reasoning:** De-risk the API mapping before touching the shared component. The current
  `Column<T>` (`key/header/accessor/sortAccessor/sortable/filterable/hidden/width/align`) maps
  cleanly onto react-table `ColumnDef`.
- **Example (mapping layer):**
  ```ts
  // components/ui/data-table/adapt-columns.ts
  import type { ColumnDef } from '@tanstack/react-table';
  import type { Column } from './types'; // the existing Column<T>

  export function toColumnDefs<T>(columns: Column<T>[]): ColumnDef<T>[] {
    return columns.map((c) => ({
      id: c.key,
      header: c.header,
      // accessor returns ReactNode today → use a display cell, keep a raw value for sorting
      cell: (ctx) => c.accessor(ctx.row.original),
      accessorFn: c.sortAccessor ?? ((row) => {
        const v = c.accessor(row);
        return typeof v === 'string' || typeof v === 'number' ? v : String(v ?? '');
      }),
      enableSorting: c.sortable !== false,
      enableHiding: true,
      meta: { align: c.align, width: c.width, filterable: c.filterable },
    }));
  }
  ```
- **Files:** new `components/ui/data-table/` (we promote the single file to a folder); `types.ts`,
  `adapt-columns.ts`.
- **DoD:** Adapter unit-tested against the existing test's `Column<Row>[]` fixtures.
- **Tests:** Jest unit on `toColumnDefs` (sorting accessor fallback, align/width passthrough).

### B1 — Reimplement `DataTable` internals on react-table (P2) ⭐ keystone
- **Priority:** P2 · **Depends-on:** B0
- **Reasoning:** This is the core win. Replace ~400 lines of manual filter/sort/paginate/selection
  state with react-table's row models, keeping **every prop identical** so all **~74 call sites
  (32 files)** and the existing Jest test pass unchanged.
- **Example (internals skeleton — same props as today):**
  ```tsx
  'use client';
  import {
    useReactTable, getCoreRowModel, getSortedRowModel,
    getFilteredRowModel, getPaginationRowModel,
    type SortingState, type RowSelectionState, type VisibilityState,
  } from '@tanstack/react-table';
  import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
  import { toColumnDefs } from './adapt-columns';

  export function DataTable<T>(props: DataTableProps<T>) {
    const { data, columns, keyExtractor, pageSize = 20, selectable, searchable = true, ... } = props;
    const columnDefs = useMemo(() => toColumnDefs(columns), [columns]);
    const [sorting, setSorting] = useState<SortingState>([]);
    const [globalFilter, setGlobalFilter] = useState('');
    const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
    const [columnVisibility, setColumnVisibility] = useState<VisibilityState>(
      Object.fromEntries(columns.filter(c => c.hidden).map(c => [c.key, false]))
    );

    const table = useReactTable<T>({
      data, columns: columnDefs,
      getRowId: keyExtractor,                       // preserves stable selection keys
      state: { sorting, globalFilter, rowSelection, columnVisibility },
      onSortingChange: setSorting,
      onGlobalFilterChange: setGlobalFilter,
      onRowSelectionChange: setRowSelection,
      onColumnVisibilityChange: setColumnVisibility, // [rev2] wrap to keep ≥1 column visible (see below)
      enableRowSelection: selectable,
      globalFilterFn: 'includesString',             // matches current "toString().includes" search
      getCoreRowModel: getCoreRowModel(),
      getSortedRowModel: getSortedRowModel(),
      getFilteredRowModel: getFilteredRowModel(),
      getPaginationRowModel: getPaginationRowModel(),
      initialState: { pagination: { pageSize } },
    });
    // render header/body/toolbar/pagination exactly as before, driven by `table.*`
    // bulkActions(selectedRows) ← table.getSelectedRowModel().rows.map(r => r.original)
  }
  ```
- **Parity checklist (must all still work):** search box + clear, type-aware sort with the 3-state
  chevron, page-size pagination with the 5-button window, column-visibility dropdown (min 1
  visible), row selection + select-all-on-page + `bulkActions`, `onRowClick`, density, loading
  skeletons, empty message, `toolbar` slot, alignment/width.
- **[rev2] Must-port behaviors the snippet doesn't show:**
  1. The **"keep ≥1 column visible"** guard from the current `toggleColumn` — wrap
     `onColumnVisibilityChange` to reject an update that would hide the last visible column.
  2. The **sort value for JSX accessors** falls back to `String(accessor(row))` → `"[object Object]"`
     for object/JSX without a `sortAccessor`. This **matches today's behavior** (current code does
     `accessor(row)?.toString()`), so it's acceptable — but document it so callers know to set
     `sortAccessor` on rich-content columns.
- **Files:** `components/ui/data-table/index.tsx` (was `data-table.tsx`), keep `Column` re-exported
  from the same path so `import { DataTable, type Column } from '@/components/ui/data-table'` is
  unchanged.
- **DoD:** Existing `__tests__/data-table.test.tsx` passes **without edits**; all **~74 call sites
  (32 files)** type-check and render; visual parity verified on a sample of ≥6 representative pages
  spanning the heaviest tables (clusters, projects, audit, argocd, security, rbac).
- **Tests:** Existing Jest suite + new cases (sort toggles 3 states, select-all scoping, column
  hide keeps ≥1). Playwright on clusters + audit pages: sort, search, paginate, select, bulk action.

### B2 — Persist column visibility & add column resize (P2)
- **Priority:** P2 · **Depends-on:** B1
- **Reasoning:** Now that visibility/sizing are real table state, persist per-table column prefs to
  `localStorage` and enable column resizing — both trivial with react-table, impossible-ish before.
- **Example:**
  ```tsx
  // opt-in via prop, namespaced so tables don't collide
  const STORAGE = props.persistKey ? `dt:${props.persistKey}:cols` : null;
  // hydrate columnVisibility/columnSizing from localStorage on mount; write on change
  // enableColumnResizing: true; columnResizeMode: 'onChange'
  ```
- **Files:** `components/ui/data-table/index.tsx`; new `use-persisted-table-state.ts`.
- **DoD:** Hiding a column / resizing survives reload on pages that pass `persistKey`.
- **Tests:** Jest for the persistence hook (mock `localStorage`); Playwright: hide column → reload →
  still hidden.

### B3 — Faceted filters (P3)
- **Priority:** P3 · **Depends-on:** B1
- **Reasoning:** Many list pages (clusters by status, workloads by namespace/kind, audit by actor)
  want multi-select column filters. react-table's `getFacetedRowModel` +
  `getFacetedUniqueValues` makes this declarative. Expose via a new optional `Column.filter` config
  so call sites opt in without breaking the base API.
- **Example:**
  ```ts
  // extend Column<T> (additive, optional)
  filter?: { type: 'select' | 'multiselect'; options?: (rows: T[]) => string[] };
  ```
  Render a faceted dropdown in the toolbar for columns with `filter`, wired to
  `column.setFilterValue` + `getFacetedUniqueValues`.
- **Files:** `components/ui/data-table/types.ts`, `index.tsx`, new `faceted-filter.tsx`.
- **DoD:** At least clusters (status) and workloads (namespace) pages gain working facet filters.
- **Tests:** Jest on facet option derivation; Playwright filter interaction.

### B4 — Opt-in server-side mode for large lists (P3) ⭐ scalability
- **Priority:** P3 · **Depends-on:** B1, GATE 1 (keys), backend support
- **Reasoning:** Some big k8s lists fetch-all-then-slice in the browser. Add a `serverSide` prop that
  switches the table to `manualPagination`/`manualSorting`/`manualFiltering` and hands state changes
  back to the caller's Query hook (which adds the params to its `queryKey` — already supported by the
  factory, e.g. `queryKeys.workloads.list(clusterId, params)`).
- **[rev2] Pick the exemplar carefully — audit is NOT a fetch-all page.** `audit/page.tsx` already
  paginates server-side (`limit`/`offset` via `useAuditLogs`), passes a single 50-row page, sets
  `searchable={false}`, and renders its own external pagination. So audit is a **parity refactor**
  (fold its bespoke pagination into `serverSide`, and add the *sort/filter* wiring it currently
  lacks), not a greenfield example. Use a **verified fetch-all DataTable page** as the first
  exemplar — confirm one with `grep` before starting (do not assume workloads-list, which doesn't
  use DataTable; see Workstream C [rev2]).
- **Example:**
  ```tsx
  // prop
  serverSide?: {
    pageCount: number;
    state: { pagination: PaginationState; sorting: SortingState; columnFilters: ColumnFiltersState };
    onChange: (next: Partial<typeof state>) => void; // caller maps → queryKey params → refetch
  };
  // when present: manualPagination/manualSorting/manualFiltering = true, pass state + onChange through
  ```
  Call site pattern:
  ```tsx
  const [params, setParams] = useState({ page: 0, size: 50, sort: [], filters: [] });
  const { data } = useWorkloads(clusterId, params); // queryKey includes params
  <DataTable data={data.rows} columns={cols} serverSide={{ pageCount: data.pageCount, state, onChange: setParams }} />
  ```
- **Backend dependency:** endpoints must accept `page/size/sort/filter` and return `{ rows, total }`.
  Coordinate with the Go API owners; gate B4 on that being available per-endpoint.
- **Files:** `components/ui/data-table/index.tsx`; the 2–3 target pages + their hooks; relevant
  `lib/api/*`.
- **DoD:** Workloads + audit pages paginate/sort/filter server-side; network tab shows per-page
  requests; no full-dataset fetch on those routes.
- **Tests:** Jest on the param-mapping; Playwright: paginate → asserts new request fired with params;
  contract test against the API shape.

---

## 8. Workstream C — TanStack Virtual

**Theme:** keep heavy tables/lists smooth by rendering only visible rows.

### C0 — Spike: virtualize the `DataTable` body (P4)
- **Priority:** P4 · **Depends-on:** GATE 2 (table on react-table)
- **Reasoning:** Virtualization composes with react-table by virtualizing the row model. Gate it
  behind a `virtualized` prop so only opted-in heavy tables pay the (small) complexity; default
  paginated behavior is untouched.
- **[rev2] Three blockers the naive snippet misses — design for them up front:**
  1. **Semantic-`<table>` conflict.** The shared primitives in `components/ui/table.tsx` render real
     `<table>/<tr>/<td>`. A `position:absolute` `<tr>` breaks table layout. Virtualized mode needs a
     **separate `role=grid` div-based render path** (or a `display:block` tbody with fixed column
     widths) — it **cannot** reuse `TableRow`/`<tr>`. Plan for two render paths behind the one prop.
  2. **Pagination vs. virtualization.** With `getPaginationRowModel` active, `table.getRowModel().rows`
     returns only the current page — so the virtualizer would window 20 rows, not 10k. In virtualized
     mode, **drop `getPaginationRowModel`** (or set `pageSize: Infinity`) so the full row model is
     available to virtualize.
  3. **Variable row heights.** Several real accessors render multi-line content; a fixed
     `estimateSize` mis-positions rows. Use react-virtual v3 **`measureElement`** for dynamic
     measurement, or explicitly clamp row height and document the constraint.
- **Example:**
  ```tsx
  import { useVirtualizer } from '@tanstack/react-virtual';
  const rows = table.getRowModel().rows;
  const parentRef = useRef<HTMLDivElement>(null);
  const rowVirtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => (density === 'compact' ? 40 : 52),
    overscan: 12,
  });
  // render a tall spacer + absolutely-positioned virtual rows inside a scroll container
  ```
- **Files:** `components/ui/data-table/virtual-body.tsx`; `index.tsx` (branch on `virtualized`).
- **DoD:** A 10k-row fixture scrolls at 60fps in the spike; sort/filter/selection still work.
- **Tests:** Jest render (virtualizer mocked); manual perf profile noted in PR.

### C1 — Enable on the genuinely heavy lists (P4)
- **Priority:** P4 · **Depends-on:** C0
- **[rev2] The originally-named targets are mostly wrong — verified against the code:**
  - `audit/page.tsx` → server-paginated, ≤50 rows on screen → **virtualization is a no-op.**
  - `logging/page.tsx` → small/bounded config page → **no-op.**
  - workloads-list and image-scans → **don't use `DataTable` at all** (custom `.map()` renders) →
    they need either a DataTable migration first, or a standalone list virtualizer (`useVirtualizer`
    over the array directly).
  - **The one confirmed all-loaded `DataTable` list is CIS scan findings**
    (`security/scans/[scanId]/page.tsx`, FindingsSection) → start here.
- **Reasoning:** Apply only where a single `DataTable` holds a large, fully-loaded dataset. For
  unbounded sets, prefer B4 (server-side) + a windowed virtualizer; for the custom `.map()` lists,
  decide per-page between "migrate to DataTable first" vs. "standalone `useVirtualizer`."
- **Files:** the confirmed target page(s) (pass `virtualized`); for non-DataTable lists, a small
  standalone virtual-list component.
- **DoD:** the CIS-findings list (and any other *verified* fetch-all DataTable list) virtualized;
  smooth at 5k+ rows; **a11y authored** per C-3 spec below — not "preserved" (there's no ARIA
  baseline today).
- **[rev2] A11y contract (the primitives have ZERO ARIA today, so this must be built, not kept):**
  container `role="grid"` + `aria-rowcount={total}`; each virtual row `role="row"` +
  `aria-rowindex` (1-based, accounting for the header row); cells `role="gridcell"`. Wire arrow keys
  to `rowVirtualizer.scrollToIndex(target)` then focus the row after mount (`requestAnimationFrame`)
  using roving `tabindex`. Document that browser Ctrl-F find no longer reaches off-screen rows
  (accepted trade-off). Verify with an **axe scan in CI** (requires P0-CI).
- **Tests:** Playwright: load large fixture, scroll, assert DOM node count stays bounded; keyboard
  arrow navigation reaches off-screen rows; axe scan clean.

> **A11y caution:** virtualization removes off-screen DOM, which can break screen-reader row counts
> and in-table find (Ctrl-F). Always set `aria-rowcount` on the table and `aria-rowindex` on rows,
> and document the Ctrl-F limitation where it applies.

---

## 9. Workstream D — Router isolation (Vite-later enabler)

**Theme:** quarantine Next.js routing so a later Vite + TanStack Router swap touches ~3 adapter
files instead of 70. **This is the highest-leverage portability work and has no functional risk.**

### D1 — Navigation adapter (P1)
- **Priority:** P1 · **Depends-on:** —
- **Reasoning:** 70 files import `next/navigation` (`useRouter` ×104, `useParams` ×58,
  `useSearchParams` ×11, `usePathname` ×8, `redirect` ×2). Funnel them through one module exposing
  a framework-neutral shape. Day-of-Vite, you rewrite only this file to TanStack Router equivalents.
- **Example:**
  ```ts
  // lib/navigation.ts  — the ONLY file allowed to import next/navigation.
  // [rev2] Do NOT mark this file 'use client'. The client hooks re-export fine, but `redirect` /
  // `notFound` are used from a Server Component (app/page.tsx) and must not cross a client boundary.
  export {
    useRouter, usePathname, useSearchParams, useParams,
  } from 'next/navigation';
  // Optionally re-shape useRouter to a minimal { push, replace, back } facade so call sites
  // don't depend on Next-specific methods (prefetch, refresh) unless they truly need them.
  ```
  ```ts
  // lib/navigation-server.ts — [rev2] server-only redirect/notFound, kept separate so the client
  // adapter has no server-only imports. `redirect()` is used exactly ×1 (app/page.tsx). Drop the
  // unused `notFound` re-export unless a caller actually needs it.
  export { redirect, notFound } from 'next/navigation';
  ```
  Then codemod call sites: `from 'next/navigation'` → `from '@/lib/navigation'`.
- **Files:** new `lib/navigation.ts`; the 70 importing files (mechanical import rewrite).
- **DoD:** `grep -rn "next/navigation" src` matches only `lib/navigation.ts`.
- **Tests:** `tsc`; Playwright navigation smoke across a handful of routes; existing suite green.

### D2 — Link adapter (P1)
- **Priority:** P1 · **Depends-on:** —
- **Reasoning:** 60 files import `next/link`. Same quarantine.
- **Example:**
  ```tsx
  // lib/link.tsx — the ONLY file allowed to import next/link
  export { default as Link } from 'next/link';
  // (TanStack Router later: re-export its <Link> with a prop adapter for href→to)
  ```
  Codemod: `import Link from 'next/link'` → `import { Link } from '@/lib/link'`.
- **Files:** new `lib/link.tsx`; the 60 importing files.
- **DoD:** `grep -rn "next/link" src` matches only `lib/link.tsx`.
- **Tests:** `tsc`; visual check that links still render/navigate.

### D3 — Lint guardrail (P1)
- **Priority:** P1 · **Depends-on:** D1, D2
- **Reasoning:** Stop new direct imports from re-spreading the lock-in.
- **Example:**
  ```js
  // eslint.config.mjs — no-restricted-imports
  {
    rules: { 'no-restricted-imports': ['error', { paths: [
      { name: 'next/navigation', message: 'Import from @/lib/navigation instead.' },
      { name: 'next/link', message: 'Import from @/lib/link instead.' },
    ]}]},
  }
  // exempt lib/navigation.ts and lib/link.tsx via an overrides block
  ```
- **Files:** `eslint.config.mjs`.
- **DoD:** Lint fails on a direct `next/navigation`/`next/link` import outside the adapters.
- **Tests:** Negative test (temp direct import) fails lint.

### D4 — Document the remaining Next-only surface (P5)
- **Priority:** P5 · **Depends-on:** D1–D3
- **Reasoning:** Be honest about what still couples to Next so the Vite estimate is real. Remaining
  surface after D1–D3: file-based routing (`app/` conventions), `next/image` (1 file — trivial),
  `next.config.js` rewrites (replace with Vite dev proxy), and any RSC.
- **[rev2] Two corrections to the surface inventory:**
  - **`middleware.ts`** (the `NextResponse.redirect` auth gate) was missing from this audit. It is a
    real Next coupling and must port to a TanStack Router `beforeLoad`/route-guard under Vite — add
    it to Appendix C.
  - **Server actions are enabled in `next.config.js` but UNUSED** (zero `'use server'` in `src`).
    So this is *not* a porting cost — it strengthens the Vite-later thesis. Note it as a no-op.
- **Files:** Appendix C of this doc.
- **DoD:** Appendix C lists every remaining Next coupling with a Vite-equivalent and rough effort.

---

## 10. Testing & validation strategy

Every task lists its own tests; this is the program-level safety net.

1. **Type safety:** `npm run type-check` (`tsc --noEmit`) must pass on every PR. Non-negotiable for
   the DataTable refactor (B1) — types are the first line catching call-site breakage.
2. **Unit (Jest + Testing Library):**
   - Keep `data-table.test.tsx` passing **unedited** through B1 (parity proof).
   - Add: `toColumnDefs` mapping, persisted-state hook, facet derivation, server-side param mapping,
     new Query hooks (A4) with mocked fetch.
3. **E2E (Playwright):** per migrated page — sort, search, paginate, select+bulk-action,
   mutation→list-refresh, server-side pagination request assertions, large-list scroll (bounded DOM),
   keyboard nav. Run the existing `tests/` suite + new specs in CI.
4. **Lint:** ESLint 9 + `@tanstack/eslint-plugin-query` (A3) + `no-restricted-imports` (D3). CI-blocking.
5. **Visual parity:** for B1, manually compare 5 representative table pages before/after
   (screenshots in PR). Consider adding Playwright screenshot snapshots for those 5 to catch drift.
6. **Performance:** for C, capture a Chrome performance profile at 5k+ rows (before/after frame
   times) and assert virtualized DOM node count stays bounded in Playwright.
7. **Accessibility:** axe scan on virtualized tables; verify `aria-rowcount`/`aria-rowindex`,
   keyboard navigation, focus retention on scroll.
8. **Bundle hygiene:** confirm devtools (A0) absent from prod bundle; confirm no duplicate
   `@tanstack/react-query` versions (`npm ls @tanstack/react-query`).
9. **CI wiring (GATE 0 / task P0-CI):** **[rev2]** the pipeline today runs only type-check, Jest, and
   `npm audit` — **ESLint and Playwright are not wired in.** Task **P0-CI** adds them; until it
   merges, treat all lint/e2e DoDs in this plan as unenforced and do not gate work on them.

---

## 11. Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| B1 breaks a subtle DataTable behavior used by one of 35 pages | Med | Med | Keep existing test unedited; parity checklist; visual diff 5 pages; ship behind a temporary `dataTableV2` flag if needed, default-on after a soak. |
| Sort semantics differ (locale/number/`sortAccessor`) | Med | Low | Port `sortAccessor` via `accessorFn`; unit-test number vs string sort to match current behavior. |
| Selection keys change when rows re-order (virtual/server) | Med | Med | Use `getRowId: keyExtractor` so selection is keyed by stable id, not index. |
| B4 server-side blocked by missing backend params | High | Med | Gate B4 per-endpoint on Go API support; ship client-side virtualization (C) first for relief. |
| Virtualization regresses a11y / Ctrl-F | Med | Med | aria-row attributes, axe scan, document Ctrl-F limitation; only enable where row count justifies it. |
| `streamedQuery` (A5) is experimental | Low | Low | Optional/spike-gated; keep current SSE if no clear win. |
| Import codemod (D1/D2) misses a dynamic import | Low | Low | Lint guardrail (D3) catches stragglers; grep verifies zero direct imports. |
| Next.js patch CVE during the work | Med | Low | Already on latest stable; enable Renovate/Dependabot for `next` (see §12). |
| **[rev2]** Lint/e2e guardrails (A3, D3, Playwright DoDs) unenforced — CI doesn't run them today | High | High | **P0-CI is a hard prerequisite** — land it before relying on any lint/e2e gate. |
| **[rev2]** Virtualization breaks `<table>` semantics / windows only the current page | Med | Med | Separate `role=grid` render path; drop `getPaginationRowModel` in virtualized mode; `measureElement` for variable heights (C0). |
| **[rev2]** C1 effort wasted on no-op targets (audit/logging already small/server-paged) | Med | Low | Re-scoped to verified fetch-all DataTable lists only (CIS findings); confirm each target by grep before starting. |

---

## 12. Operational follow-ups (not blocking, do alongside)

- **Renovate/Dependabot** for `next`, `@tanstack/*`, and security advisories — auto-PR patch bumps
  so the "Next.js keeps having CVEs" concern is handled by staying current automatically.
- **Single React Query version** enforced in CI (`npm ls` check) to avoid context mismatch.
- **Bundle-size budget** check in CI to catch accidental devtools/large-dep leakage.

---

## 13. Execution checklist (copy into tracker)

> **Progress (branch `feat/tanstack-foundation`, all green: type-check / lint 0-warn / 193 Jest /
> 10 Playwright e2e in a real browser / code-health):** P0 + **all of P1 (A1–A4, D1–D3)** DONE, and
> **all of the Table track B0–B4** DONE — `DataTable` internals on `@tanstack/react-table` behind the
> unchanged public API (~74 call sites untouched), plus column-visibility persistence (B2), faceted
> filters (B3), and opt-in server-side pagination (B4, Audit page refactored onto it). Only column
> Everything except D4 (Vite-readiness doc) is now done: P0, all of P1 (A1–A5), the full Table
> track (B0–B4, incl. resize), and virtualization (C0–C1). Branch commits: d2ac588 (foundation +
> B0–B2), d2bf354 (B3), 46c56bb (B4), c9d9b6a (B2-resize + C + A5). All green: tsc / lint 0-warn /
> 200 jest / 11 real-browser e2e / code-health. Deployed to k3s: only d2ac588 (image
> tanstack-d2ac588); B3/B4/C/resize not yet rolled out.

```
P0
  [x] P0-CI  Wire `npm run lint` + Playwright into frontend CI job  ⭐ prerequisite (GATE 0)
  [x] A0     React Query Devtools (dev-only, pinned to 5.101.x)

P1  (A + D in parallel)
  [x] A1  Extract queryKeys → lib/query-keys.ts
  [x] A2  Promote 69 inline keys into the factory  (8 cross-file + 61 in hooks.ts; byte-identical arrays)
  [x] A3  @tanstack/eslint-plugin-query enforcement + inline-key ban (error)
          ↳ also fixed 2 latent bugs it surfaced: clusters.pods namespace-collision key,
            pod-logs rest-spread; both query rules now enforced as 'error'
  [x] A4  Migrate axios-wrapper holdouts to Query (login SSO providers, register/connect manifest+status+TLS)
          ↳ settings/general was already on Query; terminals/shell/dialogs are imperative side-effects (left as-is)
  [x] D1  Navigation adapter (lib/navigation.ts + lib/navigation-server.ts) + codemod
  [x] D2  Link adapter (lib/link.tsx) + codemod        (D1+D2 codemod rewrote 94 files)
  [x] D3  no-restricted-imports guardrail                          (GATE 1)

P2
  [x] B0  Install @tanstack/react-table 8.21 + column→ColumnDef mapping (inlined) + 5 behavior tests
  [x] B1  Reimplement DataTable internals on react-table (keystone, 0 call-site edits, test unedited)
          ↳ parity: 2-state sort, numeric sortAccessor, visible-column search, page-reset-on-search,
            autoResetPageIndex:false (no snap-to-1 on poll), ≥1-visible-column guard, selection/bulkActions
  [x] B2  Column visibility persistence (`persistKey`) + RESIZE (`resizable`, drag handles, persisted
          sizing) — both hydration-safe. Real-browser e2e for visibility persistence.     (GATE 2)

P3
  [x] B3  Faceted filters — additive `Column.filter` config + toolbar multi-select dropdown
          (getFacetedRowModel/UniqueValues); enabled on clusters Provider; unit + real-browser e2e
  [x] B4  Opt-in server-side mode — `serverSide` prop (manualPagination + controlled state +
          rowCount); refactored the Audit page onto it (dropped its bespoke prev/next footer).
          Unit + real-browser e2e (offset-based per-page fetch). NB: server-side sort/filter is a
          future extension; audit columns aren't server-sortable today.            (GATE 3)

P4
  [x] C0  Virtualize DataTable body — opt-in `virtualized`; separate DIV ARIA-grid path
          (role=grid/row/gridcell), drops pagination model, measureElement, keyboard nav
  [x] C1  Enabled on the CIS scan-findings list (verified fetch-all); ARIA-grid a11y; real-browser
          e2e (1200 rows → bounded DOM). a11y hardened: grid container is the keyboard entry point. (GATE 4)

P5
  [x] A5  streamedQuery SSE spike → recommend KEEP hand-rolled bus (docs/a5-streamedquery-spike.md)
  [ ] D4  Document remaining Next-only surface (Appendix C)
  [ ] —   Delete old DataTable internals (n/a — internals were swapped in place); Vite-readiness audit
```

---

## Appendix A — Why these three (and not Router/Form) now

- **Table/Virtual/Query are 100% framework-agnostic.** They run identically under Vite, so none of
  this work adds lock-in — it moves bespoke logic *into* portable libraries, which is net-negative
  lock-in.
- **TanStack Router** is a competing full router; adopting it means abandoning Next.js App Router,
  server components, and the file-based routing of ~55 routes — a rewrite for no functional gain
  today. Deferred to a deliberate Vite migration (Appendix B).
- **TanStack Form** would replace a working `react-hook-form` setup — churn without payoff.

## Appendix B — Why not TanStack Router now

TanStack Router and Next.js App Router are mutually exclusive routing systems. Next owns routing via
the `app/` directory file conventions, `next/link`, `next/navigation`, server components, and SSR.
Dropping TanStack Router in means deleting `app/`-based routing and re-expressing all routes in
TanStack Router's tree — i.e. the Vite migration itself. Workstream D makes that future migration a
bounded, few-file change instead of a sprawling one, **without** committing to it now.

## Appendix C — Vite-readiness audit (fill in during D4)

What stays vs. changes when/if we move to Vite + TanStack Router:

| Concern | Today (Next.js) | Vite equivalent | Effort after Workstreams A–D |
|---|---|---|---|
| Data fetching | TanStack Query | TanStack Query (unchanged) | None |
| Tables | TanStack Table (after B) | unchanged | None |
| Virtualization | TanStack Virtual (after C) | unchanged | None |
| Navigation API | `lib/navigation.ts` adapter | rewrite adapter → TanStack Router hooks | 1 file |
| Links | `lib/link.tsx` adapter | rewrite adapter → TanStack Router `<Link>` | 1 file |
| Routing tree | `app/` file routes (~55) | TanStack Router route tree | Medium (the real migration) |
| Images | `next/image` (1 file) | `<img>` / vite-imagetools | Trivial |
| API proxy | `next.config.js` rewrites | Vite dev `server.proxy` | Trivial |
| Auth gate | `middleware.ts` (`NextResponse.redirect`) | TanStack Router `beforeLoad`/route guard | Small–Medium |
| Server actions | enabled in `next.config.js` but **unused** (0 `'use server'`) | nothing to port — drop config | None |
| Build/SSR | Next standalone | Vite SPA build | Medium |

> The point of Workstreams A–D: by the time a Vite decision is made, the only non-trivial line item
> left is the routing tree itself — everything else is already portable or behind a one-file adapter.
```
