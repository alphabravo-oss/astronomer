# Primitive library decision (Phase 0)

Chosen: `radix-ui` ^1.7 (single package), wrapped only inside `frontend/src/components/ui/`.

Spike result (2026-10-06): Tooltip, Popover and Separator built and shipped through `npm run build`.
- `check-csp-build.mjs`: pass (no inline styles/scripts introduced).
- `check-bundle-budget.mjs`: pass. bootstrap 343,971 gz (ceiling 418,816); login 372,068 (428,032); app 437,763 (489,472).
- Unit suite: 1,815 tests pass after migrating two RBAC tests from `getByTitle` to role queries.

Design notes
- `Tooltip` carries its own provider so components render in tests/portals without an app-root provider.
- `Tooltip` always renders Root/Trigger so the child never remounts when content toggles.
- `ActionButton` keeps a native `title` only for `disabledReason` (disabled buttons emit no pointer events).
