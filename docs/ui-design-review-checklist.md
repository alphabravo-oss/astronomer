# UI Design Review Checklist

Date: 2026-06-14

Use this checklist before merging frontend changes that add or materially change a page, table, drawer, dialog, form, command, or high-risk action.

## Layout

- Text does not overflow buttons, table cells, cards, drawers, dialogs, tabs, or sidebars at mobile, tablet, and desktop widths.
- No UI elements overlap, obscure each other, or shift unexpectedly during loading, hover, disabled, or selected states.
- Page structure uses `PageShell`, `PageHeader`, and `PageSection` for normal dashboard pages.
- Page sections are unframed layouts unless the content is a real repeated item, modal, drawer, or framed tool.
- Fixed-format elements have stable dimensions through grid tracks, aspect ratios, min/max sizes, or explicit control sizes.
- Dense operational pages avoid marketing-style hero layouts and oversized headings inside compact surfaces.

## Responsive Coverage

- Check at least these widths: 375px, 768px, 1280px, and one wide desktop viewport.
- Tables remain readable or horizontally scroll inside their own table container.
- Drawers and dialogs fit within the viewport and keep primary actions reachable.
- Command/search overlays remain centered and usable on mobile and desktop.
- Topbar breadcrumbs truncate without hiding primary actions.

## State Coverage

- Loading state reserves stable space and does not cause major layout jumps.
- Empty state explains the absence of data without instructional filler.
- Error state names the failed surface and gives the next useful action when one exists.
- Permission state names the missing permission or scope.
- Disconnected/offline state distinguishes live operations, queue-safe operations, and stale cached data.
- Stale/degraded data shows freshness or source when the distinction matters.

## Actions

- Use `ActionButton` or `ActionMenu` for operational actions.
- Destructive actions require confirmation, target identity, and audit behavior.
- High-risk actions show permission, dry-run/preview where practical, and operation progress.
- Disabled actions include a visible reason through `disabledReason`, helper text, or a permission state.
- Async actions show pending state and prevent duplicate submission unless explicitly idempotent.

## Tables

- Use `DataTable` or the low-level table primitives.
- Tables include stable loading skeletons, empty state, and clear row click behavior.
- Sortable columns use deterministic `sortAccessor` values.
- Bulk actions only appear after selection and show selected count.
- Column labels are concise and do not rely on color alone.

## Dialogs And Drawers

- Use `ModalShell`, `DrawerShell`, or `ActivityDetailsDrawer`.
- Escape and backdrop close behavior is deliberate.
- Focus moves into the overlay and returns to the trigger after close.
- Primary and secondary actions use consistent footer placement.
- Nested modal traps are avoided.
- Details with audit or operation context use field grids plus structured JSON/code blocks where useful.

## Accessibility

- Interactive elements have accessible names.
- Icon-only controls have labels or tooltips when meaning is not obvious.
- Keyboard navigation reaches every control in a predictable order.
- Focus rings are visible.
- Status is not communicated by color alone; labels or icons accompany color.
- Dialogs and drawers expose `role="dialog"`, `aria-modal`, and labelled titles.

## Visual Quality

- Palette is not dominated by one hue family.
- Avoid decorative gradient/orb backgrounds in operational pages.
- Cards are not nested inside cards.
- Status colors match the shared status map.
- Icons come from the existing icon system.
- Text scale matches the surface: page titles can be large, compact panels stay compact.

## Validation

- Run `npm run type-check`.
- Run `npm run lint`.
- Run `npm test -- --runInBand` for changed frontend logic.
- Run `npm run code-health` when changing shared UI, API wrappers, or generated contracts.
- Run `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/snap/bin/chromium npm run test:e2e` for shell, navigation, overlay, auth, or responsive workflow changes on this Ubuntu 26.04 host.
- Run `git diff --check`.
