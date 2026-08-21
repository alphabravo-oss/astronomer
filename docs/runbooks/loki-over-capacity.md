# AstronomerLokiOverCapacity

Hosted Loki is above the sizer’s pass row for the **running** mode, or
in-process ingest is hitting the fail-closed cap. New Astronomer-Loki
attaches freeze (409). Existing pipelines keep their current
`ingestion_rate_mb`. The Helm release is **not** uninstalled.

## Symptoms

- PrometheusRule expr:
  `sum(rate(astronomer_loki_ingest_requests_total{result="cap"}[5m])) > 0`
  for 10m (push-path backstop). Correlate with Loki family status
  `degraded_capacity` and `GET /sizer/` `verdicts.loki.result=fail`.
- Attach `POST .../outputs/attach-astronomer/` returns 409
  `ingest_cap_exceeded` or `degraded_capacity`.
- Alert copy: existing pipelines keep their current cap.

## Triage

1. **Sizer first-match.** `GET /api/v1/settings/monitoring/sizer/`.
   Reasons: `single_node_small`, `below_singlebinary_floor`,
   `above_hosted_scale`, `pod_list_truncated`, `object_storage_missing`,
   `wal_too_small`. Do not force SimpleScalable on a 1-node cluster.

2. **Connected clusters / GiB/day.** Defaults are 2 GiB/day/cluster.
   Cluster-count and GiB/day gates fire before the MB/s budget in normal
   operation; `result="cap"` is the real MB/s backstop once Loki is live.

3. **Do not uniformly lower per-tenant rates** just because estimated
   GiB/day crossed a line. Ratchet (`reason=sizer_ratchet`) only when
   leftover/usable drops below the **running mode floor**.

## Recovery

- Freeze is the v1 control: stop attaching. Point extra clusters at BYO
  Loki/ES/Splunk.
- If leftover/usable dropped below the running floor, an automatic
  upgrade with `reason=sizer_ratchet` may already be queued. Do not
  delete WAL PVCs or the release.
- Uninstall remains an operator action. System `logging_outputs` rows
  disable on uninstall; they are not deleted.

## Verify

- Attach of a new cluster 409s while status is `degraded_capacity`.
- Existing system destinations still ship (or fail closed on cap, not
  5xx).
- `verdicts.loki` returns to `pass` only after clusters/capacity match
  the procedure — then new attaches unfreeze.
