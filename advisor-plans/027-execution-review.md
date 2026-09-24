# Plan 027 — Execution and independent review ledger

## Starting point

The user authorized preserving all current Astronomer work on main, creating a new branch, and executing Plan 027. On 2026-09-24:

- Preserved the entire existing working snapshot as `89229ce5` (`chore: preserve platform workflow and observability baseline`): 536 files, including prior platform/observability implementation and Plans 027/028.
- Fast-forwarded main from `59619920` and pushed it to origin. Local main and origin/main both resolved to `89229ce5d65e4974107e09de4f8bf5c25e18f172` before implementation.
- Created the requested working branch `feat/027-operator-navigation-workflows` in the normal checkout.
- Created isolated executor branch `implement/027-navigation-workflows` in `/root/astronomer-all/astronomer-plan027` from that same commit. Changes are reviewed before integration onto the requested branch.

This is preservation of the existing baseline, not a new qualification claim for prior backend/Helm work. Plan 028 remains planned and unexecuted.

## Baseline checks

Commands use Node 24.21.0, as required by `frontend/package.json`. The shell's default Node 22 is not used for frontend validation.

| Check | Result |
|---|---|
| Four Plan 027 navigation test files | PASS: 4 files / 65 tests |
| `npm run type-check` | PASS |
| `npm run lint` | PASS |
| Initial full unit run with configured four workers | Interrupted after host resource pressure and a timeout; not a passing result |
| Full unit baseline with `--maxWorkers=1` | PASS: 281 files / 1,726 tests; 494.81 seconds |

Original source hashes, fixture screenshots and defect reproductions remain in `027-ux-audit/`; they are not overwritten by implementation evidence. Fixture tests do not qualify live integration behavior.

## Authorized API workflow increments

The user subsequently clarified that API limitations and underlying bugs must be fixed wherever found. These increments are required execution scope, superseding the original frontend-only gates. The backend executor uses `/root/astronomer-all/astronomer-plan027-api`, branch `implement/027-api-workflows`, from the same preserved baseline. The frontend executor owns handwritten frontend code; the backend executor owns canonical OpenAPI/SQL and generated API artifacts. The reviewer integrates verified milestones. A third isolated executor owns the custom-resource proxy continuation increment in `/root/astronomer-all/astronomer-plan027-cr`, branch `implement/027-cr-scope`; canonical OpenAPI generation remains with the API executor.

### Workload and custom-resource subset pagination

The original workload endpoint accepts a singular `namespace`. Add an explicit bounded multiple-namespace contract and apply selection and authorization before sorting, totals and page boundaries. Preserve singular callers; distinguish omitted/all from explicit empty selection, and reject ambiguous input. Custom resources need the same honest collection semantics using supported server APIs. Do not use comma-separated values in the existing singular field, filter only one server page, download the whole cluster in the browser, or merge independent pages into false global totals.

### Cluster snapshot restore progress

Restore creation stores `cluster_restores`, but the original Accepted Location points to the source snapshot. Existing SQL get/list queries are not public readback endpoints and the list is unpaginated. Implement canonical target-cluster restore list/detail endpoints, enforce target/source visibility without leaking inaccessible source identity, bound pagination, expose existing phase and observation data, and correct creation Location. Test source/target authorization, foreign IDs, queued/running/completed/partial/failed states, poll errors, history paging and replay. General `/backups/restores` and management database backups are different identities; never substitute them.

### Installed-release inspection

Add authoritative per-ID metadata retrieval with the same authorization/redaction rules as existing release surfaces. Keep Flux status projection and lifecycle ownership. A deep link must work outside the first collection page and must not require downloading the entire estate. Continue to use existing values, revisions and Catalog operation readback contracts.

### Logging pipeline inspection and editing

Add authorized GET by ID alongside existing PUT/DELETE so reload/deep links do not depend on the first 200 list rows. Verify update behavior with real database association constraints and preserve unknown supported filter objects during round trips.

Every increment updates canonical OpenAPI and generated artifacts, includes meaningful backend regression tests and is consumed by frontend workflow tests. Fixture UI checks and handler tests do not qualify every live integration; Plan 028 remains separate.

## Implementation review

In progress. First frontend milestone `d057a13d` implements scoped singular collections, consistent project selection, initial navigation/context and responsive fixes. Executor evidence: type-check passed, 106 focused unit tests passed, and 10 focused desktop/mobile browser tests passed. Independent source review is ongoing; these are milestone results, not full-plan completion.

Initial review requested list-vs-read permission correction for Delivery Templates/Overrides, discoverable resource search below wide-desktop sizes, and actionable recovery for an unavailable remembered project during cluster switching. Subsequent phase commits and independent final integration results will be recorded here. Do not mark the plan DONE while required workflow or API criteria remain unmet.


