# Runbook — Application metrics unavailable

**Alert:** `AstronomerApplicationMetricsUnavailable`  
**Severity:** warning after five minutes  
**Scope:** the alert's `namespace`, `service`, and `component` labels identify one installation's server or worker metrics.

## Meaning

No replica of the named metrics Service has a healthy Prometheus scrape target.
The target may be undiscovered, removed, or failing every scrape. Application
load, queue pressure, and latency are unknown during this gap. A healthy replica
clears this total-absence alert; it does not establish that every replica is healthy.

The rule requires the chart's ServiceMonitors and PrometheusRule to be enabled
and selected by Prometheus. It cannot detect its own missing rule, a stopped
Prometheus, or notification delivery failure. Monitor those externally.

## Triage

1. In Prometheus, inspect Targets and query `up` using the alert's exact
   `namespace` and `service` labels. Inspect the scrape error for down targets.
   An empty result requires discovery checks; zero values require endpoint,
   process, or network checks.
2. Check that both chart ServiceMonitors exist. Their metadata labels must match
   Prometheus's `serviceMonitorSelector`, and their metadata namespace must match
   its `serviceMonitorNamespaceSelector`. A monitor in another namespace still
   selects Services in the Helm release namespace through `namespaceSelector`.
3. Check the metrics Service's selectors and EndpointSlices, the selected pods'
   readiness, named `metrics` ports, and `/metrics` handler. Check NetworkPolicy
   ingress from the actual Prometheus namespace/pods to TCP 9090 and any egress
   policy applied to Prometheus. Preserve authenticated application routes.
4. Check Prometheus's `ruleSelector` and `ruleNamespaceSelector` separately.
   `metrics.prometheusRule.labels` and `metrics.serviceMonitor.labels` are
   independently configured; selecting one does not establish selection of both.
5. Check recent desired chart/configuration and image changes through their owning
   deployment workflow. Do not interpret missing application series as idle load
   or disable the alert to hide a discovery failure.

## Recovery and verification

Repair selectors, workload availability, or allowed network paths through the
installation's owning chart workflow. Preserve the base chart opt-out when
Prometheus Operator CRDs are not installed; enable the monitors only in an
installation with the required monitoring infrastructure.

Verify both server and worker targets are `up == 1` in Prometheus, application
counters advance during real requests/work, and this alert resolves. Record the
scrape gap separately from measured idle periods. A successful Helm render alone
does not verify discovery or connectivity.

## Rule regression tests

From the repository root, run:

```sh
go test ./deploy -run 'TestApplicationMetrics' -v
```

With `helm` and `promtool` on PATH, the test renders the actual chart rules into a
temporary rule file and evaluates `deploy/testdata/application-metrics.test.yaml`.
Without `promtool`, evaluation is explicitly skipped; render tests alone do not
prove PromQL behavior. Fixtures cover absence, all replicas down, recovery,
healthy replicas, other releases/namespaces, and targets becoming stale.
