# Management-plane disaster recovery runbook

This runbook covers restoring **Astronomer Go's own Postgres database** from a
nightly `pg_dump` taken by the `managementBackup` CronJob (see
`deploy/chart/templates/management-plane-backup-cronjob.yaml`). It is the
counterpart to that backup: the CronJob writes a custom-format dump to S3;
this document is how you put it back.

It is intentionally an operator-facing document. It does not assume the reader
has Astronomer Go internals memorised — but it does assume the reader is
comfortable with `kubectl`, `helm`, and `psql` / `pg_restore`.

---

## When to use this runbook

Use this procedure when the management plane's source-of-truth database is
unusable and you need to roll back to the last good nightly dump. Concrete
failure modes:

- **Logical corruption**: an accidental `DROP DATABASE`, `DROP TABLE`, or a
  rogue migration that the rollback path can't undo.
- **Bulk data loss**: a script deleted thousands of `cluster_agents` /
  `project_environments` / `audit_log` rows by mistake.
- **Storage failure on the bundled StatefulSet** that left the PVC unreadable
  (development / single-replica deployments only — production installs run
  against managed Postgres and rely on the cloud provider's own PITR first).
- **Cluster rebuild**: the management plane's Kubernetes cluster has been
  re-created from scratch and you need to repopulate state.

Do **not** use this runbook for:

- A managed *workload* cluster restore. Those are driven by the
  `Backup` / `BackupRestore` CRs handled by `internal/handler/backups.go`
  (Velero-backed) and are a separate procedure.
- Recovering a single deleted row. Bring up a side-car database from the dump
  and copy the row(s) across with `pg_dump --table` / `INSERT`.
- Rolling back a config change. Roll the Helm release back instead
  (`helm rollback astronomer <revision>`).

---

## What this restore is and isn't

**This restore brings back:**

- Every table in the `astronomer` database: clusters, cluster agents, projects,
  project environments, RBAC (users, roles, role bindings), audit log,
  monitoring config, ArgoCD instance records, integration tokens (encrypted),
  IDP / SSO config (encrypted), and migration history.
- All the structural metadata managed clusters need in order to be recognised
  again — cluster IDs, agent tokens, environment slugs, etc.

**This restore does NOT bring back:**

- **Managed workload clusters themselves.** A restored management plane will
  not call out to your fleet to "re-create" them. Each agent that is still
  running in a managed cluster keeps its identity (cluster ID + agent token)
  and will reconnect on its own once the management plane is reachable again
  and the encryption key matches.
- **Redis state.** Redis is the asynq queue backend; it is ephemeral by design.
  In-flight jobs at the moment of the disaster are lost. The worker's periodic
  re-evaluators (cluster status, project sync, etc.) bring the platform state
  forward within a few minutes of the server returning to healthy.
- **The bootstrap secret + encryption key.** These live in Kubernetes Secrets
  (`<release>-bootstrap`, `<release>-secrets`), not in Postgres. The chart
  annotates them with `helm.sh/resource-policy=keep` so they survive
  `helm uninstall`. If you have lost those Secrets, you have lost the ability
  to decrypt agent tokens / SSO secrets / wrapped integration credentials and
  this runbook can not bring them back. Restore Secrets from your secret
  manager (Vault, AWS Secrets Manager, sealed-secrets, etc.) **before** you
  start the database restore.

**The single most important invariant**: `secrets.encryptionKey` (the Fernet
key) must be the same value before and after the restore. Every encrypted blob
in the restored DB — agent tokens, SSO client secrets, integration tokens —
was Fernet-wrapped with that key. If the key changes, every decrypted column
becomes garbage and agents can not re-authenticate.

---

## Preconditions

Before starting, confirm you have:

- [ ] `kubectl` access to the management-plane Kubernetes cluster with
      permission to scale Deployments and exec into Pods in the
      `astronomer` namespace.
- [ ] `helm` (>= 3.10) on the same kubeconfig, with permission to read the
      release's values.
- [ ] AWS CLI (or the equivalent for your S3-compatible store) configured with
      credentials that can `s3 ls` / `s3 cp` against the backup bucket. Read
      access is enough; the restore reads only.
- [ ] `psql` and `pg_restore` matching the source DB's major version (the
      backup template defaults to Postgres 16; check the dump header with
      `pg_restore -l` if unsure).
- [ ] The current `secrets.encryptionKey` value. This must be identical to
      the value used by the install that produced the dump. Confirm by
      reading the live Secret **before** doing anything destructive:

      ```bash
      kubectl -n astronomer get secret <release>-secrets \
        -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d
      ```

      Save the value somewhere safe in your password manager.
