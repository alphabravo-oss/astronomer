# Management backup and restore runbook

This runbook operates backups for Astronomer's management-plane state. Member
cluster workload backups are separate Velero-backed resources exposed through
the `/api/v1/backups` API and `astro backup` commands.

| State | Authority | Protection |
|---|---|---|
| Users, RBAC, audit, adopted-cluster identity, encrypted credentials, delivery and operation history | PostgreSQL | Provider PITR plus verified logical backup |
| JWT/Fernet keys | External secret custody | Separately wrapped key bundle and tested recovery |
| Committed task/audit intent | PostgreSQL outbox tables | Included in database backup and replayed after restore |
| Asynq queues/cache | Redis | HA/recovery policy; never treated as the only durable intent |
| Member-cluster Kubernetes objects | Member cluster/etcd | Provider/Velero policy outside management-plane restore |

## Define the recovery contract

Before enabling backups, record the business-approved RPO and RTO, backup and
key custodians, restore approver, regional failure assumptions, retention and
legal-hold requirements, and the location of a clean recovery environment.
PostgreSQL PITR should normally satisfy the shortest RPO; the logical dump and
wrapped key bundle are independent recovery and portability evidence.

The Fernet key must match the ciphertext in the selected database restore
point. Store the key-wrapping passphrase in a different custody domain from
the object-store credentials and dumps. Keep old wrapping keys until every
backup encrypted with them has expired.

## Configure and prove backup

Set `managementBackup.s3`, its credentials Secret reference, retention,
resources, and `managementBackup.encryptionKeyBackup.wrappingSecretRef` in
production Helm values. Configure the identical wrapping Secret and key prefix
under `managementRestoreDrill.decryptCheck`. Retain provider PITR settings and
the matching external PostgreSQL major-version policy separately.

After Helm applies the values, verify that both CronJobs render and that no
credential value appears in their Pod specs:

```bash
kubectl -n astronomer get cronjob \
  -l app.kubernetes.io/instance=astronomer
kubectl -n astronomer get secret astronomer-key-wrap -o name
```

As a superuser, read the API status, test the configured destination, and run
an on-demand backup. Use a short-lived token without shell tracing:

```bash
export ASTRO_API_TOKEN='<short-lived-admin-token>'
export ASTRO_SERVER='https://astronomer.example.com'
curl --fail --silent --show-error \
  -H "Authorization: Bearer ${ASTRO_API_TOKEN}" \
  "${ASTRO_SERVER}/api/v1/admin/management-backup/"
curl --fail --silent --show-error -X POST \
  -H "Authorization: Bearer ${ASTRO_API_TOKEN}" \
  "${ASTRO_SERVER}/api/v1/admin/management-backup/destinations/<id>/test/"
curl --fail --silent --show-error -X POST \
  -H "Authorization: Bearer ${ASTRO_API_TOKEN}" \
  "${ASTRO_SERVER}/api/v1/admin/management-backup/destinations/<id>/run/"
unset ASTRO_API_TOKEN
```

Confirm the Job succeeds, the database dump and wrapped key bundle exist under
their separate prefixes, checksums/size are plausible, retention tiers are
present, and metrics/audit contain a successful bounded operation without
credentials. A successful upload is not restore evidence.

## Prove restore continuously

The weekly `managementRestoreDrill` must restore the newest dump into an
ephemeral PostgreSQL sidecar, verify schema and required row counts, unwrap the
matching key bundle, decrypt a known encrypted value when one exists, and
persist its result. Check both the Job and the superuser API:

```bash
kubectl -n astronomer get jobs --sort-by=.metadata.creationTimestamp
curl --fail --silent --show-error \
  -H "Authorization: Bearer <short-lived-admin-token>" \
  https://astronomer.example.com/api/v1/admin/backup-drill/
```

Treat a stale or failed drill like a production backup failure. Follow
[backup restore drill failed](backup-restore-drill-failed.md), repair the
cause, run another drill, and retain the new evidence. At least quarterly,
perform a controlled full-environment recovery rehearsal with DNS and agent
reconnect validation rather than relying only on the sidecar drill.

## Restore decision

1. Declare the incident, freeze nonessential changes, appoint incident and
   restore commanders, and record the chosen RPO/RTO clock.
2. Determine whether a point-in-time provider restore, logical dump restore,
   or selective row recovery has the lowest data loss and blast radius.
3. Identify the corruption time and select a restore point before it. Verify
   the dump, PostgreSQL major compatibility, clean `schema_migrations` state,
   release compatibility, and matching Fernet/JWT key custody.
4. Restore into an isolated database first. Compare schema, critical row
   counts, audit continuity, delivery operations, outbox rows, and encrypted
   value decryption. Never discover an incompatible backup on the production
   cutover path.
5. Obtain explicit approval before freezing production writers or changing
   DNS/database endpoints.

## Restore and cut over

Use the exact detailed procedure in the
[management-plane DR runbook](../management-plane-dr-runbook.md). Its required
sequence is:

1. capture Helm values, release/image identities, Secret object references,
   database metadata, and current failure evidence;
2. stop server, worker, migration, backup, and restore-drill writers;
3. restore the selected database into an empty target with the matching major
   version and validate it before exposure;
4. restore the exact matching JWT/Fernet Secret from separate custody;
5. point the unchanged release at the recovered database, run supported
   preflight/migration only when the release contract admits the restored
   schema, and start one server and one worker canary;
6. validate readiness, authentication, decryption, RBAC, audit, task/audit
   outbox replay, cluster reconnect, Flux status, and one canary mutation; and
7. scale to normal replicas, reopen traffic, and observe the full RTO window.

Do not restore a stale Redis snapshot over newer PostgreSQL intent, manually
delete outbox rows, force migration metadata, rotate the Fernet key during the
incident, or let both original and recovered management planes accept writes.

## Member-cluster backup operations

Member-cluster backup state is scoped to the adopted cluster and handled by
the backup API. A typical read-only check is:

```bash
astro --server https://astronomer.example.com backup controller-status
astro --server https://astronomer.example.com backup list --output json
astro --server https://astronomer.example.com backup restores --output json
```

Before a restore, verify the cluster, namespace mapping, storage location,
backup completion, object age, restore impact, and RBAC approval. Monitor the
durable restore operation until terminal state and verify workloads, data,
events, and audit afterward. A member-cluster restore never replaces the
management-plane procedure above.

## Acceptance evidence

Retain backup and key-bundle object IDs and checksums, source/recovery database
versions, schema version, PITR coordinates, decrypt proof, critical row-count
comparison, drill/restore logs, release and image digests, approvers, achieved
RPO/RTO, agent reconnect and canary results, and follow-up actions. Store this
record without raw Secrets, dumps, tokens, or unredacted support bundles.
