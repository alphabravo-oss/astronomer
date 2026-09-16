# Estate-100 local engineering run — failed

This is measured failure evidence, not a certified capacity claim.

| Field | Value |
|---|---|
| Date | 2026-09-10 |
| Build | `32100314+working-tree-2026-09-10` |
| Profile | `estate-100` |
| Environment | One local server process on a shared Codex host; PostgreSQL 16 and Redis 7 in local containers |
| Duration | 30 minutes |
| Target / achieved rate | 500 / 500.20 requests per second |
| Requests | 900,499 |
| Result | **Fail** |
| Failure | DB-pool empty-acquire rate 15.459/s exceeded the 0.100/s threshold |
| Report SHA-256 | `76b6ee5a4dcb79caba71fac7c58e04466eb76c81b22c22a09353050188185bb2` |

## Useful measurements

| Scenario | p99 | Status observations |
|---|---:|---|
| Cluster list | 65 ms | 225,408 HTTP 200 |
| Cluster pods | 79 ms | 135,084 HTTP 200; 132 transient HTTP 503 during reconnect drill |
| Cluster events | 86 ms | 44,866 HTTP 200; 47 transient HTTP 503 during reconnect drill |
| Cluster deployments | 40 ms | 44,862 HTTP 200; 51 transient HTTP 503 during reconnect drill |
| Cluster services | 42 ms | 45,012 HTTP 200; 52 transient HTTP 503 during reconnect drill |
| Audit list | 204 ms | 89,719 HTTP 200 |

The 100-agent reconnect storm completed with all 100 agents connected again.
Server resource peaks were 480 goroutines, 68,489,576 heap bytes, 209 open
file descriptors, and all 25 database connections acquired.

The run also returned 282 transient HTTP 503 responses during the reconnect
storm (0.000313 of requests). The engineering-mode report evaluator incorrectly
applied the HTTP-failure threshold only in certification mode, so the retained
report lists DB pressure as its sole reason even though the status table exposes
the 503s. That evaluator is now fixed and covered by a non-certification
regression test; future engineering runs fail on non-2xx responses too.

## Database evidence and remediation

`pg_stat_statements` identified the filtered audit-list query as the dominant
database cost: 89,719 calls, 11,365,332 ms cumulative execution time, and
126.677 ms mean execution time. It used a materialized candidate set and exact
count on every page request.

The worktree was changed after the run to use stable `LIMIT + 1` lookahead
pagination with no exact count. The API now reports `hasMore` for filtered audit
pages, and a regression test rejects reintroduction of `MATERIALIZED`,
`count(*)`, or `jsonb_agg` into that path. A new estate-100 run is required to
measure the result; this failed row must not be converted into a pass.

A post-change `EXPLAIN (ANALYZE, BUFFERS)` against the same 153K-row local
audit dataset used the partition `created_at, id` index, returned 101 rows in
0.326 ms, and touched 106 shared buffers. This isolates the query improvement;
it is not a substitute for rerunning the full mixed workload.

## Qualification caveat

This local engineering topology did not run a worker process, so the audit
outbox dispatcher was absent. The run accepted 8,996 mandatory-audit intents,
but none reached the canonical audit table before the drain deadline. That is a
topology/setup limitation of this run and independently prevents certification.
A qualifying rerun must include the complete server/worker topology and the
signed drill evidence required by the certification profile.
