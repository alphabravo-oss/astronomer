# AstronomerLokiGatewayDown

The hosted Loki chart gateway (default release `astronomer-loki`) has no
available replicas. Ingest (after tokens exist) and Grafana’s Loki
datasource (via `loki-auth` → gateway ClusterIP) fail. BYO log destinations
are unaffected. Cluster Prometheus scrape continues.

## Symptoms

- PrometheusRule expr: Deployment `astronomer-loki-gateway` has
  `spec.replicas > 0` but `status.replicas_available < 1` for 5m.
- Fluent Bit retries then drops (hosted path). Fleet Grafana Loki panels
  error. `QueryOutput` on system destinations is 501 by design — use Grafana.

## Triage

1. **Family status.** Shared stacks → Loki. `degraded` / `degraded_capacity`
   is not the same as gateway down (those freeze **new** attaches; existing
   pipelines keep the current cap). This alert is the gateway Deployment.

2. **Pods:**
   ```
   kubectl -n monitoring get deploy,po -l app.kubernetes.io/instance=astronomer-loki
   kubectl -n monitoring logs deploy/astronomer-loki-gateway --tail=100
   ```
   SingleBinary vs SimpleScalable changes which other pods (write/read/
   backend) must also be Ready.

3. **Object storage.** Loki TSDB ships to the same `storageConfigId` as
   Thanos with prefix `join(storageCfg.Prefix, "loki")`. Decrypt / bucket
   failures show in write-path logs, not only the gateway.

4. **Sizer.** `GET /api/v1/settings/monitoring/sizer/` `verdicts.loki`.
   `single_node_small` means this management cluster should not be running
   Loki; uninstall rather than force it.

## Recovery

- OOM / not Ready: do not widen mode past the sizer. In-place upgrade is
  allowed even when status is `degraded_capacity`; mode widen is a replace
  and 412s if the sizer fails.
- Gateway Service stays ClusterIP. Public ingest is Ingress on the
  operator-supplied `ingestHostname` only after tokens exist — do not
  point the alert at that host if `ingestPublic=false`.
- Uninstall is an operator action and does **not** delete the bucket prefix.

## Verify

- Gateway Deployment READY matches spec.
- From Grafana (or `loki-auth` ClusterIP) a labels query for one tenant
  succeeds. Existing system destinations stay enabled unless Loki was
  uninstalled.
