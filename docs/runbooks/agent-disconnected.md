# Runbook — Cluster agent disconnected

**Alert**: `AstronomerAgentDisconnected`
**Severity**: warning (last_seen > 120s for 2m)
**Component**: `internal/tunnel`, agent

## Symptoms

- PrometheusRule expr: `astronomer_agent_last_seen_seconds > 120` for 2m
- UI shows the cluster as "Disconnected" / red dot
- Tunnel operations against that cluster fail fast (T19 circuit breaker
  trips after 5 consecutive failures, then short-circuits with
  `ErrCircuitOpen` for 30s)
- `astronomer_agent_connections{status="connected"}` drops by 1 per
  affected cluster

## Triage

1. **One cluster or many?** The metric carries `cluster_id`; if many
   clusters dropped at once, jump to [agent-fleet-depair.md](agent-fleet-depair.md).

2. **Agent pod healthy?**
   ```bash
   # From the affected member cluster:
   kubectl -n astronomer-agent get pods
   kubectl -n astronomer-agent logs deploy/astronomer-agent --tail=200 | grep -iE 'reconnect|error|dropped'
   ```
   Look for:
   - Repeated `dial server` failures → network / DNS issue
   - `force-closing tunnel due to congestion` (T33) → server send-buffer saturated
   - Expired / rotated token → registration token issue

3. **Server-side hub state**:
   ```bash
   # From management plane:
   curl -s -H "Authorization: Bearer $TOKEN" \
     "https://${HOST}/api/v1/admin/platform/health/" | jq
   ```
   Confirms whether the server has dropped the agent from its hub
   (T18 sharded agent map).

## Recovery

### Restart the agent

```bash
# Affected member cluster:
kubectl -n astronomer-agent rollout restart deploy/astronomer-agent
```
The reconnect loop uses jittered exponential backoff (T10), so a fleet
restart spreads safely.

### Re-pair (token rotation needed)

If the agent token has been rotated server-side and the agent doesn't
have the new value, see [secret-rotation-runbook.md](../secret-rotation-runbook.md)
for the re-registration procedure.

### Clear a stuck circuit breaker (server-side)

The breaker auto-recovers — wait 30s after the underlying issue is
fixed and the first successful request transitions OPEN → HALF_OPEN
→ CLOSED. To force it sooner, restart the server pod that holds the
agent's connection.

## Verify

- `astronomer_agent_last_seen_seconds` for the cluster returns to 0–30s
- `astronomer_agent_connections{status="connected"}` includes the
  affected cluster again
- A test k8s call works:
  ```bash
  curl -s -H "Authorization: Bearer $TOKEN" \
    "https://${HOST}/api/v1/clusters/${ID}/resources/services/?namespace=default" | jq '.data | length'
  ```

## Prevention

- Run the agent with multiple replicas where the member cluster
  supports it
- Pre-stage rotated tokens before invalidating the old one
- Watch `astronomer_dropped_events_total{component="agent_tunnel_send"}` —
  congestion drops are an early warning before the disconnect fires (T33)

## Related

- [agent-fleet-depair.md](agent-fleet-depair.md) — multi-cluster version
- `internal/tunnel/server.go` — Hub lifecycle
- `internal/agent/tunnel.go` `Send` / `failClose` — T33 eager-close
- `internal/handler/cluster_breaker.go` — T19 circuit breaker
