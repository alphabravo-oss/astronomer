# AstronomerClusterLoggingFlatlined

A managed cluster's `fluent-bit` pods are **running** but their output plugin
has processed **zero records for 5 minutes**. The collector is up, so nothing
looks broken at the pod level, but logs from that cluster are being silently
lost downstream (Loki, OpenSearch, CloudWatch, etc.).

## Scope

Fires on:

```
sum by (cluster, namespace, name) (rate(fluentbit_output_proc_records_total[5m])) == 0
and on (cluster)
sum by (cluster) (up{job=~".*fluent-bit.*|.*fluentbit.*"}) > 0
```

The `up > 0` half is what makes this distinct from a plain "forwarder down"
alert: the process is alive and scrapeable, but its **output** has flatlined.
The most common causes are downstream-side, not fluent-bit-side:

- downstream auth expired / token rejected
- downstream rate-limiting or 5xx-ing
- network egress from the cluster to the sink is blocked
- a malformed output filter is dropping every record before it ships

The `cluster`, `namespace`, and `name` labels identify the exact pod set.

## Diagnose

1. Read the fluent-bit pod logs on the affected cluster — the output plugin
   almost always logs the downstream error:

   ```
   kubectl --context <cluster> -n <fluent-bit-namespace> logs \
     -l app.kubernetes.io/name=fluent-bit --tail=200
   ```

   Look for `[output:...]` lines: `401/403` (auth), `429` (rate-limit), `5xx`
   (downstream error), `connection refused` / `timeout` (egress blocked).

2. Confirm records are being *ingested* (input side healthy) but not *shipped*
   (output side stalled). Compare the input and output counters:

   ```
   # via the pod's own metrics endpoint (default :2020/api/v1/metrics/prometheus)
   kubectl --context <cluster> -n <fluent-bit-namespace> exec <pod> -- \
     wget -qO- http://127.0.0.1:2020/api/v1/metrics/prometheus \
     | grep -E 'fluentbit_(input|output)_'
   ```

   Rising `input_records` with a flat `output_proc_records` confirms an
   output-side stall.

3. Test reachability to the sink from inside the cluster:

   ```
   kubectl --context <cluster> -n <fluent-bit-namespace> exec <pod> -- \
     wget -qO- --timeout=5 <sink-health-url>
   ```

## Recover

- **Auth failure**: rotate/refresh the downstream credential or token in the
  cluster's logging config, then roll the DaemonSet:
  `kubectl --context <cluster> -n <ns> rollout restart daemonset/<fluent-bit>`.
- **Rate-limit / downstream 5xx**: fix or scale the sink; fluent-bit's retry
  buffer drains once the downstream accepts records again. If the buffer is
  full, older records may already be lost — note the window.
- **Egress blocked**: check the managed cluster's NetworkPolicy / firewall /
  egress proxy allows traffic to the sink endpoint and port.
- **Bad output filter**: if a recent config change introduced a filter that
  drops everything, revert it and re-roll.

## When this is not actionable

If the flatline is expected (the sink is intentionally being decommissioned, or
this cluster's logging was turned off but the DaemonSet not yet removed), remove
or disable the cluster's fluent-bit output so the alert stops firing.

## See also

- Alert definition: `deploy/chart/templates/prometheus-rules.yaml`
  (`AstronomerClusterLoggingFlatlined`)
- `management-logging-down.md` (management-plane forwarder, up=0 case)
