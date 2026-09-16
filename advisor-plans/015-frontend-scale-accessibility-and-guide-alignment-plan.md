# Frontend scale, accessibility, and guide-alignment plan

**Status:** IN PROGRESS — priority 5 of 5

**Planned at:** `32100314e081e635e89f43fcf14673a25449ef63` on 2026-09-16, against the current dirty working tree

**Branch:** `advisor/015-frontend-scale-a11y`

**Depends on:** Plan 011 green tests; API pagination contracts from Plan 014

**Product boundary:** equal-or-better operator experience for adopted clusters using Flux; no cluster provisioning and no Fleet.

## Implementation progress (2026-09-16)

- Dashboard children, extensions, feature queries, and SSE transports are now
  held behind current-user resolution and the forced-password gate.
- Known feature routes render loading/unavailable states and do not mount their
  protected branch until flags are known; current-user and feature-flag reads
  now propagate cancellation.
- The authoritative frontend build now runs TypeScript checking before Vite,
  and the rollout route regression fixture is router-independent.
- Server-driven estate pagination/search, the full accessibility pass, Node 24,
  reusable Playwright auth state, and technology ADRs remain outstanding.

## Objective

Make the daily operator UI truthful at 2,000-cluster scale, block restricted sessions/routes before they mount, meet keyboard/screen-reader expectations on benchmark-critical flows, and align the toolchain with the technology guide or record reviewed deviations. Completion means no collection silently truncates, action pickers can find every authorized cluster, core requests cancel, and the frontend gate includes type checking plus zero-retry Playwright coverage.

## Findings covered (global rank 41–50)

| Rank | ID | Finding | Primary evidence |
|---:|---|---|---|
| 41 | FE-SCALE-01 | Cluster inventory and overview stop at 100, corrupting rows and aggregate totals | `frontend/src/routes/dashboard/clusters/index.tsx:40-64,300-368`; `frontend/src/routes/dashboard/index.tsx:37-39,62-140` |
| 42 | FE-SCALE-02 | Workload kind tabs filter a 20-item mixed page; all-workloads silently stops at 200 | `frontend/src/lib/api/workloads.ts:237-264`; `frontend/src/components/resources/resource-list-page.tsx:206-219`; `frontend/src/routes/dashboard/clusters/$id/workloads/index.tsx:21-92` |
| 43 | FE-SCALE-03 | Alerting discards the page envelope and surfaces only the first 20 events/counts | `internal/handler/alerting_events.go:17-63`; `frontend/src/lib/api/alerting.ts:214-227`; `frontend/src/routes/dashboard/alerting/-events-tab.tsx:13-20,129-143` |
| 44 | FE-SCALE-04 | Action selectors preload only 50–200 clusters; a request for 1,000 is clamped to 200 | `frontend/src/routes/dashboard/catalog/-install-chart-modal.tsx:52-55`; `frontend/src/routes/dashboard/security/scans/new/index.tsx:49-59`; `frontend/src/routes/dashboard/rbac/-binding-modal.tsx:35-41`; `frontend/src/routes/dashboard/clusters/register/index.tsx:30-33`; `frontend/src/lib/api/clusters.ts:100-117` |
| 45 | FE-AUTH-01 | Forced-password users mount dashboard children, extensions, queries, and SSE before effect redirect | `frontend/src/routes/dashboard/route.tsx:38-51,132-203,241-270` |
| 46 | FE-FLAG-01 | Known feature routes fail open while flags load or fail | `frontend/src/routes/dashboard/route.tsx:143-153,241-247,274-303` |
| 47 | A11Y-01 | Mutation dialogs lack form semantics; logs/scope controls have unnamed controls, broken focus, and color-only state | `frontend/src/routes/dashboard/alerting/-rule-modal.tsx:90-120,406-420`; `frontend/src/routes/dashboard/rbac/-binding-modal.tsx:120-139,300-316`; `frontend/src/routes/dashboard/catalog/-install-chart-modal.tsx:134-156,285-308`; `frontend/src/components/workloads/pod-logs-viewer.tsx:154-302`; `frontend/src/components/layout/cluster-scope-controls.tsx:31-403` |
| 48 | FE-TYPE-01 | Handwritten `| string` unions/casts weaken generated contracts and core queries omit cancellation | `frontend/src/types/clusters.ts:7-75`; `frontend/src/types/wire-contract.ts:63-90`; `frontend/src/lib/hooks/clusters.ts:66-87` |
| 49 | FE-TOOL-01 | Node 22 is below guide baseline; build omits type-check; live Playwright repeatedly logs in and waits on rate limits | `frontend/package.json:6-12`; `frontend/Dockerfile:3-4`; `frontend/playwright.config.ts:36-113`; `frontend/tests/e2e-live/live.helpers.ts:19-43` |
| 50 | ADR-DX-01 | Major stack deviations and local-dev/lint conventions lack accepted decisions or guide-equivalent controls | `docs/architecture/decisions/flux-native-delivery.md`; `frontend/package.json:35-61`; `frontend/src/lib/api/generated/client.ts:1-6`; `.golangci.yml:1-61`; no `.air.toml`/Air config |

