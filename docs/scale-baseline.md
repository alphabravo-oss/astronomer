# Astronomer Go — Tested Scale Baseline

This document records the cluster-fleet envelope that the load-test harness
(`scripts/loadtest/`) has demonstrated under the workload defined in
`scripts/loadtest/scenarios.go`.

The harness is the **source of truth** — re-run after any infra change
(database sizing, replica count, rate-limit tuning, etc.) and add a new row to
the "Latest validated baseline" table below.

## Latest validated baseline

| Date | Build | Clusters | HTTP RPS | p99 (cluster-list) | p99 (resources) | DLQ at end | Verdict |
|---|---|---|---|---|---|---|---|
| _TBD — run the harness_ | _TBD_ | _TBD_ | _TBD_ | _TBD_ | _TBD_ | _TBD_ | _TBD_ |

## Initial run

There is no recorded measurement yet — populate the row above by running:

```bash
# from astronomer-go/ root, against a running management-plane deployment:
make load-test \
  LOADTEST_SERVER=https://your-server \
  LOADTEST_TOKEN=/path/to/admin.jwt \
  LOADTEST_CLUSTERS=100 \
  LOADTEST_RPS=200 \
  LOADTEST_DURATION=10m
```

Then:
1. Read the verdict line from `loadtest-report.md` (or wherever
   `LOADTEST_OUT` pointed).
2. Copy the p99 latency cells from the "HTTP latency per scenario" table.
3. Copy the worker queue pending depth from the "Server resource snapshot"
   table.
4. Append a row above with the commit SHA from `git rev-parse --short HEAD`
   in the `Build` column.

If the verdict is `fail`, the per-reason list in the report's "Notes"
section tells you which threshold tripped. Address the regression before
recording a baseline — the doc table is reserved for `pass` rows.

## How to reproduce

```bash
make load-test CLUSTERS=100 RPS=200 DURATION=10m
```

Equivalent via env vars (which is what the make target reads):

```bash
LOADTEST_CLUSTERS=100 LOADTEST_RPS=200 LOADTEST_DURATION=10m make load-test
```

For a local smoke (e.g. validating chart updates against `make dev`):

```bash
make load-test LOADTEST_CLUSTERS=5 LOADTEST_RPS=20 LOADTEST_DURATION=30s
```

## Recommended fleet sizing

These are starting points — re-validate with the harness on the actual
target infrastructure.

| Cluster count | Recommended chart values |
|---|---|
| up to 50 | base `values.yaml` (`server.replicaCount=2`, `pgxpool maxConns=25`) |
| 50–500 | `values-production.yaml` + `pgxpool maxConns=50`, `worker.replicaCount=5` |
| 500–2000 | external HA Postgres + Redis Cluster + `worker.replicaCount=10` + chart-tunable rate limits raised |
| 2000+ | custom sizing — talk to support |

## What the harness measures

Each run records, per scenario, p50 / p95 / p99 latency from the HTTP
workload, plus these server-side gauges scraped from `/metrics` every 15s:

- `astronomer_agent_connections` — number of healthy agent tunnels
- `astronomer_db_pool_acquired_connections` / `..._max_connections` /
  `..._empty_acquire_count_total` — pgxpool pressure
- `astronomer_worker_queue_depth{state="pending"}` — Asynq backlog
- `astronomer_dropped_events_total` — congestion drops anywhere in the
  bounded-channel paths
- `go_goroutines`, `go_memstats_alloc_bytes` — Go runtime baseline

Pass/fail thresholds default to:
- `cluster_list` p99 < 500 ms
- `cluster_pods` (k8s proxy) p99 < 2 s
- 100% of synthetic agents connected at end
- DLQ depth < 10
- DB pool blocking acquires < 0.1 / s sustained
- No goroutine leak (peak/start ratio < 1.5x)

All overridable via `LOADTEST_THRESH_*` env vars — see
`scripts/loadtest/README.md`.

## Workload mix

The HTTP workload models a dashboard polling pattern:

| Scenario | Weight | Path |
|---|---|---|
| cluster_list | 30% | `/api/v1/clusters/` |
| cluster_pods | 25% | `/api/v1/clusters/.../k8s/api/v1/pods` |
| auth_me | 20% | `/api/v1/auth/me/` |
| project_list | 10% | `/api/v1/projects/` |
| audit_logs | 10% | `/api/v1/audit-logs/` |
| admin_queues | 5% | `/api/v1/admin/queues/` |

If your deployment has a different traffic mix (e.g. heavy use of helm
operations or exec sessions) edit `scenarios.go` before declaring a
baseline. Note in the table's `Build` column when scenarios change so
historical rows remain comparable.
