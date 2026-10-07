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
# 1. Have the complete management plane running: server and worker. `make dev`
#    exposes the API on 8001 and metrics on 9090. A server-only run cannot
#    drain the transactional audit/task outboxes and is never qualification.
# 2. For local engineering only, let the harness exchange the cookie-only
#    browser login for an ephemeral one-day API token held only in memory.
#    The token is revoked during cleanup. Keep the password outside shell
#    arguments (a file descriptor or mode-0600 file):
make load-test LOADTEST_SERVER=http://localhost:8001 LOADTEST_METRICS_SERVER=http://localhost:9090 \
  LOADTEST_LOGIN_EMAIL=admin@example.com LOADTEST_LOGIN_PASSWORD_FILE=/secure/password \
  LOADTEST_CLUSTERS=100 LOADTEST_RPS=200 LOADTEST_DURATION=10m

# Or run a named enterprise fleet profile:
go run ./scripts/loadtest \
  -server http://localhost:8001 \
  -metrics-server http://localhost:9090 \
  -token /secure/admin-api-token \
  -profile scripts/loadtest/profiles/small.yaml \
  -out loadtest-small.md
```

Output is `loadtest-report.md` (override with `LOADTEST_OUT=...`).

## Flags / env vars

| Flag | Env var | Default | Purpose |
|---|---|---|---|
| `-server` | `LOADTEST_SERVER` | `http://localhost:8001` | Management-plane base URL |
| `-metrics-server` | `LOADTEST_METRICS_SERVER` | value of `-server` | Prometheus metrics base URL when metrics use a separate listener or Service |
| `-clusters` | `LOADTEST_CLUSTERS` | `50` | Synthetic agent count |
| `-rps` | `LOADTEST_RPS` | `100` | Aggregate HTTP request rate (bounded, credit-conserving microbatches) |
| `-duration` | `LOADTEST_DURATION` | `5m` | How long to drive load |
| `-token` | `LOADTEST_TOKEN` | _(empty)_ | Path to a pre-provisioned admin API bearer token used for fixture provisioning and HTTP workload requests; never used as an agent credential |
| `-login-email` | `LOADTEST_LOGIN_EMAIL` | _(empty)_ | Local engineering only: browser-login email used to mint an in-memory one-day API token; forbidden in certification |
| `-login-password-file` | `LOADTEST_LOGIN_PASSWORD_FILE` | _(empty)_ | Local engineering only: password file or inherited file descriptor; bearer material is never written by the harness |
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
The target deployment's per-caller API rate limits must be sized for the
profile's declared RPS. Keep production defaults in ordinary environments;
capacity jobs should raise `config.apiK8sProxyRateLimitRPS` and
`config.apiK8sProxyRateLimitBurst` explicitly in their retained values file.
Certification also requires the profile's bounded `mandatoryAudit` workload to
remain active for at least 98% of the certified window. `maxOperations` must be
large enough to sustain the declared rate for the full duration; it is a safety
cap, not the expected attempt count. The expected count is `ratePerSecond ×
duration`, with the first operation issued when the workload window opens.
Every accepted
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
| `LOADTEST_THRESH_HTTP_ERROR_RATIO` | `0` | Maximum transport plus non-2xx response ratio outside a bounded intentional reconnect window |
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

   A configured reconnect storm records its exact start, targeted agents, full
   recovery, and recovery duration. HTTP 503 responses from agent-routed
   resource scenarios are reported separately while that bounded drill is in
   progress; they do not consume the steady-state HTTP error budget only when
   every targeted agent reconnects within `jitter + 30s`. Transport failures,
   non-agent routes, responses after recovery, and an incomplete or slow
   reconnect still fail the run.

   The agent code is a slim reimplementation (not a `TunnelClient` import)
   because that package transitively pulls in `client-go` and friends. The
   wire format is identical — see `pkg/protocol/types.go`.

