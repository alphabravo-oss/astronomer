# UX/navigation review against Rancher — 2026-09-24

> First-pass review. The [second review](./027-ux-second-review.md) found additional scope, recovery and responsive defects and supersedes this report's priority/confidence assessment. Use [Plan 027](./027-operator-navigation-and-workflow-closure.md) for implementation scope and verification.

## Verdict and scope

Astronomer has a sound shell and substantial page coverage. The next improvement should make existing pages easier to find and connect investigation steps. The evidence does not justify another broad visual redesign or adding every Rancher feature.

Reviewed the current Astronomer working tree, including uncommitted changes, based on HEAD `22f633ec`; compared with the local Rancher Dashboard source at `708d60f`. Reviewed the earlier UI-overhaul decisions and current Plan 026 ledger. Visual inspection used stored desktop/mobile regression screenshots from September 22, not a newly captured live deployment. Current source corroborates the structural observations below; screenshots do not certify the current deployed appearance.

Focused on navigation, page inclusion, cluster context, workload investigation, Apps, monitoring, and alerts. This is not a backend/security/performance audit, exhaustive route audit, live installation test, accessibility certification, or observed usability study. No source files were changed.

Rancher's official [UI walkthrough](https://extensions.rancher.io/internal/getting-started/ui-walkthrough) describes its global management versus cluster-explorer distinction. Its [products/navigation documentation](https://extensions.rancher.io/internal/code-base-works/products-and-navigation) explains resource grouping and conditional navigation. The detailed comparison below is grounded in the checked-out source, rather than a claim that both products' latest deployed versions were exercised.

## Comparison of sidebar and page coverage

| Area | Rancher reference | Astronomer now | Recommendation |
|---|---|---|---|
| Global versus cluster scope | Management/product navigation and cluster explorer | Separate global and cluster sidebars; always-mounted switcher, pinned/recent clusters, project/namespace controls | Keep this model. Make switching predictable from detail pages. |
| Kubernetes resources | Cluster, Workloads, Service Discovery, Storage, Policy, additional resources | Broadly the same taxonomy, with explicit RBAC, discovery-aware custom types, favorites and counts | Keep the familiar groups. Avoid rearranging resource types just to appear different. |
| Page finding | Visible Jump to input with parent paths | Resource search plus a keyboard-only page palette; repeated Overview labels lack parent descriptions | Expose the existing palette as a clickable/touchable control and qualify results. |
| Delivery | Separate product navigation for Rancher's delivery model | Flux-native cluster and project delivery pages; global sidebar lists three destinations but page navigation exposes eight | Keep Flux and reconcile the navigation registry with the available pages. |
| Apps | Charts, installed apps, operations and repositories in Apps navigation | Installed/Browse/Recommended/Repositories, Tools management pivots, plus a global Catalog | Preserve ownership distinctions, but complete operation progress and release inspection from cluster Apps. |
| Troubleshooting | Resource list/detail and contextual action conventions | Workload pods, logs, metrics, YAML, conditions, events, relationships, rollout history and pod exec already exist | Connect them with durable URLs and reliable return links. |
| Observability | Monitoring/logging products alongside explorer | First-class Metrics, Monitoring Stack, Grafana when available, Alerting and Logging | Page coverage is strong. Improve alert investigation and links from utilization summaries. |
| Cluster lifecycle | Includes provisioning concerns | Adoption-first product with tools, onboarding, cluster operations and backup surfaces | Do not add provisioning/Fleet navigation for parity. |

Rancher evidence: `../rancher-dashboard/shell/config/product/explorer.js:97` (service discovery), `:104` (storage), `:112` (workloads), `../rancher-dashboard/shell/config/product/apps.js:47` (Apps destinations), `../rancher-dashboard/shell/components/nav/NavActionBar.vue:443` (Jump to), `:519` (parent paths).

Astronomer evidence: `frontend/src/components/layout/cluster-nav-groups.ts:119`, `frontend/src/components/layout/sidebar-navigation.ts:106`, `frontend/src/components/layout/use-sidebar-navigation.ts`, `frontend/src/components/layout/cluster-switcher-menu.tsx`, `frontend/src/components/resources/resource-detail-tabs.tsx:31`.

## Vetted findings

Effort: S = hours, M = roughly a day, L = multiple days, including appropriate verification. Estimates are approximate. Risk concerns the proposed change, not the severity of the existing behavior. All findings are source-confirmed; specific live manifestations remain untested in this review.

