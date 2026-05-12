# Runbook — High HTTP 5xx error rate

**Alert**: `AstronomerHighHTTPErrorRate`
**Severity**: warning (page after 5m at >1% 5xx)
**Component**: `internal/server`

## Symptoms

- PrometheusRule expr: `astronomer:http_5xx_ratio:5m > 0.01` for 5m
- Users see "Something went wrong" toasts in the UI
- `astronomer_http_requests_total{status=~"5.."}` rate climbing
- Server pod logs contain panic stacks or repeated DB / tunnel errors

## Triage

1. **Pod-level vs cluster-wide?** Check whether one pod or all pods are
   producing 5xx:
   ```bash
   kubectl -n astronomer logs -l app.kubernetes.io/component=server \
     --since=10m | grep '"level":"ERROR"' | head -50
   ```
   - One pod hot → the round-robin from the gateway is hitting a sick
     replica; jump to Recovery → "Drain a sick pod".
   - All pods hot → shared dependency (DB / Redis / tunnel hub) is the
     prime suspect; jump to Triage step 2.

2. **Which downstream is failing?** Compare server logs against the
   live `/readyz` checks:
   ```bash
   for pod in $(kubectl -n astronomer get pods -l app.kubernetes.io/component=server -o name); do
     echo "=== $pod ==="
     kubectl -n astronomer exec "$pod" -- wget -qO- http://localhost:8000/readyz | jq
   done
   ```
   `checks.database.error`, `checks.redis.error`, `checks.tunnel_hub.error`
   pin down the failed leg.

3. **Recent change?** Diff the running image vs main:
   ```bash
   kubectl -n astronomer get deploy astronomer-server -o jsonpath='{.spec.template.spec.containers[0].image}'
   git log --oneline -10
   ```
   If the bad image was just deployed, jump to Recovery → "Roll back".

## Recovery

### Drain a sick pod (single-pod incident)

```bash
kubectl -n astronomer delete pod <pod-name>
```
The Deployment recreates it; PDB `minAvailable=2` (production) ensures
no capacity drop.

### Restart the deployment (broad DB / Redis blip recovered)

```bash
kubectl -n astronomer rollout restart deployment/astronomer-server
kubectl -n astronomer rollout status deployment/astronomer-server
```

### Roll back to last known good

```bash
kubectl -n astronomer rollout undo deployment/astronomer-server
```
For Argo-managed installs, sync to the prior Git commit via the Argo
CD UI instead so the manifest in Git is consistent with the running
state.

## Verify

- `rate(astronomer_http_requests_total{status=~"5.."}[5m]) / rate(astronomer_http_requests_total[5m])` falls below 1%
- `AstronomerHighHTTPErrorRate` clears (≥5m below threshold)
- `/readyz` returns 200 on every server pod
- No new ERROR-level lines in server logs for 5+ minutes

## Prevention

- Pre-deploy: run `go test -count=1 ./...` and `helm template` in CI
- Use `values-production.yaml` preflight (T01/T02) to catch missing prod inputs
- Watch `astronomer_db_query_duration_seconds` p99 trends — sustained climb
  is an early warning before pool exhaustion shows up here

## Related

- [db-pool-exhausted.md](db-pool-exhausted.md) — common upstream
- [agent-disconnected.md](agent-disconnected.md) — tunnel-call 5xx
- `internal/server/readiness.go` — readiness check semantics
