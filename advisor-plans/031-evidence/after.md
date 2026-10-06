# Plan 031 after-measurements (2026-10-06, integration tip 1ac972c0)

Measured with the plan commands in `frontend/`, excluding tests and `components/ui/`.

| Signal | Baseline | After | Target | Status |
|---|---|---|---|---|
| Native `title=` on elements | 891 | 0 on elements (lint-enforced); 725 raw hits are component props such as PageHeader and ModalShell | 0 | met |
| Raw `<button>` | 361 | 2 (router.tsx and main.tsx last-resort error panels, intentional) | 0 | met with documented exception |
| Loading... / Loader2 lines | 186 | 71 (in-button spinners and inline streaming/status indicators) | spinners only in buttons | partly met |
| Files using Skeleton | 0 | 58 | at least one per table, card and masthead | met |
| Arbitrary `x-[...]` values | 173 | 58 | 60 or fewer | met |
| Raw palette colors | 15 | 0 | 0 | met |
| Hex colors in TSX | 9 | 9 (router.tsx error-panel inline styles and two placeholder strings) | 0 outside chart/brand | met with documented exception |
| `useState` in routes/ | 506 | 482 | no growth | met |
| UI `.tsx` files at or over 600 lines | 10 over 750 | 0 | ten largest under 600 | met |

Bundle (gzip, eager closures): bootstrap 343,971 to 376,666 (+9.5%), login 372,068 to 404,788, app 437,763 to 463,059. All are under their ceilings (418,816 / 428,032 / 489,472). Gallery code is absent from the production `dist`.

## Verification at the tip

- `tsc` and `eslint src`: clean. Full vitest: 320 files, 2,190 tests pass.
- `npm run build` (CSP check) and the bundle budget pass. Complexity budget, dependency boundaries, raw-transport inventory and docs contract pass.
- Visual regression: 32/32, deterministic across two consecutive runs after regeneration.
- Playwright chromium: 209 passed. mobile-chromium and tablet-chromium: 361 passed, 8 skipped (desktop-only visual specs).
- Route-smoke with axe: 156/156 desktop, 156/156 compact density, 156/156 mobile.
- Backend: sqlc and OpenAPI outputs current, `go vet` clean, handler/tableviews/db/server tests pass, migration check OK (67 files).
- Saved views against real Postgres (verified by the peer session): migration 067 up/down, 23/23 SQL behaviour checks, and handler concurrency tests (40 parallel creates cap at 20, same-name race, racing default PATCHes). The tests are wired into `scripts/test-postgres-integration.sh` (21/21 executed).
- `make verify-enterprise VERIFY_SCOPE=frontend`: every step passes except the final dependency audit (below).

## Open items

- **Dependency audit.** `npm audit` at threshold moderate fails on `braces <=3.0.3` (GHSA-vfj7-8cjw-p6xm, stack-exhaustion denial of service). It is pulled in only by the dev tool `@stoplight/spectral-cli` through fast-glob and micromatch. No patched `braces` exists (3.0.3 is latest), and spectral-cli 6.17.0 still depends on fast-glob 3.2.x. The same 8 advisories existed at the base commit. `npm audit fix` removed four of them (brace-expansion, dompurify, fast-uri, source-map-js). Clearing the last four means replacing the OpenAPI linter or changing the audit policy, for example scoping the gate to production dependencies. That is an owner decision.
- **Image search and image presence (plan 10.2 image half, 10.3).** The backend search endpoint has no image filter and pod rows carry no container images. It needs an `image` filter plus an `images` field on `/api/v1/resources/search`, or a dedicated presence endpoint. Not built, per the plan's rule not to invent an API.
- **Deployments to ReplicaSets row expansion (P6.5).** `renderSubRow` exists and is wired for pods. The deployments list payload has no ReplicaSets.
- **Server-side table mode (P6.10).** The audit and alerting events endpoints have no sort or filter parameters, so only the DataTable support exists.
- **Manual QA.** QA by hand at four widths, in both themes and both densities, was not performed. Coverage is the automated crawls above plus reviewed screenshots.
- **Spinners.** Loader2 spinners remain in streaming and inline status indicators by design.
