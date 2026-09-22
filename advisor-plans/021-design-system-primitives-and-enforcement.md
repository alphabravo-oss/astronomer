# Plan 021: Close the design-system gaps — a resource masthead, accessible tabs and tables, one status badge, card/switch/field/metric variants, tokenized charts, required empty-state actions, and lint rules that make the primitives the only path

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `advisor-plans/README.md`.
>
> **Drift check (run first)**:
> `git diff --stat 59619920..HEAD -- frontend/src/components/ui frontend/src/components/form frontend/src/styles/globals.css frontend/eslint.config.mjs frontend/src/components/monitoring/metrics-chart.tsx`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M (1.5–2 days). This plan changes primitives and adds guards; Plan 022 does the page migrations.
- **Risk**: LOW–MED (primitives are shared; every change is visual and covered by the smoke crawl + axe)
- **Depends on**: none (018/019 may land in parallel; only `components/ui` is touched here)
- **Category**: tech-debt (design system), a11y
- **Planned at**: commit `59619920`, 2026-09-21

## Why this matters

Rancher looks like one product because five primitives are mandatory (`ResourceTable`, `ResourceDetail/Masthead`, `CruResource`, `BadgeState`, one SCSS variable file) and nothing else is available. Astronomer has an equally good token layer and most of the same primitives, but they are optional: `Card` has 2 adopters against 120 hand-rolled card surfaces in 13 padding/radius combinations; there is no masthead primitive at all, so five detail pages each invented one; six status-badge implementations exist; the shared `Tabs` has no tab ARIA while three hand-rolled bars do; no table header has `scope="col"`; the ratchet tests guard one file each. This plan adds the two missing primitives, fixes the accessibility of the shared ones, adds variants so the hand-rolled copies have nowhere left to hide, and adds lint/ratchet rules so drift stops. Migration of the pages themselves is Plan 022.

## Current state

All paths under `frontend/src/`.

`components/ui/page.tsx` — `PageShell` (`space-y-6`), `PageHeader` (eyebrow / `h1 text-2xl font-semibold` / description / `actions`), `PageSection` (`h2 text-sm font-semibold`). No status, metadata, or back slot — which is why detail pages forked:

- `components/resources/resource-detail.tsx:186-205` — back button + `font-mono text-xl` h1 + inline "Kind: / Namespace: / Age:" strip.
- `routes/dashboard/clusters/$id/nodes/$nodeName/index.tsx:705-725` — near-identical copy plus an inline "Unschedulable" pill; the page has **no visible node name H1** in the gallery.
- `routes/dashboard/clusters/$id/delivery/route.tsx:87-99` and `routes/dashboard/projects/$id/route.tsx:80-95` — hand-rolled eyebrow+h1+description.
- `routes/dashboard/clusters/$id/index.tsx:235-270` — `PageHeader` with badges crammed into `title` and a separate meta line.

`components/ui/tabs.tsx:9-54`:

```tsx
export function TabsList({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return <nav className={cn("flex gap-6 border-b border-border", className)} {...props} />;
}
export function TabsTrigger({ active, className, ...props }) {
  return <button type="button" className={cn("flex items-center gap-2 border-b-2 pb-3 text-sm font-medium transition-colors", active ? "border-foreground text-foreground" : "border-transparent text-muted-foreground hover:text-foreground", className)} {...props} />;
}
export function TabsContent({ active = true, ... }) { if (!active) return null; return <div className={cn("animate-fade-in", className)} ...>; }
export function TabStrip<T extends string>({ tabs, value, onChange, className }) { ... }
```

No `role="tablist"`, `role="tab"`, `aria-selected`, `aria-controls`, roving `tabIndex`, or arrow-key handling. The correct implementation exists at `components/resources/resource-detail.tsx:233-258` (`role="tablist"`, `role="tab"`, `aria-selected`, `aria-controls`, `tabIndex={tab === item.id ? 0 : -1}`, `onKeyDown={(event) => handleTabKeyDown(event, index)}` with the handler at ~140–168).

`components/ui/table.tsx:54-64`:

```tsx
export function TableHead({ className, ...props }: ThHTMLAttributes<HTMLTableCellElement>) {
  return <th className={cn("h-10 px-3 text-left text-xs font-semibold", className)} {...props} />;
}
```

`components/ui/data-table-semantic-view.tsx:110-137` — sortable headers put `tabIndex={0}`, `onClick`, `onKeyDown` on the `<th>` itself (`aria-sort` is set correctly). `grep -rn 'scope=' components/ui/*.tsx` → 0.

