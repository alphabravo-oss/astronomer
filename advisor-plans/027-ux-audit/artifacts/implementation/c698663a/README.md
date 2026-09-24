# Plan 027 implementation browser evidence

Production frontend source: `c698663a` (metrics read-state correction), following `d2a19257` (mobile logging action layout) and `c879acf6` (scope/navigation completion). API implementations and final combined enterprise gates belong to the parent integration branch. These browser runs use intercepted canonical API fixtures; they do not qualify live extensions or restore a live cluster. Plan 028 remains separate.

## Results and provenance

| Run | Source / test checkpoint | Actual result |
|---|---|---|
| Initial complete operator diagnostic | production `c879acf6`, tests through `619a8ee0` plus pending navigation strengthening | 62 passed, 12 failed; fixture/assertion corrections followed. This is not acceptance evidence. |
| Corrected affected specifications | production `d2a19257`, tests `ba2b805e` | 30 passed, 4 failed; all four were the Delivery project fixture still missing required quota data. |
| Operator + existing keyboard/resource/pod journeys | production `d2a19257`, tests `91cc1289` | 100 passed, 2 failed (3.2m). Both failures were the legacy keyboard restore fixture lacking the new restore-list contract. All operator specifications passed. |
| Corrected keyboard restore | production `d2a19257`, tests `c895bbc5` | 4 passed (6.0s): two role setup cases plus desktop/mobile keyboard restores. |
| Existing explorer navigation axe | production `d2a19257`, tests `c895bbc5` | 4 passed (8.0s): two setup cases plus desktop/mobile CR navigation and serious/critical axe assertions. |
| Final summary/metrics + responsive shell | production/tests `c698663a` | 12 passed (34.4s): two setup cases plus desktop/mobile summary drills, metrics403 recovery, five viewport widths, and long labels. |

Counts include Playwright's role-auth setup cases. They must not be added together as a count of unique workflows. There is no claim that the 102-case invocation itself exited successfully: its two fixture failures were repaired and independently rerun successfully. The subsequent production change was limited to metrics read-state handling and was verified with the final summary/shell batch.

Fresh production builds ran `npm run build`, which includes `tsc --noEmit`, Vite and the CSP build check; all passed at the final three source checkpoints. Final Vite build took 5.94s. The complexity check passed without increasing any ceiling. The final changed-file formatting check covered 123 handwritten frontend files changed since `89229ce5`, excluding canonical generated sources, and passed. Focused unit evidence retained here is 27 tests across six files passing; broader complete frontend gates are owned by the parent and are not claimed by this report.

## Exact browser commands

Working directory was `frontend`; Node was 24.21.0. Each batch used one worker. A dedicated preview on port32128 was started from that checkpoint's newly built `dist`, reused only for the identified batches, and stopped afterward. It was not a pre-existing shared server.

```sh
export PATH=/root/.npm/_npx/538786c08bcb9442/node_modules/node/bin:$PATH
npm run build
npx vite preview --host 127.0.0.1 --port 32128
```

In a second process, with `CI=1 PLAYWRIGHT_PORT=32128 PLAYWRIGHT_REUSE_EXISTING_SERVER=1`:

```sh
PLAYWRIGHT_OUTPUT_DIR=test-results/operator-acceptance npm run test:e2e -- operator- pod-drilldown.spec.ts resource-drilldown.spec.ts critical-workflows-keyboard.spec.ts resource-actions-keyboard.spec.ts --workers=1
PLAYWRIGHT_OUTPUT_DIR=test-results/operator-keyboard-final npm run test:e2e -- critical-workflows-keyboard.spec.ts --grep 'snapshot restore' --workers=1
PLAYWRIGHT_OUTPUT_DIR=test-results/operator-explorer-axe npm run test:e2e:smoke -- explorer-navigation.spec.ts --workers=1
PLAYWRIGHT_OUTPUT_DIR=test-results/operator-metrics-final npm run test:e2e -- operator-summary-navigation operator-shell-responsive --workers=1
```

The npm pre-scripts regenerated canonical stubs/route manifests. Raw command logs, including earlier failures, are retained under `logs/`.

## Phase mapping to actual tests

Paths below are relative to `frontend/`. Consolidated specifications replace several proposed one-purpose filenames in the original plan.

