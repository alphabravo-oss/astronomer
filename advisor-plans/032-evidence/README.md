# Plan 032 implementation evidence

Implementation branch: `implement/032-technology-guide`.
Application source baseline: `3b378ef16141b9eaac35a1e5fe292e061b69f5f1`.
Catalog source baseline: `3e24ab98f0805bd54d6591d9b9b961bbfd1f5db5`.

The user authorized implementation of the entire master plan on 2026-10-09.
Each package is reviewed against its scoped tests before integration. Implementation
readiness, integration acceptance, deployment, and release publication are tracked
separately. No historic qualification is attributed to a new candidate.

| Package | Implementation | Qualification | Evidence / next action |
| --- | --- | --- | --- |
| W00 | DONE | Source contracts PASS | Integrated `c4e9993c`; [baseline and verification](../../docs/assurance/technology-guide/README.md). Live CNPG qualification belongs to W12. |
| W01 | IN PROGRESS | TODO | Latest-stable authority, Go/Node/dependency updates; isolated `implement/032-latest-stable`. |
| W02 | IN PROGRESS | TODO | Source security enforcement and exact exceptions; isolated `implement/032-security-policy`. External ruleset application is separate. |
| W03 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W04 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W05 | IN PROGRESS | Refresh regressions PASS; full scope TODO | Integrated refresh slice `7b167632`; query forwarding/forms and full gates in isolated `implement/032-browser-boundaries`. |
| W06 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W07 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W08 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W09 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W10 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W11 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W12 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W13 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W14 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W15 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W16 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W17 | TODO | TODO | Await dependencies and bounded implementation slice. |

## Source control

- `6c3da188`: master plan and advisor index preserved on the implementation branch.
- `c4e9993c`: reviewed W00 baseline, source-bound inventories and CNPG render preservation test.
- W00 review: evidence validator 12 records/402 inputs; 13 negative/positive schema cases; actual changed-image CNPG render test. Docs, config, release and dependency gates pass. Baseline Go CLI doc VCS issue retained; Go 1.27.2 passes without workaround.
- `7b167632`: reviewed bounded session refresh, per-waiter cancellation, cancellation identity, and logout refresh invalidation. Reviewer rerun: 2 test files / 15 cases pass; additional package gates remain with W05.
- Helm 4.3.0 isolated preliminary CNPG changed-image render test passes; this does not qualify SDK migration, live database ownership or release deployment.
- No deployment or release qualification is inferred from these source gates.

## Rules

- A passing mock/static gate is not live qualification.
- Every DONE task needs actual source/artifact identity, command results, and retained evidence.
- CNPG, Flux, Valkey, RustFS/S3, Charlie, tenant scoping, audit, and encryption recovery remain invariants.
- Upstream vulnerabilities follow the accepted update-or-waive policy; disclose unresolved findings.
- Future publication policy and externally qualified claims require concrete applicable evidence/decisions.

