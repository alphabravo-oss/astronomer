import { SnapshotRestoreHistory } from "./snapshot-restore-tracking";
import { QueryStates } from "@/components/ui/query-states";
/**
 * Cluster Snapshots tab.
 *
 * Default surface: Velero-backed workload snapshots and scheduled snapshots.
 * Behind the `feature.controlPlaneSnapshots` flag this route instead renders
 * the control-plane (etcd) snapshots surface (be-etcd counterpart). The flag
 * defaults off, so existing behavior is unchanged until an operator enables it
 * (see the control-plane section at the bottom of this file + the PR wiring
 * notes about giving control-plane snapshots their own route/sidebar entry).
 *
 * Read paths poll while the tab is foregrounded-sm (refetchInterval, off in
 * background). Write paths fan through TanStack mutations that invalidate the
 * relevant query keys.
 *
 * RBAC: `clusters:update` gates all create/update/delete buttons through the
 * shared permission decision helper so disabled tooltips name the missing
 * permission and where to request access.
 */

import { useState } from "react";
import { useParams } from "@tanstack/react-router";
import {
  AlertTriangle,
  Loader2,
  Plus,
  Server,
  ShieldAlert,
} from "lucide-react";

import { useCluster } from "@/lib/hooks/clusters";
import { useClustersUpdate } from "@/lib/permission-hooks";
import { EmptyState } from "@/components/ui/empty-state";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader, PageShell } from "@/components/ui/page";
import {
  NewSnapshotDialog,
  RestoreSnapshotDialog,
  ScheduleDialog,
} from "./snapshot-dialogs";
import { SnapshotSchedulesTable, SnapshotsTable } from "./snapshot-tables";
import {
  type Snapshot,
  type SnapshotSchedule,
  useVeleroSnapshotPage,
} from "./snapshot-page-hooks";

// This route renders the Velero workload-snapshots tab. Control-plane (etcd)
// snapshots live in their own control-plane snapshot module and route. They
// are distinct capabilities (application/PV backups versus control-plane DR),
// so they do not share a tab or controller.
export function ClusterSnapshotsPage() {
  const { id } = useParams({ from: "/dashboard/clusters/$id" });
  return (
    <div className="space-y-6">
      <ClusterVeleroSnapshotsPage />
      <SnapshotRestoreHistory clusterId={id} />
    </div>
  );
}

