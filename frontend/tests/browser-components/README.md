# Shared table browser tests

Run `npm run test:browser-components` from `frontend/` after installing the pinned
dependencies and Playwright Chromium. The test runner builds the real DataTable
and shared CSS in a separate Vite entry, then starts an owned preview on port 3102. No test route or fixture is included in the operator console build.

The suite covers grouping plus virtualization, a combination not currently
exercised by server-paged resource screens. It uses one worker, zero retries,
and desktop/tablet/mobile viewports. Failed checks retain screenshots and traces.
Set `PLAYWRIGHT_COMPONENT_PORT` to use another dedicated port and
`PLAYWRIGHT_OUTPUT_DIR` to retain artifacts outside the ignored `.cache/` default.
Do not run concurrently with Docker network churn.

This is browser component qualification, not a live API/agent journey. Run the
normal frontend enterprise gate and applicable product E2E separately.
