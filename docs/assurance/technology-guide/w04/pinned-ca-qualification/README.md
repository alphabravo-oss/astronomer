# Pinned registry CA and separate mirror signing authority: local qualification

Frozen source `24d9904de4599aa22ec342ae5a2d0bebccdfdb97` passed the reviewed local sequence. The clean full checkout contained 7,150 tracked files, with exact Git bytes/modes/link payloads retained and draft migration 079 absent. The unchanged historical CA implementation from `2908e9a7` passed its compatibility control and was restored after its owned child group drained. All gate snapshots were stable; final source equals initial source.

Actual tools were Go 1.27.2, Node 26.11.1 and selected Cosign 3.1.3 (binary SHA256 `4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71`). The exact environment and commands are in the original compressed receipt and retained driver. Go used `GOTOOLCHAIN=go1.27.2`, `GOMAXPROCS=4`, `GOFLAGS=-p=2`, `GOWORK=off`. Documentation used matching-lock read-only W07 Node modules via `NODE_PATH`. No dependency installation occurred in the qualifier.

| Executed gate | Actual parents / terminals | Result |
| --- | --- | --- |
| Historical provider CA compatibility race control | 3 / 3 | PASS |
| Focused physical CA, vendor/operator signing, mirror identity, rotation and cancellation race | 20 / 62 | PASS |
| Full distribution, resolver, provider and HTTP client race | 143 / 480 | PASS |
| Full distribution publisher race, including actual signed graph and readback | 34 / 187 | PASS |
| Vet on those five packages | — | PASS |
| JSON governance, complexity and dependency boundaries | — | PASS |
| `make docs-check`, `make cli-docs-check` | — | PASS |

Every race run had zero failures, skips and build failures. The exact package terminals and required selected-Cosign producer/consumer/publisher parents are checked against raw Go JSON. Twelve gate groups and 53 capacity groups exited zero, held their leaders until descendant drain, and reaped with no live descendants. All fatal capacity checks required at least 20 GiB and 20%; the original final guard measured 69 GiB and 21%. Overall/step deadlines and signal cleanup remain those of the approved bounded runner. No release-contract or DB gate is added to this scope.

The earlier failures remain original evidence: sparse source omission plus build-event parser failure; missing authenticated mirror endorsement; pre-Go capacity failure; and the mirrored feed fixture's missing declared config blob (one leaf plus its parent failed). The corrected fixture adds the unchanged `{}` config declared by the OCI manifest; production verification is unchanged. The original failed attempt is not rewritten as green.

[index.json](index.json) binds 149 byte-preserved original artifacts, including all five attempts, source selections, restored baseline originals, raw logs and runner/control sources. Raw members use deterministic gzip to keep the packet compact. Successful original receipt SHA256 is `9c7a3f231625c2682cea575f9e82a4664078ae6b4218ad6eb7b7d81f416311a3`; compressed storage SHA and expanded original SHA are distinct explicit bindings. The private originals remain under `/root/.cache/astronomer-032/qualification/w04-mirrored-ca-24d9904d`.

Run `python3 docs/assurance/technology-guide/w04/pinned-ca-qualification/verify.py` from this repository to check artifact schemas/hashes, all frozen Git objects, actual race terminals, source stability, tools, claims and numeric capacity guards. Run the adjacent `test_verify.py` for one positive packet and 22 negative schema/binding/claim controls. These Python checks read retained evidence and Git objects only; they do not rerun Go or grant authoritative release approval.

This qualifies only the reviewed frozen local CA/mirror authority and coupled producer/consumer source. The descendant shared builtin compiler, current-catalog accessor, private participant, migration 079, repository/server composition and DB transactions have not been exercised by this packet. Whole supplier payloads, actual measured DB freshness, full no-egress qualification, production OIDC publisher/server startup, durable acceptance/revocation, six-hour report refresh and actual scan-used job/report provenance remain open. Ordinary system-root behavior is preserved; optional pinned metadata transport is a separate explicit profile, with large supplier payload streaming still requiring its own bounded qualification.

Integration must cherry-pick only the six reviewed source commits onto current main. This qualifier fork predates current main's inline OCI config and newer governance fixes; neither the whole qualifier branch nor its old metadata may replace those newer sources.
