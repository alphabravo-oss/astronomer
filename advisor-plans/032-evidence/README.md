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
| W01 | IMPLEMENTATION READY | Authority, source coverage, focused compatibility and combined frontend qualification PASS | Integrated final authority/action/consumer/signature series through `c23f77bc`. All 176 components resolved, 335 maintained pin occurrences bound to 212 distinct inventory entries. Exact-source signed release resolution is mandatory for new candidates. Combined W01/W05 qualification passes on frozen `07c9f908`; positive Astronomer publisher execution remains separate. |
| W02 | IN PROGRESS | Enforcement fixtures PASS; source qualification FAIL | Integrated `02f07527`, `a8c0dc4b`, `feddd759`: source collection/enforcement, exact exceptions and publication policy. Complete scan: 569 findings, zero engine errors; 492 HIGH remain unaccepted and 71 MED need triage. External ruleset/check readback remains pending. |
| W03 | IN PROGRESS | Network policy / resolver / S3 probe regressions PASS; signed distribution chain pending | Integrated `326557ec`, `e61ab626`, `d51ccc43`: bounded destination authorization, immutable policy snapshot, metadata exclusions and credential-free public resolver redirects. Signed producer/child OCI closure and durable mirrored provisioning continue in the executor worktree. |
| W04 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W05 | DONE (scoped source/browser) | Combined latest toolchain full source suite, build and 18 browser cases PASS | Integrated refresh `7b167632`, read forwarding `c4fd5119`, awaited Vault invalidation `2867346d`, semantic modal submission `da569b90`, assertion correction `cfe5802b`, and decreasing complexity ratchets `9a896e29`. Integrated remaining observation adapters `b1ede805` and source-bound evidence. All 735 API function declarations classified; reviewer socket tests and AST checks pass. Combined W01/W05 qualification passes; live candidate authentication/stateful workflows remain W12/W17. |
| W06 | IN PROGRESS | Inventory underway | Category/domain/database/wire parity implementation in isolated `implement/032-category-contracts`; preserve v1 SDK source types, explicit dynamic boundaries and historical rollout terminal states. Migration 073 reserved; paired OpenAPI generation coordinated with W09. |
| W07 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W08 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W09 | IN PROGRESS | Inhibition, request and durable-write regressions PASS; full schema/migration work pending | Integrated `677c2246`: bounded versioned parser, once-per-sweep regex preparation, skip/audit/retry behavior and typed handler failures. Broader decoder/writer/reader inventory and reversible migration 072 continue in `implement/032-json-contracts`. |
| W10 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W11 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W12 | PREREQUISITES / CHARACTERIZATION | CNPG17 previous-major 21/21 integration PASS; full matrix TODO | Owned supported-minor K3s1.36.5 cluster has CNPG17.11 three replicas, synchronous replication and RustFS1.0.1 authenticated S3 operations. Existing 21 real-database cases pass with zero skips. Connected previous-major physical restore also passes; PostgreSQL18, failover, key usability/full application/cross-browser qualification remain open. |
| W13 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W14 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W15 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W16 | SPIKE / PREREQUISITES | Six native-mode compatibility tests PASS; diagnostic-only startup FAIL as observed | No product FIPS claim or build-policy change. Native Go module v1.0.0 spike is retained; full crypto inventory, decision, operating-environment review and historical-key recovery remain open. |
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
- W02 reviewer rerun: 25 security fixtures, five scanner-closure fixtures, 29 publication-policy fixtures and the release contract pass. Synthetic reverted-secret detection is included. The full source gate remains red; enforcement implementation is not a complete finding disposition.
- W12 preparation: the Kubernetes 1.36.5 disposable cluster also has all five CNPG, Barman and cert-manager deployments ready and both Barman certificates ready. Neither disposable cluster has an application database or application workload yet.
- W09 reviewer rerun: named inhibition tests execute and pass in the JSON contract, worker and handler packages. Baseline suppression and fabricated-response failures are retained. Valid alerts run before an audit failure is returned for retry; stored payloads are excluded from diagnostics.
- Storage/cache metadata prerequisites: RustFS 1.0.1 and Valkey 9.1.2 Alpine resolve to immutable amd64/arm64 identities. [Receipt](storage-image-prerequisites.json) proves OCI metadata only, not runtime, signature, retention or recovery qualification.
- No deployment or release qualification is inferred from these source gates.

## Rules

