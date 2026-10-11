# Compact local deployment and CNPG cutover — 2026-10-11

Implemented on `implement/032-technology-guide`; local Helm revision 241.
No PR, tag or release was published by this change.

## Database

- Installed the release-selected CloudNativePG 1.30.1 operator.
- Migrated bundled PostgreSQL 16.15 to the chart's pinned CNPG PostgreSQL 17.11
  standard-trixie image, Cluster `astronomer-cnpg`, one healthy instance.
- Rehearsed a fresh dump/restore before the maintenance window.
- Stopped API and worker writes, took a final 58,118,487-byte custom-format dump,
  restored it, compared row counts and content hashes for all 217 PostgreSQL
  tables, and ran ANALYZE. All comparisons matched; schema remained `81|false`.
- Preserved application signing/encryption keys. Retained the old 8Gi PVC
  `data-astronomer-postgres-0`; the legacy StatefulSet is no longer running.
- Added CNPG upgrade schema checks, bounded connection timeouts and matching
  network policies. Retained source PVCs are allowed after validating the target.
  Empty CNPG upgrade targets are rejected rather than silently losing data.

The private rollback material is under
`/root/.cache/astronomer-032/compact-cnpg-20261011/` (directory mode 0700):
`final.dump`, source/target fingerprints, prior Helm values, legacy controller
manifest and final site values. These include secrets and must not be committed.
Once writes resumed on CNPG, the retained old database became stale. Do not
restore it with a blind Helm rollback; first preserve/reconcile new writes.
Off-host automated backups remain a separate site configuration requirement.

## Footprint and coordination

- Added `deploy/chart/values-single-node.yaml`: one API, worker and frontend,
  one CNPG instance, one Valkey instance, worker concurrency 8, SQL pools 12/2.
- Core application/database reservations fall from 5.55 CPU / 6.125Gi memory to
  0.525 CPU / 1.125Gi. These are scheduler reservations, not actual consumption.
- Production retains three CNPG instances, synchronous replication and Sentinel;
  Sentinel processes now have their own smaller resource budgets.
- Consolidated monitoring to the two existing Flux-owned exporters. Both new
  ServiceMonitor targets are UP. Prometheus now has bounded resources; existing
  retention was preserved.
- Single-node Flux placement uses targeted replica patches on its Kustomization.
  All three controllers are 1/1 Ready; system Kustomization Ready=True. Signed
  artifact bytes, digest, image identities and verification policy are unchanged.
- Server reconciler election now runs regardless of static replica count and
  monitors the exact PostgreSQL lock session. Loss cancels and joins the guarded
  loops before retrying. Cancellation is not a fence for already accepted external
  side effects; existing durable operation fencing/idempotency remains required.

## Verification

- Server, delivery, leader and Helm contract package tests passed. Refreshed the
  previously stale route-table golden for the existing database-status endpoint.
- A real PostgreSQL test terminated its own dedicated advisory-lock connection:
  old work cancelled, competing acquisition was excluded while held, and a new
  owner acquired after loss. Ran with the race detector.
- Scaled the live API from one to two replicas while `SERVER_REPLICAS` remained
  `1`. Both pods became Ready; exactly one reconciler lock was granted. Restored
  one replica afterwards.
- Seventeen final API checks passed: login/logout, readiness, identity, clusters,
  projects, catalog, audit, delivery, nodes, metrics and project CRUD/readback.
  No smoke projects remain. Final server/worker log sample had zero error records.
- Full internal/cmd vet, updated delivery vet, complexity, dependency boundaries,
  documentation checks and `make verify-enterprise VERIFY_SCOPE=helm` passed.
- Local runtime server image: `localhost/astronomer-server:compact-cnpg-20261011-r2`.
  Migration image uses the matching local tag with unchanged schema-81 bytes.

This is local single-node acceptance and a replica-scaling/session-loss check.
It is not a multi-host HA, storage-loss, complete outage, load-capacity or remote
disaster-recovery qualification. The compact profile uses development mode;
production HA requirements remain enforced separately.
