# Runbook — reference-only Argo self-management takeover

The `astronomer-self-manage` Application must contain only non-sensitive Helm
configuration and Kubernetes Secret references. Argo persists Application
sources in status, operation results, history, API responses, and controller
logs; inline signing keys, encryption keys, bootstrap passwords, database DSNs,
Redis URLs, or Dex credentials are therefore a credential disclosure.

## Preconditions

- Back up the management database and the current Helm release Secret.
- Confirm the chart archive version matches the server's embedded repository.
- Inventory the existing core, bootstrap, Postgres, Redis, and Dex Secrets.
- Do not delete or recreate those Secrets. The migration preserves their data,
  removes owner references from newly migrated credentials, and applies
  `Prune=false,Delete=false` plus `IgnoreExtraneous` to platform-owned Secrets.
- Resolve any reported non-reference-backable values (free-form environment,
  headers, extra objects/containers, unknown chart paths) before proceeding.

## First takeover or desired-spec change

The server creates/updates the Application in `awaiting-approval` with an empty
sync policy. It records a SHA-256 digest in
`astronomer.io/self-manage-spec-hash`. Automated sync and prune remain off.
Before the first creation and before every restage, finish the Astronomer server
rollout and prove that only Pods from its current ReplicaSet remain. Use the
rollout, Deployment-count, ReplicaSet, and Pod checks in the legacy-scrub
section below. The server enforces this fence even when no Application exists
and even when the existing Application hash appears unchanged.

1. Render the embedded chart and review the complete Argo diff, including
   storage classes/sizes, Argo itself, image mirrors/pull Secrets, scheduling,
   TLS, network policy, backup, and observability resources.
2. Run chart preflight and database migration checks. Do not suppress them for
   self-managed upgrades.
3. Verify every credential source is a Secret reference and that no rendered
   ConfigMap/Application contains credential bytes.
4. Run one full **non-pruning** acceptance sync. Do not pass `--prune`,
   `--force`, `--replace`, selective resources, source overrides, or sync
   options; the awaiting-approval state machine cancels operations outside this
   narrow shape:

   ```bash
   # Authenticate against Astronomer's bundled Argo root path first.
   argocd login astronomer.example.com --grpc-web \
     --grpc-web-root-path /argocd

   # Pause credential rotation for this bounded check. Capture metadata only;
   # do not print Secret data or reusable data hashes.
   # Replace this example with only the inventoried names that exist in this
   # installation (including custom core/bootstrap and optional Redis/Dex).
   SECRET_NAMES='core-credentials bootstrap-credentials astronomer-self-manage-database'
   BEFORE=$(kubectl -n astronomer get secret ${SECRET_NAMES} -o json \
     | jq -c '[.items[] | {name:.metadata.name,uid:.metadata.uid,resourceVersion:.metadata.resourceVersion}] | sort_by(.name)')

   argocd app sync astronomer-self-manage
   argocd app wait astronomer-self-manage --sync --health --timeout 600

   AFTER=$(kubectl -n astronomer get secret ${SECRET_NAMES} -o json \
     | jq -c '[.items[] | {name:.metadata.name,uid:.metadata.uid,resourceVersion:.metadata.resourceVersion}] | sort_by(.name)')
   if [ "${BEFORE}" != "${AFTER}" ]; then
     echo 'protected Secret identity/resourceVersion changed: FAIL' >&2
     exit 1
   fi
   echo 'protected Secret metadata/data unchanged: PASS'
   ```

   Verify server/worker readiness, migration/preflight completion, login, and
   backup CronJob rendering. Platform-owned credential Secrets carry canonical
   `Prune=false,Delete=false` and `IgnoreExtraneous`; external Secrets were
   never adopted. This protects the acceptance sync while prune is disabled.
5. Only after the non-pruning sync is healthy, approve the exact digest:

   ```bash
   HASH=$(kubectl -n astronomer get application astronomer-self-manage \
     -o jsonpath='{.metadata.annotations.astronomer\.io/self-manage-spec-hash}')
   kubectl -n astronomer annotate application astronomer-self-manage \
     astronomer.io/self-manage-approved-hash="$HASH" --overwrite
   ```

The next reconcile enables automated sync/prune only if the approval equals the
current digest, no operation remains, the completed operation was the narrow
non-pruning form above, status is `Synced`/`Healthy`/`Succeeded` for the exact
staged source and destination, and the server rollout is still complete. A
changed/stale digest or stale status never arms sync.

## Bounded self-managed chart upgrade

An external Helm/server rollout can temporarily move the running binary ahead
of the active self-managed Application. Do **not** perform that rollout while
the old Argo application-controller can self-heal: it can restore the old image
and replica tuples while the new server is trying to adopt them.

1. Record and retain the exact old active Application, including its platform
   ownership label, source/destination, active phase, and spec hash. Do not
   restage, edit, or approve it.
2. Scale `astro-argocd-application-controller` to zero and wait for the
   StatefulSet/Deployment status to reach zero and for every matching controller
   Pod, including terminating Pods, to disappear.