## Independent milestone verification

- Integrated frontend milestone `d057a13d` as `1a7cf261` on the requested feature branch.
- Independently reran seven affected unit files serially: 7 files / 87 tests passed, exit 0.
- Inspected five-width browser test implementation and 390px/1280px screenshots. The 390px controls fit; a 1280px capture still showed lazy action loading, so final evidence must wait for each required control and assert overlays/keyboard recovery, not just measure currently rendered buttons.
- Follow-up review identified project-only Delivery estate scope/selector handling and potential partial-create retry state; these are sent to the executor for correction and behavioral tests.

- Integrated custom-resource API milestone `bc3a608d` as `d825708b`. Independently reran `GOMAXPROCS=2 go test -p 1 ./internal/tunnel ./internal/server`; both packages passed. Source review checked selection authorization, signed continuation binding/expiry, native-page traversal and removal of misleading namespace-local aggregate metadata.
- Integrated generated contract milestone `90d1e5ea`; runtime routes/tests remain pending at this checkpoint.
- Found a pre-existing installed-release values contract mismatch during review: the handler wraps the result in `data`, while OpenAPI declared unwrapped fields. The API executor is correcting the canonical schema and the frontend executor is matching the actual wire response. Upgrade values must load successfully before submission; missing values must never become an empty replacement.


## Further confirmed workflow dependencies

- Canonical CRD discovery originally ignored continuation after its first 500 definitions; raw CRD discovery also blocked namespace-only readers. Extend the existing management discovery metadata API with bounded continuation and consume its summaries without broadening raw CRD permissions. Retain first-page built-in metadata and distinguish CRD failure from unavailable optional built-in types.
- Cluster Apps chart search filtered an already paginated response. Add authorized SQL search before paging/counts for catalog scope and consume it in the existing search control.
- Catalog operation GET/retry handlers wrap their results in `data`, while the original schemas and adapters described unwrapped results. Correct both generated contracts and callers.
- Catalog operation outcome projection consulted the release's current Delivery target, allowing an old ready state or later operation to overwrite the current receipt's status. Bind outcome projection to this operation's durable rollout/deletion identity; retain failure/pending states and show unknown outcomes honestly when old receipts lack an anchor.

These are demonstrated defects on the selected workflows, included under the user's explicit instruction to fix underlying problems wherever found. They do not replace Plan 028's all-offerings live qualification.

## Integrated workflow checkpoint

At `0067c824`, the feature branch includes the following reviewed increments:

- `54d3f277`, `791abff5`: corrected Catalog operation response envelopes, observation fields and durable timeline events in canonical generated contracts.
- `ebb22524`: authorized multiple-namespace workload selection; exact release/pipeline readback; target-cluster restore history/detail and corrected creation Location; repeated pipeline-output updates preserve database associations; Apps collection values are redacted.
- `5a4b1747`: complete bounded CRD discovery metadata. Independent focused handler tests passed; the matching server filter selected no tests, so that command is not claimed as additional server coverage.
- `8fe64d1d`: literal Catalog chart search before authorized SQL counts/pages, stable ordering, and rejection of unsupported scoped tag combinations.
- `281d95a0`: operation outcome observes its own durable rollout or deletion target. A newer rollout cannot overwrite an older receipt, and observation failure remains explicitly unknown.
- `0067c824`: UI wiring for those contracts, URL-addressed app/restore inspection, pipeline detail/edit routes, alert investigation, and resource context.

The API executor's canonical disposable PostgreSQL gate first passed 16 expected tests with zero skips. The combined run then passed **18/18 required tests**, including search visibility/literal matching, restore visibility/pagination, repeated pipeline association updates, and exact-rollout/deletion SQL. Log: `/tmp/plan027-api-postgres-combined.log`. Subsequent SQL refinements require another run before final acceptance.

Go test binary builds require `GOFLAGS='-p=1 -buildvcs=false'` in this environment: Go's VCS discovery encounters an incomplete parent `/root/astronomer-all/.git` while the nested repository/worktrees are valid. This disables test-binary VCS stamping only; unknown parent metadata is preserved. It does not disable source, authorization, schema, race, or contract checks.

The checkpoint is not final acceptance. Browser journey coverage, remaining source refinements, complexity reductions, route fixtures and final combined enterprise gates remain in progress. No live offering is marked qualified by these fixture or database tests.

### Independent integration checks at `5b2d0d19`