## Out of scope

- Recreating every Rancher screen or importing Rancher code.
- Cluster creation/provisioning UI, Fleet concepts, or legacy Argo views.
- Migrating from TanStack Router/Form or REST/OpenAPI solely for stylistic conformity. First write an ADR and migrate only if evidence justifies it.
- Client-side aggregation of full estates as a substitute for API aggregates.
- Accessibility claims based only on static linting; benchmark-critical flows require keyboard and assistive-technology evidence.

## Preflight

1. Preserve the dirty tree. Re-run Plan 011 frontend gate and stop if it is red.
2. Record network responses and rendered counts with fixtures at 0, 1, 20, 21, 100, 101, 200, 201, and 2,001 records.
3. Inventory every `useClusters` consumer, eager `<select>` option list, collection adapter that drops pagination, broad manual cast, and Query function that ignores `signal`.
4. Establish authoritative server aggregate/count endpoints before changing overview tiles. A page length is never a fleet total.
5. Run the current keyboard path and axe checks on cluster scope, workload logs, rule creation, RBAC binding, and chart installation; retain the baseline report.

## Implementation steps

### 1. Make cluster inventory and overview server-driven

- Connect cluster inventory to the existing controlled `DataTable.serverSide` contract: server search, filters, stable sort, page/cursor, page size, total/has-next.
- Put project/cluster/namespace scope into query keys and API parameters. Reset cursor when filters change.
- Add authoritative overview aggregates for total clusters, status buckets, nodes, pods, and warnings, scoped by the same authorization rules. Do not sum the visible page.
- Keep URL state shareable and back/forward safe. Add 101/201/2,001-record tests and race tests for rapid filters.

### 2. Correct workload filtering and pagination

- Send kind, namespace, search, sort, cursor/page, and size to the API. Filter before pagination on the server or adopted-cluster request, not after receiving a mixed page.
- Preserve continuation metadata through generated client → adapter → hook → table.
- Remove the 200-item all-workload ceiling or make it a page size cap with navigation.
- Test a Deployment that appears after mixed page 1 and after item 200, namespace switching, partial cluster errors, and authorization filtering.

### 3. Preserve alert page/aggregate contracts

- Return the generated page envelope from the alert adapter and wire it to table controls.
- Add an authorization-scoped aggregate endpoint for header/dashboard firing/critical counts.
- Ensure SSE updates invalidate/update the correct page and aggregates without duplicating events.
- Test a critical alert beyond item 20 and concurrent event insertion.

### 4. Replace eager action selectors with remote search

- Standardize on the existing paged `useClusterSearch` pattern for catalog install, security scans, RBAC binding, logging, monitoring, tools, and similar actions.
- Debounce, cancel stale requests, preserve a selected ID even if it is outside the current page, and render explicit loading/error/no-access states.
- For registration duplicate checks, add a targeted server uniqueness lookup rather than downloading all clusters.
- Test selection at record 2,001, duplicate names, revoked access during search, and keyboard operation.

### 5. Gate forced-password sessions in route bootstrap

- Resolve current user in `beforeLoad`/loader before any dashboard child, ExtensionProvider, feature query, or live-event transport mounts.
- Redirect forced-password users to the rotation route and seed Query cache for allowed sessions to avoid duplicate fetch.
- Backend must continue enforcing the flag for sensitive operations; client routing is not the security boundary.
- Add browser tests proving no child API/SSE/extension request occurs before redirect.

### 6. Make feature routes fail closed

- Maintain a route-to-feature declaration close to route definitions.
- While flags are pending, show a bounded route loading state; on failure, show a retryable unavailable state. Never mount the protected branch until enabled is known.
- Invalidate route availability on live flag updates/session changes and test direct URL entry, slow response, 500, disabled, and re-enabled cases.

### 7. Complete benchmark-critical accessibility

- Let `ModalShell` host/reference a real `<form>` across body/footer. Use submit buttons, associated labels/descriptions, centralized invalid-submit focus, and an error summary for rule, binding, install, and other mutation dialogs.
- Give every log selector and icon control an accessible name; expose toggle state with `aria-pressed`; use a bounded `role="log"`/status announcement without reading every high-volume line.
- Replace manual scope popovers with the established accessible Radix/shadcn combobox/popover primitives. Focus search on open and restore the trigger on close/Escape.
- Add textual/screen-reader health state in addition to color.
- Add axe plus explicit keyboard tests; manually verify with one screen reader for the five benchmark flows.

