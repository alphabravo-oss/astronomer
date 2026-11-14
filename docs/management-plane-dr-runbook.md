# Management-plane disaster recovery runbook

This runbook covers restoring **Astronomer Go's own Postgres database** from a
nightly `pg_dump` taken by the `managementBackup` CronJob (see
`deploy/chart/templates/management-plane-backup-cronjob.yaml`). It is the
counterpart to that backup: the CronJob writes an AES-256-GCM-encrypted custom
dump, an encrypted key bundle, and an authenticated manifest to Object-Locked
S3 storage; this document is how you put it back.

The concise lifecycle and evidence checklist lives in
[management backup and restore](runbooks/management-backup-and-restore.md).
Use that document for continuous backup/drill ownership and this document for
the detailed cutover procedure. Use
[production installation](runbooks/production-installation.md) when building a
clean recovery management cluster.

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
  monitoring config, delivery sources/bundles/targets/rollouts, integration tokens (encrypted),
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

**The single most important invariant**: `secrets.encryptionKey` (the age
key) must be the same value before and after the restore. Every encrypted blob
in the restored DB — agent tokens, SSO client secrets, integration tokens —
was age-sealed with that key. If the key changes, every decrypted column
becomes garbage and agents can not re-authenticate.

---

## Key custody and the encryption-key backup (F4)

The nightly `pg_dump` captures ciphertext. It does **not**, on its own, capture
the age/JWT key that decrypts it — that key lives in a Kubernetes Secret
(`<release>-secrets`), not in Postgres. A restore onto a brand-new cluster where
that Secret is gone brings back rows that are permanently undecryptable.

The chart closes this gap when you wire
`managementBackup.encryption.wrappingSecretRef.name` and a stable
`managementBackup.encryption.sourceIdentity`:

- Every dump and key bundle is encrypted client-side with a streaming,
  versioned AES-256-GCM envelope. The key bundle is stored under
  `<prefix>/<release>/keys/<timestamp>.keys.tar.aead`; plaintext is removed
  before upload.
- A closed, HMAC-SHA256-authenticated manifest binds the ciphertext digests and
  sizes, object names, source installation, Helm release, chart version, and
  database schema version. Restore verifies the manifest and both payloads
  before decrypting or invoking `pg_restore`.
- The writer validates bucket Object Lock and its default retention before
  `pg_dump`. It has a write-only identity; the distinct retention CronJob has
  the delete-capable identity and supports dry-run review.
- The weekly restore-drill (`managementRestoreDrill.decryptCheck`) downloads the
  latest wrapped key bundle, unwraps it with the **same** passphrase, and
  age-decrypts one known sealed column in the restored DB. If it can't
  decrypt, the drill **fails** — so a broken key backup can no longer hide
  behind a green drill.

**Custody rules — read before wiring this:**

1. The wrapping passphrase MUST live in a different custody domain than the S3
   backup credentials and the DB dump. Whoever can read the backup bucket must
   NOT also be able to read the wrapping passphrase, or the separation is moot
   (they could unwrap the key and decrypt every secret in the dump). Practical
   options: a different cloud account / KMS, an offline password vault, or a
   sealed-secret whose sealing key is held by a different team.
2. Create the wrapping-passphrase Secret out-of-band (never commit it to git):

   ```bash
   kubectl -n astronomer create secret generic astronomer-key-wrap \
     --from-literal=passphrase="$(openssl rand -base64 48)"
   ```

   Then record that passphrase in your separate custody store — **losing it
   makes the key backup unrecoverable**, and rotating it invalidates every
   previously-wrapped bundle (keep the old passphrase until the retention window
   of bundles wrapped under it has rolled off).
3. Wire the authenticated backup boundary and distinct retention identity:

   ```
   --set managementBackup.encryption.sourceIdentity=prod-us-east-1 \
   --set managementBackup.encryption.wrappingSecretRef.name=astronomer-key-wrap \
   --set managementBackup.retention.credentialsSecretRef.name=astronomer-backup-retention
   ```

