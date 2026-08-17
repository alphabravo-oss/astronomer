# Astronomer v1 upgrade and rollback

This runbook upgrades an existing, healthy Astronomer v1 Helm release to one
exact tagged v1 release. The supported path is ordinary Helm ownership of the
dependency-free management-plane chart. Flux controllers remain agent-managed
inside managed clusters and are unaffected by a management-plane chart upgrade.

## Boundaries

- A pre-v1 installation is not upgradeable to v1. Install v1 with a new Helm
  release and an empty database, validate it, and retain the old installation
  and backup for rollback/audit.
- Upgrade only between stable v1 tags. Use `helm rollback` for a rollback; do
  not pass an older tag to the upgrade helper.
- Do not use mutable image tags, skip hooks, disable signature checks, or bypass
  the database preflight.
- An application rollback does not automatically reverse a database change.
  Review release notes and restore the matching database snapshot when a
  release declares that rollback requires it.

## Fast path

```bash
# Back up and render the exact signed release without changing the cluster.
./scripts/upgrade-release.sh v1.0.1

# Review the reported private backup directory, then perform the upgrade.
./scripts/upgrade-release.sh --yes v1.0.1

# Verify externally.
curl --fail https://astronomer.example.com/health/
curl --fail https://astronomer.example.com/readyz
```

The first command is intentionally non-mutating. It verifies release assets and
images, captures recovery material, backs up bundled PostgreSQL (or requires
external-backup confirmation), and runs a server-side Helm dry run.

## What the helper enforces

The helper fails closed unless all of these are true:

1. the target and installed charts are exact stable v1 semantic versions and
   the target is newer;
2. the Helm release is deployed and all selected deployments are observed,
   updated, and available;
3. the configured minimum number of schedulable nodes are Ready and every
   selected PodDisruptionBudget permits at least one disruption;
4. the backup filesystem has at least `MIN_BACKUP_FREE_KIB` free;
5. the release chart checksum and GitHub provenance attestation verify;
6. all six first-party image references are digest-pinned and their keyless
   signatures match the tagged release workflow identity;
7. the OCI chart is byte-for-byte identical to the verified release asset;
8. bundled PostgreSQL has exactly one clean migration row at version 1, or the
   operator confirms an external v1 schema and backup; and
9. Helm's server-side dry run accepts the preserved values and v1 preflight.

The live operation uses:

```text
helm upgrade --reset-then-reuse-values --atomic --cleanup-on-fail \
  --wait --wait-for-jobs --timeout <TIMEOUT>
```

After Helm succeeds, the helper verifies the rendered image digests, waits for
the deployments it observed before the upgrade, and calls `/readyz` through a
temporary local port-forward. If post-upgrade verification fails, it performs a
bounded Helm rollback to the captured previous revision.

## Prerequisites

Install and authenticate:

- Helm with `--reset-then-reuse-values` support;
- `kubectl` with read access to the release and its Secrets, plus the normal
  Helm upgrade permissions;
- GitHub CLI authenticated to read the release and verify attestations;
- Cosign configured for outbound transparency-log and certificate checks; and
- `curl`, `jq`, `sha256sum`, `awk`, `sort`, `cmp`, `df`, and `grep`.

Confirm the release compatibility contract covers the management Kubernetes
minor, agent protocol, and downstream Flux version. Resolve any compatibility
warning before the upgrade.

The supported Gateway stack is Gateway API v1.5.1 paired with NGINX Gateway
Fabric 2.6.0. The chart preflight verifies CRD and GatewayClass existence; it does not
     validate controller-owned status conditions. Confirm the GatewayClass is
Accepted and supports the installed bundle before upgrading.

## Capacity and disk

The helper requires at least one schedulable Ready node by default. Set a
higher enterprise floor when the release spans failure domains:

```bash
MIN_READY_NODES=3 ./scripts/upgrade-release.sh v1.0.1
```

It also refuses to begin when an Astronomer deployment is not fully available
or a selected PodDisruptionBudget reports zero allowed disruptions. Fix the
underlying capacity or health issue; do not delete the PDB to make the gate
pass.

The backup reserve defaults to 1 GiB of free space. For bundled PostgreSQL the
helper adds the live database size to that reserve before running `pg_dump`:

```bash
MIN_BACKUP_FREE_KIB=4194304 \
BACKUP_ROOT=/var/lib/astronomer-upgrade-backups \
./scripts/upgrade-release.sh v1.0.1
```

Choose a private filesystem large enough for the chart assets, manifests,
Secrets, and a full custom-format database dump. The helper uses `umask 077`.
It never automatically deletes recovery backups. Move verified old backup
directories to your retention system after the release is accepted so the
management host cannot fill its disk.

Useful checks:

```bash
df -h /var/lib/astronomer-upgrade-backups
kubectl get nodes
kubectl -n astronomer get deploy,pdb,pods
kubectl -n astronomer top pods  # when Metrics Server is available
```

## Database safety

For bundled PostgreSQL, the helper queries `schema_migrations`, requires the
exact clean v1 state, then captures a custom-format `pg_dump`.

For managed PostgreSQL, create and verify a provider snapshot or PITR restore
point. Independently query:

```sql
SELECT count(*), max(version), bool_or(dirty)
FROM schema_migrations;
```

The expected result is one row, version `1`, dirty `false`. Then invoke:

```bash
EXTERNAL_DB_BACKUP_CONFIRMED=1 \
EXTERNAL_DB_V1_SCHEMA_CONFIRMED=1 \
./scripts/upgrade-release.sh v1.0.1
```

These confirmations do not disable validation. During the real upgrade, the
chart's read-only preflight hook connects using the configured Secret and
independently rejects dirty, unknown, pre-v1, or malformed schemas. It never
deletes, reformats, or upgrades an old database.

## Preview and approval

Run without `--yes`:

```bash
./scripts/upgrade-release.sh v1.0.1
```

Review at least:

- `target-chart.yaml` — exact chart metadata;
- `values-user.yaml` and `values-all.yaml` — retained operator values;
- `manifest-before.yaml` and `dry-run.yaml` — resource changes;
- `history.json` — rollback revision;
- `RELEASE_IMAGES` and `SHA256SUMS` — immutable release identities; and
- database backup/snapshot evidence.

Treat the directory as Secret material. Do not attach it to tickets or chat.
Confirm the dry-run contains no mutable first-party image references and no
unexpected cluster-wide RBAC or CRD changes.

Use `--dry-run-only` in automation when a successful preview should exit zero
without displaying the interactive handoff message:

```bash
./scripts/upgrade-release.sh --dry-run-only v1.0.1
```

## Execute

```bash
./scripts/upgrade-release.sh --yes v1.0.1
```

Do not run two release operations concurrently. Watch the command until it
prints the final exact tag and backup directory. Helm waits for the preflight,
migration, workloads, and jobs; its atomic mode restores the prior Helm revision
if the upgrade itself fails.

For a slow but healthy environment, increase the bound explicitly:

```bash
TIMEOUT=30m ./scripts/upgrade-release.sh --yes v1.0.1
```

## Post-upgrade verification

The helper verifies workload rollout and `/readyz`; operators must also verify
the externally routed and delivery paths:

```bash
helm -n astronomer status astronomer
helm -n astronomer history astronomer
kubectl -n astronomer get pods,jobs
kubectl -n astronomer rollout status deployment/astronomer-server --timeout=10m
kubectl -n astronomer rollout status deployment/astronomer-worker --timeout=10m
curl --fail https://astronomer.example.com/health/
curl --fail https://astronomer.example.com/readyz
```

Then:

1. confirm management API and worker error rates remain normal;
2. enroll or reconnect a canary cluster and verify its Kubernetes, agent, and
   Flux versions are admitted by the release compatibility contract;
3. publish one canary assignment and verify acknowledgement within the SLO;
4. verify downstream reconciliation status reaches the management UI;
5. confirm source resolution through the configured proxy/CA/egress policy;
6. verify backup, audit, identity, and alerting integrations; and
7. hold the release until normal latency and error budgets remain stable for
   the enterprise observation window.

## Rollback

If Helm fails during the upgrade, `--atomic` handles the release rollback. If a
post-upgrade verification fails, the helper invokes:

```bash
helm rollback astronomer <captured-previous-revision> \
  --namespace astronomer --wait --cleanup-on-fail --timeout 15m
```

If an operator initiates rollback later:

```bash
helm -n astronomer history astronomer
helm -n astronomer rollback astronomer <previous-revision> \
  --wait --cleanup-on-fail --timeout 15m
```

After rollback, repeat readiness, rollout, authentication, assignment, and
status checks. If the target release changed the database incompatibly, stop
the application first and restore the pre-upgrade database snapshot plus the
matching encryption-key custody material. Never force the migration version or
delete delivery rows as a rollback shortcut.

If automatic rollback fails, preserve the namespace and recovery directory,
stop further mutations, and escalate with Helm history, redacted event/log
output, release tag, chart digest, image digests, database snapshot ID, and the
failed step. Do not include Secret payloads.

## Acceptance record

Record:

- source and target chart versions and Helm revisions;
- chart checksum/attestation result and six image digests;
- compatibility manifest version;
- database backup/snapshot ID and restore-test date;
- capacity-gate results;
- preflight, migration, rollout, readiness, and canary delivery results;
- alert observation window; and
- the retained backup directory or archival object reference.