| Phase | Implementation and actual verification surface |
|---|---|
| 0 | Parent verified the original 65 navigation tests and baseline type/lint. Actual non-superuser browser identities override `/auth/me`, not only browser storage: `tests/e2e/operator-role-contract.spec.ts` and `operator-restricted-navigation.spec.ts`. |
| 1 | Shared asynchronous project transaction, authoritative per-cluster project namespace scopes, explicit empty/single/multiple selections before server paging, canonical complete CR discovery, and real repeated query serialization. `src/lib/cluster-scope.test.tsx`, `src/lib/api/projects-generated.test.ts`, `transport-query.test.ts`, `resources-discovery-pages.test.ts`, `src/components/clusters/custom-resource-list.test.tsx`, and `tests/e2e/operator-scope-consistency.spec.ts`. The browser asserts actual request namespace parameters, deferred secondary-project reads, refresh, no empty-scope requests and deleted stored namespaces. |
| 2 | `src/components/resources/create-resource-dialog.test.tsx` verifies edited guided retry and mixed multi-document retry POST bodies while successful documents are retained. `tests/e2e/operator-failure-recovery.spec.ts` verifies canonical CR Back, Velero403, SMTP403, disabled draft test-send then saved-config test, and Delivery target403/404. The additional deployment and CR-delete cases described below are pending the parent's run. |
| 3 | `tests/e2e/operator-resource-history.spec.ts` verifies effective Crash Loop Back Off despite phase Running. `operator-summary-navigation.spec.ts` verifies keyboard summary drills and actual node/namespace destinations; native Metrics is reachable through explicit Overview/Grafana views. |
| 4 | `operator-navigation-contract.spec.ts` verifies all eight Delivery destinations retain project context and unique active links on cluster/global pages; restricted-role specifications distinguish list grants, read grants and inventory scope. Persistent Onboarding template entry and the exact Shared stacks active-state rule are source-reviewed; the retained browser loop does not directly assert the installed-tools template state or `/dashboard/monitoring/stacks`. |
| 5 | `src/components/layout/cluster-navigation-transition.test.ts` plus `tests/e2e/operator-cluster-transition.spec.ts`: safe destination mapping, latest rapid selection wins after awaiting the late response, invalid remembered-project clear recovery. `operator-navigation-contract.spec.ts` verifies visible page navigation beyond 40 discovered types without unbounded count requests. |
| 6 | `src/components/resources/resource-navigation-context.test.tsx`, `src/components/workloads/pod-logs-viewer.test.tsx` and `operator-resource-history.spec.ts`: validated internal origins, URL tab replacement, Events/Logs refresh, workload-to-pod push and browser Back. Existing `pod-drilldown.spec.ts`, `resource-drilldown.spec.ts`, and resource keyboard journeys passed in the combined batch. |
| 7 | `operator-alert-investigation.spec.ts`: full-message deep links, associated cluster/rule/metrics navigation, Active firing filter, durable URL inspection and truthful mutation failure. Long modal title/close controls are tested unobscured at desktop/mobile sizes. |
| 8 | `operator-app-release-lifecycle.spec.ts` plus `src/lib/api/operator-workflow-contracts.test.ts` and `src/lib/use-operation-intent.test.tsx`: install/upgrade/uninstall receipts, wrapped operations/values, saved-values preservation, denied reads, operation revisit and owner-aware release inspection. Fixtures keep list/detail owners and operation types consistent. |
| 9 | `operator-pipeline-editing.spec.ts`: same-ID PUT preserving labels and opaque filters, unsaved navigation, paged201st pipeline, canonical collection return/reload retaining page5, and mobile action rectangles. |
| 10 | `operator-snapshot-restore.spec.ts`: accepted/queued receipt, source and target identities, public restore detail/history, cross-cluster redirect, failed poll and API readback, remaining accessible when Velero status fails. `critical-workflows-keyboard.spec.ts` now uses the canonical list/detail/receipt contracts and its restore flow passed after fixture correction. |
| 11 | `operator-shell-responsive.spec.ts`: all required loaded controls, open notification/account popovers, Escape/focus return, closed-sidebar inertness and keyboard exclusion, long names, widths390/412/768/1024/1280. `src/components/ui/__tests__/modal-shell.test.tsx` covers portal/focus behavior; explorer axe passed in expanded mobile and collapsed desktop navigation. |
| 12 | Final build/type/CSP, focused tests, complexity and changed-file formatting passed here. The parent owns complete frontend enterprise/unit/lint/bundle gates, backend gates, combined artifact regeneration, final route matrix and plan status. No live offering qualification is implied. |

## Additional original-criterion tests pending parent execution

Test-only checkpoint `d2e9389c` adds three scenarios to `operator-failure-recovery.spec.ts`:

- Delivery deployment403 and404 on the actual detail route, with no Reconcile/Suspend/Advanced diagnostics controls and a scoped return link.
- Successful custom-resource DELETE returning to its canonical group/version/plural collection while retaining the project and namespace query.

These tests share a complete project fixture checked against the generated Project type. They were written after the executor lane was released; the parent will execute them on the final combined branch. They are not included in any passing count above.

Test-only checkpoint `500bd0b1` additionally includes `/dashboard/monitoring/stacks` in the unique-active-link browser loop and verifies the Management Onboarding template link after the actual tools-status API reports installed tools, followed by the applied-template page. These additions also await the parent’s final combined browser run.

The phase audit does not imply exhaustive combinations: the retained new browser runs do not separately assert duplicate Overview keyboard command selection, multi-container/tail/log-filter refresh, permission-change/deleted-parent variants, or missing/denied alert destinations. Those distinctions were sent to the parent for final acceptance coverage review; core journeys and related adapter/permission/internal-origin unit tests are listed above.

## Screenshot handling

88 PNGs are retained. `operator-metrics-final/` is the final `c698663a` source capture, including all five viewport widths and the metrics permission/Retry state. `operator-acceptance/` preserves workflow evidence from `d2a19257`, including corrected pipeline action wrapping, full alert investigation, release owner/values diagnostics, restore readback and role-specific navigation. `operator-explorer-axe/` contains the axe-reviewed CR navigation. `diagnostics/` preserves failed-run screenshots; they are intentionally not presented as successful outcomes. The two legacy keyboard failure screenshots also remain beside the raw102-case run so its failure record is not hidden.

Visual inspection confirmed settled1280 shell controls, long-name390 controls, the corrected mobile pipeline actions and the mobile metrics error/Retry state. Parent/independent review additionally inspected the workflow screenshots and prompted the logging wrap, overlay portal and consistent fixture corrections. Raw traces remain in the corresponding ignored Playwright output directories for deeper diagnosis; retained PNGs and logs are independent of those directories.
