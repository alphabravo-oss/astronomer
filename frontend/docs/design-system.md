# Design system

Astronomer's UI is built from a small set of mandatory primitives in
`src/components/ui/` (plus the form kit in `src/components/form/`). The rule
is: if a primitive exists for what you're building, use it — don't hand-roll
an equivalent. Lint rules and ratchet tests (below) enforce this for the
highest-drift surfaces; everything else is enforced by review.

This plan (021) added the missing primitives and closed the gaps in the
existing ones. Migrating the ~30-120 pre-existing hand-rolled call sites onto
them is a separate, later effort (plan 022) — most existing pages still use
the old patterns and that is expected until they're migrated.

## Primitive table

| Need | Primitive | Notes |
|---|---|---|
| Page header (title + actions) | `PageHeader` (`components/ui/page.tsx`) | `eyebrow` / `title` / `description` / `actions`. |
| Detail-page masthead (resource/cluster detail) | `ResourceMasthead` (`components/ui/page.tsx`) | Back link, eyebrow, title (+ optional `mono`, inline `status`), `actions`, a `meta` `<dl>` row, `description`. |
| Page section wrapper | `PageSection` (`components/ui/page.tsx`) | Optional heading/description/actions over `children`; no border. |
| Sortable/filterable list | `DataTable` (`components/ui/data-table.tsx`) | Search, sort, pagination, column visibility, virtualization above ~1k rows. Route modules must not import `@/components/ui/table` directly (lint-enforced). |
| Compact detail matrix (rich cells, no sort/filter) | `components/ui/operator-table.tsx` re-exports of `Table`/`TableHead`/etc. | Reserved for cases `DataTable`'s row model can't represent (nested controls, rowspans). Not a general table primitive — reach for `DataTable` first. |
| Tabs | `TabStrip` + `TabsContent` (`components/ui/tabs.tsx`) | Full ARIA (`role="tablist"/"tab"/"tabpanel"`, roving tabindex, Arrow/Home/End). Pass `value`, `onChange`, `tabs` (each with an optional `count`). |
| Status word (active/failed/OutOfSync/…) | `StatusBadge` (`components/ui/status-badge.tsx`) | Never hand-roll a status pill/dot. `shape="square"` for a denser table-cell badge (no dot by default); `dotOnly` (or the `StatusDot` export) for a bare connection-light; `tone` for an arbitrary user-chosen tag color (e.g. a cluster badge) instead of a status word. |
| Non-status label (kind, distribution, tag, count) | `Badge` (`components/ui/badge.tsx`) | For labels that are *not* a state word. If it can flip from "active" to "failed", it's a status — use `StatusBadge`. |
| Card surface | `Card`/`CardHeader`/`CardTitle`/`CardContent`/`CardFooter` (`components/ui/card.tsx`) | `padding` (`none`/`sm`/`md`/`lg`, default `none`) and `radius` (`md`/`lg`/`xl`, default `lg`). Pair `padding="none"` with the sub-components (they carry their own `p-5`) — only give `Card` itself a non-`"none"` padding when it has no `CardHeader`/`CardContent`/`CardFooter` children, or you'll double-pad. |
| Toggle | `Switch` (`components/ui/switch.tsx`) | `size` (`sm`/`md`). Never hand-roll the `rounded-full bg-white transition-transform` thumb (lint-enforced). |
| Metric tile | `MetricCard` (`components/ui/metric-card.tsx`) | `label` (preferred) or `title`; `value` is any `ReactNode`; `tone` to force a color instead of deriving one from `percentage`; `href` to make the whole tile a link; `dense` for compact grids. |
| Form field (outside the `useAppForm` kit) | `Field` (`components/form/fields.tsx`) | Label + control + helper/error, matching `TextField`'s visuals, for hand-rolled forms that own their own id/value wiring. Inside the kit, use `field.TextField`/`SelectField`/etc. |
| Empty state | `EmptyState` (`components/ui/empty-state.tsx`) | **Requires an action** (`actionLabel` + `actionHref` or `onAction`) or an explicit `terminal: true` with a one-line comment explaining why there's nothing to do (type-enforced). `variant="table"` is the one exception, for a `DataTable`'s inline empty row, which may or may not have a caller-supplied action. |
| Loading state | `LoadingState` (`components/ui/empty-state.tsx`) | Also see `ErrorState`, `PermissionState`, `PartialState`, `OfflineState`, `StaleState`, `RetryingState`, `TerminalFailureState` in the same file for the other query-lifecycle states — `QueryStates` wires most of these up automatically. |

