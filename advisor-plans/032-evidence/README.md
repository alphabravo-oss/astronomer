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
| W01 | IMPLEMENTATION READY | Authority, source coverage and focused compatibility PASS; combined frontend qualification running | Integrated final authority/action/consumer/signature series through `c23f77bc`. All 176 components resolved, 335 maintained pin occurrences bound to 212 distinct inventory entries. Exact-source signed release resolution is mandatory for new candidates. Combined W01/W05 qualification and positive Astronomer publisher execution remain separate. |
| W02 | IN PROGRESS | Enforcement fixtures PASS; source qualification FAIL | Integrated `02f07527`, `a8c0dc4b`, `feddd759`: source collection/enforcement, exact exceptions and publication policy. Complete scan: 569 findings, zero engine errors; 492 HIGH remain unaccepted and 71 MED need triage. External ruleset/check readback remains pending. |
| W03 | IN PROGRESS | Network policy / resolver / S3 probe regressions PASS; signed distribution chain pending | Integrated `326557ec`, `e61ab626`, `d51ccc43`: bounded destination authorization, immutable policy snapshot, metadata exclusions and credential-free public resolver redirects. Signed producer/child OCI closure and durable mirrored provisioning continue in the executor worktree. |
| W04 | TODO | TODO | Await dependencies and bounded implementation slice. |
| W05 | IMPLEMENTATION READY | Full source suite, build and 18 browser cases PASS; combined W01/W05 qualification pending | Integrated refresh `7b167632`, read forwarding `c4fd5119`, awaited Vault invalidation `2867346d`, semantic modal submission `da569b90`, assertion correction `cfe5802b`, and decreasing complexity ratchets `9a896e29`. Integrated remaining observation adapters `b1ede805` and source-bound evidence. All 735 API function declarations classified; reviewer socket tests and AST checks pass. Combined W01/W05 qualification remains pending. |
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
