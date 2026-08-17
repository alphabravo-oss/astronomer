# Self-Managed Application Acceptance and the Write Barrier

The self-managed Argo Application (`astronomer-self-manage`) is deployed
through a staged → acceptance-sync → exact-hash-approval → active lifecycle.
The Application CRD has **no status subresource**, so Argo CD's sync progress
(`status.operationState`) lives in the same object Astronomer reconciles.
During any Running/Terminating acceptance operation Argo is the **single
writer**: the server performs zero Application writes until Argo records a
terminal phase (`Succeeded`, `Failed`, `Error`). Transient Argo annotations
are deliberately left in place during that window and are normalized only
after terminal ownership returns to Astronomer.

## Symptoms

- Reconcile log warning: `self-managed Application has an active operation
  that is unsafe, unverifiable, or unbound from the staged spec; quiesce the
  Argo application controller before sanitation`.
- Reconcile log warning: `defer self-managed Application restage: Argo owns an
  in-flight operation`.
- The Application stays `awaiting-approval` after a sync completed.
- An acceptance sync sits in `status.operationState.phase=Running` past the
  SLO with the preflight Job already `Complete`.

## Triage

1. Read only safe fields — never dump the full object:

   ```bash
   kubectl -n astronomer get application astronomer-self-manage -o json |
     jq '{phase: .metadata.annotations["astronomer.io/self-manage-phase"],
          opPhase: .status.operationState.phase,
          sync: .status.sync.status, health: .status.health.status,
          hasOperation: has("operation")}'
   ```

2. Check the durable operation rows: `select id, status, attempt_count from
   argocd_operations where status in ('running','pending');`
3. If the reconciler reports an unsafe/unverifiable operation, inspect the
   operation shape (prune/force/dryRun/revision) — the server will not touch
   it while the controller runs. That refusal is the designed fail-closed
   behavior, not a bug.
4. Check managed-fields chronology for a foreign writer during the window:

   ```bash
   kubectl -n astronomer get application astronomer-self-manage -o json |
     jq '[.metadata.managedFields[] | {manager, operation, time}]'
   ```

## Recovery

- **Safe operation, still Running**: wait. The server is intentionally
  read-only; Argo finishes and records a terminal phase, after which
  normalization and approval proceed on the next 30s tick.
- **Unsafe or unverifiable operation**: quiesce the controller, then let the
  reconciler sanitize:

  ```bash
  kubectl -n astronomer scale statefulset astro-argocd-application-controller --replicas=0
  kubectl -n astronomer wait --for=delete pod -l app.kubernetes.io/name=argocd-application-controller --timeout=300s
  # next reconcile tick restages/sanitizes; then scale back to 1
  ```

- **Failed/Error terminal phase**: the Application stays awaiting-approval and
  automation is never armed. Fix the underlying sync failure and re-run the
  acceptance sync; do not hand-edit status or mark durable rows completed.

Never: flush Redis, annotate hook Jobs, restart Argo mid-sync, remove
`status.operationState`, or set an `argocd_operations` row to completed by
hand. Any of those invalidates acceptance evidence.

## Verify

Run the committed harness against a disposable cluster; it authenticates
through the public API, submits exactly one non-pruning sync, asserts the
zero-write single-writer interval from managed-fields evidence, compares
protected Secret/PVC evidence byte-for-byte, and applies exact-hash approval:

```bash
DISPOSABLE_CLUSTER_ACK=i-know KUBE_CONTEXT=<disposable-context> \
  ASTRO_USERNAME=admin ASTRO_PASSWORD_FILE=<private-file> \
  ./scripts/validate-live-self-management.sh
```

GO criteria: operation `completed` with `attempt_count=1`, one upstream sync
call, one successful preflight Job lifecycle, Application Synced/Healthy with
exact source/destination binding, unchanged Secret/PVC evidence, active phase
only after exact-hash approval, controller at one replica and zero
running/pending operations.

See also: [self-manage-secret-migration.md](self-manage-secret-migration.md)
for the credential model this acceptance flow protects.
