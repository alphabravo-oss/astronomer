# Local telemetry stack

Astronomer's optional local telemetry profile connects server and worker traces,
Prometheus metrics and JSON process logs in one Grafana instance. It is a
developer diagnostic stack, not a production topology or a retained
qualification artifact.

## Start it

From the repository root:

```bash
docker compose \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.telemetry.yml \
  --profile telemetry up --build
```

The profile enables 100% sampling so a single request is easy to follow. Its
storage is intentionally bounded to 24 hours.

| Service | Local URL | Purpose |
| --- | --- | --- |
| Astronomer API | <http://localhost:8001> | application traffic |
| Grafana | <http://localhost:3001> | pre-provisioned logs, metrics and traces |
| Prometheus | <http://localhost:9091> | raw server/worker metrics and exemplars |
| Tempo | <http://localhost:3200> | trace queries |
| Loki | <http://localhost:3100> | structured server/worker/migrator logs |

Grafana has anonymous administrator access only in this local profile. The
Prometheus, Loki and Tempo data sources are provisioned automatically. Sampled
worker and agent counters carry `trace_id` exemplars, and JSON log `trace_id`
fields link to Tempo without making trace IDs metric or Loki labels.

Stop the processes without deleting their local volumes:

```bash
docker compose \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.telemetry.yml \
  --profile telemetry down
```

Add `--volumes` only when deliberately deleting the local telemetry and
application data.

## Trace an adopted-cluster request

Tunnel envelopes propagate W3C `traceparent` and `tracestate` from the API to
the agent. The agent needs its own OTLP route because a remote adopted cluster
normally cannot resolve the management plane's Docker or Kubernetes Service
DNS. Before rendering a new registration manifest, optionally expose Tempo's
port 4318 on an address reachable from that cluster and start Compose with:

```bash
LOCAL_AGENT_OTEL_ENDPOINT=http://REACHABLE_HOST:4318 \
docker compose \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.telemetry.yml \
  --profile telemetry up --build
```

The generated manifest receives only the endpoint, insecure flag, sampler and
environment. Management-plane `OTEL_EXPORTER_OTLP_HEADERS` are deliberately not
copied across the adopted-cluster trust boundary. Use a cluster-local collector
and Secret-backed authentication for a secured non-local deployment.

## Logging and command policy

Server, worker, agent and migrator operational logs are JSON on stderr.
Application logs, trace attributes, metric labels and tunnel trace envelopes
must not contain HTTP bodies, tunnel payloads, bearer values, encryption
material, terminal output or shell command text.

The in-browser shell keeps its existing, bounded command-input audit in
PostgreSQL so an authorized reviewer can answer who ran what. Those command
rows are not emitted to process logs or Loki, and output bytes are never
recorded. See [In-browser kubectl shell](kubectl-shell.md#audit-log-commands-not-output).

Promtail uses the local Docker socket only to discover the Compose `server`,
`worker` and `migrate` containers. The mount is local-development-only and must
not be copied into a production deployment.
