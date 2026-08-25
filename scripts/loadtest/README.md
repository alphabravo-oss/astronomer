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

# Or run a named enterprise fleet profile:
go run ./scripts/loadtest \
  -server http://localhost:8080 \
  -token /tmp/jwt \
  -profile scripts/loadtest/profiles/small.yaml \
  -out loadtest-small.md
```

Output is `loadtest-report.md` (override with `LOADTEST_OUT=...`).

## Flags / env vars

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `-server` | `LOADTEST_SERVER` | `http://localhost:8080` | Management-plane base URL |
| `-clusters` | `LOADTEST_CLUSTERS` | `50` | Synthetic agent count |
| `-rps` | `LOADTEST_RPS` | `100` | Aggregate HTTP request rate (token bucket) |
| `-duration` | `LOADTEST_DURATION` | `5m` | How long to drive load |
| `-token` | `LOADTEST_TOKEN` | _(empty)_ | Path to an admin Bearer JWT used for fixture provisioning and HTTP workload requests; never used as an agent credential |
| `-out` | `LOADTEST_OUT` | `loadtest-report.md` | Markdown report path |
| `-profile` | `LOADTEST_PROFILE` | _(empty)_ | YAML profile that sets clusters, RPS, duration, resource cardinality, reconnect storm, and drill labels |
| `-audit-observer-dsn` | `LOADTEST_AUDIT_OBSERVER_DATABASE_URL_FILE` | _(empty)_ | Path to a read-only PostgreSQL DSN used to independently reconcile durable audit intents; required for certification |
| `-verbose` | `LOADTEST_VERBOSE` | `false` | Debug-level log output |
| `-skip-agents` | `LOADTEST_SKIP_AGENTS` | `false` | HTTP-only mode (skip WS dial) |
| `-keep-fixtures` | `LOADTEST_KEEP_FIXTURES` | `false` | Debug only: retain provisioned cluster rows instead of requesting forced decommission on exit |

## Scale profiles

Profiles live in `scripts/loadtest/profiles/`:

| Profile | Purpose | Target |
|---|---|---|
| `small.yaml` | CI/nightly smoke | 5 clusters, 500-ish resources, reconnect storm |
| `medium.yaml` | Release candidate smoke | 50 clusters, 25,000 resources and Redis/Postgres drills |
| `large.yaml` | Readiness smoke | 250 clusters, 250,000 resources, HA leader-kill and browser-budget drills |
| `extreme-lab.yaml` | Lab-only ceiling test | 1,000 simulated clusters |
| `estate-100.yaml` | Certification rung | 100 clusters |
| `estate-500.yaml` | Certification rung | 500 clusters |
| `estate-1000.yaml` | Certification rung | 1,000 clusters |
| `estate-1000-soak.yaml` | Four-hour production-envelope soak | 1,000 clusters |
| `estate-2000-lab.yaml` | Lab-only ceiling plus four-hour soak | 2,000 clusters |

The harness executes the synthetic-agent and HTTP portions directly. Profile
`day2FailureDrills` are emitted as required evidence. A report is not
certification evidence until the release workflow retains a passing artifact
for every listed drill: Flux rollout fan-out, Redis and PostgreSQL failover,
management-plane HA, and browser performance budgets.

Raw drill evidence is produced only by the protected
`day2-drill-execution.yaml` workflow. It runs the full declared profile and
requires a digest-pinned protected-environment driver for every named fault;
unsupported drivers fail instead of being replaced by unit tests. Each driver
must bind the exact target release/estate and emit a closed precondition,
injection/effect, recovery, and cleanup timeline with metrics and an operation
ID. The qualifier accepts only that successful same-commit workflow, rejects
extra files and symlinks, preserves raw run provenance, and signs the closed
manifest.

Every run also writes `<report>.json` and `<report>.sha256`. Certification jobs
must populate `LOADTEST_COMMIT`, `LOADTEST_IMAGES`, `LOADTEST_CHART_VALUES`,
`LOADTEST_KUBERNETES_VERSION`, `LOADTEST_POSTGRES_VERSION`,
`LOADTEST_REDIS_VERSION`, and `LOADTEST_HARDWARE` so reports are reproducible.
`LOADTEST_COMPONENT_REPLICAS` must be a JSON object with positive `server`,
`worker`, `tunnel`, and `audit` replica counts; the report binds these counts
to the component rate samples used by the offline sizing reducer.
Certification also requires the profile's bounded `mandatoryAudit` workload to
remain active for at least 98% of the certified window. `maxOperations` must be
large enough to sustain the declared rate for the full duration. Every accepted
mutation is independently observed through a separate read-only PostgreSQL
connection against `audit_outbox`, then reconciled through the public audit API
by correlation ID, exact action, and resource type. An HTTP 2xx is not counted
as a durable intent. Audit dropped/write-failure counters and active/dead outbox
rows must remain fully sampled and conserved.

### Threshold env vars

The pass/fail verdict is heuristic and the thresholds are tunable:

