# AstronomerGrafanaDown

Fleet Grafana on the management cluster is not serving. The lobby (long-term
metrics via Thanos, logs via Loki/BYO) is unreachable. Cluster Grafana on
member clusters is unaffected — it talks to **this** Prometheus (15d) and
survives an Astronomer outage.

## Symptoms

- PrometheusRule expr: fleet Grafana Deployment `astronomer-grafana` has
  `spec.replicas > 0` but `status.replicas_available < 1` for 5m.
- Open fleet Grafana 502s or never leaves the ticket bounce.
- `kubectl -n monitoring get deploy astronomer-grafana` shows `0/1`.

Triage the Grafana process with `GET /api/health` on the ClusterIP Service
(not the public host — that hits `grafana-proxy`).

## Triage

1. **Release present?** Shared stacks → Grafana. If status is
   `not_configured` / `uninstalled`, silence the alert (the series should
   also disappear). If status is `healthy` but pods are 0, continue.

2. **Grafana vs grafana-proxy?**
   ```
   kubectl -n monitoring get deploy,po -l app.kubernetes.io/instance=astronomer-grafana
   kubectl -n monitoring logs deploy/astronomer-grafana --tail=100
   kubectl -n monitoring logs deploy/astronomer-grafana-grafana-proxy --tail=100
   ```
   Proxy down: ticket bounce / `grafana_auth` fail, Grafana itself may still
   be healthy on ClusterIP. Grafana down: `/api/health` fails.

3. **Health endpoint (ClusterIP only):**
   ```
   kubectl -n monitoring exec deploy/astronomer-grafana -- wget -qO- http://127.0.0.1:3000/api/health
   ```
   Expect `{"database":"ok","version":"..."}`. Database/PVC errors show here.

4. **Sizer leftover.** Grafana needs leftover ≥ 250m / 256Mi. If the
   management cluster is starved, the pod will not schedule. `GET
   /api/v1/settings/monitoring/sizer/` `verdicts.grafana`.

## Recovery

- CrashLoop / OOM: inspect last-state; Grafana requests 250m/256Mi, limits
  500m/512Mi. Do not raise them past the sizer leftover floor.
- PVC bind fail (optional 1Gi): check StorageClass; Grafana can run
  stateless (`storageSize` empty).
- Proxy HMAC / ticket mint: Grafana-family Secret
  `astronomer-grafana-proxy-key`; Astronomer session + `monitoring:read` for
  mint. Do not widen `astronomer_session` Domain.
- Chart upgrade via Shared stacks (in-place). Namespace/release/storage
  class changes require replace.

## Verify

- `kubectl -n monitoring get deploy astronomer-grafana` READY matches spec.
- `GET /api/health` returns database ok.
- Open fleet Grafana from Shared stacks (only when `authMode=proxy`).
