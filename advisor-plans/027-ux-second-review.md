# Second UX review — evidence and corrections

Date: 2026-09-24. Implementation plan: [027 — Operator navigation and workflow closure](./027-operator-navigation-and-workflow-closure.md).

## Revised assessment

The first review was too reassuring about scope consistency and task completion. The sidebar's broad category coverage is still sound, but there are functional problems underneath it: selected scope does not consistently govern results, some recovery actions repeat the wrong request or route, and successful dispatch does not always lead to a recoverable operation outcome. These should precede general label/visual refinements.

No application source was edited. The review used the current working tree at `22f633ec`, including uncommitted work, local Rancher reference `708d60f`, two independent scoped source audits, a separate cold review of the implementation plan, and fresh Chromium observations of current source with intercepted fixture APIs.

## Fresh browser evidence

The audit server deliberately omits TanStack route generation and production builds so it does not rewrite source. It serves the existing generated route tree and current React/Tailwind source. This verifies interactive source behavior, not production bundling, a deployed backend or real-cluster mutation success. All operational APIs in these checks are fixtures. Development-only chrome is not treated as a production UI finding.

Initial observation run: **3 checks completed**, measuring five widths, inspecting Delivery navigation/palette, and visiting **22 representative page URLs**. Those page visits captured main content/headings/error text and screenshots; they are not 22 complete workflow qualifications.

Targeted reproduction run: **6 checks passed by asserting that the defects occur**. They must be reversed into correct-behavior regression tests during implementation; this is not a green usability gate.

| Reproduction | Observed result | Retained evidence under `027-ux-audit/` |
|---|---|---|
| Header at 390/412/768/1024/1280px | At 390px Import spans x=399–484 and user menu x=572–628. At 1024px cluster/project controls share x=264 and user menu extends to x=1096. | `artifacts/review-measure-current-source-shell-at-five-widths/shell-geometry.json` plus screenshots |
| Custom-resource Back | Certificate detail links to `/clusters/c-smoke-1/certificates`, then renders Unknown resource type. | `artifacts-verified/review-reproduce-custom-resource-back-destination/custom-back.json` |
| Custom-resource namespace selection | Header displays team-a while the list includes cert-team-b from team-b. Request is the unscoped `/apis/cert-manager.io/v1/certificates?limit=50`. | `artifacts-verified/review-reproduce-namespace-disagreement-on-custom-resources/custom-scope.json` |
| Workload pagination with selected namespace | Header displays team-b; workload request has kind/limit/offset/sort but no namespace. Fifty unrelated first-page records are filtered out and the page shows No deployments found, despite the fixture supporting a team-b match. | `artifacts-verified/review-reproduce-filtering-after-workload-pagination/workload-scope.json` |
| Shared stacks selection | Metrics and Shared stacks both carry active styling and `aria-current=page`. Logo/Overview also acquire router active attributes, so correcting CSS alone is insufficient. | `artifacts-verified/review-reproduce-shared-stacks-double-active-navigation/active-links.json` |
| Velero status 403 | The intercepted status request is confirmed to run; the page displays Velero is not installed and Install Velero. | `artifacts-verified/review-reproduce-forbidden-Velero-status-as-not-installed/velero-failed-read.json` |
| Keyboard with mobile sidebar closed | Tab from Skip to main content focuses the hidden logo in the sidebar; rectangle x=-224 to -106.48. | `artifacts-verified/review-reproduce-keyboard--0d434-ering-closed-mobile-sidebar/hidden-sidebar-focus.json` |

The first reproduction attempt had two fixture failures: namespace responses used snake_case fields where the existing adapter requires camelCase. Those failures are retained in `results-reproductions.json` and `artifacts-reproductions/`. Corrected fixtures produced the six verified reproductions in `results-verified.json`. Assertions were not relaxed to hide an application failure. The initial observation result is retained in `results.json`.

Reproduce from repository root in two terminals:

```sh
node advisor-plans/027-ux-audit/source-server.mjs
UX_AUDIT_RUN=repeat node frontend/node_modules/@playwright/test/cli.js test --config advisor-plans/027-ux-audit/playwright.config.mjs --grep reproduce
```

