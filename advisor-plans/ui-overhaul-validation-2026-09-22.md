# UI integration and Plan 020 — local validation (2026-09-22)

## Revision and scope

The seven approved UI branches were integrated at `642ab6c5`. Plan 020 and
integration fixes were implemented on `advisor/020-explorer-nav`, with production
code frozen at `7a41061db3923b9dce26fc3ec73c14f5d6aa1bc3`.

That revision passed the frontend, backend, and Helm enterprise scopes with
`source_dirty: false`, `source_tree_stable: true`, and `status: passed` in all
three evidence manifests. It was merged into `advisor/ui-overhaul-integration`
at `15025b73`. Follow-up `c0f9252c` changes only the browser test: an exact mobile
navigation selector and retained explorer screenshots. Production code is
unchanged from the qualified revision.

`main` remains at `59619920`. Nothing was pushed or deployed. Plan 025 and the
external Plan 016 qualifications remain open.

## Results

| Check | Result |
|---|---|
| Enterprise frontend | PASS: 10 subgates; types, zero-warning lint, generated contracts, 243 unit files / 1,495 tests, production build/CSP, route-tree drift, bundle budgets, npm audit (0 vulnerabilities) |
| Enterprise backend | PASS: 40 subgates; migrations, sqlc, Go build/vet/lint, vulnerability scan, full tests and race suite, API/security/dependency/documentation/release contracts |
| Enterprise Helm | PASS: 6 subgates; chart inputs, lint, development/production renders, negative security cases, chart contracts |
| PostgreSQL integration | PASS: all 15 required tests executed against an ephemeral database migrated through version 65 |
| Migration 065 up/down/up | PASS: existing-row defaults, preference JSON round trip, non-array/max-20 constraints, preserved rows on downgrade, restored defaults on reapply |
| Explorer browser checks | PASS: desktop collapsed flyout and mobile expanded navigation, CRD link navigation, axe serious/critical checks; 2 tests, no retries |
| Final full desktop/mobile smoke crawl | PASS: 296 checks in 9.3 minutes, including both explorer navigation checks; zero retries; immutable production build and corrected test selector |
| Staged formatting | PASS: all edited frontend files; generated artifacts excluded |
| Complexity and code-health inventories | PASS: regenerated and checked; ceilings only reduced or relocated |

Final eager transfer sizes (gzip bytes; no budgets increased):

| Closure | Actual | Existing ceiling |
|---|---:|---:|
| Bootstrap | 403,334 | 418,816 |
| Login | 411,747 | 428,032 |
| App | 487,750 | 489,472 |

## Retained local evidence

Artifacts are outside the repository under:
`/var/tmp/astronomer-ui-validation.biMYh2/`.

- `final-frontend/20260922T153139Z-242130-7a41061db392/evidence.json`
- `final-backend/20260922T153140Z-242247-7a41061db392/evidence.json`
- `final-helm/20260922T153141Z-242502-7a41061db392/evidence.json`
- `020-postgres-integration.log`
- `explorer-browser.log` and `explorer-browser-artifacts/` (desktop/mobile PNGs)
- `final-smoke.log` and `final-browser-artifacts/`

The enterprise scopes used Node 24.21.0 and Go 1.26.6, with
`GOFLAGS='-p=2 -buildvcs=false'`, a non-`/tmp` `GOTMPDIR`, and
`VITEST_MAX_WORKERS=2`. Browser checks used port 4003, one worker, zero retries,
and a separate immutable `dist` snapshot, so concurrent builds could not remove
assets from the running preview. Screenshots were inspected for the cluster
overview, unclipped desktop flyout, and mobile navigation.

Earlier exploratory runs are not qualification evidence: some ran while source
was changing, and the first complete immutable crawl exposed an ambiguous test
selector matching both “Open navigation” and “Open actions menu.” The exact
selector fixes that test defect; no retry or quarantine was added.

## Integration fixes included

- Testable page implementations moved to adjacent `-page.tsx` modules: exporting
  them from route entrypoints prevented TanStack component splitting.
- Optional dialogs, extension renderers, and the enabled assistant capability
  are lazy; shell read-only hooks no longer import full mutation modules.
- Helm preflight and generated release compatibility now agree with schema 65.
- Shared project/RBAC UI lives under `components/rbac`, not route modules.
- Collapsed navigation flyouts portal outside the sidebar's scroll clipping.

## Plan 020 bounds and remaining work