- `GOMAXPROCS=2 GOFLAGS='-p=1 -buildvcs=false' go test ./internal/handler ./internal/server ./internal/tunnel ./internal/delivery/catalogapp -count=1`: PASS for all four packages (20.202s, 7.229s, 0.872s, 0.011s). Database-dependent tests are qualified separately by the mandatory disposable PostgreSQL run.
- Backend enterprise attempt: formatting, shell checks, migration policy, data governance and canonical sqlc drift checks passed. Go build failed on three CLI accesses to the now-corrected values/operation response envelopes. The executor is fixing the CLI consumers and adding command/SDK HTTP regressions; this attempt is explicitly **not a passing enterprise result**.
- Independent frontend adapter check: 11 passed, one discovery mock assertion failed because the adapter now sends bounded continuation parameters. The assertion and behavioral continuation cases are being updated; nonexistent requested test paths are not counted as coverage.
- Browser screenshot review found the long alert dialog rendered beneath the sticky header. The shared overlay stacking correction and obstruction checks are required before final screenshot acceptance.

### Further fixes required by concrete integration findings

Catalog visibility must traverse beyond its former 10,000-project/repository cap. Kubernetes Events must follow native continuation before reporting totals or applying response offsets. Project scope must use actual per-cluster namespace assignments from the API; a helper test with a synthesized secondary `clusterIds` property cannot establish production support. Pipeline list navigation must retain server pagination instead of treating its first 200 rows as complete. These remain part of the authorized workflow implementation.


## Final API scope checkpoint

Integrated `32ab6aa2` as `5413d6e8` (Catalog visibility traversal, complete native Events pagination and corrected CLI envelope consumers), and frontend `ce96ccfb` as `77227c95` (workflow recovery and shared overlay correction). The Project contract/runtime/review commits `9833f4ba`, `f9376d37`, `e44860a3` are integrated as `d9acd3e0`, `65fcda44`, `9605d4b2`.

Project reads now expose authoritative cluster membership and per-cluster namespace assignments. The cluster picker filters authorized membership before matching page/count queries, includes secondary membership, and does not turn a secondary cluster grant into permission to read an entire project. Namespace-narrowed grants remain narrowed. Unexpected bracketed namespace query keys fail closed rather than silently requesting all namespaces.

- Full handler/server tests on this API source: PASS, 20.559s / 7.009s (`/tmp/plan027-api-project-packages.log`).
- Canonical disposable PostgreSQL run: PASS **18/18 required tests, zero skips**, all 66 migrations applied (`/tmp/plan027-api-project-postgres.log`). This supersedes the earlier SQL checkpoint.
- `OPENAPI_BASELINE=main:docs/openapi.yaml scripts/openapi-breaking-change.sh`: PASS after exact review entries for corrected pre-existing Catalog response envelopes (`/tmp/plan027-api-project-compat.log`). Actual wire responses were already wrapped; generated consumers and CLI tests now match them.
- Browser workflow test commit `6c04a360`, integrated as `a36a7f4e`: **22/22 passed** across desktop/mobile (20 journeys and two setup checks), 38.9s, one worker, zero retries. Covers alert investigation, release inspection and lifecycle receipts, pipeline editing, and target-cluster restore tracking. Source/build: frontend `ce96ccfb` (browser-worktree cherry-pick `e25fa8f4`). Evidence: `/tmp/plan027-workflow-e2e-final.log` and executor `frontend/test-results/operator-workflow-final/`. Root inspected the mobile alert and restore screenshots.

### Verification attempts that do not count as passing

The next frontend enterprise attempt stopped on a stale canonical code-health inventory; a combined-source regeneration is required. A standalone full frontend unit run and a backend enterprise attempt were terminated under host memory and temporary-filesystem pressure (exit 143). The backend attempt had passed formatting, shell, migration and sqlc checks but had not finished the build. These are **not passing full gates**. Final checks will use one heavy process at a time and disk-backed temporary directories. Existing unrelated temporary artifacts and live services are preserved.


## Combined frontend acceptance candidate

Frontend checkpoint `c879acf6` is integrated as `2eb073f5`, with browser additions `d6520847`, `aa3649db`, `31514353` integrated through `fed20d15`. This includes authoritative secondary-cluster project scope, repeated-key API query serialization, object-bound Exec activation, actual reachable Metrics Overview/Grafana views, single Delivery project selection and permission-aware summaries, and URL-backed pipeline collection/page return. The original `ce96ccfb` five-width screenshots are retained with an explicit checkpoint README.

Additional browser testing exposed two source gaps before acceptance: Metrics route never mounted the native summary component, and project-only Delivery duplicated its project picker and linked unauthorized summary destinations. Static journey review also found pipeline Back pointed to a nonexistent global tab. These were repaired in the candidate, along with preserving the current pipeline page and gating its create action. The new install receipt, restricted-role, metrics and 201st-pipeline journeys remain **pending final combined-source execution** at this checkpoint.