Use an unused `UX_AUDIT_RUN` name to preserve prior artifacts. This runs only fixture-backed browser operations. The server is bound to loopback port 32127 and should be stopped afterward. `source-manifest.json` fingerprints 1,053 current frontend source/test files, including untracked files; no file contents or secret values are included.

## Additional findings and revised priorities

Evidence paths below are relative to `frontend/src/` unless explicitly prefixed otherwise. H = high confidence in source behavior, B = additionally reproduced in the browser. Effort S/M/L includes appropriate verification. Risk is implementation risk.

| ID | Finding and impact | Evidence | Priority / effort / risk / confidence | Plan phase |
|---|---|---|---|---|
| D01 | Workload namespace filtering happens after pagination, creating false empty scoped pages and wrong scope totals. | `components/resources/resource-list-page.tsx:244`, `explorer-data-table.tsx:242`, `lib/hooks/workloads.ts:46` | P1 / M / medium / H+B | 1 |
| D02 | Custom-resource lists ignore shared namespace scope; Image Scans and Installed Apps also have independent/broader scope than header selection. | `components/clusters/custom-resource-list.tsx:46`, `routes/dashboard/clusters/$id/image-scans/index.tsx:94`, `apps/-queries.tsx:27` | P1 / M / medium / H; CR case B | 1 |
| D03 | Apps/Delivery project changes retain the prior project's namespaces; valid secondary-cluster project membership can display as All projects. | `routes/dashboard/clusters/$id/apps/-page.tsx:86`, `components/delivery/shared.tsx:117`, `components/layout/cluster-scope-controls.tsx:115`, `lib/cluster-scope.ts:232` | P1 / M / medium / H | 1 |
| D04 | Delivery navigation requires permissions different from its destinations; a project-only operator can be authorized on the page but lose its navigation entry. | `components/layout/sidebar-navigation.ts:100`, `use-sidebar-navigation.ts:56`, `routes/dashboard/delivery/-page.tsx:71`, `delivery/targets/index.tsx:33`, `lib/permissions.ts:153` | P1 / M / medium / H | 4 |
| D05 | Applied onboarding-template management becomes undiscoverable once optional tools are installed. | `components/clusters/tools-tab.tsx:75,84,99`; template route reapply/detach at `routes/dashboard/clusters/$id/template/index.tsx:165,176` | P1 / S / low / H | 4 |
| D06 | Multiple sidebar destinations identify themselves as the current page. | `components/layout/sidebar-nav-items.tsx:28,39`, `sidebar-navigation.ts:168,175` | P2 / S / low / H+B | 4 |
| D07 | Correcting a guided manifest after an API rejection still retries the saved old failed body. | `components/resources/create-resource-dialog.tsx:225,407,415` | P1 / S / low / H | 2 |
| D08 | Custom-resource Back and delete-success return to an invalid built-in resource URL. | `components/clusters/custom-resources-page.tsx:79`, `components/resources/resource-detail.tsx:156,197` | P1 / S / low / H+B | 2 |
| D09 | Failed reads masquerade as Velero not installed, SMTP unconfigured, or deployment status still loading. | `components/clusters/snapshot-page-hooks.ts:34`, `snapshots-page.tsx:99`, `routes/dashboard/settings/smtp/index.tsx:439`, `delivery/deployments/$deploymentId/index.tsx:145` | P1 / M / low / H; Velero 403 B | 2 |
| D10 | Cluster snapshot restore receipt is discarded and its outcome is not reachable from this workflow. A similarly named general Velero backup restore API is a separate domain. | `components/clusters/snapshot-dialogs.tsx:348`, `lib/api/cluster-velero.ts:242`; contract checkpoint linked below | P1 / L / medium / H | 10, contract gate |
| D11 | Existing logging pipelines expose toggle/delete but no full inspect/edit path; changing collection rules requires reconstruction. | `routes/dashboard/logging/-pipelines-tab.tsx:120,133,154`, `-pipeline-modal.tsx:18,65` | P1/P2 / M / medium / H | 9 |
| D12 | SMTP test sends only recipient and tests saved settings, despite being embedded in the unsaved configuration editor. | `routes/dashboard/settings/smtp/index.tsx:89,95`, `lib/api/settings-email.ts:112` | P2 / S / low for saved-only behavior / H | 2 |
| D13 | Narrow header controls clip/overlap; the initial visual concern is now reproduced, including tablet widths. | `components/layout/topbar.tsx:220,250`, `header-cluster-actions.tsx:79`; geometry above | P1 / M / medium / H+B | 11 |
| D14 | The closed off-canvas sidebar remains keyboard-focusable outside the viewport. | `components/layout/sidebar.tsx:105` renders translated aside without an inert/hidden closed state; browser focus evidence above | P1 / M / low/medium / H+B | 11 |

