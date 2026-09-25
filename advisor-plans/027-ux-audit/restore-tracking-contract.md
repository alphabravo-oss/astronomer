# Cluster snapshot restore tracking — contract checkpoint

Original review: 2026-09-24 at Astronomer `22f633ec` plus its working tree. The original findings below are retained as baseline evidence. The user subsequently authorized the API extension as part of Plan 027; no live restore has been performed.

## Implemented contract

Integrated runtime commit `ebb22524` adds these canonical management API routes:

| Route | Contract |
|---|---|
| `GET /api/v1/clusters/{cluster_id}/snapshot-restores/` | Paged history for the target cluster. Target `clusters:read` is required; source-cluster visibility filters rows before counts and pagination. Stable order is creation time descending, then ID descending. |
| `GET /api/v1/clusters/{cluster_id}/snapshot-restores/{id}/` | Exact durable `cluster_restores` identity, wrapped in `data`. Target read is required; an unrelated target or inaccessible source returns 404. |
| `POST /api/v1/clusters/{cluster_id}/snapshots/{id}/restore` | Existing creation flow now checks target `clusters:update` before preflight and returns the target-cluster restore detail as Accepted Location. Source and target backup-storage locations must match. |

`SnapshotRestoreResponse` includes source snapshot, source cluster, target cluster, phase, error/warning counts, start/completion timestamps, and the existing poller's `last_poll_at` / `last_poll_error`. A stale observation is not proof of completion. Queued, running, completed, partial failure and failed phases remain distinct. The original restore worker and durable rows remain authoritative.

The UI addresses history under the target cluster's Snapshots page and selects the receipt with `?restore=<id>`. History remains available even when Velero readiness cannot be established. Source snapshot completion is never substituted for restore completion.

Handler authorization/readback tests pass, and the isolated PostgreSQL gate has verified source-filtered counts and stable pagination. Final integrated browser and enterprise evidence is tracked in [the execution ledger](../027-execution-review.md). Real restores remain part of Plan 028 qualification.

## Original dependency findings (superseded by the extension above)

- Cluster snapshot restore creation endpoint: `POST /api/v1/clusters/{cluster_id}/snapshots/{id}/restore`, `docs/openapi.yaml:25287`.
- Its returned domain type is `SnapshotRestoreResponse`, `docs/openapi.yaml:5898`; the client maps it to `SnapshotRestore` in `frontend/src/lib/api/cluster-velero.ts:242`.
- The handler stores `cluster_restores`, captures source snapshot and target cluster, and enqueues the restore worker: `internal/handler/cluster_snapshots_restore.go:134`.
- `ClusterSnapshotQuerier` has `ListClusterRestores` and `GetClusterRestoreByID` interfaces (`internal/handler/cluster_snapshots.go:66`). Those interface methods alone do not establish a mounted public retrieval route.
- A different `GET /api/v1/backups/restores/{id}` exists, returning `RestoreOperationResponse` (`docs/openapi.yaml:12968`, schema at 6121). General Velero backup restore list/detail routes are mounted through `Backups.ListRestores/GetRestore` in `internal/server/routes_tools_controlplane.go:97`.
- The cluster snapshot UI discards its receipt and closes after “Restore queued” (`frontend/src/components/clusters/snapshot-dialogs.tsx:348`). The legacy frontend backup restore detail route redirects to management backup settings.

No matching cluster-snapshot restore read/list route was established by this review. Treat durable status/history retrieval as an unresolved contract dependency. Do not poll the general Velero backup endpoint with a cluster snapshot restore ID merely because both domains use UUIDs and the word “restore.”

Implementation must first either demonstrate a mounted, authorized read path for this exact receipt ID/domain, or specify a bounded backend extension with source/target authorization, state freshness, terminal/partial outcomes, paged history, generated OpenAPI/client updates and tests. Receipt retention and an honest explanation of tracking availability can proceed independently. A source backup's Completed state is not proof that a restore completed.

Terminology correction during Plan 028 cold review: `/backups/restores/` belongs to the general cluster-resource Velero BackupHandler. Astronomer management-plane pg_dump/destinations use `/api/v1/admin/management-backup/` and restore-drill reporting uses `/api/v1/admin/backup-drill/`. A frontend redirect to management settings does not establish backend ownership. This correction does not resolve the missing matching cluster-snapshot restore read contract.
