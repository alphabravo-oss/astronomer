# Plan 027 final acceptance

Completed 2026-09-25; verification ran on September 24–25. Production source: `5b4c2bc814270e879f8ee2e01507642e10ffbe71`. The later `b137a31d` adds only the explicit permission-revocation browser test; its typecheck, lint and browser checks passed separately. Subsequent plan/evidence commits contain no application changes.

The original working state was preserved as `89229ce5d65e4974107e09de4f8bf5c25e18f172`, fast-forwarded onto `main` and pushed to `origin/main`. Implementation remains on `feat/027-operator-navigation-workflows` for review. No feature merge or release is implied.

## Passing verification

| Check | Actual result | Retained evidence |
|---|---|---|
| Frontend enterprise, `5b4c2bc8` | PASS: 289 files / 1,792 unit tests, lint, types, formatting contract, production/CSP build, all bundle budgets, generated route drift, dependency audit | [Commit-bound report](./enterprise-frontend/evidence.json), [subgates](./enterprise-frontend/subgates.tsv) |
| Backend enterprise, `5b4c2bc8` | PASS: 111 packages in the ordinary suite and 111 with race detection; formatting, migrations, sqlc, build/vet/lint, vulnerability scan, API compatibility/generated clients, documentation and release contracts | [Commit-bound report](./enterprise-backend/evidence.json), [subgates](./enterprise-backend/subgates.tsv) |
| Required disposable PostgreSQL qualification | PASS: all 18 required tests executed, zero required skips, 66 migrations; includes final Project scope/search/restore/output-association SQL. No SQL changed afterward. | [Log](./logs/postgres-integration.log) |
| Final operator + existing keyboard/resource journeys, `5b4c2bc8` | PASS: 134 cases, desktop/mobile, two workers, zero retries, 1.8m | [Log](./logs/browser-root-accepted.log) |
| Full generated route/accessibility crawl, `8987c38c` | PASS: 308 cases, 146 routes on desktop and mobile plus navigation/detector cases, 5.1m | [Log](./logs/route-root-final.log) |
| Explicit tablet shell, `5b4c2bc8` | PASS: four cases, including setup; checks all five required widths and long names, 9.5s | [Log](./logs/browser-tablet-accepted.log) |
| Resource history + permission change, `b137a31d` | PASS: 14 cases, desktop/mobile, one worker, zero retries, 22.9s; typecheck and focused ESLint also pass | [Browser log](./logs/browser-permission-accepted.log), [static log](./logs/final-test-types.log) |

Browser counts include two role-auth setup cases per invocation and must not be summed as unique workflows. The route crawl preceded the final create-editor initialization correction; the final 134-case run includes the affected keyboard create workflow on both desktop/mobile. No routes changed afterward. All browser APIs are intercepted canonical fixtures, not live extension qualification.

The backend's optional live agent-identity check explicitly skipped because no `AGENT_IDENTITY_TEST_CONTEXT` was supplied. Required database tests were exercised separately in the disposable PostgreSQL run. A filtered release subcommand selected no tests in `internal/releasecontract`; the full ordinary and race suites did execute that package. These distinctions are retained in the logs.

## Exact commands and environment

Node 24.21.0 was selected through `/root/.npm/_npx/538786c08bcb9442/node_modules/node/bin`. Heavy checks ran serially with disk-backed `TMPDIR=/root/astronomer-all/.tmp-plan027-root`; unknown temporary artifacts and running cluster services were preserved. Go used `GOFLAGS='-p=1 -buildvcs=false'`, `GOMAXPROCS=2`, `GOMEMLIMIT=2GiB`. The VCS flag avoids an incomplete parent repository when stamping test binaries; it does not disable contract checks. Frontend used `NODE_OPTIONS=--max-old-space-size=2048` and `VITEST_MAX_WORKERS=2`.

```sh
make verify-enterprise VERIFY_SCOPE=frontend
OPENAPI_BASELINE=main:docs/openapi.yaml make verify-enterprise VERIFY_SCOPE=backend
```

The backend command explicitly unset the optional live agent-identity context. The real PostgreSQL gate used `make test-postgres-integration` through the canonical disposable runner. Its original log retains the exact invocation.

The final browser runs reused a dedicated loopback Vite preview on port 32138 serving the identified production build. It was stopped afterward. From `frontend`, with `CI=1 PLAYWRIGHT_PORT=32138 PLAYWRIGHT_REUSE_EXISTING_SERVER=1` and the output directories shown by each retained log:

```sh
npm run test:e2e -- operator- pod-drilldown.spec.ts resource-drilldown.spec.ts critical-workflows-keyboard.spec.ts resource-actions-keyboard.spec.ts --workers=2
npm run test:e2e:smoke -- --workers=2
npx playwright test operator-shell-responsive.spec.ts --project=tablet-chromium --workers=1
npm run test:e2e -- operator-resource-history.spec.ts --workers=1
npm run type-check
npx eslint tests/e2e/operator-resource-history.spec.ts --max-warnings=0
```

## Final corrections and review

The final gates found and repaired eager loading of nine shared Delivery pages. Their bodies moved to adjacent page modules; global/cluster routes and search schemas retain their behavior. The bootstrap chunk fell from 743,313 to 461,261 raw bytes. Final eager gzip totals are 325,255 bootstrap /353,320 login /423,195 app. No budget ceiling increased; four complexity entries moved with unchanged limits, and the create-editor limit subsequently decreased.

The first combined browser run passed 121 cases and failed 13. Twelve failures were incomplete fixtures or ambiguous selectors in new acceptance cases: header notification queries versus alert history, namespace inventory for CR deletion, the actual workload-pods read for parent denial, and a duplicated template label. One failure exposed a real early-mode-switch race in resource creation. Initialization now has independent ready/error state, blocks early editing, supports Retry, and preserves recovery after an empty YAML object. Three new unit regressions cover those paths. Later static-test failures referenced moved page modules; only their paths were corrected. Failed logs remain beside passing evidence.

Sixty fresh screenshots are retained under `screenshots/`. Root review inspected the final shell at 390/412/768/1024/1280, mobile pipeline page-five actions, and installed-template navigation, in addition to the earlier reviewed alert, app, restore, restricted-role and Metrics captures. The template navigation screenshot includes an editor still loading and proves entry/header/action reachability only; it is not evidence of editor readiness. Automated geometry checks cover individual controls, focus return, overlays and closed-sidebar inertness.

The [earlier phase mapping](../c698663a/README.md) identifies the implementation/test files for phases 0–12. Its pending-case notes are historical: all queued deployment, CR-delete, template, keyboard command, log/history, missing/denied alert and permission-change cases now pass in the runs above. The [current scope matrix](../../../route-scope-matrix.md) covers all 146 route fixtures and intentional omissions.

## Qualification boundary

Plan 027 is complete for navigation/workflow implementation and its API dependencies. Plan 028's 174-row all-offerings campaign remains NOT_RUN. Real install/upgrade/uninstall, identity, metrics/log delivery, restored bytes and other functional canaries must still be proved through supported APIs in disposable environments. Fixture results, HTTP acceptance and static gates do not qualify those outcomes. Protected live/release/assistive-technology qualifications remain with their owning plans.
