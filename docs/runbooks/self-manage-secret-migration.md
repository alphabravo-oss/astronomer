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

1. Render the embedded chart and review the complete Argo diff, including
   storage classes/sizes, Argo itself, image mirrors/pull Secrets, scheduling,
   TLS, network policy, backup, and observability resources.
2. Run chart preflight and database migration checks. Do not suppress them for
   self-managed upgrades.
3. Verify every credential source is a Secret reference and that no rendered
   ConfigMap/Application contains credential bytes.
4. Approve the exact digest:

   ```bash
   HASH=$(kubectl -n astronomer get application astronomer-self-manage \
     -o jsonpath='{.metadata.annotations.astronomer\.io/self-manage-spec-hash}')
   kubectl -n astronomer annotate application astronomer-self-manage \
     astronomer.io/self-manage-approved-hash="$HASH" --overwrite
   ```

The next reconcile enables automated sync/prune only if the approval equals the
current digest. A changed or stale digest never arms sync.

## Scrubbing a legacy plaintext Application

This is deliberately operator-gated. The server never scales Argo controllers.

1. Scale `astro-argocd-application-controller` to zero and wait until the
   StatefulSet/Deployment reports zero replicas and **no matching controller
   Pods exist**. A terminating Pod is not quiesced.
2. Let the server reconcile. With the pinned Argo CRD (no status subresource),
   it performs one full-object replacement that removes `spec` plaintext plus
   `operation`, compared sources, sync-result sources, and every history source.
3. Confirm the Application is `awaiting-approval`, has no `status` or
   `operation`, has `revisionHistoryLimit: 0`, and its source is reference-only.
4. Restore the controller, review the non-pruning diff, then approve the exact
   digest using the procedure above.

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