Status rendering:
- `components/ui/status-badge.tsx` — canonical (48 importers); `sm: "px-2 py-0.5 text-[10px] leading-4"` at line 11 although `--text-2xs` exists in `styles/globals.css:50-51`.
- `components/ui/badge.tsx` — `rounded-md` variants `default/secondary/outline/success/warning/error/info/high`, 8 importers.
- `routes/dashboard/clusters/$id/network-policies/index.tsx:38-58` `StatusPill`; `components/window-manager/window-manager.tsx:333-343` `StatusDot`; `components/clusters/cluster-badge.tsx:3-10` `ClusterBadge`; ~24 inline `rounded-full px-2 … text-xs` pills.

`components/ui/card.tsx` — `Card` hard-codes `rounded-lg border border-border bg-card`; `CardHeader` `p-5`; `CardTitle` `h3 text-sm font-semibold` (identical to `PageSection`'s h2). 2 importers. 120 literal `rounded-(lg|xl|md) border border-border bg-card p-N` surfaces exist (top combos: `rounded-lg…p-4` ×33, `rounded-xl…p-6` ×27, `rounded-lg…p-6` ×16, `rounded-xl…p-5` ×15).

`components/ui/switch.tsx:34` thumb `inline-block h-4 w-4 transform rounded-full bg-white transition-transform`; 5 adopters; 7 hand-rolled copies at two sizes (`h-4` and `h-3.5`).

`components/ui/empty-state.tsx:22-27` — `actionLabel`/`actionHref`/`onAction` optional; 24 of 48 call sites pass none (e.g. `routes/dashboard/clusters/index.tsx:291` — the clusters list has no "Register cluster" CTA when empty).

`components/ui/metric-card.tsx` — 5 adopters; 12 file-local equivalents (`MetricTile` ×2 different, `SummaryTile`, `SummaryCard` ×2, `FactCard`, `Stat` ×2, `Metric`, `SummaryRow`, `DetailRow`, `SummaryStrip`, `MetricLink`).

`components/form/fields.tsx` — `TextField`/`SelectField`/`TextareaField` via `useAppForm` (`lib/form.ts:20-36`); 9 route-local `function Field` copies exist (5 near-identical in delivery/register routes; a divergent one in `settings/read-audit/index.tsx:350`).

Charts: `components/monitoring/metrics-chart.tsx:25-29` uses `hsl(var(--status-info))` etc.; `routes/dashboard/clusters/$id/image-scans/index.tsx:836,845,855,861` uses `#f97316` / `#dc2626`.

`components/ui/operator-table.tsx:1-9` documents that route modules must not import `@/components/ui/table` directly; 7 modules do (`components/resources/resource-overview.tsx`, `resource-overview-additional.tsx`, `resource-detail-tabs.tsx`, `rollout-history.tsx`, `components/charlie/settings/agent-tab.tsx`, `automation-tab.tsx`, `components/charlie/safe-markdown.tsx`).

Ratchets: `components/ui/__tests__/design-system-adoption.test.ts` guards one file (`actionSurfaces = ["src/routes/dashboard/clusters/$id/index.tsx"]`, asserting `ActionButton` import and no `<button\b`); `table-adoption.test.ts` guards three files and repo-wide raw `<table`.

ESLint (`frontend/eslint.config.mjs`): flat config; already uses `no-restricted-syntax` (bans `"use client"` and inline `queryKey` arrays), `no-restricted-imports` (bans `next/*`), `no-restricted-globals`/`no-restricted-properties` (bans `confirm`/`alert`) scoped to `src/routes/**` and `src/components/**`. Add new rules in the same style.

## Commands you will need

| Purpose | Command (in `frontend/`) | Expected |
|---|---|---|
| Gate | `npm run type-check && npm run lint && npm test` | exit 0 |
| Smoke + axe | `npm run test:e2e:smoke` | pass (axe serious/critical gate is part of the crawl) |
| Gallery | `SMOKE_GALLERY=1 npx playwright test --project=route-smoke` | PNGs |
| Visual regression baselines (only if a spec asserts pixels on a changed primitive) | `npm run test:e2e:visual` | pass or regenerate with `--update-snapshots` and commit |

## Scope

**In scope**:
- `frontend/src/components/ui/page.tsx`, `tabs.tsx`, `table.tsx`, `data-table-semantic-view.tsx`, `status-badge.tsx`, `badge.tsx`, `card.tsx`, `switch.tsx`, `empty-state.tsx`, `metric-card.tsx`, `operator-table.tsx`
- `frontend/src/components/form/fields.tsx` (export `Field`)
- `frontend/src/lib/chart-colors.ts` (create); `components/monitoring/metrics-chart.tsx` (import it); `routes/dashboard/clusters/$id/image-scans/index.tsx` (4 lines)
- `frontend/src/styles/globals.css` (brand tokens)
- `frontend/eslint.config.mjs`
- `frontend/src/components/ui/__tests__/*` (new ratchet tests and allowlists)
- The 7 `ui/table` importers (import path change only)
- `frontend/docs/design-system.md` (create)

**Out of scope**:
- Migrating pages onto the new/changed primitives (Plan 022) — except where a primitive change would otherwise break a consumer (keep backwards-compatible props).
- Deleting `Badge`. It stays; only its role is documented.
- Any layout/nav change.

## Git workflow

- Branch: `advisor/021-design-system`
- One commit per step.

## Steps

### Step 1: `ResourceMasthead`

Add to `components/ui/page.tsx`:

```tsx
export function ResourceMasthead({
  backTo, backLabel = "Back", eyebrow, title, mono = false, status, meta = [], actions, description,
}: {
  backTo?: string; backLabel?: string; eyebrow?: ReactNode; title: ReactNode; mono?: boolean;
  status?: ReactNode;                        // a <StatusBadge/> (or several)
  meta?: Array<{ label: string; value: ReactNode }>; // "Kind", "Namespace", "Age", "Version"…
  actions?: ReactNode; description?: ReactNode;
})
```

Layout (match Rancher `ResourceDetail/Masthead`): optional back `RouterLink` (icon `ArrowLeft`, `aria-label`), then a row with `eyebrow` (same classes as `PageHeader`'s eyebrow) + `<h1 className={cn("truncate text-2xl font-semibold tracking-tight", mono && "font-mono")}>` + `status` inline after the title, `actions` right-aligned (reuse the `PageHeader` actions container), then a `<dl className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">` of `meta` pairs (`<dt>` visually `label:`), then `description`. Add a story-like unit test rendering all slots and asserting one `<h1>` and the `dl` pairs.

**Verify**: `npm test -- page` pass.

### Step 2: Accessible `Tabs`

Rewrite `components/ui/tabs.tsx` keeping the same exports and props (so existing 8 consumers compile unchanged):

- `TabsList` → `<div role="tablist" aria-label={props["aria-label"] ?? "Sections"}>` (no `<nav>`).
- `TabsTrigger` gains `id`, `role="tab"`, `aria-selected={active}`, `aria-controls`, `tabIndex={active ? 0 : -1}`; `TabStrip` wires ids (`tab-${key}` / `tabpanel-${key}`) and implements Arrow Left/Right/Home/End with focus movement + `onChange` (port `handleTabKeyDown` from `resource-detail.tsx`).
- `TabsContent` → `role="tabpanel"`, `id`, `aria-labelledby`, `tabIndex={0}` only when it has no focusable child (mirror the existing `jsx-a11y/no-noninteractive-tabindex` allowance for `tabpanel`).
- Optional `count?: number` on `TabStrip` tabs rendered as a muted `<span>` (the Apps page and Catalog already render counts inline).

**Verify**: new `tabs.test.tsx`: roles present; ArrowRight moves selection and focus; Home/End work. `npm run test:e2e:smoke` axe passes.

### Step 3: Table header semantics

- `TableHead`: default `scope="col"` (allow override via props).
- `data-table-semantic-view.tsx:110-137`: move the click/keydown/tabIndex off the `<th>` onto an inner `<button type="button" className="inline-flex items-center gap-1 …" aria-label={\`Sort by ${header}\`}>` that wraps the header content and sort icon. Keep `aria-sort` on the `<th>`.
- Check `tests/e2e/data-table.spec.ts` and `components/ui/__tests__/data-table.behavior.test.tsx` for selectors that click the `<th>`; update them to click the button.

**Verify**: `npm test -- data-table` pass; `npx playwright test tests/e2e/data-table.spec.ts --project=chromium` pass.

### Step 4: One status vocabulary

- `status-badge.tsx`: change `sm` to `text-2xs leading-4`; add `shape: "pill" | "square"` (square = `rounded-md`, no dot by default) and `dotOnly?: boolean` (renders only the dot with `aria-label={displayLabel}` and `title`).
- `badge.tsx`: add a JSDoc: "For non-status labels (kind, distribution, tags). For any state word use `StatusBadge`." Keep API.
- Export `StatusDot` from `status-badge.tsx` as `(props) => <StatusBadge dotOnly {...props} />` and delete the local `StatusDot` in `components/window-manager/window-manager.tsx:333-343`, importing the shared one (this single migration is in scope because it deletes a duplicate primitive).
- Delete `StatusPill` from `routes/dashboard/clusters/$id/network-policies/index.tsx:38-58` in favour of `<StatusBadge status=… />` (one file; the palette map there maps `pending/applied/failed/drifting` — check `lib/utils.ts statusBgColor` covers `applied`/`drifting`; if not, add those two words to its tables with tests in `lib/__tests__/utils.test.ts`).
- Fold `components/clusters/cluster-badge.tsx` into `StatusBadge` via a `tone` prop **only if** it has ≤ 3 importers; otherwise leave it and document it as the cluster-badge primitive.

**Verify**: `npm test -- status-badge utils window-manager` pass; `grep -rn "function StatusPill\|function StatusDot" frontend/src` → 0 outside `components/ui`.

### Step 5: `Card`, `Switch`, `MetricCard`, `Field` variants

- `card.tsx`: add `padding?: "none" | "sm" | "md" | "lg"` (`p-0` / `p-4` / `p-5` / `p-6`; default `md`) and `radius?: "md" | "lg" | "xl"` (default `lg`) via cva; keep `CardHeader/Title/Description/Content/Footer`. `CardTitle` stays `text-sm font-semibold`.
- `switch.tsx`: add `size?: "sm" | "md"` (thumb `h-3.5 w-3.5` / `h-4 w-4`, track sized to match).
- `metric-card.tsx`: ensure props cover `label`, `value`, `hint` (sub-text), `icon`, `href`, `tone` (`default | success | warning | error`), `dense?: boolean`. Add missing ones without breaking the 5 existing call sites.
- `components/form/fields.tsx`: export a standalone `Field({ label, description, error, required, htmlFor, children })` for non-TanStack forms and the register wizard, matching the visual of `TextField` (label `text-sm font-medium`, description `text-xs text-muted-foreground`, error `text-xs text-status-error` with `role="alert"`).

**Verify**: `npm test -- primitives` (extend `components/ui/__tests__/primitives.test.tsx`) pass.

### Step 6: `EmptyState` requires an action

- Change the props type so that either `actionLabel` (+ `actionHref` or `onAction`) is present **or** `terminal: true` is passed explicitly. TypeScript will flag all 24 sites; for each, either add the obvious CTA (e.g. `clusters/index.tsx:291` → "Register cluster" → `/dashboard/clusters/register`, gated by the page's existing create permission) or mark `terminal` with a one-line comment why. Do this pass now (it is compiler-driven and small).
- Add `variant?: "page" | "table"` if not present so the DataTable inline empty state stays compact.

**Verify**: `npm run type-check` exit 0; `grep -rn "<EmptyState" frontend/src | wc -l` unchanged (48) and `grep -rn "terminal" frontend/src --include=*.tsx | grep EmptyState -c` ≤ 10.

### Step 7: Tokens and charts

- `styles/globals.css`: add `--brand-aws`, `--brand-gcp`, `--brand-azure`, `--brand-do` HSL tokens (light + dark) and expose them under `@theme inline` as `--color-brand-*`. Point `components/projects/cloud-credentials/provider-badge.tsx` at them (one file).
- Create `lib/chart-colors.ts` exporting `SERIES` (moved from `metrics-chart.tsx:25-29`) and `SEVERITY = { critical: "hsl(var(--status-error))", high: "hsl(var(--status-high))", medium: "hsl(var(--status-warning))", low: "hsl(var(--status-info))" }`; replace the four hex literals in `image-scans/index.tsx:836-861`.

**Verify**: `grep -rnE 'stroke="#|fill="#' frontend/src/routes frontend/src/components` → 0.

### Step 8: Lint rules and ratchets

Add to `eslint.config.mjs`, scoped to `src/routes/**` and `src/components/**` (excluding `src/components/ui/**` and `src/components/form/**`):

- `no-restricted-imports` pattern `@/components/ui/table` → "Import from @/components/ui/operator-table (compact matrices) or use DataTable." Repoint the 7 offenders' imports.
- `no-restricted-syntax`:
  - `JSXOpeningElement[name.name='h1']` → "Use PageHeader or ResourceMasthead." (add `// eslint-disable-next-line` with a reason only in `routes/auth/login/index.tsx` and `reset-password` if they intentionally differ).
  - `JSXOpeningElement[name.name='table']` → "Use DataTable / operator-table." (matches the existing vitest ratchet).
  - `Literal[value=/rounded-full bg-white transition-transform/]` → "Use Switch."
  - `Literal[value=/\\b(text|bg|border)-(red|green|blue|amber|emerald|zinc|gray|slate|orange|sky)-\\d{2,3}\\b/]` → "Use semantic tokens (status-*, muted-foreground, brand-*)." Allowlist `provider-badge.tsx` via a per-file override.
- Extend `design-system-adoption.test.ts` so `actionSurfaces` is a list Plan 022 appends to; add a **counting ratchet**: read all `src/routes/**/*.tsx`, count `<button\b` occurrences, and assert `count <= BASELINE` where `BASELINE` is the number you measure now (`grep -rn "<button" src/routes | wc -l`). Same for `border border-border bg-card` literals in `src/routes`. Plan 022 lowers the baselines as it migrates.
- Write `frontend/docs/design-system.md`: the primitive table (what to use for page header, detail masthead, list, compact matrix, status, non-status label, card, tabs, modal vs route rule, empty state, loading), the spacing scale (page `space-y-6`, section `space-y-3`, field `space-y-1.5`), type scale rule (`text-2xs` only for non-essential metadata), and the three ratchets with how to lower baselines.

**Verify**: `npm run lint` exit 0 after repointing the 7 imports and disabling the h1 rule on the two auth pages; `npm test -- adoption` pass with the measured baselines.

### Step 9: Gate

```bash
cd frontend && npm run type-check && npm run lint && npm test && npm run test:e2e:smoke
```

## Test plan

- `page.test.tsx` (ResourceMasthead slots), `tabs.test.tsx` (roles + keyboard), `data-table.behavior.test.tsx` (sort button), `status-badge.test.tsx` (shape/dotOnly/text-2xs), `primitives.test.tsx` (card/switch/metric/field variants), `design-system-adoption.test.ts` (baselines), `utils.test.ts` (new status words).
- Smoke crawl with axe must stay green — the `Tabs` and `<th scope>` changes are the a11y payload.

## Done criteria

- [ ] Gates exit 0
- [ ] `grep -n "role=\"tablist\"" frontend/src/components/ui/tabs.tsx` → 1; `grep -n 'scope="col"' frontend/src/components/ui/table.tsx` → 1
- [ ] `grep -n "text-\[10px\]" frontend/src/components/ui/status-badge.tsx` → 0
- [ ] `grep -rn 'from "@/components/ui/table"' frontend/src/routes frontend/src/components | grep -v components/ui/` → 0
- [ ] `grep -rnE 'stroke="#|fill="#' frontend/src/routes frontend/src/components` → 0
- [ ] `frontend/docs/design-system.md` exists and names every ratchet
- [ ] `EmptyState` type forbids a missing action without `terminal`
- [ ] `git status` limited to in-scope files
- [ ] README row updated

## STOP conditions

- Excerpts don't match live code.
- Changing `TabsList` from `<nav>` to `<div role="tablist">` breaks a Playwright selector you cannot find in scope (search `tests/` for `nav >> button`, `getByRole("navigation")`) — update the test; if it is in a live-only spec (`tests/e2e-live`), STOP and report.
- The `<button\b` baseline test cannot be made deterministic (e.g. generated files under `src/routes`) — exclude `routeTree.gen.ts` explicitly; if still flaky, STOP.
- Adding `scope="col"` triggers a visual-regression diff in `test:e2e:visual` — regenerate snapshots and commit them; if diffs are not explainable by the change, STOP.

## Maintenance notes

- Plan 022 consumes every variant added here. Do not add page migrations to this branch beyond the single-file deletions listed (StatusPill, StatusDot, the 7 import repoints, the 4 chart literals, EmptyState CTAs).
- When a new primitive is added, add it to `design-system.md` and, if it replaces a hand-rolled pattern, add a lint selector for the pattern.
- The counting ratchets are intentionally coarse (grep-level); they exist so numbers only go down. Do not raise a baseline without a written reason in the PR.