4. The first-party `astronomer-dr` image contains the exact `dr-crypto`,
   PostgreSQL, and AWS CLI toolchain used by both writer and drill.

**Recovering the key during a real restore onto a new cluster:**

```bash
# Download the selected manifest and the exact two object keys it names. Run
# manifest-verify with the expected release/namespace/source and both payload
# files before either decrypt command (the automated drill is the reference).
dr-crypto decrypt --input keys.tar.aead --output keys.tar \
  --passphrase-file ./wrapping-passphrase --expect-purpose key-bundle \
  --expect-context '<context copied from the authenticated backup set>'
dr-crypto key-bundle-extract --input keys.tar --output ./keys
cat keys/ASTRONOMER_ENCRYPTION_KEY   # feed this into secrets.encryptionKey
```

Pre-GA `.keys.tar.enc` AES-CBC bundles are read-only legacy inputs and are not
accepted by the automated restore drill because they cannot be authenticated.
Manual compatibility ends on **2027-09-17**. Before then,
`dr-crypto legacy-key-bundle-decrypt --sunset 2027-09-17` can read the former
OpenSSL envelope; immediately pass its output through `key-bundle-extract` in
an isolated environment and create a current AEAD backup. The automated drill
never accepts CBC, and an unauthenticated bundle is never promoted directly
into production.

Restore `secrets.encryptionKey` (and `secrets.secretKey` from
`keys/SECRET_KEY`) into the new install **before** you restore the database, so
the decrypted columns line up with the key.

When `catalog.ca.existingSecret` was configured, the wrapped bundle also
contains `keys/catalog-ca/`. When catalog publisher signatures were enabled it
contains `keys/catalog-signature-trust/` as well. Recreate both Secrets from
their recovered files before starting Astronomer and retain the same Helm
references. This restores the transport CA and offline publisher trust material
alongside the database-held catalog state; no private catalog URL, proxy
credential, or source credential is printed by the backup job.

Use the `dr-crypto` extractor shipped with the backup producer or a newer
compatible release. The bounded tar format preserves existing flat bundles and
adds files one level below only `catalog-ca/` and `catalog-signature-trust/`.
Older extractors reject these nested bundles. The extractor validates the whole
archive before writing: at most 1,024 files, 1 MiB per file and 16 MiB total
payload, with no links, traversal, duplicate paths or arbitrary directories.
Extraction creates private directories (0700) and files (0600) and refuses an
existing destination. Kubernetes Secret projection metadata is not archived.

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
        -o jsonpath='{.data.ASTRONOMER_ENCRYPTION_KEY}' | base64 -d
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

      Pick the most recent `.manifest.json` that pre-dates the corruption.
      Authenticate it with `dr-crypto manifest-verify`, download only the exact
      archive/key objects it names, and run verification again with
      `--archive-file` and `--key-bundle-file` before decryption:

      ```bash
      aws s3 cp s3://<bucket>/astronomer-pg/<release>/daily/<timestamp>.manifest.json ./backup.manifest.json
      ```

      Only after authenticated decryption should `pg_restore -l
      ./astronomer.pgcustom` show a table of contents. Any manifest, digest, AEAD,
      source, or context failure rejects the recovery point.

---

## Procedure

The high-level shape is: freeze the writers, drop and recreate the database,
restore the dump, validate schema/data/keys while writers remain stopped, then
thaw the captured writer state and validate end-to-end.

### 1. Capture the current state

Even if the DB is corrupted, the rest of the chart's config is good — capture
it so you can compare after the restore.

