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
| W01 | IN PROGRESS | Core authority and focused regressions PASS; final coverage/gates pending | Integrated `34574ffb`: Go 1.27.2, Node 26.11.1, npm 12.2.0 and compatible dependencies. Resolver hardening, complete consumer coverage and final images/browser qualification continue in `implement/032-latest-stable`. |
| W02 | IN PROGRESS | TODO | Source security enforcement and exact exceptions; isolated `implement/032-security-policy`. External ruleset application is separate. |
| W03 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W04 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W05 | IN PROGRESS | Focused transport, socket cancellation, forms and cache regressions PASS; final suite pending | Integrated refresh `7b167632`, read forwarding `c4fd5119`, awaited Vault invalidation `2867346d`, semantic modal submission `da569b90`, assertion correction `cfe5802b`, and decreasing complexity ratchets `9a896e29`. Clean full suite/build/browser run continues in `implement/032-browser-boundaries`. |
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
- W01 core review: 22 resolver/binding tests, closed authority schema and three-curve JWK parsing regressions pass. Existing SDK exported declarations remain unchanged with compatible generator 2.5.1. Full W01 completion is not inferred from this slice.
- W05 review: 47 focused form-adoption, persistent SIEM error and actual extension signal assertions pass. Five complexity ceilings decrease; none increase. Full combined toolchain/browser qualification remains required.
- W02 bounded static triage: five exact G109/G404 claims have positive source controls and are retained as expiring source-bound false positives. The complete scanner reports 569 findings without engine errors; other findings remain open. Retained triage artifact: `/root/.codex/state/plugins/codex-security/scans/astronomer/artifacts-5817c9cd58f3f1c5a377c37b7bb81d5adb90d025fb12a3874ce4497a5798d9c2/findings/plan032-five-source-controls.json`, SHA256 `ca585e17101b078551fbdf409eb75c7ce7c3ab5d7afcc208b7a928f5f44524cc`.
- W12 preparation: owned disposable cluster `astro032-cnpg-20261009` has three ready K3s 1.37.1 server nodes. This connected prerequisite is not CNPG failover, backup/restore, signed candidate or disconnected qualification. See [environment receipt](prerequisite-environment.json).
- No deployment or release qualification is inferred from these source gates.

## Rules

- A passing mock/static gate is not live qualification.
- Every DONE task needs actual source/artifact identity, command results, and retained evidence.
- CNPG, Flux, Valkey, RustFS/S3, Charlie, tenant scoping, audit, and encryption recovery remain invariants.
- Upstream vulnerabilities follow the accepted update-or-waive policy; disclose unresolved findings.
- Future publication policy and externally qualified claims require concrete applicable evidence/decisions.