### 8. Restore generated type contracts and cancellation

- Correct OpenAPI enums first, regenerate clients, then derive UI types from generated schemas. Remove `| string` where the backend is closed-world.
- For deliberately open strings, validate at the wire adapter and map to an explicit `unknown` UI case.
- Enable the disabled cluster wire-contract assertion and add equivalent high-risk resource assertions.
- Thread TanStack Query's `AbortSignal` through cluster, alert, catalog, RBAC, Kubernetes proxy, and all core read adapters. Add a static rule/test preventing new query functions from dropping it.

### 9. Upgrade and tighten the frontend toolchain

- Upgrade Node in `engines`, `.nvmrc`, CI, and the digest-pinned Docker builder together to Node 24 LTS (or Node 26 only after dependency qualification). Refresh the lockfile with the selected runtime.
- Make `npm run build` run type-check before Vite build, or create one authoritative CI command that cannot omit either.
- Add Playwright setup projects and per-role `storageState`; retain fresh-context tests for login/logout/rotation/invalidation. Keep retries at zero.
- Inject build commit/date/Node version alongside app version and expose it in diagnostics. Configure the development server for container use only through an explicit host setting.
- Run full browser, component, bundle, and live suites; record duration/flakes before and after.

### 10. Record and enforce technology decisions

- Add accepted ADRs for TanStack Router vs React Router, TanStack Form vs RHF/Zod, REST/OpenAPI/Axios vs Connect-Web, `slog` vs zerolog, `golang-migrate` vs Goose, Alpine vs minimal runtime images, and OTLP/HTTP vs gRPC. One ADR may cover a cohesive stack if each trade-off and compensating control is explicit.
- Add Air configuration for server/worker/agent local loops or document an equivalent reproducible live-reload workflow.
- Bring golangci-lint v2 configuration to an explicit STANDARD policy with read-only module downloads, formatter checks, and agreed complexity thresholds. Baseline existing findings rather than disabling categories globally.
- Add a root `make check`/documented one-command fast gate and a non-mutating `make updates` report. Split or replace oversized shell orchestration over time; new Bash must use strict mode and shellcheck.
- Link every accepted deviation from architecture docs and set a review trigger/date. Do not claim guide alignment by silently ignoring canonical choices.

## Verification

```bash
cd frontend
npm ci
npm run type-check
npm run lint
npm test
npm run build
npx playwright test
```

Then run backend/OpenAPI generation and repository gates:

```bash
go test ./... -count=1
go vet ./internal/... ./cmd/...
make verify
```

Required evidence:

- fixture/browser runs at 101, 201, and 2,001 clusters/resources;
- no overview number is derived from visible page length;
- no protected child request occurs for forced-password or disabled-feature sessions;
- axe has no serious/critical findings on the five benchmark flows and manual keyboard/screen-reader notes are attached;
- Playwright retries remain zero and shared auth state removes repeated rate-limit waits;
- generated client/type drift check is green.

## Commit sequence

1. `feat(ui): page cluster inventory and aggregates`
2. `fix(ui): filter workloads before pagination`
3. `fix(alerting): preserve event page metadata`
4. `feat(ui): search all authorized action targets`
5. `fix(auth): gate dashboard before route mount`
6. `fix(flags): fail closed before feature route mount`
7. `fix(a11y): make operator forms and controls semantic`
8. `refactor(ui): derive types and propagate cancellation`
9. `build(ui): upgrade node and reuse browser auth state`
10. `docs(adr): record technology guide deviations`

## Done criteria

- Cluster, workload, and alert views remain complete/truthful beyond 2,000 records.
- Every action selector can find any authorized target without eager estate download.
- Forced-password and disabled-feature sessions cannot mount protected route infrastructure.
- Critical dialogs, scope controls, and log inspection pass keyboard, axe, and manual assistive checks.
- High-risk UI types derive from generated contracts and core reads cancel on navigation/filter change.
- Node/build/CI versions agree; the authoritative build includes type checking.
- Live Playwright uses reusable role state while auth-specific tests remain isolated; retries are zero.
- Every material technology-guide deviation has an accepted ADR and compensating verification.
- The paired Rancher benchmark is rerun after these changes; equal-or-better is claimed only if the documented automated and human thresholds pass.

## Stop conditions

- Stop if an aggregate endpoint would expose counts outside the caller's project/cluster/namespace scope.
- Stop before changing a public pagination envelope without a generated-client compatibility plan.
- Stop a framework migration unless an accepted ADR demonstrates that compensating controls cannot meet the requirement.
