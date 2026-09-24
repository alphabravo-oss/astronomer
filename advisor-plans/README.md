# Advisor plans

This directory now has one active execution authority. Earlier audits and plans
are preserved under [`archive/`](./archive/) with a closeout ledger and residual
mapping.

## Active plans

| Plan | Priority | Status | Next gate |
|---|---|---|---|
| [016 — Production and GA closure](./016-production-ga-closure-master-plan.md) | P0/P1 + release qualification | **IN PROGRESS** | The broad local replay and coherent revision-61 deployment are green for exact production candidate `fe4b51eb`; run the retained estate-100 qualification, then the eight external Phase 6 qualifications. Phase 7 remains separately claim-gated and unauthorized. |
| [UI overhaul status (2026-09-22)](./ui-overhaul-status-2026-09-22.md) | status | **CURRENT** | Branches, merge order, what landed, open follow-ups, execution rules. Read this first. |
| [026 — Platform robustness and workflow closure](./026-platform-robustness-and-workflow-closure.md) | P1/P2 + qualification | **IN PROGRESS** | Expanded audit remediation: agent inventory, safe forms/mutations, truthful states, pagination, integration/workflow closure; external evidence remains under Plan 016. |
| [027 — Operator navigation and workflow closure](./027-operator-navigation-and-workflow-closure.md) | P1/P2 · L | **PLANNED — REVIEWED; implementation not started** | [Second review](./027-ux-second-review.md): scope/pagination, recovery, navigation permissions, responsive shell, investigation and operation continuity. Uses current Plan 026 working state; coordinate overlapping files. Multi-namespace/restore API dependencies are explicit gates; Plan 016 retains release authority. |
| [028 — All-offerings API qualification](./028-all-offerings-api-qualification.md) | P1 · L + live qualification | **PLANNED — independently reviewed inventory and test specification; live execution not started** | [Per-offering test matrix](./028-offering-test-inventory.md): 21 catalog apps, 12 Tools (25 unique names), all provider variants and optional integrations. Require real outcomes, supported API lifecycle/recovery, complete inventory coverage and no workarounds. Plan 016 retains release authority. |
| [017 — UI overhaul review vs Rancher (2026-09-21)](./017-ui-overhaul-review-2026-09-21.md) | audit | **REPORT** | Findings table, screenshot verdict, direction, rejected items. Source of plans 018–025. |
| [018 — Navigation model and labels](./018-navigation-model-and-labels.md) | P1 · L | **DONE** on branch `advisor/018-navigation-model` (stacked on `advisor/p0-ui-slice`; reviewed + approved 2026-09-21 after one revision; gate green incl. 278 smoke + complexity budget). Deviation: Continuous Delivery group lists only Estate/Templates/Overrides — see 017 §7 | Multi-open sidebar, regrouped global nav, delivery estate layout, one label registry → breadcrumbs + `document.title`, palette derived from nav, orphan routes, SSO tab removal, monitoring re-home. |
| [019 — Global shell: cluster switcher, header actions](./019-global-shell-cluster-switcher-and-header-actions.md) | P1 · L | **DONE** on branch `advisor/019-global-shell` (base `advisor/ui-integration` = 021+018; reviewed + approved 2026-09-21 after one revision; adds migration 063 + schema version 63; gate green incl. serial smoke 138+138, go build/vet/tests, sqlc-check, migrations lint, complexity). Also fixes the 021×018 integration axe regression on `/dashboard/delivery/*` | Always-mounted switcher with pinned/recent (adds `pinned_clusters` pref), header kubeconfig + Import YAML, uncrowded cluster topbar, collapsed rail flyout, icon uniqueness. |
| [020 — Cluster explorer nav: discovery + CRD groups](./020-cluster-explorer-nav-discovery-and-crd-groups.md) | P2 · L | **DONE — INTEGRATED** on `advisor/ui-overhaul-integration` at `15025b73`; migration 065/schema 65; all three enterprise scopes, 1,495 unit tests, and 296 browser checks pass; [evidence](./ui-overhaul-validation-2026-09-22.md) | Discovery gating, Gateway regrouping, dynamic CRD subgroups, bounded counts, and persisted starred resource types. |
| [021 — Design-system primitives and enforcement](./021-design-system-primitives-and-enforcement.md) | P1 · M | **DONE** on branch `advisor/021-design-system` (stacked on `advisor/p0-ui-slice`; reviewed + approved 2026-09-21; gate green incl. 278 smoke, 50 visual, complexity budget). Deviations: `TabStrip` omits auto `aria-controls`; `Card` padding defaults to `none` | `ResourceMasthead`, accessible `Tabs`, `<th scope>`, one status badge, card/switch/metric/field variants, tokenized charts, required empty-state actions, lint rules + counting ratchets, `frontend/docs/design-system.md`. |
| [022 — Page migration onto primitives](./022-page-migration-onto-primitives.md) | P2 · L | **DONE** on branch `advisor/022-page-migration` (includes 019 via merge; reviewed + approved 2026-09-22; 46 commits; gate green incl. serial smoke 292/292, complexity, code-health). Deliberately unconverted: 2 collapsible Sections, 4 row-shaped tiles, 6 auth/markdown h1 exemptions, `components/delivery/shared.tsx` button constants | Mastheads, tab bars, raw h1/button/card/table/tile/switch migrations, god-file splits (fixes `nodeActionPending`), modal-vs-route rule for backups/settings. |
| [023 — Truthful states, forms, URL state](./023-truthful-states-forms-and-url-state.md) | **P0** (steps 1–3) / P1 · L | **DONE** — steps 1–3 on `advisor/p0-ui-slice`; steps 4–9 on branch `advisor/023-forms-and-url-state` (base 022; reviewed + approved 2026-09-22; gate green incl. serial smoke 294/294, complexity, code-health). Deviations: mutation lint rule → counting ratchet (28 pre-existing sites); extension not-found renders inline; rollout project seeding descoped (017 §7); registries list has no filter to persist | Delivery overview error roll-up, empty-on-error fixes, register-wizard draft id, unsaved-changes guard, validators + `FormErrorSummary`, widget mutation feedback, router-native Back/URL state, extension `$name` route, error-state test harness. |
| [024 — Rancher parity quick wins](./024-rancher-parity-quick-wins.md) | P1 · M–L | **DONE** — steps 1–2 on `advisor/p0-ui-slice`; steps 3–8 on branch `advisor/024-parity-quick-wins` (base 022; reviewed + approved 2026-09-22; adds migration 064 + schema version 64; gate green incl. serial smoke 292/292, go build/vet/tests, sqlc, migrations lint). Members card is built from project-scoped RBAC bindings; Clone/Download YAML only on the workloads table (follow-up in 017 §7) | Wire Helm upgrade modal, render branding/banners, Clone/Download YAML, inline labels/annotations, project members card, probe + typed-secret fields, provider column + "showing N of M", `rows_per_page`/`date_format` prefs. |
| [025 — Guided form depth and table parity](./025-guided-form-depth-and-table-parity.md) | P2 · L | **IN PROGRESS — VIA 026**, source implemented and locally verified for the bounded workflows | Indexed fields, namespace grouping, bulk restart/scale and common related resources implemented; advanced affinity/Ingress unions and `questions.yaml` ADR remain. See Plan 026 for exact evidence and residual scope. |

