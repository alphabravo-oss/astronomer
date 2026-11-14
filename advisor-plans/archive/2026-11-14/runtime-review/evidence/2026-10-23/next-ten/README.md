> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# R07–R16 continuation — 2026-10-23

Source checkout `/root/work/astronomer-fixes`, branch `fix/2.1-runtime-verification`,
baseline `e2223136030fa17541e6be082a511caf444b7067`. Existing R01–R06 edits are
preserved. This is local source qualification, not a published or deployed release.
The retained task authority is advisor Plan 032 §20 in `/root/astronomer-all/astronomer`.

## Disposition of the next ten

| Task | Result | Remaining boundary |
| --- | --- | --- |
| R07 widgets | Scope-keyed TanStack Query, cancellation and one shared polling cadence; controlled-clock tests and desktop/mobile browser cases pass | New candidate deployment |
| R08 Applications | SQL Delivery projection before pagination; deleted targets excluded from rows/count; generation/digest checked for Ready; read errors fail closed | Deploy and repeat lifecycle against changed Apps endpoint |
| R09 literal keys | Machine paths remain string arrays; display paths escape literal dots/slashes; real ToolCatalogFields browser roundtrip preserves unrelated keys | New candidate deployment |
| R10 bundles | Node-detail body moved out of route export into lazy page; existing budgets enforced in PR CI | Fresh candidate login/editor journeys |
| R11 DR instructions | Strict transactional restore; private state capture; actual Goose table; captured replica/CronJob state; required schema/key/data gates before resume | Same-host/RPO/RTO and actual management-backup/S3 restore qualification remain open |
| R12 admission claims | Current four bundle-compatibility static passes, unsigned exception, exact prefix scope, ownership and review/exit criteria documented | Compatible verifier/signed-mirror change and positive/negative live admission qualification |
| R13 dependencies | DOMPurify npm override 3.4.17; locked audit reports zero; actual Monaco normal-mode sanitizer negatives pass | **OPEN:** Monaco 0.57.0 public assets vendor DOMPurify 3.4.15; npm override does not replace it |
| R14 reproducibility | Python 3.15 restored persistently; supported sync verifies all 62 tool versions; 49 tests pass. Seven previously skipped fixtures now pass; all 32 accounted for. Full frontend suite passes | Original pagination timing failure not reproduced/causally closed; 25 other skip dispositions remain; provider qualification remains separate |
| R15 Charlie | Actual state identified; supported feature-disable attempted; emergency latch verified true, mode disabled, identity retained | **BLOCKED:** HTTP 409, agent ceiling confirmation pending; feature remains true and workload remains 0/2 Ready |
| R16 candidate/estate | Exact source hashes, local gates, real isolated restore, fresh cluster/registry reads, historic runner tests and preflight evidence | **BLOCKED:** no immutable published candidate; sanitizer unresolved; preflight rejects dirty source/only one explicit target; 174 lifecycle cases NOT_RUN |

## Verification and corrections

- `frontend-tests.log`: full suite 351 files / 2,435 tests passed. A final run after
  moving widget keys into the required query-key factory is retained separately
  in `frontend-tests-final.log`; use the final summary for the current result.
- `browser-components.log`: four passes, desktop and mobile, exercising real
  WidgetGrid and ToolCatalogFields. Fixtures are isolated browser components,
  not a live backend journey. `browser-components-first.log` records two harness
  failures: DOM `innerText` collapsed YAML whitespace; reading `textContent`
  fixed the assertion without changing the production implementation.
- `frontend-build.log`, `bundle-after.log`: typecheck/build/CSP and unchanged
  bundle budgets pass. Bootstrap 321,623 / 418,816 gzip bytes; login 350,044 /
  428,032; app 453,766 / 489,472. Original bundle failures retained in
  `bundle-before.log`. Root import path was main → router → route tree → node
  route's exported test body → node tabs → DataTable. The optional page now loads
  through the existing lazy-route convention.
- `catalog-postgres.log`, `catalog-read-errors.log`, `changed-integration.log`:
  actual PostgreSQL endpoint pagination/terminal-state tests and durable
  operation-status tests pass. Unit error tests verify list/count errors return
  sanitized 500 responses instead of stale success.
- `runtime-status.log`: real deployed uninstall operation
  `9a830945-3bff-4e26-b10d-d49f39e54b51` now reports `completed`, with the durable
  workload `removed` event. This closes the earlier unknown completion, not the
  deployed stale Apps-row defect. No DB status was patched.
- `restore-rehearsal.log` / `changed-integration.log`: disposable PostgreSQL
  17.11, production Goose migrations through 85, actual pg_dump/pg_restore,
  authenticated dump/key envelopes, both optional trust directories and
  application column encryption survive recovery. Missing key produces no
  plaintext; wrong expected schema fails the comparison; a conflicting restore
  exits nonzero with transaction rollback. This rehearses manual runbook gates;
  it does not assert automatic fencing of an operator's writer restart, S3
  restore, management data restore, independent-host recovery, RPO or RTO.