| Env var | Default | Pass condition |
|---|---|---|
| `LOADTEST_THRESH_CLUSTER_P99_MS` | `500` | `cluster_list` p99 latency <= this value (ms) |
| `LOADTEST_THRESH_RESOURCES_P99_MS` | `2000` | `cluster_pods` p99 latency <= this value (ms) |
| `LOADTEST_THRESH_CONNECTED_MIN` | `1.0` | Fraction of N agents connected at end |
| `LOADTEST_THRESH_DLQ_MAX` | `10` | `astronomer_worker_queue_depth{state="pending"}` at end |
| `LOADTEST_THRESH_EMPTY_ACQUIRE_QPS` | `0.1` | Rate of `db_pool_empty_acquire_count_total` over the run |
| `LOADTEST_THRESH_GOROUTINE_RATIO` | `1.5` | post-warm-up/terminal `go_goroutines` window ratio |
| `LOADTEST_THRESH_HEAP_RATIO` | `1.5` | post-warm-up/terminal `go_memstats_alloc_bytes` window ratio |
| `LOADTEST_THRESH_OPEN_FD_RATIO` | `1.25` | post-warm-up/terminal `process_open_fds` window ratio after more than 8 FDs of growth |
| `LOADTEST_THRESH_OPEN_FD_GROWTH` | `64` | maximum absolute post-warm-up/terminal open-FD growth |
| `LOADTEST_THRESH_QUEUE_AGE_SECONDS` | `60` | Oldest pending worker task age |
| `LOADTEST_THRESH_EVENT_LAG_SECONDS` | `30` | Distributed event/cache relay lag |
| `LOADTEST_THRESH_HTTP_ERROR_RATIO` | `0` | Maximum transport plus non-2xx response ratio |
| `LOADTEST_THRESH_ACHIEVED_RPS_RATIO` | `0.95` | Minimum observed/target request-rate ratio |
| `LOADTEST_THRESH_DURATION_RATIO` | `0.98` | Minimum observed/configured workload-window ratio |
| `LOADTEST_THRESH_EVENT_RATE_RATIO` | `0.95` | Minimum emitted/declared state-event-rate ratio |

## How it works

1. **Verify**: hits `/api/v1/auth/me/` once. Every non-2xx response or transport
   failure aborts the run with `VERDICT: fail (harness error: …)`.
2. **Provision agent fixtures**: for each synthetic agent, uses the admin JWT
   with the public cluster APIs to create a real imported-cluster row and mint
   a short-lived registration token bound to that returned cluster ID. The run
   aborts before load begins if any ID/token mapping is missing or mismatched.
   The admin JWT remains separate and is never presented to the tunnel.
   On every normal or error exit the harness requests bounded, forced
   decommissioning of its fixtures. `-keep-fixtures` is an explicit debugging
   escape hatch and must not be used for certification runs.
3. **Spawn agents**: opens N WebSocket tunnels to
   `/api/v1/ws/agent/tunnel/{cluster_id}/`, each using its own registration
   token. Each agent:
   - sends `CONNECT`, expects `CONNECT_ACK`
   - adopts the distinct durable agent credential returned by the first ACK,
     so reconnect and reconnect-storm traffic follows production auth semantics
   - emits a `HEARTBEAT` every 30 seconds
   - waits for every initial connection before starting the measured window
   - replies with exact declared pod, deployment, service, and event cardinality
   - emits declared estate-wide `eventsPerSecond` as tunnel state updates
   - replies to `K8S_STREAM_REQUEST` with a header + end frame
   - on disconnect, retries with jittered exponential backoff (matches
     `internal/agent/tunnel.go BackoffDurationWithJitter`)

   The agent code is a slim reimplementation (not a `TunnelClient` import)
   because that package transitively pulls in `client-go` and friends. The
   wire format is identical — see `pkg/protocol/types.go`.

4. **HTTP workload**: a global `golang.org/x/time/rate.Limiter` token bucket
   shapes the aggregate rate to `-rps`. Each tick draws a scenario from the
   weighted mix in `scenarios.go`:

   | Scenario | Weight | Path |
   |---|---|---|
   | cluster_list | 25% | `/api/v1/clusters/` |
   | cluster_pods | 15% | `/api/v1/clusters/{real_fixture_id}/k8s/api/v1/pods` |
   | cluster_deployments | 5% | `/api/v1/clusters/{real_fixture_id}/k8s/apis/apps/v1/deployments` |
   | cluster_services | 5% | `/api/v1/clusters/{real_fixture_id}/k8s/api/v1/services` |
   | cluster_events | 5% | `/api/v1/clusters/{real_fixture_id}/k8s/api/v1/events` |
   | auth_me | 20% | `/api/v1/auth/me/` |
   | project_list | 10% | `/api/v1/projects/` |
   | audit_logs | 10% | `/api/v1/audit-logs/` |
   | admin_queues | 5% | `/api/v1/admin/queues/` |

5. **Scrape**: every 15 seconds, `GET /metrics` and pluck the metrics listed
   in `metrics.go::scrapedMetrics`. The driver also snapshots its own
   `runtime.NumGoroutine` and `HeapAlloc` so the report has a baseline for
   the harness itself. Certification requires at least eight server samples
   for goroutines, heap, and open FDs. Leak comparisons exclude connection
   ramp-up and average windows to reduce garbage-collection and scrape noise.

6. **Report**: the verdict block is the first non-frontmatter line in the
   output file, in the form `VERDICT: pass` / `VERDICT: fail`. Grep for
   `^VERDICT:` in CI.

The certification workflow binds the report, report JSON and digest, rendered
values, raw qualification JSON, drill JSON, and deterministic baseline row in
`astronomer-scale-evidence-v1`. It keyless-signs and verifies that manifest
against the exact workflow identity. The aggregate workflow accepts exactly the
four production rungs, verifies each source signature, requires identical
release/environment metadata, and signs the resulting certification set.
`scripts/reduce-component-sizing.py` consumes three or more passing, same-release
reports and emits deterministic recommendation JSON plus a Markdown table
fragment. It fails closed for absent counters, replica counts, invalid rates,
mixed releases, duplicate source runs, heterogeneous replica topologies,
decreasing capacity, nonlinear scaling outside 50–125%, inconsistent units, or
insufficient declared-load samples. Its
capacity basis is a one-sided 95% Student-t lower confidence bound capped at
the minimum observed per-replica rate, never the optimistic maximum. Its output
stays explicitly unpublished until backed by retained real runs.

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
