# Plan 031 after-measurements (2026-10-06, final integration tip)

Measured with the plan commands in `frontend/`, excluding tests and `components/ui/`.

| Signal | Baseline | After | Target | Status |
|---|---|---|---|---|
| Native `title=` on elements | 891 | 0 on elements (lint-enforced); the raw grep hits are component props such as PageHeader and ModalShell | 0 | met |
| Raw `<button>` | 361 | 2 (router.tsx and main.tsx last-resort error panels, intentional) | 0 | met with documented exception |
| Loading... / Loader2 lines | 186 | about 71 (in-button spinners and inline streaming/status indicators) | spinners only in buttons | partly met |
| Files using Skeleton | 0 | 58 | at least one per table, card and masthead | met |
| Arbitrary `x-[...]` values | 173 | 58 | 60 or fewer | met |
| Raw palette colors | 15 | 0 | 0 | met |
| Hex colors in TSX | 9 | 9 (router.tsx error-panel inline styles and two placeholder strings) | 0 outside chart/brand | met with documented exception |
| `useState` in routes/ | 506 | 482 | no growth | met |
| UI `.tsx` files at or over 600 lines | 10 over 750 | 0 | ten largest under 600 | met |

Bundle (gzip, eager closures): bootstrap 343,971 to about 376,700 (+9.5%), login 372,068 to about 404,800, app 437,763 to about 463,100. All under their ceilings (418,816 / 428,032 / 489,472). Gallery code is absent from the production `dist`.

## Verification at the final tip

- `make verify-enterprise` passes for all three scopes: frontend (2,194 unit tests, `npm audit` 0 vulnerabilities), backend and helm.
- Playwright chromium: 210 passed. mobile-chromium and tablet-chromium: 381 passed (desktop-only visual specs not run there). No failures.
- Route-smoke with axe: 156/156 desktop, 156/156 compact density, 156/156 mobile.
- Visual regression: 32/32 desktop, deterministic across consecutive runs.
- QA matrix (see `qa/README.md`): 16 pages x 1440/1920/390 x light/dark x comfortable/compact = 192 combinations, all pass overflow, axe and console checks; two narrow-screen defects found and fixed.
- Saved views against real Postgres (peer session): migration 067 up/down, 23/23 SQL behaviour checks, handler concurrency tests; wired into `scripts/test-postgres-integration.sh`.

## Gate findings fixed after the first completion record

- `npm audit` failed on `braces <=3.0.3` (no patched release) via the spectral CLI. `scripts/openapi-spectral.mjs` now drives `@stoplight/spectral-core` with the same `.spectral.yaml`; verified it still reports operation-tags, path-params and operationId-unique errors. Four more advisories cleared with `npm audit fix`.
- `user_table_views` was missing from `docs/data-governance/deletion-policies.json`.
- Chart/release target schema bumped 66 to 67 (values, schema, compatibility files, doc) for migration 067.
- `TableViewState` now derives from the generated OpenAPI schema; skeleton rows use the table primitives; dialog and sheet joined the overlay allowlist.
- Gallery visual tests moved to `ui-gallery-visual.spec.ts` (ignored by the mobile projects) instead of a `test.skip`, satisfying the flake/quarantine policy.
- Generated code-health and operation-task inventories refreshed.

## Open items

- **Image search and image presence (plan 10.2 image half, 10.3).** The backend search endpoint has no image filter and pod rows carry no container images. It needs an `image` filter plus an `images` field on `/api/v1/resources/search`, or a dedicated presence endpoint. Not built, per the plan's rule not to invent an API.
- **Server-side table mode (P6.10).** The audit and alerting events endpoints have no sort or filter parameters, so only the DataTable support exists.
- **Manual QA.** No person walked the pages by hand; the matrix above is automated plus reviewed screenshots. A full keyboard-only walkthrough beyond the existing keyboard e2e specs was not done.
- **Spinners.** Loader2 spinners remain in streaming and inline status indicators by design.
- **Merge.** `feat/031-ui-refinement` is not merged to main or pushed.
