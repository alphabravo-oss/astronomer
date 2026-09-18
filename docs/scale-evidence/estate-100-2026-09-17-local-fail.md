# Estate-100 exact-image local engineering run — failed

This is measured failure evidence, not a certified capacity claim. It is
retained because it closed several implementation questions and exposed the
remaining database-capacity and harness defects. A later run must produce its
own evidence; this row must never be converted into a pass.

| Field | Value |
|---|---|
| Date | 2026-09-17 |
| Build | `0e0a321dd20a9b7f3072afb6fb705c150c1f710d` |
| Profile | `estate-100` |
| Environment | Local shared 16-CPU, 32-GiB host; one server, one worker, bundled PostgreSQL 16 and Valkey 8 on k3s `v1.34.2+k3s1` |
| Helm release | `astronomer`, revision 45 |
| Duration | 30 minutes |
| Target / achieved rate | 500 / 473.55 requests per second |
| Requests | 852,397 |
| Result | **Fail** |
| Primary reported failure | DB-pool empty-acquire rate 12.030/s exceeded the 0.100/s threshold |
| Markdown report SHA-256 | `9730424476d4c53060ee509dfb9e3111d1ca7401bb04e57597010579d7f1e896` |
| Machine report SHA-256 | `6e54a6ccba3d772f40959f45ea40fcb642eb94856d2fe51812eda27cfa955b50` |

## Exact deployed artifacts

The release was deployed with immutable manifest digests:

| Component | Manifest digest |
|---|---|
| server | `sha256:6fd555f05fe7de7d4e79f661560e638856d17d92e0fa5a2e9381253e8305667c` |
| worker | `sha256:ec47fb1ea5385c3af4d8a5a276171dbc3c0ba68abd5319d12ee5092d621693dc` |
| agent | `sha256:c93323880690ccfbaab3f244699bec2cdafc24468abb8e28a3f2daf615217c3f` |
| migrate | `sha256:cebdc99b435e6f9c63fdc780558c1a8dd6ee0b78025d1bc6318c03ac85a2fe17` |
| frontend | `sha256:102245cf8b6f815ac9b0d79a9a2aff0cae8aaaf18a6e97129e3335d45d3c23b2` |
| shell | `sha256:b126e515e5cf873180c69454efd9b0a7998017d414ec7eb1b9382b294d9c0ce6` |
| DR | `sha256:1998c551026e761b25adf08f13a489a584d1061f1f454bb92077fc90c2d38eeb` |

The server requested 4 CPU / 4 GiB and was limited to 8 CPU / 8 GiB. The
worker requested 1 CPU / 1 GiB and was limited to 4 CPU / 2 GiB. The
application pool was fixed at 150 minimum and maximum connections, while the
bundled database used 400 maximum connections. The deployment passed health
and readiness checks before the run.

## Useful measurements

All endpoint latency budgets passed. Non-2xx responses were confined to the
bounded reconnect window.

| Scenario | Requests | p99 |
|---|---:|---:|
| Cluster list | 213,326 | 298 ms |
| Cluster pods | 127,298 | 127 ms |
| Cluster events | 42,896 | 122 ms |
| Cluster deployments | 42,766 | 106 ms |
| Cluster services | 42,682 | 102 ms |
| Audit logs | 85,100 | 97 ms |
| Authentication identity | 170,619 | 149 ms |
| Project list | 85,321 | 99 ms |

The deliberate reconnect storm recovered 100/100 agents in 2m0.811s. Its 310
agent-route HTTP 503 responses were expected inside that recovery interval.
The server did not restart or OOM. Its observed resources remained bounded and
shrunk over the run: goroutines peaked at 811 and moved from 722 to 572, heap
peaked at 378,197,712 bytes and moved to 275,852,280, and open file descriptors
peaked at 324 and moved from 279 to 234.

The run accepted 8,964 mandatory audit operations. PostgreSQL contained 8,964
outbox intents and 8,964 canonical audit rows, with no duplicate or lost
accepted operation, no active or dead outbox row at the end, and 955 ms maximum
delivery lag.

## Why the run failed

The database pool's cumulative empty-acquire counter moved from 1 to 23,099,
or 12.030 waits per second. Although sampled simultaneous acquisition peaked at
99 of 150, transition bursts still exhausted the pool. Raising the failure
threshold would conceal that pressure, so the final local topology instead
needs an explicitly sized application pool and PostgreSQL connection limit.

The engineering run also exposed three independent harness/accounting defects:

1. A one-token rate limiter started every request serially. It achieved 473.55
   RPS, just below the 475 RPS minimum that certification mode would enforce.
2. Mandatory audit operations were serialized behind request latency. One
   operation was cancelled at the window boundary, producing 8,965 attempts,
   8,964 accepted operations, and one rejection.
3. Synthetic state events continued during the two-minute drain, inflating the
   observed count to 1,438,512 instead of limiting emission to the measured
   workload window.

Because `certification=false`, the local report evaluator did not add the
achieved-rate shortfall or rejected audit operation to its failure notes. They
remain additional reasons not to treat this run as qualifying evidence.

## Remediation after the run

Commit `1c7fdfe8` replaces one-at-a-time traffic scheduling with bounded 100-Hz
microbatches, lets already scheduled requests finish after the workload
deadline, schedules audit requests concurrently and joins them, and binds
state-event and reconnect production to the measured workload context. Focused
normal and race tests cover the corrected scheduling and conservation rules.

Commit `11a243bf` makes the bundled PostgreSQL maximum-connection setting a
validated, rendered chart value so the next local topology is reproducible
instead of relying on a manual database alteration. The final exact candidate
must be rebuilt, deployed, and rerun for the full 30-minute window. These fixes
are not retroactive proof that this failed run passes.

Subsequent short preflight work found that the initial 100-Hz correction still
released five requests together every 10 ms and that engineering-mode reports
did not enforce every numerical conservation rule. Commit `176fb894` derives
the scheduled target from elapsed time at up to 1,000 Hz, fails engineering
runs on rate/duration/state-event/audit gaps, and waits for a fresh post-drain
outbox metric. With a 150-maximum/50-warm application pool, the two-minute
estate-shaped preflight delivered exactly 60,000 requests at 499.96 RPS with
50 ms cluster-list p99, zero new empty-pool acquires, and 600/600/600 audit
conservation. That short result validates the corrective direction but is not
a substitute for the required full rerun.

## Qualification caveat

This was an unsigned local engineering run and did not supply the protected
day-2 drill evidence required by certification mode. Even if every numeric
threshold had passed, it would not be a certified production rung. A qualifying
run must use the final exact commit and image set and satisfy the signed
evidence contract in [`../scale-baseline.md`](../scale-baseline.md).
