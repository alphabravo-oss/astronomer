# Control-plane selective query cost at 100,000 rows

Frozen source `3c405b96` passed the same actual PostgreSQL 17 and 18 characterization: one parent and 27 filter/mode cases per major, zero failures or skips. Each major produced 54 `EXPLAIN ANALYZE EXECUTE` plans and 54 assertions of eight actual prepared-query executions. Handler vet passed. This is synthetic transaction-local query characterization, not a fleet capacity qualification or whole W07 completion.

The connection retains pgx's production default statement cache. Nine filter shapes run under `auto`, `force_generic_plan`, and `force_custom_plan`, set only within the rollback transaction. Before/after `pg_prepared_statements` counters prove the forced modes and record the observed auto behavior; every measured statement executes eight times before explaining the existing cached statement. Matching predicates are checked against returned rows. Short or empty initial pages do not advertise a continuation; their After SQL is measured separately with a synthetic upper anchor.

The following ranges are identical on both tested majors. They count scan rows returned plus rows removed by filters/index rechecks, multiplied by actual loops. They describe first and continuation measurements, including the explicitly synthetic short-page After path. They do not infer a universal 201-row bound from the response limit.

| Filter                       | Auto examined | Forced generic examined | Forced custom examined |
| ---------------------------- | ------------: | ----------------------: | ---------------------: |
| Unfiltered                   |           201 |                     201 |                    201 |
| Common status                |           203 |                     203 |                    203 |
| Sparse status                |       202–203 |           20,211–20,300 |                202–203 |
| Common controller            |       203–204 |                 203–204 |                203–204 |
| Sparse controller            | 20,000–20,100 |           20,000–20,100 |          20,000–20,100 |
| Common status and controller |       205–206 |                 205–206 |                205–206 |
| Sparse status and controller |             9 |                 100,000 |                      9 |
| No matching controller       |       100,000 |                 100,000 |                100,000 |
| No matching combination      |     0–100,000 |                 100,000 |                      0 |

The selective cost requirement remains open. Sparse controller and absent controller filters materially scan unrelated rows; generic nullable predicates also lose status selectivity. A separately reviewed follow-up should choose finite cursor-mode SQL variants with direct filter predicates and add only indexes justified by these measurements. No index 076–078, schema, connection-cache setting, or production query changed in this slice.

The first frozen fixture (`3122535f`) failed before measuring cost because repeated active `(controller, condition_type)` identities violated the existing unique index. Its named parent failed in 0.07 seconds. The correction gives every synthetic condition a distinct identity while retaining status/controller selectivity and timestamp ties. The transaction rolls back all rows on success and failure.

Read-only preflight also rejected the old private "17" reference because its service targeted PostgreSQL 18. Both original references remain untouched. Fresh exclusive references use mode 0600; each lane verifies the current cluster UID/owner, primary pod UID, service selector/UID, endpoint target, node host, and connected server major/database/schema before the test. A separate initial `psql` invocation failed because an environment `PGDATABASE` URI did not select the intended connection; split private libpq variables corrected the invocation. These are retained preflight failures, not product failures.

[packet.json](packet.json) binds source files, commands, external raw originals and hashes. [results.json](results.json) contains all plan measurements and cache counter deltas without synthetic entity identifiers or filter expressions. [run.py](run.py) is the retained guarded lane runner; [summarize.py](summarize.py) reconstructs the structured projection. Raw Go events remain external and available for independent verification; old committed evidence/history is unchanged.
