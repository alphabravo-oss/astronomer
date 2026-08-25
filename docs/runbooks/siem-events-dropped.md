# AstronomerSIEMEventsDropped

`astronomer_siem_dropped_total` is incrementing — best-effort product events
destined for an external SIEM forwarder are being discarded. Downstream tooling
(Splunk, Sentinel, Chronicle, an HTTP collector, etc.) has a blind spot for
the duration of the drop window.

## Scope

Fires on `rate(astronomer_siem_dropped_total[10m]) > 0`. The metric is labelled
by `reason` (queue full, retries exhausted, forwarder disabled/unreachable, or
serialization error). A drop means a non-transactional product event was
accepted by the platform but never delivered to the configured SIEM
destination. Transactional audit receipts are durable PostgreSQL rows and are
never deleted by queue pressure, retry exhaustion, or age retention.

This is distinct from `AstronomerAuditEventsDropped`, which is about the local
audit-log DB write. A transactional audit event and its matching SIEM receipts
commit together; older/unmigrated audit writers and ordinary product events do
not inherit that guarantee.

## Diagnose

1. Identify which forwarder and why. Break down the counter by `reason`:

   ```
   # In Prometheus:
   sum by (reason) (rate(astronomer_siem_dropped_total[10m]))
   ```

2. List configured forwarders and their status via the admin API:

   ```
   curl -sH "Authorization: Bearer $ADMIN_TOKEN" \
     $URL/api/v1/admin/siem-forwarders/ | jq '.'
   ```

   Look for a forwarder in an error/disconnected state, or one whose queue depth
   is pinned at its cap.

3. Test connectivity to the destination from the server pod:

   ```
   kubectl -n <release-namespace> exec deploy/astronomer-server -- \
     wget -qO- --timeout=5 <forwarder-endpoint>
   ```

4. Check server logs for the forwarder subsystem:

   ```
   kubectl -n <release-namespace> logs -l app.kubernetes.io/component=server \
     --tail=500 | grep -i 'siem\|forwarder'
   ```

## Recover by `reason`

- **`unreachable` / retries exhausted**: the destination is down or blocked by
  egress policy. Fix connectivity (destination health, NetworkPolicy egress,
  credentials/token). Mandatory audit receipts continue retrying; disposable
  events that exhaust the budget increment the drop counter.
- **`queue_full`**: the forwarder can't keep up with event volume. Confirm the
  destination is healthy (not just rate-limiting), then scale the server or raise
  the forwarder's queue/batch settings. If a single noisy source is responsible,
  narrow the forwarder's event filter.
- **`auth` / rejected**: rotate the destination credential/token in the
  forwarder config (`PUT /api/v1/admin/siem-forwarders/{id}`) and use the
  built-in Test action to confirm before saving.
- **`disabled`**: if the forwarder was intentionally disabled, expected events
  will "drop". Either re-enable it or remove it so the alert stops.

## Assess the gap

After recovery, record the drop window. Transactional audit receipts replay
automatically with their stable destination key. If non-transactional events
were dropped and the destination supports imports, back-fill available audit
history from `GET /api/v1/audit/` for the affected period.

## See also

- Metrics: `internal/siem/metrics.go` (`astronomer_siem_dropped_total{reason=...}`)
- Admin API: `/api/v1/admin/siem-forwarders/*` (list, create, edit, delete, test, status)
- `audit-events-dropped.md` (local audit DB write loss)
