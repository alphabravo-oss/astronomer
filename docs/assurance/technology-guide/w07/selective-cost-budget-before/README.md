# Selective cursor cost budget baseline

Frozen source `2aded732c65e1b9f285d038410f344d778ad842e` fails the representative cost budget on both owned PostgreSQL 17 and 18 databases at schema 75. Each run completes all 27 filter/mode subcases: 17 pass and 10 fail, with the enclosing parent failing too. There are 28 named terminal results per major, not 28 independent parents, and zero skips.

The fixture inserts 100,000 synthetic alerts inside a transaction and rolls them back. For each of nine filters and three cache modes, it captures first and continuation query plans plus observed counters around eight executions on the same physical pgx connection. The 250-row fixture budget sums every scan's returned rows, discarded filter/index-recheck rows and loops, starting at the plan root. It is not a response-size limit or a claim about a real fleet.

| Major | Named pass/fail/skip | Plans | Eight-execution counter deltas | Plans over the total scan budget | Maximum examined rows |
| ----- | -------------------- | ----- | ------------------------------ | -------------------------------- | --------------------- |
| 17    | 17 / 11 / 0          | 54    | 54                             | 19                               | 100,000               |
| 18    | 17 / 11 / 0          | 54    | 54                             | 19                               | 100,000               |

The common/unfiltered cases pass; sparse-controller, missing-controller and generic-plan selective combinations expose the costly nullable-filter query shape. Every plan remains available in [results.json](results.json), including failing budget cases. Root independently reconstructed all 108 plans and 108 counter deltas from the raw outputs and found the same projection. The earlier [characterization packet](../selective-cost-100k/README.md) has no budget assertion and remains unchanged.

[packet.json](packet.json) binds ten source files, selected Go 1.27.2, full source fingerprints before and after, the exact command, raw external receipts, ownership fences, and all 14 successful capacity checks. PostgreSQL 17 took 49.459 seconds including compilation; PostgreSQL 18 took 21.853 seconds. Both owned process groups joined before the compiler lease was released. No SQL query, index, schema, or production behavior changed during these runs.

The first runner attempt stopped before launching Go because its schema matcher omitted the explicit `int64` in the canonical declaration. Its original 2.495-second failure receipt is retained separately. The corrected runner matches the unique anchored typed declaration, checks the requested schema exactly, and passes 17 compact Python fixtures, including the actual frozen file and missing/comment/untyped/wrong-type/duplicate negatives. The approved runner and fixtures are copied byte-for-byte as [run-bounded.py](run-bounded.py) and [test_runner.py](test_runner.py). They are historical source-bound qualification artifacts; do not rerun them against a different source or overwrite private references.

Raw Go output stays external at `/tmp/w07-budget-before-2aded732-run2`, with hashes in the packet. The structured projection omits dynamic entity identifiers and SQL filter values while retaining scan/removal/loop measurements and counters. Original logs, the initial matcher failure, and prior runner bytes remain available for review. The [summarizer](summarize.py) projects those exact raw originals; it does not run Go or change database state.

The proposed finite direct-filter queries and two concurrent indexes need their own generated-source review and actual after qualification. This packet establishes the before failure; W07 remains in progress. It supplies no race, vet, full-handler, native source-security or release-approval claim.