Root backend enterprise verification on `d6b32f6a` passed formatting, shell checks, migration/data governance, canonical sqlc drift, Go build, Go vet, and reachable Go vulnerability scanning. It then failed on three lint findings in new tests (ineffectual initialization, numeric HTTP status, unused embedded test field). Log: `/root/astronomer-all/.tmp-plan027-root/backend-final.log`. These are being corrected; this is not a complete enterprise pass. The frontend executor separately passed 27 focused tests in six files; its first type-check found an optional cluster ID in a return link, now guarded, with final type-check pending.


## Browser acceptance and visual review

- Full operator diagnostic batch: **62 passed / 12 failed**, 4.7m. Failures were incomplete or mismatched fixtures/assertions (SMTP trailing-slash routing, incomplete Project DTO, humanized pod status, preserved pipeline page query, old-document request accounting and mobile backdrop click location). Original failures remain in executor `frontend/test-results/operator-final/` and `/tmp/plan027-final-operator-browser.log`. This is not a passing batch.
- Root visually reviewed restricted-role Sources, installed-release diagnostics, Metrics Overview and pipeline page-five screenshots. The pipeline screenshot exposed a real mobile action overflow: Create Pipeline extended beyond the viewport. The action row now wraps and the browser test checks each action's rectangle after the header finishes loading. Root reviewed the corrected screenshot with all actions visible and pipeline 201 retained on page five.
- Source/test correction `d2a19257` is integrated as `a6658ac0`. Both canonical inventory checks still pass on this source; no inventory rewrite was required.
- Correction batch: **30 passed / 4 failed**, 1.6m. Remaining failures were the still-incomplete Delivery Project fixture, subsequently fixed explicitly in `91cc1289` (`a495591f`). Additional fixture-consistency corrections preserve the actual operation type, release ownership and alert summary count.
- Combined operator, keyboard and resource-drilldown batch: **100 passed / 2 failed**, 3.2m (`/tmp/plan027-browser-acceptance.log`). All operator desktop/mobile cases passed. The two legacy keyboard snapshot cases lacked the new canonical paginated restore readback fixture. The fixture was corrected in `c895bbc5` (`77343527`); replay is pending at this checkpoint.
- Formatting review covered all **122 changed handwritten frontend files** against preserved main. Three test files required formatting; no production formatting or complexity baseline inflation was needed.

The Metrics Overview route is now reachable, so its previously dormant error behavior also needs acceptance: denied/offline metrics reads must not become installation advice. The final bounded correction and permission regression are in progress. Final full enterprise gates, accessibility acceptance and complete evidence handoff remain required before marking Plan 027 DONE.


## Complete-suite integration review

The final browser edge additions are integrated through `7a9fb3a8`: deployment403/404 and scoped CR deletion, Shared stacks active state and installed-tools template reachability, keyboard selection of both Overview commands, multi-container log URL state, invalid/Exec URL non-execution, inaccessible parent recovery, and missing/denied/history alert cases. These additions remain pending the combined browser invocation at this checkpoint.

- Backend enterprise at `1a7adbf2`: **111 Go packages passed ordinary tests and the same 111 passed race detection**. Formatting, shell, migrations, sqlc, build, vet, vulnerability scan, lint and Charlie contracts also passed. The subsequent documentation check failed because the comparison marker still counted845 operations/819 mounted routes. Correct totals are849/823. This is not a complete passing enterprise invocation. Log: `/root/astronomer-all/.tmp-plan027-root/backend-final2.log`.
- Exact comparison correction and canonical source inventory refresh are integrated as `2783cb86` and `6581c49c`. All18 lightweight contract checks and the remaining main-baseline OpenAPI compatibility, Go SDK, CLI documentation, release and qualification-producer checks passed independently. Logs and commands: `/root/astronomer-all/.tmp-plan027-api/contracts-final/` and `contracts-closeout/`.
- Frontend enterprise first completed unit run: **1788 passed/1 failed**. The failed catalog assertion expected the old query without server-side search; `9c096dd5` now verifies search and project isolation. The five-test file passed independently.
- Frontend enterprise at `6581c49c`: code-health, lint, types, formatter and **all289 files/1789 unit tests passed** (279.92s, two workers). Production build/CSP passed. Bundle verification then failed on the bootstrap chunk:743313 raw/209021 gzip bytes versus the unchanged650000/220000 per-chunk limits. Eager closure budgets themselves passed. This is not a passing enterprise invocation; a real loading-boundary correction is required. Log: `/root/astronomer-all/.tmp-plan027-root/frontend-final3.log`.

The88 retained implementation PNGs, nine historical logs and phase0–12 mapping are now tracked under `027-ux-audit/artifacts/implementation/c698663a/`. Historical failures and queued tests are explicitly distinguished from passing evidence. No live extension is qualified by these fixture or static checks.