- [ ] The current bootstrap secret if you intend to log in as the bootstrap
      admin after restore:

      ```bash
      kubectl -n astronomer get secret <release>-bootstrap \
        -o jsonpath='{.data.password}' | base64 -d
      ```
- [ ] A recent dump. List candidates:

      ```bash
      aws s3 ls s3://<bucket>/astronomer-pg/<release>/daily/ | sort
      ```

      Pick the most recent dump that pre-dates the corruption, and download it:

      ```bash
      aws s3 cp s3://<bucket>/astronomer-pg/<release>/daily/<timestamp>.pgcustom ./astronomer.pgcustom
      ```

      `pg_restore -l ./astronomer.pgcustom | head` should show a table of
      contents — if it errors, the dump is truncated and you must pick another.

---

## Procedure

The high-level shape is: freeze the writers, drop and recreate the database,
restore the dump, thaw the writers, validate.

### 1. Capture the current state

Even if the DB is corrupted, the rest of the chart's config is good — capture
it so you can compare after the restore.

```bash
helm -n astronomer get values astronomer > /tmp/astronomer-values-pre-restore.yaml
kubectl -n astronomer get deploy,sts,svc,cronjob -o wide \
  > /tmp/astronomer-resources-pre-restore.txt
```

Take a screenshot of `helm -n astronomer get values astronomer` so you have
the production override values handy if the cluster goes fully sideways during
the procedure.

### 2. Stop the writers

Scale the server and worker to zero so nothing else writes during the restore.
Leave the bundled Postgres (or your managed Postgres) running — you need it
to be reachable for the restore client.

```bash
kubectl -n astronomer scale deploy/astronomer-server --replicas=0
kubectl -n astronomer scale deploy/astronomer-worker --replicas=0
# If the frontend Deployment exists, scale it too (it polls /health endpoints
# but no longer writes).
kubectl -n astronomer scale deploy/astronomer-frontend --replicas=0 || true

# Wait for pods to disappear.
kubectl -n astronomer wait --for=delete pod \
  -l app.kubernetes.io/component=server --timeout=120s
kubectl -n astronomer wait --for=delete pod \
  -l app.kubernetes.io/component=worker --timeout=120s
```

Also suspend the backup CronJob so a partially-restored DB doesn't get
captured as the new "latest good" dump:

```bash
kubectl -n astronomer patch cronjob astronomer-management-backup \
  -p '{"spec":{"suspend":true}}'
```

### 3. Open a psql session against the management Postgres

#### Bundled (dev) Postgres

```bash
kubectl -n astronomer exec -it sts/astronomer-postgres -- \
  psql -U astronomer -d postgres
```

#### External (managed) Postgres

Start a temporary client Pod with `psql`:

```bash
kubectl -n astronomer run psql-restore --rm -it --restart=Never \
  --image=postgres:16-alpine \
  --env="PGURL=$(kubectl -n astronomer get secret <dsn-secret> \
        -o jsonpath='{.data.<dsn-key>}' | base64 -d)" \
  -- psql "$PGURL" -d postgres
```

You should land at a `postgres=#` prompt.

### 4. Drop and recreate the database

> **Destructive — confirm one more time** that you have the dump in hand
> (`pg_restore -l ./astronomer.pgcustom` shows a table of contents) before
> running these.

In the psql session opened above:

```sql
-- Kick any remaining sessions off the target DB.
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = 'astronomer'
  AND pid <> pg_backend_pid();

DROP DATABASE astronomer;
CREATE DATABASE astronomer OWNER astronomer;
\q
```

### 5. Restore the dump