- `editor-sanitizer.log`: actual Monaco sanitizer removes executable markup,
  handlers and dangerous link protocols while preserving permitted text/HTTPS.
  CI now runs this bounded regression. It is not an advisory waiver.
- `npm-audit-before.json`, `npm-audit-after.json`, `npm-audit-gate.log`,
  `editor-dependency-disposition.json`: the graph patch addresses the reported
  advisories, but the copied Monaco assets contain an older embedded sanitizer.
  [DOMPurify releases](https://github.com/cure53/DOMPurify/releases) and
  [GHSA-cg2g-xh64-v4q7](https://github.com/advisories/GHSA-cg2g-xh64-v4q7) identify
  the 3.4.17 patch. The inspected Monaco wrapper does not expose SAFE_FOR_TEMPLATES
  or IN_PLACE; exploit reachability was not established. Remaining work: replace
  the vendored runtime through a supported upstream release or reproducible
  source build, verify the *shipped* sanitizer version and rerun editor/CSP/bundle
  and advisory regressions. Do not equate a clean npm audit with completion.
- `catalog-validation-final.log`, `catalog-tools.json`: Python 3.15.0 from
  persistent mise storage, uv 0.13.0, all 62 exact locked distributions verified,
  all 49 tests plus schema/artifact lock/35 generated files pass. The broken
  `.venv` pointed into removed temporary Python storage; it was renamed
  `.venv-expired-20261023` and recreated. A plain uv sync first rejected lock
  freshness; the repository's `scripts/sync_tools.py` sanitizes inherited index
  configuration and succeeds without modifying uv.lock. The source was not
  patched for the unsupported system interpreter.
- `skip-dispositions.json`: all 32 prior skips. Two PromQL suites now run using
  the deployed Prometheus image's promtool, monitoring render uses pinned chart
  61.3.2, and four selected Cosign tests use pinned 3.1.3. The ordinary make gate
  still skips these unless explicit fixtures are configured; separate logs prove
  execution. Eight retired synchronous Dex tests, sixteen private repository/
  proxy fixture cases and one live OCI ingestion case remain open. No obsolete
  test was deleted or silently waived.
- `live-browser-tests.log`: aggregate real-login live project against deployed
  v2.1.1, with an owned loopback forward to the server Deployment's metrics port.
  The Service exposes only HTTP 8000, so forwarding Service:9090 fails; forwarding
  Deployment:9090 is the documented working binding. The owned forward closes
  in `finally`. This is baseline evidence; local fixes are not deployed.
- T3 preview returned: “Chrome will not start its sandbox as root”. Browser work
  used the documented repository Playwright workflow after that explicit error.

## Charlie and qualification blockers

`charlie-status.log` proves feature=true, connected identity, requested/authoritative
mode=disabled, agents 0/2 Ready. PUT `/api/v1/admin/settings/feature.charlie/`
with `{value:false}` returned 409 “Charlie is locally disabled; agent ceiling
confirmation is pending”. `charlie-after-disable.log` proves the durable emergency
latch is now true while feature=true remains. No credentials, retained identity,
namespace or unrelated workload were deleted. No direct state repair or repeated
restarts were used. Next action is supported ceiling/enrollment recovery followed
by feature suspension, or a reviewed source fix for offline suspension with
all-replica fencing tests; do not mark feature disable complete.

The historic qualification runner exists in the planning checkout, not this
source checkout. Its tests pass (`historical-qualification-tests.log`). Its compiled
binary was used only in GET inventory/preflight modes with a private authenticated
binding and current source identity. All six registry contracts match and all
174 cases remain NOT_RUN (`offerings-inventory.json`). Preflight fails closed
because this source is dirty and only the owned replacement/default project is
explicitly bound. Site-demo is healthy but has no distinct task-owned qualification
namespace/project configured; its existing workloads are preserved. The old
preflight also needs current Kubernetes-support/independent-estate checks before
porting, not just copying its historical success criteria. No old runner was
silently introduced into the new release.

`cluster-inventory.json` confirms local/site-demo/replacement at 1.35.7 and the old
ui-demo at 1.30.4. Replacement nodes are Ready, but its old deployed system release
still reports the standby health timeout (`replacement-rollout.json`). R06 source
changes remain undeployed. Preserve the old cluster until a new signed immutable
candidate passes the progress-deadline, leader-loss/recovery and application
lifecycle checks. The release workflow requires a versioned main-ancestor tag and
GitHub OIDC signatures; local binaries do not satisfy that admission contract.

No release tag, push, merge, deployment, old-cluster retirement or workload/data
migration occurred. `source-manifest.json` hashes all changed source files;
`verification-summary.json` records final gate results and post-check hash comparison.