| ID | Finding | Category | Impact | Effort | Fix risk | Confidence | Key evidence |
|---|---|---|---|---|---|---|---|
| 01 | Show effective pod status in workload Pods | Correctness / UX | Failure reason can be hidden behind Running | S | Low | High | `workload-resource-tabs.tsx:71` |
| 02 | Reconcile Delivery sidebar, tabs and scope | Navigation | Available destinations cannot be found consistently; selector scope is ambiguous | M | Medium | High | `sidebar-navigation.ts:138`, `delivery/route.tsx:13` |
| 03 | Make cluster switching detail-aware | Navigation | Carries old object identity into the next cluster | M | Medium | High for carryover | `cluster-switcher-menu.tsx:249` |
| 04 | Make operational summaries navigable | Navigation | Users must find the same node/namespace again elsewhere | S | Low | High | `cluster-metrics-page.tsx:63`, `:144` |
| 05 | Give alerts an investigation path | Workflow | Easier to acknowledge/resolve than inspect the cause | M | Low | High | `alerting/-events-tab.tsx:59` |
| 06 | Preserve resource investigation context | Navigation | Logs/Events cannot be shared; page Back loses the originating workload | M | Medium | High | `resource-detail.tsx:56`, `:156` |
| 07 | Complete the cluster Apps operation loop | Workflow | Dispatch feedback ends at a toast; no release inspection link | M, L for full detail | Medium | High | `api/cluster-apps.ts:234`, `app-install-modal.tsx:208` |
| 08 | Expose and clarify quick page navigation | Discoverability | Pointer/touch access missing; some results are ambiguous or omitted | S–M | Low/medium | High | `command-palette.tsx:13`, `command-palette-dialog.tsx:255` |

### 01 — Effective pod status

`frontend/src/components/resources/workload-resource-tabs.tsx:71` renders `pod.phase`. The mapper already provides `pod.status` at `frontend/src/lib/api/workloads.ts:135`, and resource detail derives waiting/termination reasons at `frontend/src/components/resources/resource-detail.tsx:228`.

A pod with phase Running and status CrashLoopBackOff can therefore look less urgent in the workload's Pods table than on its detail page. Display the effective status consistently; keep phase as secondary metadata if needed. Validate healthy, waiting/image-pull, crashing and terminating cases. This is a small change using existing data.

### 02 — Delivery navigation and scope

`frontend/src/components/layout/sidebar-navigation.ts:138` lists only Estate, Templates and Overrides. `frontend/src/routes/dashboard/delivery/route.tsx:13` also exposes Sources, Bundles, Targets, Rollouts and Deployments. Because Estate is exact-matched, those five pages have no matching Delivery sidebar item. `frontend/src/components/layout/command-palette-pages.ts:45` derives global pages from the smaller sidebar registry, so they also lack their own global page-palette entries.

There is a second scope issue: the layout shows a project selector above all these pages (`delivery/route.tsx:62`), but a user with estate access sees a global estate query with no project parameter (`delivery/-page.tsx:67`). Changing project does not filter that estate overview. This needs explicit page-scope labeling or conditional selector placement.

Use one route/label model for all actual Delivery destinations. Prefer the section sidebar for durable destinations and contextual tabs for detail content, avoiding two competing lists. Preserve project selection in links, retain permission checks, and clearly distinguish All-cluster overview from Project delivery. The earlier three-link sidebar was a recorded temporary compromise before global delivery lists existed; the current implementation warrants revisiting it.

Acceptance: each delivery destination is directly discoverable, its section remains visibly selected, and the UI accurately states whether project selection affects the displayed data.

### 03 — Cluster switching

`frontend/src/components/layout/cluster-switcher-menu.tsx:249` copies the whole path suffix and reattaches it to the selected cluster. This includes namespaced resource names and delivery object IDs, not only list types. A switch from cluster A's pod detail can land on a missing pod in B or a different same-named pod, without an explicit comparison action.

Rancher normally uses the selected cluster's default route; retaining the current route is alternate behavior (`../rancher-dashboard/shell/components/nav/TopLevelMenu.vue:583`). Astronomer can retain the useful list-to-equivalent-list behavior while mapping object detail to its parent list. Fall back to cluster overview when the destination lacks the capability. Clear object-specific query values and restore the destination's valid scope. This review establishes unconditional carryover, not a reproduced authorization defect.

Acceptance: switch from a list, pod detail, namespaced workload detail, delivery detail and unavailable add-on page. None should silently retain an inappropriate old object identity.

### 04 — Operational summary links

