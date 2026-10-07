# Visual regression

`tests/e2e/visual-regression.spec.ts` holds Playwright `toHaveScreenshot`
baselines (light/dark) for the reference pages (estate, cluster overview,
resource explorer, logging, delivery, rbac, management backup, settings,
clusters list, workloads, deployment detail, apps, new webhook) and the component gallery at `/dashboard/dev/ui` (light/dark x
comfortable/compact, desktop `chromium` project only). Baselines live in
`tests/e2e/visual-regression.spec.ts-snapshots/`.

The gallery is dev-only. The Vite dev server and builds made with
`VITE_UI_GALLERY=1` (the Playwright web server does this) include it; normal
production builds fold `__UI_GALLERY__` to `false` and do not emit the chunk.
If you reuse a running preview server, make sure it came from a flagged build.

Run (from `frontend/`):

    node scripts/generate-route-manifest.mjs && node scripts/generate-e2e-stubs.mjs
    PLAYWRIGHT_PORT=3221 npx playwright test tests/e2e/visual-regression.spec.ts --project=chromium

Update baselines after an intentional visual change, then review the PNG diff:

    PLAYWRIGHT_PORT=3221 npx playwright test tests/e2e/visual-regression.spec.ts --project=chromium --update-snapshots

Only update the projects you changed (`--project=chromium`, `mobile-chromium`,
`tablet-chromium`); the gallery has no mobile/tablet baselines. Baselines are
rendered on Linux Chromium: regenerate them in the same environment as CI.