### Modal vs. route

Use a modal (`ModalShell`) for a short, self-contained action that doesn't
need its own URL or deep-linkable state: create/edit a single record, confirm
a destructive action, a narrow settings form. Use a route (a new page under
`src/routes/`) when the content is itself navigable or shareable: a resource
detail view, a multi-step wizard, anything with its own sub-tabs, or
anything a user should be able to bookmark, refresh, or open in a new tab.
When in doubt, prefer a route — modals that grow multiple steps or their own
internal navigation are a sign they should have been a route.

## Charts

Charts (`recharts` or otherwise) must use color tokens from
`src/lib/chart-colors.ts` (`SERIES` for multi-series line/area charts,
`SEVERITY` for critical/high/medium/low) or reference `hsl(var(--status-*))`
directly — never a hard-coded hex/rgb literal. This keeps chart colors
consistent with the rest of the theme in both light and dark mode. Brand
marks (cloud-provider badges) use `--brand-aws`/`--brand-gcp`/`--brand-azure`/
`--brand-do` (see `styles/globals.css`) instead of raw Tailwind color
utilities, since they identify a vendor rather than a semantic state.

## Spacing scale

- Page (`PageShell`): `space-y-6` between top-level sections.
- Section (`PageSection`, a `<section>` or major sub-block): `space-y-3`.
- Field (a label + control + helper/error group): `space-y-1.5`.

Don't invent a new spacing value between these three; if a surface needs
tighter or looser rhythm than one of them, that's a sign it should be broken
into an explicit sub-section instead.

## Type scale