```bash
set -Eeuo pipefail
umask 077
restore_state=$(mktemp -d "$PWD/astronomer-restore.XXXXXX")
helm -n astronomer get values astronomer > "$restore_state/values.yaml"
kubectl config current-context > "$restore_state/context.txt"
kubectl -n astronomer get deploy,hpa,cronjob -o json > "$restore_state/controllers.json"
kubectl -n astronomer get deploy,sts,svc,cronjob -o wide > "$restore_state/resources.txt"
```

Values can contain credentials: keep this directory private and copy it only to
approved protected recovery storage. Do not put values or key material in
screenshots, shell history or public evidence. Record the intended kube context,
namespace, database host/name and backup manifest identity before any mutation.

### 2. Stop the writers

Capture each Deployment's original replica count and each CronJob's original
suspend state from `controllers.json`. Pause any GitOps reconciler, HPA or other
controller that can restore writer replicas, using its supported maintenance
mechanism and retaining its previous configuration. Stop active backup Jobs and
other database clients after identifying their owner. Verify no writer sessions
remain; scaling Deployments alone does not establish this.

Scale the server and worker to zero so nothing else writes during the restore.
Leave the bundled Postgres (or your managed Postgres) running — you need it
to be reachable for the restore client.

```bash
kubectl -n astronomer scale deploy/astronomer-server --replicas=0
kubectl -n astronomer scale deploy/astronomer-worker --replicas=0
# If the frontend Deployment exists, scale it too (it polls /health endpoints
# but no longer writes).
# Only if captured above; retain its original replica count.
kubectl -n astronomer scale deploy/astronomer-frontend --replicas=0

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

Use a private, authenticated PostgreSQL client with a Secret-mounted libpq
service/password file (`PGSERVICEFILE`/`PGPASSFILE`, mode 0600). In Kubernetes,
mount an existing authorized Secret into an owned temporary client Pod; do not
expand a DSN into `kubectl run --env`, command arguments or logs. Use a client
version compatible with the dump and the target database. Select the maintenance
`postgres` database for creation and the intended application database for
restore. Keep the exact service name in the private recovery record.

You should land at a `postgres=#` prompt.

### 4. Drop and recreate the database

> **Destructive — confirm one more time** that you have the dump in hand
> (`pg_restore -l ./astronomer.pgcustom` shows a table of contents) before
> running these.

In the psql session opened above:

```sql
-- Compare these with the private recovery record before proceeding.
SELECT current_database(), inet_server_addr(), inet_server_port(), current_user;
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
restored objects are owned by the restore role. Required roles and extensions
must already be available; these flags do not recreate roles.

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
    --single-transaction --exit-on-error \
    --verbose \
    /tmp/astronomer.pgcustom
```

#### External Postgres

```bash
pg_restore \
  --dbname "service=astronomer-restore" \
  --no-owner --no-acl \
  --single-transaction --exit-on-error \
  --verbose \
  ./astronomer.pgcustom
```

Both examples require exit status 0. `--single-transaction --exit-on-error`
rolls back on the first SQL error; do not combine it with parallel jobs. Any
restore error leaves writers stopped. Diagnose permissions, extensions or dump
compatibility, recreate only the confirmed disposable/restore target, and rerun.
Never accept an "errors ignored" summary as success.

### 6. Spot-check the data before bringing the app back

Still using the psql session (reconnect to the `astronomer` database):

```sql
\c astronomer

-- Compare with the backup manifest and the server's embedded Goose migrations.
WITH latest AS (
  SELECT DISTINCT ON (version_id) version_id, is_applied
  FROM public.goose_db_version ORDER BY version_id, id DESC
)
SELECT COALESCE(MAX(version_id), 0) AS applied_version
FROM latest WHERE is_applied;

-- The big tables have rows.
SELECT 'clusters' AS t, count(*) FROM public.clusters
UNION ALL SELECT 'projects', count(*) FROM public.projects
UNION ALL SELECT 'users', count(*) FROM public.users
UNION ALL SELECT 'audit_log', count(*) FROM public.audit_log;
```

