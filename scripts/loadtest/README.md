# `scripts/loadtest` — synthetic-agent + HTTP load driver

This directory contains a self-contained Go program that exercises a running
management-plane deployment at a target cluster count and HTTP RPS, then
emits a markdown report with a pass/fail verdict.

It is the **source of truth** for the "Astronomer Go validated up to N
clusters under M RPS" claim in `docs/scale-baseline.md` and the chart README.
Re-run after any infra change (DB size, replica count, rate-limit knob) and
add a row to the doc.

## Quick start

```bash
# 1. Have a running server (local dev: `make dev` + `make run` in one terminal).
# 2. Get an admin JWT and put it in a file:
curl -s -X POST http://localhost:8080/api/v1/auth/login/ \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"..."}' | jq -r .access_token > /tmp/jwt
# 3. Run the harness:
make load-test LOADTEST_TOKEN=/tmp/jwt LOADTEST_CLUSTERS=100 LOADTEST_RPS=200 LOADTEST_DURATION=10m
```

Output is `loadtest-report.md` (override with `LOADTEST_OUT=...`).

## Flags / env vars

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `-server` | `LOADTEST_SERVER` | `http://localhost:8080` | Management-plane base URL |
| `-clusters` | `LOADTEST_CLUSTERS` | `50` | Synthetic agent count |
| `-rps` | `LOADTEST_RPS` | `100` | Aggregate HTTP request rate (token bucket) |
| `-duration` | `LOADTEST_DURATION` | `5m` | How long to drive load |
| `-token` | `LOADTEST_TOKEN` | _(empty)_ | Path to a Bearer JWT (required for auth'd endpoints) |
| `-out` | `LOADTEST_OUT` | `loadtest-report.md` | Markdown report path |
| `-verbose` | `LOADTEST_VERBOSE` | `false` | Debug-level log output |
| `-skip-agents` | `LOADTEST_SKIP_AGENTS` | `false` | HTTP-only mode (skip WS dial) |

### Threshold env vars

The pass/fail verdict is heuristic and the thresholds are tunable:

| Env var | Default | Pass condition |
|---|---|---|
| `LOADTEST_THRESH_CLUSTER_P99_MS` | `500` | `cluster_list` p99 latency <= this value (ms) |
| `LOADTEST_THRESH_RESOURCES_P99_MS` | `2000` | `cluster_pods` p99 latency <= this value (ms) |
| `LOADTEST_THRESH_CONNECTED_MIN` | `1.0` | Fraction of N agents connected at end |
| `LOADTEST_THRESH_DLQ_MAX` | `10` | `astronomer_worker_queue_depth{state="pending"}` at end |
| `LOADTEST_THRESH_EMPTY_ACQUIRE_QPS` | `0.1` | Rate of `db_pool_empty_acquire_count_total` over the run |
| `LOADTEST_THRESH_GOROUTINE_RATIO` | `1.5` | `go_goroutines` peak / start ratio |

## How it works

1. **Verify**: hits `/api/v1/auth/me/` once. Bad token, unreachable host, or
   5xx aborts the run with `VERDICT: fail (harness error: …)`.
2. **Spawn agents**: opens N WebSocket tunnels to
   `/api/v1/ws/agent/tunnel/{cluster_id}/`, each with a fresh UUID v4
   cluster ID. Each agent:
   - sends `CONNECT`, expects `CONNECT_ACK`
   - emits a `HEARTBEAT` every 30 seconds
   - replies to `K8S_REQUEST` with a canned 200 `PodList`
   - replies to `K8S_STREAM_REQUEST` with a header + end frame
   - on disconnect, retries with jittered exponential backoff (matches
     `internal/agent/tunnel.go BackoffDurationWithJitter`)

   The agent code is a slim reimplementation (not a `TunnelClient` import)
   because that package transitively pulls in `client-go` and friends. The
   wire format is identical — see `pkg/protocol/types.go`.

3. **HTTP workload**: a global `golang.org/x/time/rate.Limiter` token bucket
   shapes the aggregate rate to `-rps`. Each tick draws a scenario from the
   weighted mix in `scenarios.go`:

   | Scenario | Weight | Path |
   |---|---|---|
   | cluster_list | 30% | `/api/v1/clusters/` |
   | cluster_pods | 25% | `/api/v1/clusters/.../k8s/api/v1/pods` |
   | auth_me | 20% | `/api/v1/auth/me/` |
   | project_list | 10% | `/api/v1/projects/` |
   | audit_logs | 10% | `/api/v1/audit-logs/` |
   | admin_queues | 5% | `/api/v1/admin/queues/` |

4. **Scrape**: every 15 seconds, `GET /metrics` and pluck the metrics listed
   in `metrics.go::scrapedMetrics`. The driver also snapshots its own
   `runtime.NumGoroutine` and `HeapAlloc` so the report has a baseline for
   the harness itself.

5. **Report**: the verdict block is the first non-frontmatter line in the
   output file, in the form `VERDICT: pass` / `VERDICT: fail`. Grep for
   `^VERDICT:` in CI.

## Compiling vs running

The harness is self-contained — `go run ./scripts/loadtest` works, or build
a binary via `go build -o bin/loadtest ./scripts/loadtest`. There is no
build tag; `go build ./...` will compile it as part of the module. Nothing
imports the `main` package back into the production binaries.

## What it does NOT measure

- **CPU saturation curves** — that's `go tool pprof` territory; this
  harness records counts, latencies, and gauges only.
- **Cold-start performance** — the workload starts immediately after agents
  finish their CONNECT_ACK, so DB / pgxpool warm-up effects show up in
  the first few seconds of latency samples but aren't separated out.
- **Frontend asset latency** — the workload hits API paths only. If you
  care about how the SPA shell behaves at scale, run a separate k6
  scenario against the chart's frontend Service.

## Adding scenarios

Drop a new entry in `defaultScenarios()` in `scenarios.go`. Make sure the
total weight still sums to ~1.0 (the cumulative-distribution picker tolerates
small floating-point drift but anything > 0.01 off will skew the mix). The
scenario `name` is what shows up in the report's HTTP-latency table.

## Why "VERDICT:" must be a literal grep target

CI plumbing pipes the harness output into a runner that fails the job if it
doesn't see `^VERDICT:` in the markdown. Don't reformat the line.