4. **HTTP workload**: a credit-conserving scheduler running at up to 1,000 Hz
   shapes the aggregate rate to `-rps`. Rates through 1,000 RPS emit at most one
   request per tick; higher rates use bounded microbatches. The first request
   cannot unlock a one-second burst, and fractional credits are conserved
   across ticks. Each scheduled request runs concurrently and is joined after
   the measured window. Each request draws a scenario from the weighted mix in
   `scenarios.go`:

   | Scenario | Weight | Path |
   |---|---|---|
   | cluster_list | 25% | `/api/v1/clusters/` |
   | cluster_pods | 15% | `/api/v1/clusters/{real_fixture_id}/k8s/api/v1/pods` |
   | cluster_deployments | 5% | `/api/v1/clusters/{real_fixture_id}/k8s/apis/apps/v1/deployments` |
   | cluster_services | 5% | `/api/v1/clusters/{real_fixture_id}/k8s/api/v1/services` |
   | cluster_events | 5% | `/api/v1/clusters/{real_fixture_id}/k8s/api/v1/events` |
   | auth_me | 20% | `/api/v1/auth/me/` |
   | project_list | 10% | `/api/v1/projects/` |
   | audit_logs | 10% | `/api/v1/audit/` |
   | admin_queues | 5% | `/api/v1/admin/queues/` |

5. **Scrape**: every 15 seconds, `GET /metrics` from `-metrics-server` and pluck the metrics listed
   in `metrics.go::scrapedMetrics`. The driver also snapshots its own
   `runtime.NumGoroutine` and `HeapAlloc` so the report has a baseline for
   the harness itself. Certification requires at least eight server samples
   for goroutines, heap, and open FDs. Leak comparisons exclude connection
   ramp-up and average windows to reduce garbage-collection and scrape noise.

6. **Report**: the verdict block is the first non-frontmatter line in the
   output file, in the form `VERDICT: pass` / `VERDICT: fail`. Grep for
   `^VERDICT:` in CI. Rate, duration, resource cardinality, state-event, HTTP,
   and mandatory-audit conservation are enforced in local engineering reports
   as well as certification; certification adds signed provenance, drill, and
   evidence-density requirements rather than weakening local verdicts.

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

## Real-estate observation collector (engineering only)

The existing command also measures **pre-provisioned real agents**. This partial
E04 collector never reports qualification: its verdict is `incomplete` when its
implemented checks succeed, or `failed` when they fail. Synthetic certification,
protected drill provenance, and component sizing rules are unchanged. A zero exit
status means this limited collection succeeded, not that E04 passed.

Start from `testdata/real-estate.example.json`. Its IDs and hashes are placeholders,
not deployed fixtures. Replace them with reviewed estate identities and frozen
provenance. The manifest requires 2–10 distinct adopted remote members, exactly
1, 10, or 100 declared delivery deployments **per member's declared project**,
expected ready generations/spec digests, project namespace binding, and actual
namespace pod/deployment/service counts. Other projects' assignments are outside
this scoped census and must be controlled by the benchmark dataset separately.

Declare the tested commit, SHA256 digests of image inventory, chart values,
dataset and hardware description, Kubernetes version, and server/worker replica
counts. These environment fields are declarations, not an independent live digest
attestation. The report separately records the manifest and fixture digests and
Go driver VCS revision/dirty status when available; `unavailable` is explicit.

Each member declares its own `/metrics` URL and expected `astronomer_instance_id`.
The collector independently verifies that the same origin's `/healthz.cluster_id`
matches that member before collection and each scrape. Shared instance IDs such
as `unknown` alone do not establish member identity. URLs must use HTTPS or an
explicit numeric loopback HTTP address (existing port-forwards are supported).
Redirects, URL userinfo, queries and fragments are rejected. An optional per-target
`token_file` supplies only that target's credential; the management API token is
never inherited. No listener, deployment, ServiceMonitor or RBAC is created.

Validate locally without network or credential reads:

```sh
go run ./scripts/loadtest -real-estate scripts/loadtest/testdata/real-estate.example.json \
  -check-only -server https://api.example.test
```

Collect only after preparing reviewed fixtures and reachable member endpoints:

```sh
go run ./scripts/loadtest -real-estate "$ESTATE_MANIFEST" \
  -server "$TEST_API" -token "$TEST_TOKEN_FILE" \
  -out "$RUN_DIR/real-estate.md"
```