Audit log row counts should match (or be very close to) the last good count
you have from monitoring / your data warehouse. If `audit_log` is empty but
`clusters` is populated, the dump is partial — pick an older one and restart
from step 4.

### 7. Bring the writers back up only after all gates pass

Before any writer starts, require all of the following:

- The encrypted dump and every required key/trust artifact decrypt, authenticate
  and match the backup manifest. Missing keys are a failed restore gate.
- `pg_restore` exited 0 and the Goose applied version equals the backup's
  recorded version. Confirm the intended application binary's embedded migration
  set is compatible. Do not start the server to discover a mismatch: it migrates
  automatically at boot. Schema mismatch keeps writers stopped.
- Expected tenant/project/cluster and audit data is present, and encrypted
  application values can be read with the recovered keys using the supported
  recovery tooling. Counts alone do not prove decryptability or completeness.

Restore server/worker/frontend replica counts from the captured
`controllers.json`, not production defaults. For each captured Deployment:

```bash
jq -r '.items[] | select(.kind == "Deployment") |
  select(.metadata.name == "astronomer-server" or
         .metadata.name == "astronomer-worker" or
         .metadata.name == "astronomer-frontend") |
  [.metadata.name, (.spec.replicas | tostring)] | @tsv' \
  "$restore_state/controllers.json" |
while IFS="$(printf '\t')" read -r deployment replicas; do
  kubectl -n astronomer scale "deployment/$deployment" --replicas="$replicas" || exit 1
done
kubectl -n astronomer rollout status deploy/astronomer-server --timeout=300s
kubectl -n astronomer rollout status deploy/astronomer-worker --timeout=300s
```

After validation, restore each CronJob's captured suspend state and the original
HPA/GitOps configuration. A previously suspended CronJob stays suspended. On any
failed gate, leave writers and backups stopped, retain private diagnostics and
choose a known-good backup; never auto-resume from an exit trap.

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
/ "decryption failed" / "crypto: unknown key id". Agents in managed clusters
flap between `connecting` and `error`.

Almost always, the root cause is **encryption-key mismatch**: the age key
in the running pod is not the key that wrapped the encrypted columns in the
dump.

### Confirm the symptom

```bash
# What the running server thinks the key is.
kubectl -n astronomer get secret <release>-secrets \
  -o jsonpath='{.data.ASTRONOMER_ENCRYPTION_KEY}' | base64 -d
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

If you still have the original key in your secret manager, patch **only** the
affected key(s) so the other variables the chart renders into `<release>-secrets`
(`SECRET_KEY`, `GITHUB_*`, `GOOGLE_*`, `OIDC_*`, and — for the bundled Postgres —
`POSTGRES_PASSWORD`) are preserved. `envFrom` loads every key in the Secret as an
env var, so a full recreate that omits them would silently blank those consumers:

```bash
kubectl -n astronomer patch secret <release>-secrets --type merge \
  -p '{"stringData":{"ASTRONOMER_ENCRYPTION_KEY":"<original-age-keyring>"}}'

# If the JWT signing key also drifted, patch it in the same call:
kubectl -n astronomer patch secret <release>-secrets --type merge \
  -p '{"stringData":{"SECRET_KEY":"<original-jwt-key>"}}'

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
Each target cluster's Kubernetes/etcd is authoritative for live workload and
Flux reconciliation objects. The agent's accepted checkpoint is prune evidence;
it is not a substitute for PostgreSQL delivery intent or rollout history.

When snapshots are from different points in time:

