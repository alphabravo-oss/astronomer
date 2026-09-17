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

## Final coherent local candidate

Two follow-up commits close the matching cross-pod classifications before the
last qualification wave:

- `05abe58f` maps internal Kubernetes agent-send pressure to retryable 503 and
  covers the full-buffer path deterministically.
- `fe4b51ebca8ccc302fb53553b97ed6af74703482` maps internal Helm send failure
  and replaced-session closure to 503, preserves 412 for an unsupported
  capability, and preserves 502 for malformed agent responses.

The full tunnel package, focused race coverage, vet, and complexity checks
passed for these changes. The latter paths require multiple server replicas,
so the single-server local environment cannot turn them into cross-pod live
evidence; that limitation is explicit rather than hidden.

The exact `fe4b51ebca8ccc302fb53553b97ed6af74703482` source was rebuilt as a
coherent seven-image set with version `1.1.0-advisor016.fe4b51eb`, label-checked,
imported into local k3s, and atomically deployed as Helm revision 61. The
preflight and migration hooks succeeded. All five running release workloads
were Ready with zero restarts, `/health/` and `/readyz` were green, schema 52
was clean, the tunnel reported one connected cluster, and release
`v1.1.0-advisor016.fe4b51eb` was registered in draft state with the exact agent
digest. The only server warnings occurred during initial startup and did not
recur.

| Component | Revision-61 image digest |
|---|---|
| server | `sha256:20a083a81c37c2c88de0fdc089d72d0b785a4df23fffbbc1977212ad87286a5a` |
| agent | `sha256:3d13b2fdfae63f673cde2999facac789e5ebe099c8e40839fe64cf04451f8201` |
| worker | `sha256:adffdb047d421b0f510d538d4b96c3f6166c280787c47817ab9e5d6a5ea0720a` |
| migrate | `sha256:53c5bcf13e61c581f3e98b4419ffae0daaf92e51e95b0ed0cce7c256614e9001` |
| frontend | `sha256:00c0fc0245024ca0cedd07b52b873a18eb51734fd868631fb001b4e1f6d06bdb` |
| shell | `sha256:ebb681a53d60b00b8850226b10b5222b3a433df826048c4bea69902ca4b772b8` |
| DR | `sha256:48ac3b4ba735f0b7dca5c94be67f3ce6704783121909fc70339bb9314e161554` |

The five-minute reconnect run remains the focused live proof for the original
defect.

## Final broad local replay

The broad local gate was replayed after the revision-61 deployment at
`230f3971be9016760d1a62704383f2a867e31813`, a documentation-only child of
`fe4b51eb`; a path diff confirms that every production, test, deployment, and
frontend file is identical to the image-producing commit. The first pass
stopped at the release-contract disk-headroom guard after all static backend,
normal Go, and race Go gates had passed. Removing 121 superseded
`advisor016-*` Docker tags restored 190 GiB/58% free space. Verification then
resumed from the failed stage instead of repeating the expensive work.

The resumed constituent gates all passed:

- release, image-inventory, air-gap, protected-producer, Helm lint/render, and
  chart contracts;
- 1,207 frontend units, type/lint/build/bundle/audit, 124 primary browser
  cases, 264 route-smoke cases, and 50 visual cases;
- all 15 PostgreSQL integration contracts in normal and race modes, worker
  runtime and process-restart qualification in normal and race modes, Redis
  and PostgreSQL outage qualification in normal and race modes, tunnel queue
  HA, and PostgreSQL failover certification; and
- 15/15 disposable live-browser journeys, including successful Flux
  reconciliation and completed Velero snapshot/restore against disposable
  MinIO object storage.

The live agent-identity lane remained a declared skip because
`AGENT_IDENTITY_TEST_CONTEXT` was not supplied. The retained 30-minute
estate-100 rerun remains intentionally deferred; no scale-capacity claim is
made from the broad replay.