Run `pg_restore` against the empty database. We pass `--no-owner --no-acl`
because the dump was produced with the same flags (it's environment-portable);
the schema's roles are recreated by the new DB's owner.

#### Bundled Postgres

Copy the dump into the Postgres pod and restore from there:

```bash
kubectl -n astronomer cp ./astronomer.pgcustom \
  astronomer-postgres-0:/tmp/astronomer.pgcustom

kubectl -n astronomer exec -it astronomer-postgres-0 -- \
  pg_restore \
    --username=astronomer \
    --dbname=astronomer \
    --no-owner --no-acl \
    --jobs=4 \
    --verbose \
    /tmp/astronomer.pgcustom
```

#### External Postgres

```bash
pg_restore \
  --dbname "$PGURL" \
  --no-owner --no-acl \
  --jobs=4 \
  --verbose \
  ./astronomer.pgcustom
```

`--jobs=4` parallelises the data-load phase. Bump it up on a beefy DB; the
schema phase always runs single-threaded.

Expect `pg_restore` to emit a small number of "errors ignored" — these are
typically `CREATE EXTENSION` no-ops or comments on system objects that the
restoring role doesn't own. As long as the final summary line reports the
expected object counts (the dump's TOC tells you what to expect), the restore
is good.

### 6. Spot-check the data before bringing the app back

Still using the psql session (reconnect to the `astronomer` database):

```sql
\c astronomer

-- Migration history matches what the chart's `migrate` Job expects.
SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 5;

-- The big tables have rows.
SELECT 'clusters' AS t, count(*) FROM clusters
UNION ALL SELECT 'cluster_agents', count(*) FROM cluster_agents
UNION ALL SELECT 'projects', count(*) FROM projects
UNION ALL SELECT 'users', count(*) FROM users
UNION ALL SELECT 'audit_log', count(*) FROM audit_log;
```

Audit log row counts should match (or be very close to) the last good count
you have from monitoring / your data warehouse. If `audit_log` is empty but
`clusters` is populated, the dump is partial — pick an older one and restart
from step 4.

### 7. Bring the writers back up

```bash
kubectl -n astronomer scale deploy/astronomer-server --replicas=3
kubectl -n astronomer scale deploy/astronomer-worker --replicas=3
kubectl -n astronomer scale deploy/astronomer-frontend --replicas=3 || true

kubectl -n astronomer rollout status deploy/astronomer-server --timeout=300s
kubectl -n astronomer rollout status deploy/astronomer-worker --timeout=300s
```

Replica counts above mirror `values-production.yaml`. For dev / k3d, use `1`.

Re-enable the backup CronJob:

```bash
kubectl -n astronomer patch cronjob astronomer-management-backup \
  -p '{"spec":{"suspend":false}}'
```

### 8. Validate end-to-end

1. **Health probe**:

   ```bash
   kubectl -n astronomer port-forward svc/astronomer-server 8000:8000 &
   curl -sf http://127.0.0.1:8000/health/
   curl -sf http://127.0.0.1:8000/readyz
   ```

2. **Sign in** as the bootstrap admin (or your normal SSO identity) via the
   UI. If SSO is broken, jump to the "SSO broken after restore" section
   below.

3. **Cluster list** in the UI shows the clusters you expected to see. Each
   one should transition to `connected` within a couple of minutes as its
   agent reconnects. Watch the worker logs:

   ```bash
   kubectl -n astronomer logs -l app.kubernetes.io/component=worker \
     --tail=200 -f | grep -i 'agent'
   ```

4. **Audit log has history** from before the disaster:

   ```sql
   SELECT min(created_at), max(created_at), count(*) FROM audit_log;
   ```

   The `min` should be older than your retention window; the `max` should
   be roughly the time the disaster happened.

5. **Trigger a manual backup** to confirm the restored DB can be dumped end
   to end:

   ```bash
   kubectl -n astronomer create job \
     --from=cronjob/astronomer-management-backup \
     manual-postrestore-$(date -u +%Y%m%d-%H%M%S)
   ```

   Watch it complete cleanly, then verify the new object lands in
   `s3://<bucket>/astronomer-pg/<release>/daily/`.

---

## What to do if SSO is broken after restore

Symptom: the UI loads, but every SSO redirect throws "invalid client secret"
/ "decryption failed" / "fernet token invalid". Agents in managed clusters
flap between `connecting` and `error`.

Almost always, the root cause is **encryption-key mismatch**: the Fernet key
in the running pod is not the key that wrapped the encrypted columns in the
dump.

### Confirm the symptom

```bash
# What the running server thinks the key is.
kubectl -n astronomer get secret <release>-secrets \
  -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d
```

Compare against the key you saved during step 0 of the preconditions. If
they differ, one of these happened:

- The `<release>-secrets` Secret was lost when the cluster was rebuilt, and
  Helm re-created it with the default dev key shipped in `values.yaml`.
- A `--set-file secrets.encryptionKey=...` during the post-restore
  `helm upgrade` pointed at the wrong file.
- The Secret was rotated between the dump being written and the disaster
  happening (rotation should re-encrypt in place; if it was interrupted, the
  DB has a mix of two keys' wrapped values).

### Fix

If you still have the original key in your secret manager:

```bash
kubectl -n astronomer create secret generic <release>-secrets \
  --from-literal=ENCRYPTION_KEY='<original-fernet-key>' \
  --from-literal=SECRET_KEY='<original-jwt-key>' \
  --dry-run=client -o yaml \
  | kubectl apply -f -

kubectl -n astronomer rollout restart deploy/astronomer-server deploy/astronomer-worker
```

If the original key is genuinely lost, you can not recover the encrypted
columns. The least-bad recovery is:

1. Clear the encrypted columns (`UPDATE cluster_agents SET token_ciphertext = NULL`,
   same for SSO secrets / integration tokens).
2. Reissue each cluster's agent token from the UI (the old agents will fail
   to reconnect; re-run the agent install on each managed cluster).
3. Re-enter SSO client secrets and integration tokens from your IdP / vendor
   portals.

This is painful enough that **protecting the encryption key Secret should be
your highest-priority post-incident action**.

---

## Postgres and Kubernetes snapshot mismatch

Postgres is the authoritative store for users, RBAC, audit history,
credentials, cluster inventory, operation rows, and product metadata.
Kubernetes/etcd is authoritative for live Kubernetes objects, controller-owned
status, and ArgoCD reconciliation resources.

When snapshots are from different points in time:

| Scenario | Recovery rule |
|----------|---------------|
| Postgres is older than Kubernetes | Keep Postgres as the source for product state. Reconcile CRD-owned `Cluster` and `Project` objects by `external_ref_*`; import only rows that carry ownership proof or after an explicit operator import. |
| Kubernetes is older than Postgres | Keep Postgres rows. Recreate missing ArgoCD cluster Secrets, ApplicationSets, and CRD status from DB-owned intent where encrypted credential material is available. |
| ArgoCD cluster Secret exists but DB row is missing | Do not silently adopt it. Import only if Astronomer ownership labels match, otherwise leave unmanaged and surface drift. |
| CRD exists but same-name DB row is UI/API-owned | Do not take ownership automatically. Resolve through an explicit ownership transfer/import procedure. |
| Redis jobs are missing after restore | Treat Redis as ephemeral. Run recovery sweeps and retry idempotent operations from Postgres state instead of trying to restore queue contents. |

After any mismatch recovery, run the drift/repair checks once they are
available, then trigger an ad-hoc management backup so the new baseline is
captured.

---

## Limits

| Metric | Value |
|--------|-------|
| RPO    | up to 24h (gap to the last successful nightly dump). Take an ad-hoc dump before any risky change (`kubectl create job --from=cronjob/...`). |
| RTO    | ~15 min for a single-region, single-DB restore once the operator is at the keyboard. Roughly linear in DB size: ~30 GB dumps restore in ~10 min on managed Postgres with `--jobs=4`. |
| Data not covered | Redis (asynq queue state), in-flight Jobs at the moment of disaster, Prometheus metrics history (lives in your monitoring stack, not in Astronomer's DB). |
| Cross-region failover | Out of scope. The bucket is single-region by default. For multi-region resilience, set `managementBackup.s3.region` to a region that replicates to another region via the cloud provider's bucket replication. |

If your business requires a tighter RPO than 24h, move to managed-Postgres
PITR (RDS, Aurora, CloudSQL, Hetzner Cloud Managed Postgres) and treat this
CronJob as the cold backup of last resort. Hourly dumps from this CronJob are
possible (`schedule: "0 * * * *"`) but they are not a substitute for proper
WAL-based PITR.

---

## Appendix: emergency one-liner restore (managed Postgres)

For an operator who is comfortable enough to skip the runbook narrative:

```bash
# 0. Save the encryption key.
kubectl -n astronomer get secret astronomer-secrets \
  -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d > /tmp/enc.key

# 1. Freeze.
kubectl -n astronomer scale deploy/astronomer-server deploy/astronomer-worker --replicas=0
kubectl -n astronomer patch cronjob astronomer-management-backup \
  -p '{"spec":{"suspend":true}}'

# 2. Fetch the dump.
aws s3 cp \
  s3://my-astronomer-backups/astronomer-pg/astronomer/daily/<stamp>.pgcustom \
  ./astronomer.pgcustom

# 3. Drop + recreate + restore.
PGURL='postgres://...'
psql "${PGURL%/*}/postgres" -c "DROP DATABASE astronomer;"
psql "${PGURL%/*}/postgres" -c "CREATE DATABASE astronomer OWNER astronomer;"
pg_restore --dbname "$PGURL" --no-owner --no-acl --jobs=4 ./astronomer.pgcustom

# 4. Thaw.
kubectl -n astronomer scale deploy/astronomer-server deploy/astronomer-worker --replicas=3
kubectl -n astronomer patch cronjob astronomer-management-backup \
  -p '{"spec":{"suspend":false}}'
```

If anything in the one-liner version surprises you, read the long version
above instead.