| Scenario | Recovery rule |
|----------|---------------|
| Postgres is older than Kubernetes | Keep Postgres as the source for product state. Reconcile CRD-owned `Cluster` and `Project` objects by `external_ref_*`; import only rows that carry ownership proof or after an explicit operator import. |
| A target cluster is older than Postgres | Keep PostgreSQL intent. Let the authenticated agent fetch the latest generation and deterministically recreate only Astronomer-owned Flux objects. Do not hand-apply an assignment or advance a checkpoint. |
| Agent-owned Flux object exists but its deployment row is missing | Do not silently adopt it. Quarantine/orphan it through the delivery API after verifying ownership and restore chronology; never infer product intent from downstream state. |
| CRD exists but same-name DB row is UI/API-owned | Do not take ownership automatically. Resolve through an explicit ownership transfer/import procedure. |
| Redis jobs are missing after restore | Treat Redis as ephemeral. Run recovery sweeps and retry idempotent operations from Postgres state instead of trying to restore queue contents. |

After any mismatch recovery, run the drift/repair checks once they are
available, then trigger an ad-hoc management backup so the new baseline is
captured.

---

## CloudNativePG continuous backup (Barman Cloud plugin)

In `postgres.mode: cloudNativePG` the chart wires WAL archiving and base
backups through the upstream CNPG-I Barman Cloud plugin
(`barman-cloud.cloudnative-pg.io`). This is the PITR layer; the
`managementBackup` dumps above remain the encrypted, Object-Locked cold copy.

For a single-node install with an off-host, non-AWS S3 store (RustFS/MinIO,
Object Lock GOVERNANCE 30 days, retention in dry-run) start from
`deploy/chart/values-single-node-backup.yaml`, layered after
`values-single-node.yaml`.

### Chart-managed ObjectStore

Install the plugin (pinned `tag@digest`, see [verify-images](verify-images.md))
and create the S3 credentials Secret yourself; then let the chart render the
`barmancloud.cnpg.io/v1` ObjectStore:

```yaml
postgres:
  cloudNativePG:
    barmanCloud:
      enabled: true
      objectStoreName: offhost-archive
      objectStore:
        create: true
        destinationPath: s3://astronomer-recovery-offhost/
        endpointURL: http://rustfs.backup.example:9000   # empty = AWS S3
        region: us-east-1
        s3Credentials:
          accessKeyId: {name: offhost-rustfs-credentials, key: ACCESS_KEY_ID}
          secretAccessKey: {name: offhost-rustfs-credentials, key: SECRET_ACCESS_KEY}
        wal: {compression: gzip, maxParallel: 2}
        data: {compression: gzip, jobs: 2}
        retentionPolicy: 14d
      scheduledBackup:
        enabled: true
        schedule: "0 0 3 * * *"
        immediate: true        # first base backup right after install
networkPolicy:
  walArchiveEgressCIDRs: [10.60.0.10/32]   # the endpointURL host
  walArchiveEgressPorts: [9000]
```

The ObjectStore carries `helm.sh/resource-policy: keep`: uninstalling the
release never stops archiving or deletes the bucket contents. To adopt an
ObjectStore that was created by hand, label and annotate it before the
upgrade:

```bash
kubectl -n astronomer label objectstore <name> app.kubernetes.io/managed-by=Helm
kubectl -n astronomer annotate objectstore <name> \
  meta.helm.sh/release-name=astronomer meta.helm.sh/release-namespace=astronomer
```

### NetworkPolicy for CNPG

The `astronomer-postgres` policy selects the CNPG instance pods
(`cnpg.io/cluster=<cluster>`) and opens, besides DNS and replication:

| Flow | Values |
|------|--------|
| Instance manager -> kube-apiserver TCP 443/6443 | `networkPolicy.kubernetesAPIEgressCIDRs` (and the legacy `externalEgressCIDRs`). List the `kubernetes` Service ClusterIP **and** the API endpoint/node addresses; without it a restarted instance never passes its startup probe. |
| Instance manager -> Barman Cloud plugin | `networkPolicy.cnpgSystemNamespace` (default `cnpg-system`), `networkPolicy.barmanCloudPluginPort` (default 9090); rendered only with `barmanCloud.enabled`. |
| Plugin sidecar -> object store (WAL + base backups) | `networkPolicy.walArchiveEgressCIDRs` / `walArchiveEgressPorts` (default `[443]`). |
| DR jobs (`management-backup`, `restore-drill`) -> Postgres 5432 | automatic; `management-backup-retention` only talks to S3 and is not admitted. |
| DR jobs -> S3 | `networkPolicy.objectStoreEgressCIDRs` / `objectStoreEgressPorts` (default `[443, 80]`; add `9000` for RustFS/MinIO). |

