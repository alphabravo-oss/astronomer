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
   kubectl -n astronomer-system get pods
   kubectl -n astronomer-system logs deploy/astronomer-agent --tail=200 | grep -iE 'credential_source|reconnect|error|dropped'
   ```
   Look for:
   - Repeated `dial server` failures → network / DNS issue
   - `force-closing tunnel due to congestion` (T33) → server send-buffer saturated
   - `credential_source=durable_secret` plus rejection → durable token binding/revocation issue
   - `credential_source=bootstrap_secret` plus rejection → expired or wrong-cluster registration bootstrap
   - durable Secret read error → fix API/RBAC; the agent intentionally does not downgrade to bootstrap

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
kubectl -n astronomer-system rollout restart deploy/astronomer-agent
```
The reconnect loop uses jittered exponential backoff (T10), so a fleet
restart spreads safely.

### Recover credential state

If durable rotation or persistence failed, see
[secret-rotation-runbook.md](../secret-rotation-runbook.md) and
[agent-credential-ownership.md](../agent-credential-ownership.md). Do not
overwrite the durable Secret by reapplying bootstrap YAML; they are separate
objects and the agent owns durable rotation.

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

- Preserve the singleton `Recreate` deployment strategy; multiple replicas
  compete for the same cluster tunnel identity.
- Use the control-plane durable rotation endpoint and wait for agent adoption
  before revoking old material.
- Watch `astronomer_dropped_events_total{component="agent_tunnel_send"}` —
  congestion drops are an early warning before the disconnect fires (T33)

## Related

- [agent-fleet-depair.md](agent-fleet-depair.md) — multi-cluster version
- `internal/tunnel/server.go` — Hub lifecycle
- `internal/agent/tunnel.go` `Send` / `failClose` — T33 eager-close
- `internal/handler/cluster_breaker.go` — T19 circuit breaker