The mode rejects synthetic agent/profile flags, `-metrics-server`, certification,
login bootstrap, audit mutation configuration and their conflicting environment
variables. The v2 manifest declares 1–8 named phases, each with 5–60 minutes
warmup and 30 minutes–4 hours measurement. Rates are requests/second: idle is
exactly zero; resources/delivery allow 0.01–1000; search is capped at 1/6 to
respect the existing shared per-user 10/minute limiter (0.1 is recommended).
Legacy `-rps`, `-warmup`, `-duration` overrides are rejected for estate mode;
phase definitions are the sole workload contract. There is no short-window
qualification override. All requests are GET;
this increment never creates or deletes fixtures. Start/end checks verify the
exact scoped deployment set, cluster/project/target identity, ready generation
and spec digest, namespace binding and paginated Kubernetes resource census.

Optional `rendered_resources` use the generated public inventory identity schema.
Complete current source observation, matching generation/spec digest, matching
inventory references and independent resource GETs are required for rendered
namespace verification. Supported references are namespaced Pod, Service, PVC,
Deployment, StatefulSet and DaemonSet. Missing, stale, legacy or truncated source
inventory is **unavailable**, never inferred from receipt time or namespace census.
No Secret payloads are fetched. This verifies declared references, not arbitrary
ownership of all resources on the member.

The measured phases reuse the existing scheduler with deterministic request
selection; synthetic scenarios and their integer-rate scheduler behavior remain
unchanged. Idle schedules no application HTTP but collects member metrics for the
whole window. Resource browsing lists the first bounded page of pods/deployments/
services only in each manifest namespace. It is browsing, not a repeated complete
census. Delivery reads project/cluster-scoped inventory and deployment lists plus
every exact manifest assignment detail. Every catalog entry must receive a
successful request; the minimum phase rate/window must cover the full catalog.
Warmup has a separate recorder. Each phase has independent metric baselines. Started requests have a bounded 30-second post-window drain;
the report separates scheduling windows and drain intervals. Metric samples
finishing outside the measurement window are counted as excluded boundary
samples, not transport failures. Preflight duration is not agent cold start.

Outputs are Markdown, adjacent `.json`, and `.sha256` covering both files. Metric
URLs and token paths/values are omitted. A hash identifies each declared metric
origin/producer; per-member series preserve only allowlisted kind/verb/outcome/
source labels. The collector bounds bodies to 4 MiB and legacy observation/process samples to
256 series;
unknown selected-family labels fail rather than introduce resource-name labels.
It reduces samples online, keeping a timestamped hash chain and bounded per-series
counts/min/max/deltas, not unbounded raw metric bodies. Resets and gaps invalidate
complete deltas. Source age uses original observed time and separately requires
recent producer sampling; absent, future or stopped samples cannot become zero.

The v2 report records scheduled/completed/success/failed counts per member,
scenario and assignment; one request has one terminal outcome even if both its
HTTP status and body are broken. HTTP 200 with a truncated body fails. Header
latency keeps the historical meaning; the separate full-response latency includes
bounded body consumption and validation. Every completed request contributes to fixed, bounded, non-cumulative latency
histograms, including failures. Each report contains millisecond bucket upper
bounds/counts, sample and overflow counts, and a conservative p99 upper bound.
The boundaries include 500ms and 2000ms. A percentile falling into overflow is
null, never clamped to the last bucket. No first-N sample subset represents a
full window. Histogram timing semantics are versioned for comparisons.
These new scoped
scenarios do not replace the synthetic cluster-list ≤500ms and whole-cluster
pod-list ≤2000ms gates or silently redefine their latency measurements.

Checks require zero HTTP failures, ≥95% achieved successful traffic, full
member/scenario/assignment coverage, ≥98% measured duration/metric coverage,
and bounded heap/goroutine/open-FD growth. Reported CPU/memory belong to each
member process. Management queue/event-relay, audit, cold start, fixture lifecycle,
churn, multiple panels/tabs, reconnect, repeated-run acceptance and change-to-UI
freshness remain `NOT_RUN`. Implemented phases are only marked measured after
their checks pass. Source age is **not** end-to-end freshness p95.

