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

## Management-plane dashboard

The management-plane dashboard has separate server and worker scrape availability
panels. A value of 1 means at least one discovered replica is up; 0 means every
discovered replica is down. An absent series displays **No data**, not healthy or
idle. Aggregate availability does not establish complete replica coverage: inspect
individual Prometheus targets when a replica or its application metrics are missing.

Process CPU (cores), Go heap in-use bytes, and goroutine panels show each replica
using the default Go/Prometheus collectors already served by server and worker.
They retain gaps for missing metrics and filter out targets with `up != 1`.
CPU rates require at least two samples. The disconnected-agent count likewise
uses only currently healthy server targets; no connection series yields no data,
not a fabricated zero. This is scrape health, not proof of fresh agent observations.

Helm sets the dashboard's `metrics_namespace` and `metrics_fullname` constants to
the release namespace and resolved chart fullname. This follows the metrics
Services even when ServiceMonitors live in a different namespace. The standalone
JSON defaults both constants to `astronomer`; when importing it directly, edit
those constants to the installation's namespace and metrics Service name prefix.
Grafana's normal Prometheus datasource configuration is still required. Other
existing dashboard panels retain their original query scope; these constants
scope the resource/availability, embedded observation, server DB acquisition,
and disconnected-agent panels.
Enabling dashboards does not enable ServiceMonitors.

Embedded observation panels use server metrics targets only, retaining each
`instance` and `source` separately. Both require a successful scrape and a
producer sample timestamp no more than 60 seconds old and not in the future.
A stopped observation loop therefore becomes a gap even if `/metrics` remains
healthy. Sample freshness uses
`astronomer_agent_delivery_observation_sampled_at_timestamp_seconds`.

Source age advances as Prometheus `time()` minus
`astronomer_agent_delivery_observation_observed_at_timestamp_seconds`, preserving
the original evidence time. Unknown (0) and future observation timestamps are
omitted. Availability is 1 only when the sampled state is current and evidence
has a positive, nonfuture timestamp no more than 240 seconds old. A recently
sampled noncurrent, unknown or expired source remains 0. Missing or stale
producer samples remain gaps, not a fabricated unavailable value. The older
sampled age/availability gauges alone cannot establish current freshness.
Source labels identify tracked kinds and controller evidence; discovery/dynamic
refresh duration and frequency are separate measurements. The p95 groups by
instance, source and outcome, so failed refreshes are not averaged into successes.

The LIST/WATCH panel counts actual tracked observation requests by kind, verb and
outcome. It is not total downstream Kubernetes API load: dynamic/discovery calls,
other clients and watch lifecycle events are outside that counter. Refresh
frequency counts coalesced refresh executions, not individual API calls. The
server DB panel shows average time for all pool acquisitions, not exclusively
blocked wait, and omits intervals with zero acquisition rate. Missing or failed
scrapes remain gaps; no panel fabricates a healthy zero.

Standalone agents expose `/metrics` on their health listener at :8081 and have
pod scrape annotations. These annotations do not establish downstream scraping
or aggregation into management Prometheus. The chart's ServiceMonitors select
server and worker targets, not standalone agents. These panels therefore make
no claim about remote agent coverage. Check actual targets before interpreting
an absent metric, including whether the deployed producer version exports it.

Dashboard render tests verify selectors and missing-data configuration; they do
not evaluate PromQL or prove live target discovery. Real PromQL evaluation needs
Prometheus or promtool; its absence is not a successful evaluation.