- A passing mock/static gate is not live qualification.
- Every DONE task needs actual source/artifact identity, command results, and retained evidence.
- CNPG, Flux, Valkey, RustFS/S3, Charlie, tenant scoping, audit, and encryption recovery remain invariants.
- Upstream vulnerabilities follow the accepted update-or-waive policy; disclose unresolved findings.
- Future publication policy and externally qualified claims require concrete applicable evidence/decisions.

- W01 final reviewer acceptance: 36 authority/resolution/inventory fixtures, 25 security-policy fixtures with the real Gitleaks integration case, and the existing public-client/nullable SDK runtime test pass. Official CNPG signature verification succeeds with networking disabled; the Astronomer signer policy rejects that foreign publisher. New release preflight has only contents/actions read permission for the cross-run signed record. No Astronomer-produced signed record or publication is claimed.

- CNPG17 reviewer characterization: migrations and all 21 expected integration tests pass on frozen `d4274b07`, zero structured skips, against an owned database via private-node NodePort. The first attempt failed after a dropped port-forward and remains retained separately. No restored-key, failover, backup/restore or PostgreSQL18 claim follows from this result. See [stateful prerequisites](cnpg36-stateful-prerequisites.json) and [integration receipt](cnpg17-integration.json).
- W09 decoder followups `c8c506a2` and `3bfce84b` reviewed: malformed/trailing/chunked bodies cannot reach ordinary mutation writes; explicit least-privilege shell fallback retains read-only Role verbs. Safe nested inhibition fields and typed errors pass actual handler tests. Paired canonical OpenAPI generation with historical Go SDK pointer compatibility remains owned by W06.

- W03 reviewer acceptance: frozen `9c91340b` focused HTTP client, resolver, handler, config and server tests pass, plus config documentation discovery gate. Mixed private/public CIDRs and metadata exceptions are rejected; the legacy public HTTPS redirect path strips sensitive headers and preserves existing CDN downloads. This source qualification does not establish signed distribution or complete RustFS operational recovery.
- W09 durable-write reviewer acceptance: integrated `ebc39669` after actual task envelope, no-write enqueue, corrupt-row dispatch continuation and FrozenRollout prewrite tests pass. Zero-byte historical task payloads are read as an empty object without rewriting the durable row; new envelopes validate before SQL, retaining the existing dedupe/upsert semantics. Remaining audit envelope, inventory, migration 072 and repair work remains open.

- CNPG17 physical recovery characterization: Barman backup `20261009T202229` completed against RustFS; fresh Cluster UID differs from the source and three replicas become healthy. Schema71, all201 public table row counts and the synthetic recovery marker match. The restore does not archive into the original prefix. This single idle synthetic-fixture sample does not qualify production RPO/RTO, PITR, historical ciphertext/key usability or the complete application. See [physical restore receipt](cnpg17-physical-restore.json).

- W06 terminal-state reviewer acceptance: frozen `24ec277e` model/state/rollout suites pass. Integrated `c0fbf1e6` retains existing database `aborted`/`rollback_failed` values as terminal unavailable states, without adding writer transitions or resumption. Full category parity/SDK generation remains open.
- W16 exploratory evidence: selected native Go module v1.0.0 with `fips140=on` passes six actual auth encryption/legacy-key/password compatibility tests, zero skips. `fips140=only` fails at startup in redis script SHA1 identification before those auth tests run; its credential package selector matched no tests and is not counted. Diagnostic-only mode is not a production policy or certified product claim. See [native mode](fips-on-auth.json), [diagnostic mode](fips-only-auth.json), and [official Go mechanism/limitations](https://go.dev/doc/security/fips140). Full W16 remains open.

- Combined W01/W05 frontend receipt integrated `6b74694a`: exact frozen `07c9f908`, 337 files/2344 tests, strict install/type/lint/code-health/build/CSP/budget and 18 Chromium component cases pass. Reviewer verified every retained gate-log digest, full-suite counts, both real pre-suite doc-drift failures, and reran all18 browser cases (one worker, zero retries): PASS. This does not qualify Firefox/WebKit, server authentication, stateful deployment or publisher signing.
- W09 audit envelope series `e1142bc2`/`e6242374`: reviewer frozen named tests pass for no-write marshal rejection, mandatory error/no-bus behavior, safe failure diagnostics/metrics, corrupt audit dispatch continuation, preserved opaque numbers and exact bounded frozen-plan reader. Inventory, staged constraints and operator repair remain open.
