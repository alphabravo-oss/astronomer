# Plan 030: RustFS development recovery worksheet

Status: prepared from source; not applied or exercised. The user selected existing RustFS for development. Same-host storage is accepted for this exercise; host-loss recovery remains deferred production qualification.

## Existing service and isolation

The earlier Docker snapshot identified `charlie-dev-rustfs`; Charlie's Compose file declares RustFS `1.0.0-beta.12` at internal endpoint `http://rustfs:9000`. Current Docker and k3s socket access is denied in this session. Neither current availability nor compatibility with this backup implementation has been established.

Use a new bucket, proposed name `astronomer-dev-management-backups`, and dedicated bucket-scoped credentials. Do not alter Charlie's existing bucket, lifecycle policies, users or data. Do not assume a Compose hostname resolves from Kubernetes. Identify the existing Docker network and a route reachable from backup Pods before choosing the endpoint; avoid publishing an unauthenticated public endpoint. If the existing instance cannot provide the required bucket behavior, report the exact failed operation before choosing a separate dev instance or upgrade.

## Inputs to resolve with live access

| Input | Proposed value or check |
| --- | --- |
| Namespace / Helm release | Read actual deployed ownership; do not assume names from defaults |
| S3 endpoint | Determine reachable endpoint and verify from the backup Pod network |
| Bucket | New `astronomer-dev-management-backups`, verify unused before creating |
| Region | `us-east-1`, verify actual signing behavior |
| Prefix | `astronomer-plan030-dev` |
| Writer credentials Secret | Proposed `astronomer-dev-backup-writer`, key `credentials`, AWS shared-credentials-file format |
| Retention credentials Secret | Separate principal/Secret if retention is enabled; leave deletion disabled for first drill |
| Wrapping key Secret | Proposed `astronomer-dev-backup-wrap`, key `passphrase`; generated securely, never included in evidence |
| Source identity | Record stable identity of the actual development management database |
| Key bundle source | Existing runtime Secret by default; retain matching JWT/Fernet keys |
| Object Lock | Confirm enabled plus default retention at least `managementBackup.immutability.minimumRetentionDays` (default 30) |
| Restore target | Existing ephemeral PostgreSQL sidecar drill; match source major version |

Names above are proposals, not existing Kubernetes resources or proof of provisioning. Do not output Secret values, connection strings, raw backup contents or credentials into logs/evidence.

## Configuration shape

Merge this shape with the actual deployment's owner-managed values after replacing placeholders. It is deliberately not an apply-ready configuration.

```yaml
managementBackup:
  enabled: true
  s3:
    endpoint: <endpoint-verified-from-k3s-pods>
    bucket: astronomer-dev-management-backups
    region: us-east-1
    prefix: astronomer-plan030-dev
    credentialsSecretRef:
      name: astronomer-dev-backup-writer
      key: credentials
  immutability:
    mode: objectLock
    minimumRetentionDays: 30
  encryption:
    sourceIdentity: <actual-stable-dev-source-identity>
    wrappingSecretRef:
      name: astronomer-dev-backup-wrap
      key: passphrase
  encryptionKeyBackup:
    enabled: true
  retention:
    enabled: false
    dryRun: true
managementRestoreDrill:
  enabled: true
```

Retention deletion is disabled only for the initial isolated exercise; bucket Object Lock and encrypted key capture remain active. Add a separate retention principal and review dry-run candidates before enabling the existing retention workflow. Account for immutable objects remaining for the configured retention duration when recording eventual cleanup.

## Execution and acceptance checklist

- [ ] Recheck service/image/network identity and the Astronomer release; retain sanitized names and digests.
- [ ] Verify new-bucket S3 signing, versioning, Object Lock configuration and default retention using the actual RustFS version. Test allowed upload/read and refused protected deletion with a disposable object confined to this bucket. Record its version ID and test deletion of that protected version with the restricted writer principal; deleting only the key can create a delete marker and does not establish whether retention is enforced. Verify the protected version remains readable by version ID.
- [ ] Establish dedicated Secret references without recording values; confirm both backup and restore can authenticate to the intended bucket only.
- [ ] Render the actual release with the overlay; inspect the diff and confirm backup and drill CronJobs render, key mounts are correct, and restore targets the ephemeral sidecar.
- [ ] Follow the existing management-backup API/chart ownership flow rather than creating a second backup implementation.
- [ ] Run a backup. Verify clean schema version, encrypted dump, encrypted key bundle, authenticated manifest uploaded last, and successful recorded operation.
- [ ] Run the existing isolated restore drill. Verify successful schema/row checks and decryption of a known encrypted fixture; check the drill result API. Never restore into the source database.
- [ ] Complete isolated application-level authentication and representative read checks required by E10; the PostgreSQL sidecar check alone does not prove those journeys.
- [ ] Record backup age, elapsed restore duration, image identities, object IDs/checksums and sanitized success/failure evidence. Proposed dev targets: RPO <=24 hours, RTO <=2 hours; report actual values.
- [ ] Confirm restore had no member-cluster or external notification side effects. Record permitted source audit/result-row writes separately from restored data.
- [ ] Clean up only exercise-owned resources, honoring Object Lock; retain the recovery checkpoint needed for the candidate rollout.

## Source references inspected

Paths relative to the implementation worktree `astronomer-efficiency-030`:

- `deploy/chart/values.yaml`: `managementBackup` and `managementRestoreDrill` configuration.
- `deploy/chart/templates/management-plane-backup-cronjob.yaml`: custom endpoint, pre-dump Object Lock check, encryption and manifest commit marker.
- `deploy/chart/templates/management-plane-restore-drill-cronjob.yaml`: ephemeral sidecar restore and recorded result.
- `docs/runbooks/management-backup-and-restore.md`: supported API run/test endpoints and recovery acceptance evidence.

No live backup, bucket creation or restore has occurred during this preparation.

## RustFS compatibility reference

The official [S3 compatibility matrix](https://docs.rustfs.com/en/reference/s3-compatibility), checked 2026-10-06, reports selected versioning and Object Lock tests against commit `1e6f5f1e` (2026-08-09). This supports trying the existing development instance but does not certify the locally configured `1.0.0-beta.12` image. The official [Object Lock documentation](https://github.com/rustfs/docs.rustfs.com/blob/main/content/en/administration/data/object/object-lock.md) distinguishes protected object versions from delete markers and documents governance bypass. Use a principal without bypass permission for the retention test; keep image-specific compatibility unverified until the actual exercise passes.
