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

The API executor's canonical disposable PostgreSQL gate passed 16 expected tests with zero skips, including restore visibility/pagination and repeated pipeline association updates. A combined run including the subsequently added search and exact-operation SQL regressions is pending at this checkpoint.

Go test binary builds require `GOFLAGS='-p=1 -buildvcs=false'` in this environment: Go's VCS discovery encounters an incomplete parent `/root/astronomer-all/.git` while the nested repository/worktrees are valid. This disables test-binary VCS stamping only; unknown parent metadata is preserved. It does not disable source, authorization, schema, race, or contract checks.

The checkpoint is not final acceptance. Browser journey coverage, remaining source refinements, complexity reductions, route fixtures and final combined enterprise gates remain in progress. No live offering is marked qualified by these fixture or database tests.
