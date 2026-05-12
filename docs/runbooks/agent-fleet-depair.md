# Runbook — Agent fleet de-pair / mass disconnect

**Severity**: page (multi-cluster outage)
**Component**: `internal/tunnel` server-side, agent fleet

## Symptoms

- Multiple `AstronomerAgentDisconnected` alerts firing simultaneously
- `astronomer_agent_connections{status="connected"}` drops sharply
- `astronomer:agent_disconnected_ratio:5m` (recording rule from T03)
  jumps from baseline
- UI dashboards show all clusters red

## Triage

1. **Server side or fleet side?**
   - All agents lost → server-side; check management plane health first
     (server pod rollouts, hub state, network).
   - Subset lost → cluster-side issue (network split, common upstream).

2. **Recent management plane change?**
   - New server image rolled out? `kubectl get deploy/astronomer-server -o jsonpath='{.spec.template.metadata.labels}'`
   - Gateway / Service touched? `kubectl get gateway,httproute -n astronomer`
   - Server URL changed? Agents won't auto-discover a new hostname.

3. **Agent token rotation?** If a fleet-wide token rotation went
   sideways, agents fall back to reconnect-with-old-token which the
   server rejects.

## Recovery

### Restore the server first

```bash
kubectl -n astronomer rollout undo deploy/astronomer-server
kubectl -n astronomer rollout status deploy/astronomer-server
```
Then watch `astronomer_agent_connections{status="connected"}` over the
next 1–5 minutes. The agents' jittered exponential reconnect (T10)
spreads the reconnect storm.

### Mass re-register (if tokens were nuked)

Generate a new shared registration token, push it to every member
cluster's agent-config Secret, restart the agent Deployments. See
[secret-rotation-runbook.md](../secret-rotation-runbook.md) for the
detailed procedure; this scenario is rare enough that it gets
treated as a planned operation, not a fire drill.

### Selective agent restart (subset failed)

If only certain regions / clouds lost connectivity, restart agents in
those clusters one at a time and watch `agent_last_seen_seconds`
recover before moving to the next.

## Verify

- `astronomer_agent_connections{status="connected"}` returns to the
  expected count (matches `SELECT COUNT(*) FROM clusters WHERE
  decommissioned_at IS NULL`)
- Every cluster's `astronomer_agent_last_seen_seconds` < 30s
- A test k8s call to each cluster succeeds (or at least: sample 5)

## Prevention

- Use staged rollouts on server changes (`maxUnavailable: 1`, PDB
  `minAvailable: 2` — see [chart README quorum-safe-drain section](../../deploy/chart/README.md))
- Pre-stage rotated tokens before invalidating the old one
- Agents have automatic reconnect — most "outages" self-heal within
  one backoff cycle

## Related

- [agent-disconnected.md](agent-disconnected.md) — single-cluster version
- `internal/tunnel/server.go` — Hub.Drain / shutdown semantics (T14)
- `internal/agent/tunnel.go` — `reconnectLoop` (T10 jitter)
