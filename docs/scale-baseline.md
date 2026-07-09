# Astronomer Go — Scale Baseline (Honest Status)

## Status: no validated load-test baseline yet

This document intentionally does **not** claim a certified fleet size or RPS
envelope. The load-test harness exists under `scripts/loadtest/`, but as of the
last review **no pass row has been recorded** against a production-like
management plane. Treat any prior marketing numbers as aspirational until a
harness run lands a `pass` verdict here.

## What exists today

| Artifact | Role |
|---|---|
| `scripts/loadtest/` | HTTP + agent-style workload harness |
| `make load-test` | Entry point (see Makefile) |
| This file | Place to record **measured** results only |

## How to produce a baseline

Run against a real management-plane deployment (not a unit-test fake):

```bash
# from astronomer/ root
make load-test \
  LOADTEST_SERVER=https://your-server \
  LOADTEST_TOKEN=/path/to/admin.jwt \
  LOADTEST_CLUSTERS=100 \
  LOADTEST_RPS=200 \
  LOADTEST_DURATION=10m
```

Then:

1. Confirm the report verdict is `pass` (not `fail` / partial).
2. Append a row to the table below with commit SHA, cluster count, RPS, and
   p99 latencies from the report.
3. Do **not** invent numbers. If the harness was not run, leave the table empty
   and record a blocked row with the failure reason.

## Latest validated baseline

| Date | Build | Clusters | HTTP RPS | p99 (cluster-list) | p99 (resources) | DLQ at end | Verdict |
|---|---|---|---|---|---|---|---|
| _none recorded_ | — | — | — | — | — | — | — |

## Blocked / failed harness attempts

| Date | Build | Environment | Reason | Evidence |
|---|---|---|---|---|
| 2026-07-09 | `2991f9d` + residual tree | host k3s | Management-plane namespace `astronomer` empty (no server pods); leftover agents in `astronomer-system` unhealthy (`ImagePullBackOff`/`Error`). Cannot dial a live LOADTEST_SERVER. | Goal session residual assessment; harness not started against a reachable control plane. |

## Engineering guidance (unvalidated)

These are **starting points for capacity planning**, not SLOs:

| Cluster count | Suggested chart posture |
|---|---|
| up to 50 | base `values.yaml` |
| 50–500 | `values-production.yaml`, raise worker replicas / pool |
| 500–2000 | external HA Postgres + Redis; tune rate limits |
| 2000+ | custom sizing after a harness pass at that scale |

Until a **pass** row is recorded above, operators should assume the product has
not been load-certified for their target fleet size.