`text-2xs` (`0.625rem`/10px, see `--text-2xs` in `styles/globals.css`) is
reserved for non-essential metadata — timestamps, secondary counts, helper
text a user skims but doesn't need to read closely. Never use it for a
primary label, a value the user is acting on, or anything that must clear
body-text-equivalent contrast on its own (it pairs with the AA-safe
`--muted-foreground` token specifically because 10-12px text needs the extra
contrast headroom — don't pair `text-2xs` with a lower-contrast color).

## Ratchets

Three CI-enforced ratchets prevent drift from creeping back in once a
primitive exists. All three live in
`src/components/ui/__tests__/design-system-adoption.test.ts` and
`table-adoption.test.ts`.

1. **Per-file adoption tests** (`design-system-adoption.test.ts`'s
   `actionSurfaces` list, `table-adoption.test.ts`'s `operationalGrids`
   list): a named file must use the shared primitive and contain zero raw
   markup. When you migrate a page onto `ActionButton`/`DataTable`, add its
   path to the relevant list so it can never regress.
2. **Repo-wide raw-markup bans** (`table-adoption.test.ts`'s "does not allow
   unreviewed raw HTML tables" test): a hard-coded allowlist of files that
   may still contain a raw `<table`. Shrink the allowlist as files migrate;
   never grow it without a reason.
3. **Counting ratchets** (`design-system-adoption.test.ts`'s
   `BASELINE_BUTTONS` / `BASELINE_CARD_FRAME_LITERALS`): count raw
   `<button` elements and hand-rolled `border border-border bg-card` card
   frames across `src/routes`, asserting the count is `<=` a hard-coded
   baseline. To lower a baseline after migrating pages onto
   `ActionButton`/`Card`: re-run the count, confirm it went down, and update
   the constant with a comment noting the date/plan. Never raise a baseline
   to make a new raw usage pass — that's what the primitive is for.

## Design-system lint rules

`eslint.config.mjs` bans raw `<h1>`, raw `<table>`, the hand-rolled switch
thumb (`rounded-full bg-white transition-transform`), and non-semantic
Tailwind color utilities (`text-red-500`, `bg-blue-600`, etc.) under
`src/routes/**` and `src/components/**`, excluding `src/components/ui/**`
and `src/components/form/**` (the primitives themselves) and two named
brand-content exceptions (`provider-badge.tsx`, `login-branding.tsx`). It
also bans importing `@/components/ui/table` directly outside the primitives
— use `DataTable` or `@/components/ui/operator-table`.

Existing call sites predating these rules carry
`// eslint-disable-next-line no-restricted-syntax -- migrated in plan 022`
(or `-- marketing hero` for the login page's hero content) rather than being
migrated as part of this change — migration is plan 022's job. Don't add a
new disable comment for new code; fix it to use the primitive instead. When
a disable comment would land inside JSX children (between `>` and the next
element), it must be a JSX comment expression (`{/* eslint-disable-line
no-restricted-syntax -- reason */}`) placed on the *same* line as the
element it covers (`eslint-disable-line`, not `-next-line`) — a bare `//`
placed as JSX children text is not a comment at all, it's a literal text
node that will render on the page. `eslint-disable-next-line` is fine only
in a plain JS/TS expression or attribute-list context (never JSX children).

## Create and edit flows

**Create and edit are routes; modals are for confirmations, single-field
actions, and pickers.** A page that creates or edits a multi-field resource
(a template, a target, a token, a forwarder, a destination) gets its own
`*/new/index.tsx` (and, where it exists, `*/$id/edit/index.tsx`) route
rendered with `PageHeader` + `FormShell`, reached from an `ActionButton`
`RouterLink` on the list/detail page rather than a click-to-open dialog.
Routes are linkable, back-navigable, and don't lose the operator's place in
a long form on an accidental Escape/backdrop click.

Keep using `ModalShell` for:
- **Confirmations** — `ConfirmDialog` for destructive/impactful actions.
- **Single-field actions** — a small one- or two-input action that isn't
  really a persisted resource with its own lifecycle (e.g. picking a date
  window to kick off a one-off export job).
- **Pickers** — assigning existing resources to each other (e.g. adding
  already-registered clusters to a cluster group) rather than creating a
  new resource.

Plan 022 migrated the multi-field **create** flows for backups (S3
destinations) and several Settings resources (read-audit policies, API
tokens, cluster groups, SIEM forwarders, SCIM tokens, group mappings) to
routes. Editing an *existing* row on those same pages intentionally stays a
modal for now (out of scope for plan 022) — converting edit-in-place to
routes is tracked as follow-up work, not a design-system violation.

## Complexity budget (`node scripts/check-complexity-budget.mjs`, from the repo root)

This is a CI gate, run separately from `npm run lint`. It requires an
**exact** line-count match for every file/function baselined in
`docs/architecture/complexity-baseline.json`:

- A baselined unit that *grew* past its ceiling fails with "exceeds
  no-growth ceiling" — never let a baselined unit grow. If you must touch
  one, extract logic into a new small file/function instead of inlining
  more lines into it (this includes single-line additions like a comment —
  merge it onto an existing line, e.g. an inline block comment before a
  token, rather than inserting a new line, if the unit is at its ceiling).
- A baselined unit that *shrank* below its ceiling also fails, with "below
  stale ceiling" — the baseline must track the real count. Run
  `node scripts/check-complexity-budget.mjs --write-baseline`, confirm with
  `git diff docs/architecture/complexity-baseline.json` that every changed
  number went **down** (never up — a `--write-baseline` run after code that
  grew a unit would silently launder a real regression into the baseline),
  and commit the baseline change as its own `chore(frontend): lower
  complexity baseline …` commit, separate from the functional change.