`frontend/src/components/monitoring/cluster-metrics-page.tsx:63` and `:144` render node/namespace names as plain spans; their tables at `:329` and `:345` have no row navigation. Cluster overview CPU/Memory/Nodes/Pods cards at `frontend/src/routes/dashboard/clusters/$id/-page.tsx:355` have no destination, whereas the adjacent CVE/Tools cards are linked.

Link named nodes/namespaces to their pages, inventory counts to the relevant list, and utilization summaries to Metrics. Use explicit links so keyboard navigation and opening in a new tab work. Preserve applicable cluster and namespace scope. This makes existing overview content useful as a starting point for work.

### 05 — Alert investigation

`frontend/src/routes/dashboard/alerting/-events-tab.tsx:59` renders the rule as text, `:66` truncates the diagnostic message without expansion, `:79` renders cluster as text, and `:103` exposes Ack/Resolve actions. There is no row drill-down. Also, `alerting/-page.tsx:21` calls the initial tab Active Alerts while the table initializes its status filter to all statuses (`-events-tab.tsx:21`).

Provide an inspectable alert view with full message, rule, cluster, timestamps, state and investigation links. Link to workload/resources only when the alert data actually identifies them. Start Active Alerts on unresolved alerts and expose history explicitly. Reuse cluster metrics/rule pages rather than forcing a manual search.

Acceptance: open a long-message alert, read the full diagnosis, navigate to its cluster/rule, and return with filters intact. Resolved history should not appear unexpectedly in the initial Active view.

### 06 — Resource context in URLs

`frontend/src/components/resources/resource-detail.tsx:56` holds the selected tab only in local component state. The page Back destination at `:156` always targets the kind's list. `frontend/src/components/resources/workload-resource-tabs.tsx:54` links a workload's pod to generic detail without preserving its originating view.

Persist validated tab values in the URL using the existing `frontend/src/lib/use-tab-param.ts` pattern. Retain filters/scope and a safe, explicit origin when following workload → pod. Show Back to workload when that relationship is known, alongside canonical resource navigation. Do not automatically reopen an exec session merely because a query parameter requests it.

Acceptance: refresh and share Logs/Events links; browser Back and the page's contextual return both recover the appropriate originating view. Invalid or unauthorized tab values fall back predictably.

### 07 — Cluster Apps progress and release inspection

`frontend/src/lib/api/cluster-apps.ts:234` returns only the installation ID, dropping the operation receipt. `frontend/src/components/clusters/app-install-modal.tsx:208` then shows a dispatch toast, invalidates the installed collection and closes. By comparison, `frontend/src/routes/dashboard/catalog/-install-chart-modal.tsx:101` retains the operation ID for the existing `CatalogOperationTimeline`.

Installed release names are plain text (`frontend/src/routes/dashboard/clusters/$id/apps/-installed-tab.tsx:253`); the ordinary release action area at `:329` provides Upgrade and Uninstall without a release-inspection link. Tools-owned releases correctly have a management pivot; preserve it.

First retain the receipt, show persistent operation status, and provide a direct transition to the installed release. Then add release inspection with namespace/resources, values/version, history, operation diagnostics and an explicit managing system. Reuse the existing progress component. The receipt/pivot work is medium effort; a comprehensive release-detail page is a separate larger increment.

Acceptance: install succeeds, fails, remains pending or loses connectivity. In every case the operator can find the operation and release after the toast disappears, without guessing where to look or being offered the wrong ownership-specific action.

### 08 — Find pages without knowing a shortcut

`frontend/src/components/layout/command-palette.tsx:13` only opens the palette on Ctrl/Cmd+K. The visible search navigates to resource search (`global-search.tsx:44`); the keyboard cue is noninteractive (`:78`). Cluster palette results only show a label (`command-palette-dialog.tsx:255`), so Cluster / Overview and Workloads / Overview appear alike.

Rancher has a visible Jump to input and parent-path result labels (`../rancher-dashboard/shell/components/nav/NavActionBar.vue:443`, `:519`). Add a visible Go to page trigger to the existing palette; include parent section and API group in display and search keywords.

Keep the sidebar bounded, but do not use its display limit as the entire searchable catalog. Current discovery truncates dynamic entries to 40 (`cluster-discovery-navigation.ts:37`); the palette uses those same groups (`command-palette-pages.ts:28`). The all-custom-resources page remains available, so this is extra navigation friction rather than inaccessible resources. Index the complete authorized type model while preserving the sidebar's cap and favorites.

## Visual assessment and responsive follow-up

The stored desktop screenshots show a coherent neutral palette, consistent tables/cards, recognizable active states and sensible separation between navigation and content. The resource taxonomy is easier to scan than a flat list of every route. Preserve the shell and existing shared components.

