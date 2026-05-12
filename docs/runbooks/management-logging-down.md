# AstronomerManagementLoggingForwarderDown

The chart-side Fluent Bit DaemonSet that forwards the management plane's
own server / worker logs has stopped reporting `up=1` to Prometheus for
more than 5 minutes. Until it recovers, audit / error / access logs are
not landing in the configured external sink (Loki, Elasticsearch,
Splunk, or HTTP).

## Scope

Fires when `sum(up{job="astronomer-mgmt-logging"}) < 1`. The
`astronomer-mgmt-logging` job is auto-discovered via the Prometheus
scrape annotations on the DaemonSet pod template — if scraping itself
is broken (e.g. ServiceMonitor missing), the alert fires the same way
as if Fluent Bit had crashed.

The alert is only rendered when
`managementLogging.enabled=true` so installs that intentionally route
logs through a node-level collector instead are quiet by default.

## Diagnose

1. Confirm the DaemonSet exists and is rolled out:

   ```
   kubectl -n <release-namespace> get daemonset \
     -l app.kubernetes.io/component=management-logging
   ```

   `DESIRED`, `CURRENT`, `READY` and `UP-TO-DATE` should all match the
   node count. If `READY < CURRENT`, drop to step 2.

2. Inspect a non-ready pod:

   ```
   kubectl -n <release-namespace> get pods \
     -l app.kubernetes.io/component=management-logging
   kubectl -n <release-namespace> describe pod <pod>
   kubectl -n <release-namespace> logs <pod>
   ```

   Common failure modes:
     - `CreateContainerError` / `ImagePullBackOff` — the
       `managementLogging.image.repository:tag` isn't reachable.
       Re-check air-gapped registry overrides
       (`managementLogging.image.registry` /
       `image.registry`).
     - `CrashLoopBackOff` with a config error in the log — usually
       points at a malformed `managementLogging.endpoint` or a missing
       `auth.bearerSecretRef` Secret.
     - `Running` but `0/1 Ready` — readiness probe failing. Fluent Bit
       binds `:2020` for its HTTP metrics endpoint; if you see a
       `bind: address already in use` error in the container log,
       another DaemonSet is fighting for the same port on the host.

3. Confirm the external sink is reachable from the cluster:

   ```
   kubectl -n <release-namespace> exec -ti <pod> -- \
     wget -qO- <managementLogging.endpoint>/ready
   ```

   (Substitute the backend's health URL — e.g. Loki `/ready`, ES `/`,
   Splunk HEC `/services/collector/health`.)

4. Confirm Prometheus is actually scraping the DaemonSet:

   ```
   kubectl -n <prometheus-ns> port-forward svc/prometheus 9090
   # Then visit http://localhost:9090/targets and grep for
   # astronomer-mgmt-logging
   ```

   A 0-target scrape means the scrape annotations didn't get picked up
   — re-check that Prometheus is configured to honor
   `prometheus.io/scrape`-style annotations or that the
   ServiceMonitor selector matches.

## Recover

- **Bad endpoint**: edit values, `helm upgrade`. The ConfigMap's
  checksum annotation rolls every Pod automatically.
- **Bad credentials**: rotate the Secret named in
  `managementLogging.auth.bearerSecretRef`; kill the Pods so they pick
  up the new value (the env-var Secret reference is read at start).
- **Node-pressure eviction**: if pods are getting OOM-killed, bump
  `managementLogging.resources.limits.memory` (default 256Mi). Heavy
  log volume — especially with the Kubernetes filter enabled — pushes
  3.x past the default cap.
- **Full restart**: `kubectl rollout restart daemonset/<name>-mgmt-logging`.

## When this is not actionable

If management-plane log forwarding is being decommissioned (e.g.
migrating to a node-level collector), the right fix is to flip
`managementLogging.enabled=false` and `helm upgrade`. The alert
template is gated on the same value and will stop being rendered on
the next render.

## See also

- Chart values: `deploy/chart/values.yaml` under `managementLogging:`
- ConfigMap render:
  `deploy/chart/templates/management-logging-configmap.yaml`
- DaemonSet:
  `deploy/chart/templates/management-logging-daemonset.yaml`
- Read-side UI endpoint: `GET /api/v1/admin/management-logs/` —
  superuser-only, returns up to 1000 lines from the current pod logs.
