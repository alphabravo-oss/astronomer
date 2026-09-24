# Cluster snapshot restore tracking — contract checkpoint

Reviewed 2026-09-24 at Astronomer `22f633ec` plus the current working tree. This is a read-only contract checkpoint, not authorization to add an API or perform a restore.

- Cluster snapshot restore creation endpoint: `POST /api/v1/clusters/{cluster_id}/snapshots/{id}/restore`, `docs/openapi.yaml:25287`.
- Its returned domain type is `SnapshotRestoreResponse`, `docs/openapi.yaml:5898`; the client maps it to `SnapshotRestore` in `frontend/src/lib/api/cluster-velero.ts:242`.
- The handler stores `cluster_restores`, captures source snapshot and target cluster, and enqueues the restore worker: `internal/handler/cluster_snapshots_restore.go:134`.
- `ClusterSnapshotQuerier` has `ListClusterRestores` and `GetClusterRestoreByID` interfaces (`internal/handler/cluster_snapshots.go:66`). Those interface methods alone do not establish a mounted public retrieval route.
- A different `GET /api/v1/backups/restores/{id}` exists, returning `RestoreOperationResponse` (`docs/openapi.yaml:12968`, schema at 6121). General Velero backup restore list/detail routes are mounted through `Backups.ListRestores/GetRestore` in `internal/server/routes_tools_controlplane.go:97`.
- The cluster snapshot UI discards its receipt and closes after “Restore queued” (`frontend/src/components/clusters/snapshot-dialogs.tsx:348`). The legacy frontend backup restore detail route redirects to management backup settings.

No matching cluster-snapshot restore read/list route was established by this review. Treat durable status/history retrieval as an unresolved contract dependency. Do not poll the general Velero backup endpoint with a cluster snapshot restore ID merely because both domains use UUIDs and the word “restore.”

Implementation must first either demonstrate a mounted, authorized read path for this exact receipt ID/domain, or specify a bounded backend extension with source/target authorization, state freshness, terminal/partial outcomes, paged history, generated OpenAPI/client updates and tests. Receipt retention and an honest explanation of tracking availability can proceed independently. A source backup's Completed state is not proof that a restore completed.

Terminology correction during Plan 028 cold review: `/backups/restores/` belongs to the general cluster-resource Velero BackupHandler. Astronomer management-plane pg_dump/destinations use `/api/v1/admin/management-backup/` and restore-drill reporting uses `/api/v1/admin/backup-drill/`. A frontend redirect to management settings does not establish backend ownership. This correction does not resolve the missing matching cluster-snapshot restore read contract.
