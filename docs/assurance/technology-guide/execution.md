# Execution ownership and local prerequisites

The integration reviewer (`/root`) owns sequencing, review, plan/index/evidence-ledger changes and integration branch `implement/032-technology-guide`. Executor `/root/execute032` owns bounded source work in `implement/032-baseline` at worktree `astronomer-032-w00`. Role owners below are review responsibilities, not invented human approvers. Exactly one executor is active; subsequent work starts only after this slice is reviewed/integrated.

| Packages | Implementation/review responsibility | Ready before implementation / acceptance |
| --- | --- | --- |
|W00 | Executor / technical integration reviewer, backend/platform/frontend input | Source/deployed/claim inventory, dispositions, invariant contracts |
|W01 | Platform/tooling plus backend/frontend | W00; stable metadata/platform/digest trust; compatible generators/plugins |
|W02 | Security/platform | W00 and W01 scan-tool compatibility; real finding disposition/check identity |
|W03 | Delivery/platform/security | W00, W02 trust policy; verified loader/closed assets before W04 |
|W04 | Security/platform qualification | W03 implementation ready; fresh disposable caches/host+pod egress controls |
|W05 | Frontend/API | W00; W01 frontend tooling as applicable; refresh regression before fix |
|W06 | API/backend/frontend/data | W00; writer/reader/DB state decision before migrations |
|W07 | API/backend/frontend | W00; compatible progress/error contract and scope-bound traversal |
|W08 | Backend/data | W00; W06 coupled categories; transaction characterization |
|W09 | API/data | W00; W06 shared states; supported-client/durable-data inventory |
|W10 | Backend/platform | W01; W00 reconciliation with 030; preserve measured signal schema |
|W11 | Platform/agent | W01/W03; W12 characterization first, final real qualification after migration |
|W12 | QA/platform/backend/frontend | W00/W01; candidate-specific dependencies for final evidence; independent CNPG estate |
|W13 | Backend architecture | W06/W08/W09 and W12 characterization; no simultaneous edit of extracted domain |
|W14 | CLI/platform | W01/W12 characterization; W17 release rehearsal where applicable |
|W15 | Catalog/platform | W01/W03, compatible producer/consumer commits; preserve sibling dirty work |
|W16 | Security/backend/product | W00/W01/W12; explicit FIPS/product decision before optional artifacts |
|W17 | Release/platform/QA | Frozen coherent candidate and applicable packages; actual future-tag policy |

Shared-file leases: integration reviewer serializes `go.mod/go.sum`, frontend manifest/lock, `compatibility.yaml` and derived docs/chart copy, release schemas, OpenAPI generated family, canonical SQL/sqlc and migration numbers. Executors report lease scope before edits; one reviewed commit integrates before a dependent checkout is synchronized. Current highest schema is 71; re-read the directory before reserving any new migration. W00 claims no lease on those product inputs.

Observed prerequisites: bundled host Go 1.26.4 selects Go 1.26.6 when `go version` runs inside this module; host Node 22.22.1/npm 9.2.0 differs from source Node 24.21.0/npm 11.19.0; Helm 3.21.0, Python 3 with jsonschema 4.19.2 and Kubernetes CLI are available. Root prepared cached Go 1.27.2 and checksum-verified Node 26.11.1/npm 12.2.0 for later W01, not as baseline runtime qualification. W00 Go verification records its effective 1.27.2 explicitly. No frontend installation or runtime version edit is needed for this documentation slice.

Existing W00 commands are `make docs-check`, `make release-contract-check`, `make config-docs-check`, `make cli-docs-check`, and `node scripts/check-dependency-boundaries.mjs`. `docs-check` includes link/classification, complexity, boundary, flake and security-review checks; `release-contract-check` includes Python release/kit/waiver validators, shell fixture tests, image inventory diff and filtered Go tests. Its filter selects **zero** tests in internal/releasecontract today; W00 supplements that package explicitly instead of treating its empty run as behavioral evidence.

CLI docs verification may hit the pre-existing malformed parent `/root/astronomer-all/.git` when Go discovers VCS metadata for its temporary binary. Retain the ordinary command failure. The reviewer-authorized scoped workaround is `GOFLAGS=-buildvcs=false make cli-docs-check` (with recorded toolchain): disables optional stamping of only the temporary documentation binary, not production release checks. Do not change/delete the parent Git directory. Current Go 1.27.2 runs that same generator successfully; W01 should prefer the toolchain fix and change the generator only if the issue persists.

Existing later gates are listed in Plan 032 section 10 and their names were checked in Makefile: `verify`, enterprise scopes, sqlc/SDK/Charlie/data-governance/migration checks, real PostgreSQL/worker integration, outage/restart/failover, live browser, Local CI and verify-all. Read each runner's ownership/preflight before execution; W00 has not run destructive or live mutation lanes.

Proposed commands remain absent: `make versions-check`, `make updates` (W01), fresh disconnected dedicated entrypoint (W04), actual CNPG recovery entrypoint (W12). Do not report them run. Docker lacks buildx in the inspected host; candidate multiarch builds need that prerequisite in W01/W17, without weakening artifact platform requirements.

Source-hash/digest validation here proves retained input/artifact binding, not a signature, reviewer approval, or qualification of a rebuilt release. Follow the exact candidate manifest's schema/rollback floor, role set, identities and exceptions when crossing W17.
