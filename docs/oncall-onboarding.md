# Oncall onboarding

Your first week as oncall for Astronomer. Read this end-to-end; it
takes 15 minutes and saves a 3am page from feeling worse than it is.

---

## Architecture in 5 minutes

Three processes, one chart:

- **`astronomer-server`** — HTTP API (`/api/v1/...`), WebSocket tunnel
  hub for connected cluster agents, Argo CD UI reverse proxy, frontend
  static-asset host (when `frontend.enabled=true`). 2 replicas in
  prod, behind a PDB. Listens on `:8000` (app) and `:9090` (metrics).
- **`astronomer-worker`** — asynq queue consumer + scheduler for
  periodic reconcilers. 2 replicas. Listens on `:9090` (metrics only).
- **`astronomer-agent`** — runs in each managed cluster. Dials the
  server's WebSocket tunnel and proxies k8s API requests, exec
  sessions, pod logs back through it. NOT in the management chart;
  installed via per-cluster registration manifest.

State lives in:

- **Postgres** (chart-bundled in dev, external HA in prod) — the
  source of truth for clusters, users, RBAC, audit log, SSO config,
  ArgoCD instances, encrypted secrets. Backed up nightly via
  `pg_dump` to S3 per `management-plane-dr-runbook.md`.
- **Redis** — asynq queue + scheduler state. Loss of Redis loses
  in-flight tasks but doesn't lose persistent data.

The chart is self-managed via an Argo CD Application
(`astronomer-self-manage`) so most chart-level changes flow through
Argo. See the upgrade-runbook for the manual `helm upgrade` path.

---

## Dashboards

Open these in tabs at the start of every shift.

| Dashboard | What it shows | Where |
|---|---|---|
| Platform Overview | Cluster count, agent count, recent activity | `/dashboard` |
| Cluster list | Per-cluster status, agent reachability, CPU/mem | `/dashboard/clusters` |
| Observability → Health summary | Aggregate degraded/disconnected count, DLQ depth | `GET /api/v1/platform/health-summary/` (no UI yet — curl it) |
| Prometheus | `/metrics` scrapes of server + worker + agent | Operator-installed Prometheus |
| Worker queue state | asynq queue / DLQ depth | Same Prometheus; OR `unzip -p $(curl -sH "Bearer $TOKEN" $URL/api/v1/support-bundle/) asynq-queues.json | jq` |

---

## Top alerts (and what to do)

These ship in the default PrometheusRule per FEATURES-051126 T03.
All have a `runbook_url` annotation pointing at `docs/runbooks/`.

### `AstronomerHighHTTPErrorRate`
5xx rate > 1% for 5m. Causes: panic loop, DB down, downstream tunnel
hung. Triage: check server pod logs for stack traces, hit `/readyz`,
look at `astronomer_http_requests_total{status_class="5xx"}` per
`route_template` to see if the errors are concentrated on one route.

### `AstronomerWorkerQueueBacklog`
Pending tasks > 1000 for 5m. Workers can't keep up. Triage: check
`astronomer_worker_jobs_total{status="error"}` rate — is one task
type failing every run? Check worker pod logs. Bump
`Concurrency` if persistent (FEATURES-051126 T17 will help — async
helm).

### `AstronomerWorkerDLQGrowing`
A task is exhausting retries and landing in the archived queue.
Triage: `unzip -p $(curl ...) asynq-queues.json | jq '.["default"].archived_tasks'`
shows the last 50 DLQ entries with last error.

### `AstronomerDBPoolExhausted`
Pool wait events firing. Either bump `database.pool.maxConns` (chart
values) or hunt the long-held conn — most often a stuck advisory lock
from a periodic task that crashed without releasing.

### `AstronomerAgentDisconnected`
A cluster agent hasn't pinged in > 120s. Triage: check the agent pod
in the target cluster (`kubectl --context <cluster> -n astronomer get
pods`). Common causes: network partition, agent OOMKilled, expired
registration token. See the agent-disconnected runbook.

### `AstronomerArgoSelfManageDrift`
The Argo CD app that owns this install isn't Synced. Triage: check
the Argo CD UI for the `astronomer-self-manage` Application; look at
what differs between Git and the live state. Usually a manual
`kubectl edit` somewhere — reconcile or revert.

---

## Common runbooks

- `docs/management-plane-dr-runbook.md` — restore from pg_dump
- `docs/secret-rotation-runbook.md` — Fernet / JWT / agent token rotation
- `docs/upgrade-runbook.md` — `helm upgrade` + `helm rollback`
- `docs/verify-images.md` — cosign + SBOM verification (when CI is enabled)

Runbooks for individual alerts live under `docs/runbooks/<alert>.md`.
The runbook directory is allowed to be skeletal — fill it in as you
respond to real incidents.

---

## What to NEVER touch

- **Postgres bundled StatefulSet's PVC.** Even with `bundled.enabled:
  false` in production, the chart leaves the dev PVC orphaned on
  uninstall. Don't `kubectl delete pvc` it without a current backup.
- **The Argo CD `astronomer-self-manage` Application's source.** It
  points at the chart used to install this very plane; changing it
  can wedge the install into reconciling against the wrong values.
- **`kubectl edit` on a Helm-managed resource.** It'll work for the
  current pod cycle but Argo will fight you and the change will
  evaporate. Use `helm upgrade --set` or the values file.
- **The Fernet `secrets.encryptionKey` without following the runbook.**
  Rotating it without the multi-key transition makes every encrypted
  column unreadable.

---

## Escalation

Severity ladder (rough):

- **SEV-3** — single endpoint slow / one cluster degraded. Open an
  issue, fix on next-business-day cadence.
- **SEV-2** — multiple endpoints affected OR data integrity concern OR
  >1 cluster degraded. Page the on-call author of the affected
  component. Use `#astronomer-platform` for coordination.
- **SEV-1** — plane is down (`/health/` returns 5xx or pod isn't
  Ready) OR data loss confirmed. Page the platform lead immediately.
  Start a Zoom bridge. Document timeline.

After every SEV-1 or SEV-2: write a postmortem under
`docs/postmortems/<date>-<short-title>.md`. The point isn't blame, it's
to capture the failure mode so the next responder recognises it.

---

## End-of-shift checklist

- [ ] All alerts firing 24h ago are resolved (or have an open ticket)
- [ ] `/api/v1/platform/health-summary/` returns all zeros
- [ ] Worker DLQ depth is 0
- [ ] Latest nightly pg_dump completed (S3 object exists for last 24h)
- [ ] No `helm upgrade` in progress (`helm history astronomer` last
      revision is `deployed`)

If any of these are red, hand off explicitly to the next oncall.
