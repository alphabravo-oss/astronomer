# AstronomerLokiIngestDenied

`loki-auth` is rejecting hosted Loki pushes. Fluent Bit on member clusters
retries then drops. BYO sinks are independent. Metrics scrape is unaffected.

## Symptoms

- PrometheusRule expr:
  `sum(rate(astronomer_loki_ingest_requests_total{result=~"unauth|cap"}[5m])) > 0`
  for 5m.
- Server / `loki-auth` logs: `event=loki_ingest_denied`
  `reason=bad_token|cap|org_mismatch`.
- Cluster logging CTA still shows attached; ConfigMap `bearer_token` may be
  stale after a rotate.

`result` is bounded: `ok|unauth|cap|error`. There is **no** `cluster` label.

## Triage

1. **Which result?**
   - `unauth`: unknown bearer, missing token, or `X-Scope-OrgID` ≠ bound
     cluster UUID. Rotate or re-attach; hash Secret
     `astronomer-loki-token-hashes` must contain the SHA-256.
   - `cap`: per-tenant or global ingest budget (SingleBinary 1 MB/s per
     tenant, 8 MB/s global; SimpleScalable 2 / 32). This is fail-closed,
     not a banner.

2. **Hash Secret vs member ConfigMap.** List APIs never return the bearer.
   Re-render is Fernet `token_encrypted` → Fluent Bit ConfigMap. A rotate
   without waiting for the logging reconciler leaves members on the old
   token (`unauth`).

3. **Ingress.** Until tokens exist, ingest is ClusterIP-only
   (`ingestPublic=false`). Public 401s before that are expected.

## Recovery

- Bad token: `POST .../logging/outputs/{id}/rotate-token/` on the system
  row, wait for ConfigMap apply, confirm Fluent Bit reloaded.
- Cap: do **not** raise `ingestion_rate_mb` by hand. Freeze new attaches
  (already 409 `ingest_cap_exceeded` / `degraded_capacity`). BYO Loki is
  the scale-out. Existing pipelines keep their current cap.
- Org mismatch: Fluent Bit `tenant_id` must be the cluster UUID; the
  system renderer ignores operator-supplied tenant ids.

## Verify

- `rate(astronomer_loki_ingest_requests_total{result="ok"}[5m])` rising.
- `unauth` / `cap` rates back to 0.
- Member fluent-bit logs show successful Loki output, not 401/429.
