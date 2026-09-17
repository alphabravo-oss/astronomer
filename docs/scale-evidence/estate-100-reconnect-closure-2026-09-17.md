# Estate-100 reconnect classification closure — local engineering evidence

This evidence closes a runtime defect found by a full estate-shaped local run.
It does **not** certify the estate-100 capacity rung and does not replace the
protected Phase 6 executions. The full run failed one strict threshold; the
fix was then built, deployed, and exercised by a shorter targeted live run.

## Full 30-minute finding

| Field | Value |
|---|---|
| Date | 2026-09-17 |
| Build | `f0d9dd2cc6220f0a55ea15a962574e82e3d75cdc` |
| Helm release | `astronomer`, revision 59 |
| Profile | `estate-100` |
| Environment | Local single-node k3s `v1.35.7+k3s1`, 16 vCPU, approximately 32 GiB RAM, one server and one worker |
| Duration | 30 minutes |
| Target / achieved rate | 500 / 500.00 requests per second |
| Requests | 900,000 |
| Mandatory audit conservation | 9,000 accepted / 9,000 independently observed intents / 9,000 canonical rows |
| Reconnect | 100/100 agents in 2m1.409s; 317 bounded 503 responses |
| Result | **Fail** — one tunnel-backed `cluster_events` request returned 502 outside the zero-error budget |
| Markdown report SHA-256 | `98c92e79156761dbe357f50283db6593deccefdcba05e5c6ee85626fb37633fe` |
| Machine report SHA-256 | `13c0cdc07c48168e40d5b36530755049380dadef9bceb5684691ca6db6aa58d7` |

All other evaluated thresholds passed. Cluster-list p99 was 9 ms, cluster-pod
p99 was 14 ms, the measured window was exactly 30 minutes, the database pool's
empty-acquire counter grew by 18 (0.010/s, below the 0.100/s ceiling), worker
queue depth and event-relay lag ended at zero, and terminal goroutine, heap,
and open-file windows remained bounded. All platform workloads stayed Ready
with zero restarts.

The one 502 was real rather than a harness transport error. During the planned
reconnect, a request could remain attached to the old agent connection after a
new session replaced it. Closing the old connection closed that request's
stream, and the proxy incorrectly classified the closure as an invalid agent
response (502) instead of temporary agent unavailability (503).

## Fix and targeted live proof

Commit `f5be97448d5de9b1c8463349d68782bd134593d1` introduces a closed-stream
sentinel and maps a closure to 503 only when the Hub's current agent connection
differs from the one that owned the request. Malformed, overflowed, and other
invalid responses remain 502. Focused normal and race tests preserve both
branches, and the repository complexity and code-health gates pass.

The exact commit was rebuilt as a coherent seven-image set and atomically
deployed as Helm revision 60. A five-minute targeted profile then drove 100
agents, 500 RPS, 750 state events/s, and 5 mandatory audited mutations/s, with
a 100% reconnect storm at minute 1 and 15 seconds of jitter.

| Metric | Observed |
|---|---:|
| Verdict | **Pass** |
| Requests / rate | 150,000 / 500.00 RPS |
| Reconnect | 100/100 in 16.797s |
| Bounded reconnect responses | 284 HTTP 503; zero HTTP 502 |
| Mandatory audit conservation | 1,500 / 1,500 / 1,500 |
| Cluster-list / pod / event p99 | 14 ms / 17 ms / 32 ms |
| DB empty-acquire counter | unchanged at 1 |
| Worker queue / relay lag | 0 / 0 |
| Workload restarts | 0 |
| Markdown report SHA-256 | `964603ac3e441eacafce1c2bc8a6977dfb74762dd277be45650a26b4fb8fdd0a` |
| Machine report SHA-256 | `277bd61b9138428c49e28ab0166532d54865e09acd5936598c556e65e2a5e340` |

## Exact revision-60 artifacts

| Component | Image digest |
|---|---|
| server | `sha256:e35f137e1e5fcae05b921b2fc8227e23307e47878bdef745599f13d6cb2d40a1` |
| agent | `sha256:8e1367745985d3d8ea9889941bd13a36ed40cfea441246cc52c9a8f1e8c91410` |
| worker | `sha256:28436faa55a24f313b820aca4fb8ce3db93b9cb5b92eb25c2dfde3237594f0d3` |
| migrate | `sha256:0ac26c262fa4d18bd7fd8ff7f228e6319d77ad4fd426df2ed0f25e59ec030e1b` |
| frontend | `sha256:c589ebd6f6521dad897aab31ddca20ce0a8da771c150a8181e41b0640c266557` |
| shell | `sha256:b8408a36081401626aa890bfef40aaa356ffe0ec7398f025e231a9391ebe11f9` |
| DR | `sha256:03193af57f3a89359404b18ea5bc58dd3310d0d20a91232d99b5094ce7a0a158` |

Post-deploy health and readiness were green, schema migration 52 was clean,
and release `v1.1.0-advisor016.f5be9744` was registered with the exact agent
digest. The short pass proves the scoped reconnect correction against the live
deployment. A final full estate-100 rerun is intentionally deferred to the
last qualification wave and is still required before any retained capacity
claim.