Search declares a namespace, benign types (pods/deployments/services), limit and
an explicit expected active-cluster UUID set of 1–32 entries, separate from the
2–10 remote benchmark members. Include the active management cluster if it is
part of the authorized fleet; the example third ID is a placeholder, not an
implicitly accepted local cluster. The existing search API has no cluster/project
filter: this is namespace/type-scoped authorized-estate search. The same token
must prove `self=true, superuser=true` through `/api/v1/rbac/my-permissions` and
fully paginated active-cluster listing must equal the declared set. An optional
`search.token_file` supplies a separate credential; its path/value is omitted
from reports. Restricted credentials are rejected because `clusters:list` and
resource-specific search grants can describe different estates.

Permission and fanout checks run before/after the search phase and once per
minute through warmup/measurement, outside scheduled workload accounting. A
changed set cancels the phase. Response counts, failures, type, namespace and
returned cluster IDs are validated. Partial/truncated responses are failures,
including a legitimate top-K truncation: select a reviewed limit/dataset that
can support complete search evidence. These are snapshot checks, not atomic
membership fencing: a successful empty cluster is not identified in the search
response, and changes between checks cannot be ruled out. Frozen estate
membership is a prerequisite. No invented query filters or RBAC changes are used.

Reports compare only identical v2 phase/rate/scope/timing definitions and frozen
fixture/environment provenance. V1 reports are not comparable with v2.

The collector also understands the actual
`astronomer_agent_kubernetes_requests_total` counter and
`astronomer_agent_kubernetes_request_instrumentation_info{schema="v1"}=1`
installation marker. It strictly permits the producer's four consumers, five
operations, eighteen resource labels and seven outcomes: at most 2,520 transport
series, independently of the existing 256-series observation/process bound.
The marker and unlabeled `process_start_time_seconds` add at most two series.
Counter values must be finite nonnegative integers below 2^53. Names, namespaces,
URLs and arbitrary errors cannot enter report labels. Existing observation and
freshness requirements remain unchanged.

Per-member `transport_evidence` reports schema/process-start coverage, first/last
scrape times, observed restarts, failed scrapes, timing gaps, per-series outcomes,
per-consumer/operation aggregates and a family total. `window_delta` covers only
the first-to-last successful sampled interval, not unobserved phase edges. It is
null unless coverage is complete for that interval. `observed_adjacent_delta`
counts only monotonic increments between adjacent valid samples sharing the same
observed process-start value, with no failed scrape or excessive timing gap.
These descriptive increments are not substitutes for a complete window.

CounterVec series are sparse. A newly seen series is **not** assigned an implicit
starting zero; its first observed value is not counted as a delta. Late birth,
disappearance/reappearance or observed counter reset makes that series' complete
window and affected aggregate unavailable. Increments never bridge a missing
series/scrape, invalid marker/identity, observed process restart or >30s sample
gap. Changed process-start values invalidate the window even if the new counter
has already surpassed the old value. All-failed and trailing-failed scrape paths
retain unavailable evidence, not a fabricated zero.

A stable absent vector can report zero observed instrumented-family growth only
when the installation marker and process-start value remain valid and unchanged;
this relies on the current CounterVec implementation never deleting label sets.
The sentinel proves installation of this transport family, not all-client
coverage. Process-start timestamps are sampled restart evidence, not a
cryptographic identity: timestamp collisions or events hidden between scrapes
cannot be ruled out. The counters count observable wrapped RoundTrip attempts,
not watch frames, underlying transport retries or every process client.

No comparison/acceptance CLI is added here. The ≥80% LIST reduction and zero
recurring assignment GET criteria remain `NOT_RUN` pending matched complete
transport windows, successful workload/coverage, preserved freshness and actual
repeated execution. A transport-instrumented baseline missing observation
metadata still fails existing freshness checks; it must never be presented as
freshness-qualified. The current strict report comparator remains unchanged.

Offline before/after estate reports can be compared without network access using
the [offline comparison contract](REAL_ESTATE_COMPARISON.md). Results distinguish
descriptive request rates from optimization eligibility and never qualify a release.