The stored mobile cluster screenshot shows the header extending beyond the visible right edge while the cluster name collapses to an icon. Current `frontend/src/components/layout/topbar.tsx:220` retains a single-row shell with cluster/project/namespace controls and the actions at `:250`; `header-cluster-actions.tsx:79` keeps full Kubeconfig text plus Import. Treat this as a high-value responsive verification item, not a fresh live-browser reproduction. At 390–412px, prioritize readable cluster/namespace context and put secondary actions in an accessible overflow menu. Verify menus, account/notification access and selected scope, not just absence of page-level horizontal scrolling.

The overview screenshots devote much of the first screen to summary tiles. This is a design opportunity rather than a correctness finding: emphasize actionable exceptions before secondary inventory numbers, particularly on narrow screens. Do not infer stale telemetry defects from deliberately frozen screenshot fixtures.

## Direction options, separate from defects

1. **Make Apps a clearer workspace.** Consider its own cluster sidebar section for Installed, Charts, Repositories and Operations as the surface grows. Keep Apps, operational Tools and Flux Delivery ownership distinct, with plain labels and direct management pivots. Apps/Tools coexistence is a documented decision, not a reason to merge the lifecycles. Global Catalog still has install capabilities despite describing cluster Apps as the browse/install home (`routes/dashboard/catalog/index.tsx:125`); reconcile the product story before removing a route. Trade-off: clearer entry points versus more sidebar rows.
2. **Clarify labels and global administration grouping.** Evaluate All clusters or Delivery overview instead of Estate; Access control instead of RBAC at the global level; Cluster add-ons instead of Tools where accurate. Cluster Agents under global Security is worth a findability check because its work concerns cluster operation as well as security. Keep Kubernetes terminology available for experienced operators. These are testable naming hypotheses, not established bugs.
3. **Prioritize exceptions on overview pages.** Surface unhealthy workloads, failed operations and actionable alerts with links before secondary totals. The existing overview metrics, agent state, alerts and delivery attention data provide grounding. Trade-off: requires a clear priority policy and permission-aware summaries; avoid another duplicate aggregate dashboard.

## Considered and rejected

- Do not reopen the prior broad static-sidebar finding: discovery, resource counts, favorites and permissions are already implemented.
- Do not restore multi-open groups as an assumed improvement: single-open behavior was intentionally restored in the current work.
- HPA in Service Discovery and ConfigMaps/Secrets in Storage are also Rancher conventions; they are not parity defects.
- ReplicaSets under More Resources is a reasonable progressive-disclosure choice; no evidence here warrants promoting it.
- Missing Rancher provisioning/cloud-provider/Fleet pages are justified by Astronomer's adoption-first, Flux-only boundary.
- Existing logs, exec, YAML, relationships, bulk workload actions and form improvements should not be reported as missing.
- Screenshot fixture values and old report findings are not fresh runtime evidence.

## Recommended sequence and verification

Start with truthful pod status, summary links, Delivery navigation consistency and the visible page finder. Make cluster switching and URL-backed detail navigation coherent next; validate those before relying on them for alert and release drill-downs. Restore Apps operation receipts independently, then add richer release inspection. Verify responsive shell behavior alongside shell changes.

Existing focused checks run for this review:

```sh
cd frontend
npm test -- src/components/layout/sidebar-navigation.test.ts src/components/layout/nav-open-groups.test.ts src/components/layout/command-palette.test.tsx src/components/layout/cluster-discovery-navigation.test.ts
```

Result: **4 files, 65 tests passed**. These confirm the existing navigation baseline, not usability or the absence of the findings above. No new tests, source changes, build or live deployment were performed.

For selected implementation work, use the existing `npm run type-check`, `npm run lint`, focused Vitest tests and targeted Playwright journeys; run the required frontend enterprise gate at integration. Do not change snapshot expectations simply to accept a clipped header.

Task-based acceptance should cover:

- Find a failing pod from a workload and explain its actual failure state.
- Open and share its Logs/Events view, then return to the originating workload.
- Switch clusters from both a list and a detail page without inheriting an unrelated identity.
- Install an app and find progress, failure diagnostics and resulting resources after navigation or refresh.
- Open a firing alert and investigate its associated cluster/rule without manually searching for it.
- Find Sources/Rollouts and an installed custom-resource kind with pointer, keyboard and touch.
- Reach cluster scope, Import, notifications and account controls at desktop/tablet/mobile widths.

This report identifies the next changes; it does not create a new execution authority or mark existing plans complete. Implementation plans can be scoped from the selected findings without duplicating Plans 018–026.