### Adding Barman Cloud to a live cluster (primary restart hazard)

Enabling `barmanCloud.enabled` on a running Cluster makes the operator restart
every instance to inject the plugin sidecar, **including the primary**. During
that shutdown PostgreSQL runs a smart shutdown and its `archive_command` (now
routed to the plugin) cannot reach a sidecar that does not exist yet, so the
primary waits for the full `smartShutdownTimeout` (CNPG default 180 s) and then
the `stopDelay` (default 1800 s) before it is killed. On astronomer-dev the
resulting primary restart collided with a concurrent `--atomic` Helm upgrade,
which then rolled back. Pick one:

1. **Add the plugin before data is live** (preferred): enable `barmanCloud` in
   the very first install, or on a fresh Cluster before the server starts.
2. **Shorten the shutdown for the migration**: run the upgrade that adds the
   plugin with
   ```yaml
   postgres:
     cloudNativePG:
       stopDelay: 60
       smartShutdownTimeout: 15
   ```
   in a maintenance window (the server/worker lose the database for about a
   minute), without `--atomic` and with `--timeout 15m`; confirm
   `kubectl cnpg status <cluster>` shows `Continuous Backup status: OK` and a
   recent `Last Archived WAL`, then drop the two values again (`0` = CNPG
   defaults) in the next upgrade.

After the switch, take a backup immediately
(`kubectl cnpg backup <cluster> --method plugin --plugin-name barman-cloud.cloudnative-pg.io`)
or set `scheduledBackup.immediate: true`.

### Client tools for the DR jobs

`pg_dump` refuses to dump a server with a newer major version. The
`astronomer-dr` image therefore ships the PostgreSQL **17** client tools to
match the chart's CNPG `postgresql:17.11` image, and the restore drill's
sidecar defaults to `postgres:17-alpine` (tag@digest). Keep all three in
lockstep when the CNPG image moves to a new major.

---

## Limits

| Metric | Value |
|--------|-------|
| RPO    | up to 24h (gap to the last successful nightly dump). Take an ad-hoc dump before any risky change (`kubectl create job --from=cronjob/...`). |
| RTO    | ~15 min for a single-region, single-DB restore once the operator is at the keyboard. Roughly linear in DB size: ~30 GB dumps restore in ~10 min on managed Postgres with `--single-transaction --exit-on-error`. |
| Data not covered | Redis (asynq queue state), in-flight Jobs at the moment of disaster, Prometheus metrics history (lives in your monitoring stack, not in Astronomer's DB). |
| Cross-region failover | Out of scope. The bucket is single-region by default. For multi-region resilience, set `managementBackup.s3.region` to a region that replicates to another region via the cloud provider's bucket replication. |

If your business requires a tighter RPO than 24h, move to managed-Postgres
PITR (RDS, Aurora, CloudSQL, Hetzner Cloud Managed Postgres) and treat this
CronJob as the cold backup of last resort. Hourly dumps from this CronJob are
possible (`schedule: "0 * * * *"`) but they are not a substitute for proper
WAL-based PITR.

---

## No emergency unauthenticated shortcut

There is deliberately no direct `aws s3 cp ...pgcustom | pg_restore` shortcut.
Select and authenticate the manifest, verify both named ciphertext payloads,
AEAD-decrypt into private scratch space, and inspect `pg_restore -l` before the
freeze/drop/recreate steps above. Skipping that order defeats the tamper and
wrong-source protections the backup format exists to provide.
