> **Archived 2026-11-14. Historical record only.** All execution authority and remaining work moved to [the current working plan](../../CURRENT.md). Historical statuses, commands and checkboxes below are not current instructions.

# Advisor plans

> **Identity scope corrected 2026-10-23:** Dex is being retired in favor of the
> existing AlphaBravo Identity shared login component. [Plan 032 §20.13](032-technology-guide-alignment-and-latest-stable-master-plan.md#2013-shared-identity-adoption-and-dex-retirement--scope-correction-2026-10-23)
> supersedes Dex-hardening continuations below. Shared Identity v0.1.0 is built;
> Astronomer adoption and Dex deletion remain open. Local login is always available; SSO is optional. The former three Dex follow-ups are cancelled as target scope;
> ten replacement integration/removal work packages are open. The 174-case figure
> is the historical matrix, pending regeneration—not a current remaining total.


The current technology-guide and latest-stable modernization program is
[Plan 032](032-technology-guide-alignment-and-latest-stable-master-plan.md).
It preserves CNPG and Astronomer's product architecture, reconciles existing
work, and separates compatible v1 improvements from preparation for v2.
Earlier release and UI programs retain their scoped authority and evidence;
their status rows below are historical until rechecked against current source.
Earlier audits and plans are preserved under [`archive/`](archive) with a
closeout ledger and residual mapping. Release gates follow the current machine
contract and explicitly approved release-specific exceptions.

## October 23 current continuation

Plan 032 [§20](032-technology-guide-alignment-and-latest-stable-master-plan.md#20-v211-system-review-remediation--2026-10-23) now owns all findings from the v2.1.1 system review, detailed tasks R00–R16, dependency order and current execution status. R00 is complete; R01–R03 authentication fixes are implemented and locally verified (`make check` and `make security` pass; 32 explicitly recorded skips). They are not deployed. R04 idempotency, R05 optional-trust backup and R06 standby rollout health are source verified (15,657 passed / 32 skipped / 0 failed; security and Helm gates pass); their deployment checks remain open. R07–R12 source/documentation work is now verified within its bounded scope: widgets, Apps projection, literal configuration keys, lazy bundle fix/CI, strict DR guidance/rehearsal and corrected admission claims. The next-ten continuation is in Plan 032 §20.7. R13 remains open because Monaco public assets vendor an older sanitizer despite a clean npm audit; R14 has residual fixture/timing work; R15 is emergency-latched pending agent ceiling confirmation; R16 awaits an immutable qualified candidate. **Security/release sign-off remains blocked.** Original ui-demo is preserved; supported replacement passed an application smoke but still has the deployed system-rollout health failure; the local R06 fix is not deployed.

Open-plan reconciliation is recorded in §20.2. Notable corrections: Plan 028 has historical inventory-runner evidence, but that runner is absent from current v2.1.1 source and requires reconciliation (full functional matrix still open); Plan 030's old socket-access block is no longer current; Plan 033 implementation evidence exists but lifecycle assurance remains open. Historical status rows below retain their original candidate identities and do not certify v2.1.1. Source implementation is in `/root/work/astronomer-fixes`; this backlog stays in the existing checkout.

## Active plans

| Current modernization plan | Priority | Status | Next gate |
| --- | --- | --- | --- |
| [032 — Technology guide alignment and latest stable](032-technology-guide-alignment-and-latest-stable-master-plan.md) | P0–P2 · whole-product modernization | **IN PROGRESS** on `implement/032-technology-guide` | W00/W05/W06/W09 scoped completion reviewed; selected current toolchains, fixed SQL and signed delivery slices integrated. Actual current/previous PostgreSQL matrix, CNPG18 pool failover and historical-key physical restore pass with explicit synthetic-sample limits. Remaining security, disconnected freshness, pagination, observability, Helm, architecture, runners, catalog and whole candidate qualification continue. [Evidence/task status](032-evidence/README.md). |

### Retained programs and historical status

| Plan                                                                                                                     | Priority                      | Status                                                                                                                                                                                                                                                                                                                                                                                                              | Next gate                                                                                                                                                                                                                                                                                                                                                                                                         |
| ------------------------------------------------------------------------------------------------------------------------ | ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [016 — Production and GA closure](016-production-ga-closure-master-plan.md)                                            | P0/P1 + release qualification | **IN PROGRESS**                                                                                                                                                                                                                                                                                                                                                                                                     | The broad local replay and coherent revision-61 deployment are green for exact production candidate `fe4b51eb`; run the retained estate-100 qualification, then the eight external Phase 6 qualifications. Phase 7 remains separately claim-gated and unauthorized.                                                                                                                                               |
| [UI overhaul status (2026-09-22)](ui-overhaul-status-2026-09-22.md)                                                    | status                        | **CURRENT**                                                                                                                                                                                                                                                                                                                                                                                                         | Branches, merge order, what landed, open follow-ups, execution rules. Read this first.                                                                                                                                                                                                                                                                                                                            |
| [026 — Platform robustness and workflow closure](026-platform-robustness-and-workflow-closure.md)                      | P1/P2 + qualification         | **IN PROGRESS**                                                                                                                                                                                                                                                                                                                                                                                                     | Expanded audit remediation: agent inventory, safe forms/mutations, truthful states, pagination, integration/workflow closure; external evidence remains under Plan 016.                                                                                                                                                                                                                                           |
| [027 — Operator navigation and workflow closure](027-operator-navigation-and-workflow-closure.md)                      | P1/P2 · L                     | **DONE — implementation and acceptance verified**                                                                                                                                                                                                                                                                                                                                                                   | Scope/pagination, recovery, permissions, responsive navigation and operation continuity implemented, including API dependencies. [Final acceptance](027-ux-audit/artifacts/implementation/final/README.md): both enterprise gates, complete route crawl and workflow tests pass. Baseline preserved on main; feature branch retained. Plan028 live qualification and Plan016 release authority remain separate. |
| [028 — All-offerings API qualification](028-all-offerings-api-qualification.md)                                        | P1 · L + live qualification   | **PLANNED — independently reviewed inventory and test specification; live execution not started**                                                                                                                                                                                                                                                                                                                   | [Per-offering test matrix](028-offering-test-inventory.md): 21 catalog apps, 12 Tools (25 unique names), all provider variants and optional integrations. Require real outcomes, supported API lifecycle/recovery, complete inventory coverage and no workarounds. Plan 016 retains release authority.                                                                                                          |
| [029 — Install and functionally qualify every offering](029-two-cluster-offering-install-and-functional-validation.md) | P1 · XL + live qualification  | **READY FOR ENVIRONMENT PREPARATION**                                                                                                                                                                                                                                                                                                                                                                               | Build an internally consistent candidate, adopt two independent K3d members on a separate host, optionally add native K3s, then execute all 174 cases through supported APIs with real canaries and retained evidence.                                                                                                                                                                                            |
| [030 — Efficiency and operational readiness](030-efficiency-and-operational-readiness.md) | P1 · multi-week | **BLOCKED — reviewed branch `implement/030-efficiency` at `d1d4875a`; live access and remaining verification required** | Application telemetry, integration repair, Flux inventory correctness, shared observation, measured UI/backend tuning, sizing, backup/restore and staged qualification. Plan 016 retains release authority; reuse Plans 026–029. |
| [031 — UI refinement and design-system depth](031-ui-refinement-and-design-system-depth.md) | P1/P2 · L | **IMPLEMENTED** (residuals in [after.md](031-evidence/after.md)) | Topbar/table/status/copy fixes, Radix-backed primitives (Tooltip, Popover, Menu, Combobox, Dialog, Skeleton), lint ratchets, page anatomy, resource masthead, DataTable pinning/views/expansion/export, density tokens, component gallery + visual regression. |
| [017 — UI overhaul review vs Rancher (2026-09-21)](017-ui-overhaul-review-2026-09-21.md)                               | audit                         | **REPORT**                                                                                                                                                                                                                                                                                                                                                                                                          | Findings table, screenshot verdict, direction, rejected items. Source of plans 018–025.                                                                                                                                                                                                                                                                                                                           |
| [018 — Navigation model and labels](018-navigation-model-and-labels.md)                                                | P1 · L                        | **DONE** on branch `advisor/018-navigation-model` (stacked on `advisor/p0-ui-slice`; reviewed + approved 2026-09-21 after one revision; gate green incl. 278 smoke + complexity budget). Deviation: Continuous Delivery group lists only Estate/Templates/Overrides — see 017 §7                                                                                                                                    | Multi-open sidebar, regrouped global nav, delivery estate layout, one label registry → breadcrumbs + `document.title`, palette derived from nav, orphan routes, SSO tab removal, monitoring re-home.                                                                                                                                                                                                              |
| [019 — Global shell: cluster switcher, header actions](019-global-shell-cluster-switcher-and-header-actions.md)        | P1 · L                        | **DONE** on branch `advisor/019-global-shell` (base `advisor/ui-integration` = 021+018; reviewed + approved 2026-09-21 after one revision; adds migration 063 + schema version 63; gate green incl. serial smoke 138+138, go build/vet/tests, sqlc-check, migrations lint, complexity). Also fixes the 021×018 integration axe regression on `/dashboard/delivery/*`                                                | Always-mounted switcher with pinned/recent (adds `pinned_clusters` pref), header kubeconfig + Import YAML, uncrowded cluster topbar, collapsed rail flyout, icon uniqueness.                                                                                                                                                                                                                                      |
| [020 — Cluster explorer nav: discovery + CRD groups](020-cluster-explorer-nav-discovery-and-crd-groups.md)             | P2 · L                        | **DONE — INTEGRATED** on `advisor/ui-overhaul-integration` at `15025b73`; migration 065/schema 65; all three enterprise scopes, 1,495 unit tests, and 296 browser checks pass; [evidence](ui-overhaul-validation-2026-09-22.md)                                                                                                                                                                                   | Discovery gating, Gateway regrouping, dynamic CRD subgroups, bounded counts, and persisted starred resource types.                                                                                                                                                                                                                                                                                                |
| [021 — Design-system primitives and enforcement](021-design-system-primitives-and-enforcement.md)                      | P1 · M                        | **DONE** on branch `advisor/021-design-system` (stacked on `advisor/p0-ui-slice`; reviewed + approved 2026-09-21; gate green incl. 278 smoke, 50 visual, complexity budget). Deviations: `TabStrip` omits auto `aria-controls`; `Card` padding defaults to `none`                                                                                                                                                   | `ResourceMasthead`, accessible `Tabs`, `<th scope>`, one status badge, card/switch/metric/field variants, tokenized charts, required empty-state actions, lint rules + counting ratchets, `frontend/docs/design-system.md`.                                                                                                                                                                                       |
| [022 — Page migration onto primitives](022-page-migration-onto-primitives.md)                                          | P2 · L                        | **DONE** on branch `advisor/022-page-migration` (includes 019 via merge; reviewed + approved 2026-09-22; 46 commits; gate green incl. serial smoke 292/292, complexity, code-health). Deliberately unconverted: 2 collapsible Sections, 4 row-shaped tiles, 6 auth/markdown h1 exemptions, `components/delivery/shared.tsx` button constants                                                                        | Mastheads, tab bars, raw h1/button/card/table/tile/switch migrations, god-file splits (fixes `nodeActionPending`), modal-vs-route rule for backups/settings.                                                                                                                                                                                                                                                      |
| [023 — Truthful states, forms, URL state](023-truthful-states-forms-and-url-state.md)                                  | **P0** (steps 1–3) / P1 · L   | **DONE** — steps 1–3 on `advisor/p0-ui-slice`; steps 4–9 on branch `advisor/023-forms-and-url-state` (base 022; reviewed + approved 2026-09-22; gate green incl. serial smoke 294/294, complexity, code-health). Deviations: mutation lint rule → counting ratchet (28 pre-existing sites); extension not-found renders inline; rollout project seeding descoped (017 §7); registries list has no filter to persist | Delivery overview error roll-up, empty-on-error fixes, register-wizard draft id, unsaved-changes guard, validators + `FormErrorSummary`, widget mutation feedback, router-native Back/URL state, extension `$name` route, error-state test harness.                                                                                                                                                               |
| [024 — Rancher parity quick wins](024-rancher-parity-quick-wins.md)                                                    | P1 · M–L                      | **DONE** — steps 1–2 on `advisor/p0-ui-slice`; steps 3–8 on branch `advisor/024-parity-quick-wins` (base 022; reviewed + approved 2026-09-22; adds migration 064 + schema version 64; gate green incl. serial smoke 292/292, go build/vet/tests, sqlc, migrations lint). Members card is built from project-scoped RBAC bindings; Clone/Download YAML only on the workloads table (follow-up in 017 §7)             | Wire Helm upgrade modal, render branding/banners, Clone/Download YAML, inline labels/annotations, project members card, probe + typed-secret fields, provider column + "showing N of M", `rows_per_page`/`date_format` prefs.                                                                                                                                                                                     |
| [025 — Guided form depth and table parity](025-guided-form-depth-and-table-parity.md)                                  | P2 · L                        | **IN PROGRESS — VIA 026**, source implemented and locally verified for the bounded workflows                                                                                                                                                                                                                                                                                                                        | Indexed fields, namespace grouping, bulk restart/scale and common related resources implemented; advanced affinity/Ingress unions and `questions.yaml` ADR remain. See Plan 026 for exact evidence and residual scope.                                                                                                                                                                                            |

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

The [archive ledger](archive/README.md) records the closeout status of Plans
000–015 and exactly where each unresolved outcome moved. Archived files remain
the decision/evidence record and should not be deleted.


#### 2026-10-23 follow-up ten checkpoint

See [Plan 032 §20.8](032-technology-guide-alignment-and-latest-stable-master-plan.md)
and [verification/dispositions](runtime-review/evidence/2026-10-23/followup-ten/README.md). Patched emitted Monaco assets,
three-browser editor/CSP regressions, private Helm/proxy fixtures and real OCI
registry/PostgreSQL ingestion are locally verified. Pagination timing cause,
queued Dex rollout/provider gaps, live Charlie suspension and qualification
runner port remain open. No immutable candidate/release or deployment is claimed;
174 lifecycle cases remain NOT_RUN and original ui-demo is preserved.


#### 2026-10-23 qualification prerequisites checkpoint

[Plan 032 §20.9](032-technology-guide-alignment-and-latest-stable-master-plan.md)
now has an implemented GET-only runner with version, task ownership, independent
host evidence, candidate and transport gates. [Current evidence](runtime-review/evidence/2026-10-23/qualification-ten/README.md)
records six registry passes and 174 NOT_RUN cases. Preflight remains blocked;
replacement and site-demo nodes report the same system UUID. Independent hosting,
dedicated targets, immutable release/deployment and lifecycle qualification remain
required. No original cluster or existing workload was deleted or repurposed.


#### 2026-10-23 Dex durability checkpoint

**Historical/superseded:** see the identity scope correction above; these results do not authorize further Dex implementation or count as replacement acceptance.

[Plan 032 §20.10](032-technology-guide-alignment-and-latest-stable-master-plan.md#2010-dex-durability--next-ten-2026-10-23)
implements attempt/lease fencing for Dex progress and terminal writes, strict queue
payload validation, and real PostgreSQL concurrency/recovery/replay coverage.
[Evidence and final gate outcomes](runtime-review/evidence/2026-10-23/dex-ten/README.md).
One obsolete synchronous-503 test is retired against its active durable replacement;
seven legacy Dex rollout/provider gaps remain. This local batch does not close
immutable-candidate/live acceptance, Charlie suspension, pagination timing cause,
independent-host qualification or any of the 174 NOT_RUN lifecycle cases.


#### 2026-10-23 Dex registration boundary checkpoint

**Historical/superseded:** see the identity scope correction above; these results do not authorize further Dex implementation or count as replacement acceptance.

[Plan 032 §20.11](032-technology-guide-alignment-and-latest-stable-master-plan.md#2011-dex-registration-boundaries--next-ten-2026-10-23)
adds fail-closed provider lookup and pre-Kubernetes payload/credential-pair validation.
Real PostgreSQL also exposed an orphan JSON contract left by migration 82 that
blocked all Dex settings writes; corrective migration 86 removes that obsolete
contract while preserving validation of remaining JSON fields.
Real PostgreSQL tests cover admission/replay/conflict, read failures, stage/audit
rollback and invalid durable worker input. [Results and scope](runtime-review/evidence/2026-10-23/dex-registration-ten/README.md).
All seven legacy Dex rollout/provider scenarios remain open, with admission atomicity
subcases now covered. No live rollout, estate independence or lifecycle qualification
is claimed; existing release/DR, Charlie and pagination blockers remain unchanged.


#### 2026-10-23 Dex real rollout checkpoint

**Historical/superseded:** see the identity scope correction above; these results do not authorize further Dex implementation or count as replacement acceptance.

[Plan 032 §20.12](032-technology-guide-alignment-and-latest-stable-master-plan.md#2012-dex-real-rollout--next-ten-2026-10-23)
now has a real Kubernetes/PostgreSQL worker lifecycle harness. Seven race-enabled
live scenarios pass: registration, fresh fixed point, foreign Secret preservation,
connector restoration, failing rollout/retry, manual disablement and stale operation.
Production fixes recognize the chart's staged Secret mount, reject old-pod surge
readiness and route health/probes under the configured issuer path.
[Evidence and gate status](runtime-review/evidence/2026-10-23/dex-rollout-ten/README.md).
The owned Kubernetes 1.35 test cluster was removed; all existing clusters remain.
Full `make check` passed: 15,782 passes, 14 existing skips, zero failures/errors.
Security, focused checks and all 108 final source hashes passed verification.
[Coverage reconciliation](runtime-review/evidence/2026-10-23/dex-rollout-ten/coverage-map.md)
retains first-provider creation, registration-specific rollout failure/rotation
and finalization fault cases as follow-ups. Seven legacy synchronous tests remain
skipped, not counted as passing. This component proof does not close immutable
release/deployment, independent-host qualification, 174 NOT_RUN lifecycle cases,
Charlie suspension, pagination cause, or full release/DR acceptance.
