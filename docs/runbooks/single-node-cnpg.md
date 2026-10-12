# Compact single-node and CNPG migration

Use `deploy/chart/values-single-node.yaml` for a compact development/evaluation
management plane. Install the release-selected CloudNativePG operator first.
One CNPG instance is a supported PostgreSQL topology, but has no database replica
or host failover. The profile retains durable storage and does not disable backup
settings: configure backup storage, credentials and retention for the site.

The compact baseline reserves 100m/256Mi for each API and worker, 25m/64Mi for
the frontend, 250m/512Mi for PostgreSQL and 50m/64Mi for Valkey. Those are starting
reservations, not certified capacity. Monitor latency, pool waits, queue age,
database connections, CPU throttling and memory during representative workloads.
Each process has an application pool of 12 connections and minimum two, plus a
separate bounded pool for advisory locks. The worker starts with concurrency 8.

HA uses `values-production.yaml`: three CNPG instances with synchronous
replication, Valkey Sentinel, distributed application replicas and disruption
budgets. Keep enough capacity after losing a node. Independent Kubernetes control
plane, routing, storage and backup failure domains remain operator requirements.
Growing CNPG to three instances also requires independent schedulable nodes.

## Migrating an existing bundled PostgreSQL database

Changing `postgres.mode` is not a migration. Use a maintenance window:

1. Retain private copies of current Helm values and the legacy PostgreSQL
   StatefulSet/Service manifests. Preserve application encryption/signing keys.
2. Create a separately named CNPG Cluster, such as `astronomer-cnpg`, using the
   chart's pinned PostgreSQL image. Never reuse the legacy StatefulSet name/PVC.
   Set its Helm ownership metadata before adopting it into the existing release.
3. Take a custom-format `pg_dump`. Rehearse `pg_restore --no-owner --no-acl`
   into the target application database using its application owner role.
   Check schema version, extensions and representative reads.
4. Stop all writers, including API, workers, scheduled jobs and external clients.
   Take the final dump. Restore into a fresh target application database, then
   compare **every application table's row count and content fingerprint**.
   Run `ANALYZE`. Do not cut over if any comparison fails.
5. Upgrade Helm with the compact profile and
   `postgres.cloudNativePG.clusterName=astronomer-cnpg`. Keep the image/migration
   schema contract, routing and application keys unchanged. CNPG's generated
   application Secret supplies the new database URI.
6. Verify readiness, login, encrypted credential use, queue processing, delivery
   reconciliation, a create/read/update/delete cycle and metrics. Retain the old
   PVC and the verified dump privately until the rollback window closes.

Before target writes begin, rollback can reconnect the application to the retained
source. After target writes begin, the old database is stale: do not blindly run
`helm rollback`. Stop writers and reconcile/reverse-migrate the new data first.
A Helm rollback alone does not roll back database contents or a major version.

## Monitoring and delivery controllers

When Flux already owns kube-state-metrics and node-exporter, use
`deploy/monitoring/values-single-node.yaml` with kube-prometheus-stack. It disables
the duplicate exporters, creates ServiceMonitors for the existing services and
bounds Prometheus resources. Verify the new targets are UP and node/container
queries return data. Keep retention a deliberate site setting.

The agent applies a narrow Flux replica patch on single-node clusters:
one source, kustomize and helm controller. It does not alter signed artifact bytes,
images, verification settings or trust policy. Registered node count determines
this choice; temporary loss of node readiness does not collapse an HA topology.

Server reconciler leadership is independent of configured replica count. The
dedicated PostgreSQL lock connection is probed every second, with a one-second
deadline. Loss cancels and joins guarded loops before attempting election again.
This bounds detection; cancellation cannot fence an already accepted external
side effect. Mutation paths must retain their idempotency and durable fencing.

References: [CNPG bootstrap](https://cloudnative-pg.io/docs/1.28/bootstrap/),
[database import](https://cloudnative-pg.io/docs/1.28/database_import/),
[K3s HA with embedded etcd](https://docs.k3s.io/datastore/ha-embedded).