Discovery uses the existing authorized Kubernetes proxy. Permission filtering is
cluster-scoped. Failed discovery does not imply absent CRDs. Counts are
metadata-only, capped at 15 requests including namespace fan-out, and unavailable
or partial totals are omitted. Dynamic navigation is capped at 40 kinds; up to
20 persisted starred types are retained within that cap. Existing supported
destinations remain available (47 standard entries with empty discovery, 49
with optional Grafana/Snapshots; seven entries in the Cluster group).

Before pushing a PR, run the lockfile-pinned `make local-ci-pr-representative`
and `make local-ci-pr`; neither was run in this local implementation turn.
The backend gate explicitly skipped live agent-identity acceptance because
`AGENT_IDENTITY_TEST_CONTEXT` was not supplied. No live acceptance, external
accessibility, cloud/DR/scale, release approval, or production-readiness claim
is made here.

Next feature work is [Plan 025](./025-guided-form-depth-and-table-parity.md):
guided array fields, namespace grouping, bulk restart/scale, related resources,
and the `questions.yaml` decision spike. Reconcile its original source excerpts
against the integrated tree before implementation.

## Post-review cleanup (working tree, 2026-09-22)

Implemented after the integration closeout above, without changing backend
authorization, audit, schemas, dependencies, or bundle ceilings:

- Project-member binding failures use the shared query-state contract, including
  explicit permission denial and retryable errors. Cached members are hidden
  after a denied refresh; only a successful empty response shows an empty roster.
- Count queries follow transient collapsed flyouts as well as persisted expanded
  groups. Visible starred CRDs participate without duplicate requests; namespace
  restrictions, metadata-only responses, and the 15-request bound remain intact.
- Kind-less YAML imports use discovered CRD plurals, scope, and served versions.
  Same-named kinds in different API groups cannot collide. A mixed import is
  resolved completely before submitting any writes; unknown/denied discovery
  produces an actionable error. Built-in imports remain available independently.
- Added regression coverage for star/unstar persistence across reloads, denied
  preference-save rollback, restricted discovery access, first-open flyout/drawer
  counts, denied member reads, and mixed built-in/custom-resource imports.

Evidence root: `/var/tmp/astronomer-ui-cleanup.uIsUeS/`.

- Focused unit tests: **54 passed**.
- Frontend enterprise gate: **PASS**, including **245 files / 1,521 unit tests**,
  lint, type-check, build/CSP, bundle budgets, generated-code health, and npm audit
  (zero vulnerabilities). Evidence:
  `enterprise/20260922T161735Z-343724-22f633ece6a9/evidence.json`.
  This qualifies the uncommitted working tree (`source_dirty=true`,
  `source_tree_stable=true`), not a newly committed revision. Subsequent edits
  only correct browser assertions and update planning documentation.
- Targeted desktop/mobile browser checks: **12 passed**, no retries or skips
  (`browser-final.log`, `browser-final-artifacts/`). Initial assertions wrongly
  required a prefix-only toast and removal of the entire More Resources group;
  corrected assertions verify the actual error and unauthorized CRD links.
- Full browser coverage: **153 desktop checks passed** in a separate isolated
  invocation (`desktop-isolated.log`); all **151 mobile checks passed** in the
  earlier combined invocation. That combined invocation was **299/304**, not
  green: five desktop checks failed while Docker-based Local CI was running
  (`full-smoke.log`, `full-smoke-artifacts/`). Two reported
  `ERR_NETWORK_CHANGED`; three timed out before mounting the app shell. After
  Local CI stopped, the complete desktop suite passed against the same build
  with unchanged source/assertions. No timeout increases, error allowlists,
  automatic retries, or quarantines were introduced. Retain the initial failure
  evidence; run host-browser crawls separately from Docker network churn.
- Complexity, dependency boundaries, docs links, and flake-ownership checks pass.
  The editor's function ceiling decreased from 340 to 322; none increased.
- Representative Local CI: **NOT PASSING**. It passed migration roundtrips,
  release-upgrade checks, PostgreSQL integration (15/15), and failover
  certification, but its frontend job failed in Charlie admin/shell/route tests
  with timeouts and assertion failures. The remaining run was cancelled after
  that failure; no complete backend/PR qualification is claimed. Retained log:
  `local-ci-representative.log`; detailed frontend failure:
  `/root/.local/state/local-ci/logs/local-ci-29-j4/steps/Run-authoritative-frontend-gate.log`.
  The complete `make local-ci-pr` matrix was not run. Investigate the Local CI
  failure and rerun both preflight targets before promotion.

Plan 025 and the previously recorded parity/API follow-ups remain out of scope
for this cleanup. Nothing was pushed, merged to `main`, or deployed.