Status values: `TODO — READY`, `TODO (after …)`, `IN PROGRESS`, `BLOCKED` with a
concrete reason, or `DONE` with retained evidence links. Executors update their
own row.

### UI overhaul execution order (plans 017–025)

1. **023 steps 1–3** (truth bugs; small PR, no dependencies) and **024 steps 1–2** (finished features that are never rendered) — ship first.
2. **021** (primitives + ratchets) in parallel with **018** (navigation model).
3. **019** (shell/header) after 018; **022** (migrations) after 021 + 018; **023** remainder any time.
4. **020** (explorer nav) after 019; **024** remainder after 021; **025** last.

These plans touch `frontend/` (plus three small `UserPreferences` schema additions) and do not overlap Plan 016's remaining scale/external-evidence gates. If a Plan 016 phase touches the same frontend files, 016 wins and the UI plan's drift check will flag it.

## Execution order (Plan 016)

1. **Phase 0:** source-control and artifact reproducibility.
2. **Phase 1:** critical runtime lifecycle/readiness.
3. **Phase 2:** refresh/JWT/browser-session/ciphertext boundaries.
4. **Phase 3:** production networking, DR, air-gap, and supply chain.
5. **Phase 4:** durable dispatch, bounded data access, scale, and telemetry.
6. **Phase 5:** estate-scale frontend truth, accessibility, and toolchain.
7. **Phase 6:** eight external release-candidate/GA qualifications.
8. **Phase 7:** destructive physical K3s/RKE2/Constellation campaign only when
   that interoperability claim is in release scope.

Do not execute an archived plan directly. Plans 018–025 were reconciled against
the archived 010 review: 21 of its 24 UI findings are already fixed on `main` and are
not re-planned; the 017 report records what was re-verified and what was rejected.
If new evidence invalidates Plan 016,
reconcile Plan 016 and record the decision in this index rather than reopening a
historical Argo/Fleet path or duplicating a completed finding.

## Completed outcomes that stay closed

- Vite/React/TanStack frontend migration.
- Flux-native v1 delivery replacement; Flux remains the only delivery engine.
- Approved Charlie P1 platform-pack program.
- Audit archive name backfill and guarded cluster-tombstone retention.
- The completed local portions recorded in archived Plans 008 and 011–015,
  enumerated in Plan 016's reconciliation verdict.
- Plan 016 Phases 1–3 are locally implemented. Phase 4 implementation and
  Phase 5 automated implementation/browser qualification are locally complete;
  their remaining scale and manual/external evidence stays open in Plan 016.

## Archive

The [archive ledger](./archive/README.md) records the closeout status of Plans
000–015 and exactly where each unresolved outcome moved. Archived files remain
the decision/evidence record and should not be deleted.