D01/D02 are UI scope-consistency findings, not claims of a server authorization bypass. D04 is strongest for the verified Delivery contracts; do not generalize every project-role page into an authorization defect. D09 must distinguish recoverable errors from 5xx handled by the global boundary.

The original eight findings remain relevant: effective pod status, Delivery navigation/scope labeling, unconditional object-suffix carryover on cluster switching, missing summary links, alert investigation, URL-backed resource context, cluster Apps receipt/release inspection, and page-palette discoverability. The second pass changes their ordering and connects them to the new scope/recovery issues.

## Contract and plan corrections

- **Multi-namespace workload paging is not a one-line frontend fix.** The current Workloads API takes one `namespace`, and the handler uses it as a namespace path. Do not send a comma list or combine separately paginated pages and claim a globally correct page. Phase 1 separates supported single-namespace repair from a gated multi-scope contract increment.
- **Restore domains must stay separate.** See [restore tracking contract checkpoint](./027-ux-audit/restore-tracking-contract.md). Cluster snapshot `SnapshotRestoreResponse` and general Velero backup `RestoreOperationResponse` are distinct; a shared UUID shape does not make their endpoints interchangeable.
- **Scope fixtures must represent real allowed data.** `seedAuth` overwrites effective permissions and namespace fixtures. Override these after seeding, then assert permitted data actually renders. Merely changing the user role and asserting an empty page would provide false confidence.
- **Route additions require more than a build.** The smoke manifest generator has an expected route count and parameter fixture map. The plan now permits and specifies those source changes, followed by regeneration and tests.
- **History semantics are explicit.** Same-page tab selection replaces history; navigation to a child pushes it. Back restores the parent's tab/filter URL. Do not globally change the shared tab helper.
- **Palette identity must be unique.** Two Overview entries need distinct command values as well as visible parent descriptions.
- **Scope lists now include the actual owners.** Delivery shared scope, pod-log state, route generator/stubs and logging/snapshot components are explicitly covered. The plan contains a transition decision table and pending/error/rapid-switch behavior.

## Coverage and rejected claims

The [route/scope matrix](./027-ux-audit/route-scope-matrix.md) records **22 major families** and the existing **144 representative route records**. Family review covered global/cluster navigation, native/custom resources, delivery/apps/tools/onboarding, observability/security, backup/restore, projects/access/settings/integrations, audit/account/Charlie/extensions and shared forms/overlays. Browser observation covered the listed representative pages and targeted defects; it did not exercise every route or role.

Retained/rejected conclusions:

- Core resource grouping already resembles Rancher and remains useful; missing provisioning/Fleet pages are intentional product boundaries.
- Single-open groups are intentional. Existing discovery/favorites/counts and optional-feature gating are present.
- Management backup, workload snapshots and control-plane DR have separate homes. Legacy redirect routes are not missing navigation products.
- SCIM, compliance baselines and shell audit have parent links; do not add duplicate top-level sections to fix nonexistent orphans.
- Applied template access is specifically lost after installation; the page is not always orphaned.
- Generic networking/storage tables already have scope-aware server paging. The workload and CR gaps do not justify rewriting every table.
- Existing ModalShell/OverlayShell focus management is present. The independent sidebar's closed-state focus behavior is the reproduced issue.
- Tools has explicit status/progress behavior. Cluster Apps has the receipt gap; global Catalog already has a progress component worth reusing.
- All-cluster inventory and project/namespace operation scope are different concepts. Fix labeling/coordination rather than silently changing backend semantics.

Not qualified: live deployment behavior, real install/restore success, every role's backend permissions, all extended CRD schemas, screen-reader/manual accessibility certification, production performance or backend security. These limitations do not weaken the concrete fixture-backed reproductions; they limit the claims made from them.