3. Perform the external Helm/server rollout. Keep the controller stopped.
4. Prove the new server rollout is complete with the Deployment, ReplicaSet,
   and Pod ownership checks below. No old or unowned server Pod may remain.
5. Let the new server reconcile while the controller remains stopped. For an
   older same-major active Application with the exact expected identity and
   hash, it adopts the live server, worker, migrate, agent, pull-Secret, and
   replica tuples and stages the newer embedded chart revision. Optional
   frontend absence is adopted as disabled only when the highest deployed Helm
   release explicitly records `frontend.enabled=false`; wait for its Deployment
   deletion to converge before reconciliation. Bundled Postgres, bundled Redis,
   and Dex use their effective chart intent (including defaults): an enabled
   component must have a non-terminating workload, while a disabled component
   must have no workload. Wait for creation/deletion to converge before letting
   the server adopt exact live runtime references and settings. A non-active,
   inconsistent, non-semantic, cross-major, or newer revision fails closed and
   requires operator remediation; it never silently reuses old runtime tags.
6. Restore the controller only after the new reference-only Application is
   staged. Run the full non-pruning sync, verify exact source/destination status,
   health, completed operation, workload rollout, login, and protected Secret
   identity, then approve the exact new digest as described above.

The server checks both controller quiescence and complete server rollout before
reading live tuples. Immediately before restaging the Application, it rechecks
those gates, the exact highest deployed Helm release identity/version, and the
presence plus UID/resourceVersion of every optional topology workload. Any
intervening controller restart, Helm release change, or workload convergence
event aborts the write and the next reconcile rebuilds from fresh evidence.
Live adoption with concurrent Argo self-healing is not a supported or safe
upgrade path.

## Scrubbing a legacy plaintext Application

This is deliberately operator-gated. The server never scales Argo controllers.

1. Finish the Astronomer server rollout first and prove no old reconciler is
   running:

   ```bash
   kubectl -n astronomer rollout status deployment/astronomer-server --timeout=10m
   kubectl -n astronomer get deployment astronomer-server \
     -o jsonpath='{.status.replicas} {.status.updatedReplicas} {.status.readyReplicas} {.status.availableReplicas}{"\n"}'
   SELECTOR='app.kubernetes.io/name=astronomer,app.kubernetes.io/instance=astronomer,app.kubernetes.io/component=server'
   kubectl -n astronomer get rs -l "${SELECTOR}" -L pod-template-hash
   kubectl -n astronomer get pods -l "${SELECTOR}" -L pod-template-hash
   ```

   Desired, updated, ready, and available counts must match; every Pod must be
   non-terminating and owned by the current ReplicaSet/pod-template-hash. No old
   or dangling Pod may remain even when its ReplicaSet desires zero or has
   already been deleted. The server enforces the same gate before touching
   credentials or the Application and again before hash promotion.
2. Scale `astro-argocd-application-controller` to zero and wait until the
   StatefulSet/Deployment reports zero replicas and **no matching controller
   Pods exist**. A terminating Pod is not quiesced.
3. Let the server reconcile. With the pinned Argo CRD (no status subresource),
   it performs one full-object replacement that removes `spec` plaintext plus
   `operation`, compared sources, sync-result sources, and every history source.
4. Confirm the Application is `awaiting-approval`, has no `status` or
   `operation`, has `revisionHistoryLimit: 0`, and its source is reference-only.
5. Restore the controller, run the explicit non-pruning sync and health checks
   above, then approve the exact digest. Approval itself arms automated prune;
   there is no intermediate automatic-prune trial.

If the installed Application CRD enables a status subresource, migration fails
closed. Do not bypass this guard; upgrade the migration logic for that CRD.

## Rotation

Rotating a referenced Secret does not change the Application. Kubernetes does
not restart Pods when Secret-backed environment variables change, so explicitly
restart the consuming workloads after a successful atomic Secret update.

- Core signing/encryption keys: follow the encryption-key rewrap sequence, then
  restart server and worker. Never rotate the Fernet key without re-encrypting
  stored ciphertext and preserving the old key for rollback.
- Bundled Postgres: change the database role password and atomically update both
  `password` and `dsn` in `astronomer-self-manage-database`, then restart
  Postgres, server, worker, migrations, and backup/restore consumers.
- Redis: update the referenced password/URL, then restart server and worker.
- Dex static client rotation is blocked until DEX-01 atomically coordinates the
  Kubernetes runtime Secret with Astronomer's encrypted SSO client record. Do
  not rotate only one side: that breaks login. Follow the DEX-01 procedure once
  it lands.

Verify logins, API readiness, worker jobs, backup/restore checks, and Argo health.
The Application source and digest must remain unchanged for a credential-only
rotation.

## Ownership, disaster recovery, and decommission

Stable self-managed credential Secrets are server-owned, have no owner
references, and intentionally survive Application deletion. Back them up under
separate wrapping-key custody. During disaster recovery, restore Secrets before
restoring/approving the Application. During final decommission, delete them only
after database/export retention and rollback windows have expired.
