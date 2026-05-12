# Runbook — Argo CD self-manage drift

**Alert**: `AstronomerArgoSelfManageDrift`
**Severity**: warning (OutOfSync for 10m)
**Component**: gitops / `astronomer-self-manage` Application

## Symptoms

- PrometheusRule expr:
  `argocd_app_info{name="astronomer-self-manage", sync_status!="Synced"} > 0` for 10m
- Argo CD UI shows the `astronomer-self-manage` Application as
  `OutOfSync`
- Manifests in the cluster differ from what Git declares

## Triage

1. **What's different?**
   ```bash
   # Via Argo CD UI: open the app → "App Diff"
   # Via CLI:
   argocd app diff astronomer-self-manage
   ```

2. **Why did it drift?**
   - Manual `kubectl apply` / `kubectl edit` against a chart-owned object → revert to Git.
   - Genuine intentional change → update Git, then sync.
   - Auto-sync disabled and Git advanced normally → just sync.
   - Argo CD repo connection broken → see `argocd repo list` for status.

3. **App health vs sync status** — `OutOfSync` is structural drift;
   `Degraded` is a runtime issue with a managed resource. The alert
   fires on the former; degraded resources warrant their own runbook
   (which alert/metric depends on the resource type).

## Recovery

### Sync from Git (preferred — Git is canonical)

```bash
argocd app sync astronomer-self-manage
argocd app wait astronomer-self-manage --timeout 300
```
Or in the UI: "Sync" → review the resources Argo intends to change → "Synchronize".

### Adopt the live state INTO Git (only if the live change is right)

1. Capture the live manifest:
   ```bash
   kubectl -n astronomer get <kind> <name> -o yaml > /tmp/live.yaml
   ```
2. Update `deploy/chart/templates/...` (or the relevant values overlay)
   so `helm template` produces the same shape.
3. Commit + push.
4. `argocd app sync` to confirm SYNCED.

### Re-create the Application (last resort)

If the Application object itself is broken (rare), delete + recreate
from the bootstrap manifest. The chart's self-manage app is documented
in [project_live_env.md](../../../.claude/memory/project_live_env.md)
for the dev environment.

## Verify

- `argocd_app_info{name="astronomer-self-manage"}` shows `sync_status="Synced"`
  and `health_status="Healthy"`
- Alert clears (≥10m back below threshold)
- No diff: `argocd app diff astronomer-self-manage` reports nothing

## Prevention

- Treat the cluster as read-only — every change goes through Git
- Enable Argo CD auto-sync with prune + self-heal for the self-manage app
- Add CI lint to catch chart template changes that would produce
  diffs against a known-good rendered baseline (future work)

## Related

- `deploy/chart/templates/` — Helm chart definition that Argo applies
- `deploy/chart/values-production.yaml` — production posture inputs