// ─── Velero snapshots page (unchanged existing behavior) ─────────────────────
function ClusterVeleroSnapshotsPage() {
  const params = useParams({ from: "/dashboard/clusters/$id" });
  const clusterId = params.id;
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);

  // Modals/dialogs state
  const [newSnapshotOpen, setNewSnapshotOpen] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState<Snapshot | null>(null);
  const [deleteOpen, setDeleteOpen] = useState<Snapshot | null>(null);
  const [scheduleOpen, setScheduleOpen] = useState<
    { mode: "create" } | { mode: "edit"; schedule: SnapshotSchedule } | null
  >(null);
  const [scheduleDeleteOpen, setScheduleDeleteOpen] =
    useState<SnapshotSchedule | null>(null);

  const snapshotPage = useVeleroSnapshotPage(clusterId, {
    closeSnapshotDelete: () => setDeleteOpen(null),
    closeScheduleDelete: () => setScheduleDeleteOpen(null),
  });
  const { veleroQuery, snapshotsQuery, schedulesQuery } = snapshotPage;
  const veleroStatus = veleroQuery.data;
  const veleroReady = snapshotPage.veleroReady;
  const defaultStorageLocation = snapshotPage.defaultStorageLocation;

  // ─── Loading / not-found ────────────────────────────────────────────────
  if (clusterLoading || veleroQuery.isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  if (veleroQuery.isError)
    return (
      <QueryStates
        query={veleroQuery}
        permission="clusters:read"
        errorTitle="Velero status unavailable"
      >
        <></>
      </QueryStates>
    );

  if (!veleroReady) {
    return (
      <PageShell>
        <PageHeader
          title="Snapshots"
          description={`Velero-backed workload snapshots for ${cluster.displayName}.`}
        />
        <EmptyState
          icon={ShieldAlert}
          title="Velero is not installed"
          description={
            veleroStatus?.reason
              ? `Workload snapshots show up here after Velero is installed on this cluster. ${veleroStatus.reason}`
              : "Workload snapshots show up here after Velero is installed on this cluster. Install it from Cluster Tools, then return to take and schedule snapshots."
          }
          actionLabel="Install Velero"
          actionHref={`/dashboard/clusters/${clusterId}/apps?section=browse&install=velero`}
          actionIcon={Plus}
        />
      </PageShell>
    );
  }

  // ─── Banners ────────────────────────────────────────────────────────────
  const showBslBanner = veleroReady && !veleroStatus?.storageReady;

  return (
    <PageShell>
      <PageHeader
        title="Snapshots"
        description={`Velero-backed snapshots and scheduled snapshots for ${cluster.displayName}.`}
      />

      {showBslBanner && (
        <StorageLocationWarning reason={veleroStatus?.reason} />
      )}

      {/* Schedules section */}
      {veleroReady && (
        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium text-foreground">
              Snapshot schedules
            </h2>
            <button
              onClick={() => canWrite && setScheduleOpen({ mode: "create" })}
              disabled={!canWrite}
              title={canWrite ? undefined : reason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-sm text-xs font-medium
                border border-border text-foreground hover:bg-accent transition-colors
                disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Plus className="h-3.5 w-3.5" />
              New Schedule
            </button>
          </div>
          <SnapshotSchedulesTable
            loading={schedulesQuery.isLoading}
            schedules={schedulesQuery.data ?? []}
            isError={schedulesQuery.isError}
            error={schedulesQuery.error}
            onRetry={() => void schedulesQuery.refetch()}
            canWrite={canWrite}
            disabledReason={reason}
            onToggle={(s) =>
              snapshotPage.toggleScheduleMutation.mutate({
                id: s.id,
                enabled: !s.enabled,
              })
            }
            onEdit={(s) => setScheduleOpen({ mode: "edit", schedule: s })}
            onDelete={(s) => setScheduleDeleteOpen(s)}
          />
        </section>
      )}

      {/* Recent snapshots section */}
      {veleroReady && (
        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium text-foreground">
              Recent snapshots
            </h2>
            <button
              onClick={() => canWrite && setNewSnapshotOpen(true)}
              disabled={!canWrite}
              title={canWrite ? undefined : reason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-sm text-xs font-medium
                bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
                disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Plus className="h-3.5 w-3.5" />
              New Snapshot
            </button>
          </div>
          <SnapshotsTable
            loading={snapshotsQuery.isLoading}
            snapshots={snapshotsQuery.data ?? []}
            isError={snapshotsQuery.isError}
            error={snapshotsQuery.error}
            onRetry={() => void snapshotsQuery.refetch()}
            canWrite={canWrite}
            disabledReason={reason}
            onRestore={(s) => setRestoreOpen(s)}
            onDelete={(s) => setDeleteOpen(s)}
          />
        </section>
      )}

      {/* New snapshot dialog */}
      {newSnapshotOpen && (
        <NewSnapshotDialog
          clusterId={clusterId}
          onClose={() => setNewSnapshotOpen(false)}
          defaultStorageLocation={defaultStorageLocation}
        />
      )}

      {/* Restore dialog */}
      {restoreOpen && (
        <RestoreSnapshotDialog
          clusterId={clusterId}
          snapshot={restoreOpen}
          onClose={() => setRestoreOpen(null)}
        />
      )}

      {/* Schedule create/edit */}
      {scheduleOpen && (
        <ScheduleDialog
          clusterId={clusterId}
          mode={scheduleOpen.mode}
          schedule={
            scheduleOpen.mode === "edit" ? scheduleOpen.schedule : undefined
          }
          onClose={() => setScheduleOpen(null)}
        />
      )}

      {/* Delete-snapshot confirm */}
      <ConfirmDialog
        open={!!deleteOpen}
        onClose={() => setDeleteOpen(null)}
        onConfirm={() =>
          deleteOpen &&
          snapshotPage.deleteSnapshotMutation.mutate(deleteOpen.id)
        }
        title="Delete snapshot"
        description={
          deleteOpen
            ? `This initiates a Velero DeleteBackup request for "${deleteOpen.name}". The backup is removed from object storage as well.`
            : ""
        }
        confirmText="Delete"
        variant="destructive"
        loading={snapshotPage.deleteSnapshotMutation.isPending}
      />

      {/* Delete-schedule confirm */}
      <ConfirmDialog
        open={!!scheduleDeleteOpen}
        onClose={() => setScheduleDeleteOpen(null)}
        onConfirm={() =>
          scheduleDeleteOpen &&
          snapshotPage.deleteScheduleMutation.mutate(scheduleDeleteOpen.id)
        }
        title="Delete schedule"
        description={
          scheduleDeleteOpen
            ? `Delete the snapshot schedule "${scheduleDeleteOpen.name}"? Existing snapshots produced by this schedule are kept.`
            : ""
        }
        confirmText="Delete"
        variant="destructive"
        loading={snapshotPage.deleteScheduleMutation.isPending}
      />
    </PageShell>
  );
}

function StorageLocationWarning({ reason }: { reason?: string | null }) {
  return (
    <div className="rounded-lg border border-status-warning/30 bg-status-warning/10 p-4 flex items-start gap-3">
      <AlertTriangle className="h-5 w-5 text-status-warning shrink-0 mt-0.5" />
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium">Backup storage location not ready</p>
        <p className="text-xs text-muted-foreground mt-1">
          {reason ||
            "Velero is installed but the backup storage location is not yet Available. Snapshots will fail until it reconciles."}
        </p>
      </div>
    </div>
  );
}
