# Exact inline configuration qualification

Frozen source ad2aaa28 supports the observed exact OCI empty inline configuration without changing the original manifest. This packet qualifies the resolver boundary. Producer/parent archive/offline graph compatibility and whole supplier payload qualification remain open.

The changed signed/supplier tests first ran against the unchanged production parser at 02364e25. Both inline positive leaves failed, as expected: the parser rejected `config.Data`. The original baseline log and before/after fingerprints are retained unchanged in `baseline/`. Those snapshots include revised test bytes but unchanged production source; this distinction is intentional. The other negative controls did not fail.

On the corrected clean source, with Go 1.27.2, GOMAXPROCS=4, GOFLAGS=-p=2, GOWORK=off:

| Gate | Actual outcome |
| --- | --- |
| Focused resolver race | 3 parents, 48 total test terminals, zero failed/skipped |
| Full resolver race | 42 parents, 136 total test terminals, zero failed/skipped |
| Resolver vet | PASS |
| JSON governance | PASS; no ownership/inventory regeneration |
| Unchanged complexity budget | PASS |
| Dependency boundaries | PASS |
| Node 26 documentation gate | PASS; matching-lock read-only NODE_PATH |

Every gate had a separate passing maintained capacity check. The frozen before/after maps contain 92 identical inputs. The actual metadata signature fixtures use an in-process QA key; they are not a production OIDC signature or a selected Cosign CLI producer fixture. The gate record deliberately sets `producerFixturePassed=false` rather than claiming a test outside the resolver package ran.

The same approved metadata-only observer and driver then ran against clean ad2aaa28. Its build passed in 6.832 seconds, and its bounded network operation passed in 2.872 seconds. Both roles returned `descriptors-captured-not-payload`. The two original manifest bodies, 51 runtime source hashes and before/after equality are retained in `observer/`. Each role made only HEAD 401, token GET 200, HEAD 200 and immutable manifest GET 200. There was no configuration replacement fetch and no database layer request. Token bodies, credentials and signed redirect queries were not retained. All three observer capacity checks passed.

The observer has no authority to endorse the supplier or accept an update. Full compressed payload hashes, expanded layout, actual database metadata freshness, publication, durable acceptance and scan-used measurements remain unperformed by this packet. The configured scanner identity remains compatibility intent only. The previously failed original whole-transfer and metadata-only receipts stay immutable in `../supplier-metadata-diagnosis/`.

`check.py --self-test` verifies raw test terminals, hashes, source/Git binding and conservative observer scope. A passing checker asserts retained evidence consistency, not whole W04 completion. The private observer executable is preserved with its receipt hash; it is intentionally not stored in Git. The command driver is retained for exact invocation provenance and must only run in a separately authorized compiler window.
