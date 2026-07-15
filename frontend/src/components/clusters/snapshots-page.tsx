'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
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
 * Read paths poll while the tab is foregrounded (refetchInterval, off in
 * background). Write paths fan through TanStack mutations that invalidate the
 * relevant query keys.
 *
 * RBAC: `clusters:update` gates all create/update/delete buttons through the
 * shared permission decision helper so disabled tooltips name the missing
 * permission and where to request access.
 */

import { useMemo, useState } from 'react';
import { Link } from '@/lib/link';
import { useParams } from '@/lib/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import {
  AlertTriangle,
  Archive,
  BookOpen,
  CalendarClock,
  CheckCircle2,
  Clock,
  Cloud,
  Loader2,
  Lock,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Server,
  ShieldAlert,
  Trash2,
  XCircle,
} from 'lucide-react';

import { queryKeys, useCluster, useClusterNamespaces, useClusters } from '@/lib/hooks';
import { liveFallback } from '@/lib/live/status-store';
import { useClustersUpdate } from '@/lib/permission-hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { EmptyState } from '@/components/ui/empty-state';
import {
  createControlPlaneSnapshot,
  getControlPlaneSnapshotRestoreGuidance,
  listControlPlaneSnapshots,
  type ControlPlaneSnapshot,
  type ControlPlaneSnapshotStatus,
} from '@/lib/api/cluster-snapshots';
import {
  createSnapshot,
  createSnapshotSchedule,
  deleteSnapshot,
  deleteSnapshotSchedule,
  getVeleroStatus,
  listSnapshotSchedules,
  listSnapshots,
  restoreSnapshot,
  updateSnapshotSchedule,
  type Snapshot,
  type SnapshotPhase,
  type SnapshotSchedule,
  type SnapshotSpec,
} from '@/lib/api/cluster-detail';
import { cn } from '@/lib/utils';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { OverlayShell } from '@/components/ui/overlay-shell';

// ─── Phase pill ─────────────────────────────────────────────────────────────
function PhasePill({ phase }: { phase: SnapshotPhase }) {
  const tone = (() => {
    switch (phase) {
      case 'Completed':
        return 'bg-status-success/10 text-status-success border-status-success/20';
      case 'InProgress':
      case 'New':
        return 'bg-status-info/10 text-status-info border-status-info/20';
      case 'PartiallyFailed':
        return 'bg-amber-500/10 text-amber-500 border-amber-500/20';
      case 'Failed':
      case 'FailedValidation':
        return 'bg-status-error/10 text-status-error border-status-error/20';
      case 'Deleting':
        return 'bg-muted text-muted-foreground border-border';
      default:
        return 'bg-muted text-muted-foreground border-border';
    }
  })();
  const Icon = (() => {
    switch (phase) {
      case 'Completed':
        return CheckCircle2;
      case 'InProgress':
      case 'New':
        return Loader2;
      case 'Failed':
      case 'FailedValidation':
        return XCircle;
      case 'PartiallyFailed':
        return AlertTriangle;
      default:
        return Clock;
    }
  })();
  const spinning = phase === 'InProgress' || phase === 'New';
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs border font-medium',
        tone,
      )}
    >
      <Icon className={cn('h-3 w-3', spinning && 'animate-spin')} />
      {phase}
    </span>
  );
}

function fmt(iso?: string) {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

// This route renders the Velero workload-snapshots tab. Control-plane (etcd)
// snapshots live on their own route (../control-plane-snapshots) which
// re-exports ClusterControlPlaneSnapshotsPage below — they're distinct
// capabilities (application/PV backups vs control-plane DR), so they don't
// share a tab.
export function ClusterSnapshotsPage() {
  return <ClusterVeleroSnapshotsPage />;
}

// ─── Velero snapshots page (unchanged existing behavior) ─────────────────────
function ClusterVeleroSnapshotsPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const queryClient = useQueryClient();
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const { data: veleroStatus } = useQuery({
    queryKey: queryKeys.clusterPages.veleroStatus(clusterId),
    queryFn: () => getVeleroStatus(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
    refetchIntervalInBackground: false,
  });

  const veleroReady = !!veleroStatus?.installed;

  const { data: snapshots, isLoading: snapsLoading } = useQuery({
    queryKey: queryKeys.clusterPages.snapshots(clusterId),
    queryFn: () => listSnapshots(clusterId),
    enabled: !!clusterId && veleroReady,
    refetchInterval: liveFallback(30000),
    refetchIntervalInBackground: false,
  });
  const { data: schedules, isLoading: schedLoading } = useQuery({
    queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId),
    queryFn: () => listSnapshotSchedules(clusterId),
    enabled: !!clusterId && veleroReady,
    refetchInterval: liveFallback(60000),
    refetchIntervalInBackground: false,
  });

  // Modals/dialogs state
  const [newSnapshotOpen, setNewSnapshotOpen] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState<Snapshot | null>(null);
  const [deleteOpen, setDeleteOpen] = useState<Snapshot | null>(null);
  const [scheduleOpen, setScheduleOpen] = useState<{ mode: 'create' } | { mode: 'edit'; schedule: SnapshotSchedule } | null>(null);
  const [scheduleDeleteOpen, setScheduleDeleteOpen] = useState<SnapshotSchedule | null>(null);

  // Mutations
  const deleteSnap = useMutation({
    mutationFn: (snapshotId: string) => deleteSnapshot(clusterId, snapshotId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.snapshots(clusterId) });
      toastSuccess('Snapshot delete initiated');
      setDeleteOpen(null);
    },
    onError: (e: Error) => toastApiError('Delete failed', e),
  });
  const deleteSched = useMutation({
    mutationFn: (scheduleId: string) => deleteSnapshotSchedule(clusterId, scheduleId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId) });
      toastSuccess('Schedule deleted');
      setScheduleDeleteOpen(null);
    },
    onError: (e: Error) => toastApiError('Delete failed', e),
  });
  const toggleSched = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      updateSnapshotSchedule(clusterId, id, { enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId) });
    },
    onError: (e: Error) => toastApiError('Toggle failed', e),
  });

  // ─── Loading / not-found ────────────────────────────────────────────────
  if (clusterLoading) {
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

  // ─── Banners ────────────────────────────────────────────────────────────
  const showInstallBanner = veleroStatus && !veleroStatus.installed;
  const showBslBanner = veleroStatus?.installed && !veleroStatus.bslReady;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Snapshots</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Velero-backed snapshots and scheduled snapshots for {cluster.displayName}.
          </p>
        </div>
      </div>

      {/* Velero install banner */}
      {showInstallBanner && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-4 flex items-start gap-3">
          <ShieldAlert className="h-5 w-5 text-amber-500 flex-shrink-0 mt-0.5" />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">Velero is not installed</p>
            <p className="text-xs text-muted-foreground mt-1">
              Install Velero on this cluster to enable on-demand and scheduled snapshots.
            </p>
          </div>
          <Link
            href={`/dashboard/clusters/${clusterId}/apps?section=browse&install=velero`}
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors flex-shrink-0"
          >
            <Plus className="h-3.5 w-3.5" />
            Install Velero
          </Link>
        </div>
      )}

      {/* BSL banner */}
      {showBslBanner && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-4 flex items-start gap-3">
          <AlertTriangle className="h-5 w-5 text-amber-500 flex-shrink-0 mt-0.5" />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">Backup storage location not ready</p>
            <p className="text-xs text-muted-foreground mt-1">
              {veleroStatus?.message ||
                'Velero is installed but the backup storage location is not yet Available. Snapshots will fail until it reconciles.'}
            </p>
          </div>
        </div>
      )}

      {/* Schedules section */}
      {veleroReady && (
        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium text-foreground">Snapshot schedules</h2>
            <button
              onClick={() => canWrite && setScheduleOpen({ mode: 'create' })}
              disabled={!canWrite}
              title={canWrite ? undefined : reason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                border border-border text-foreground hover:bg-accent transition-colors
                disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Plus className="h-3.5 w-3.5" />
              New Schedule
            </button>
          </div>
          <SchedulesTable
            loading={schedLoading}
            schedules={schedules || []}
            canWrite={canWrite}
            disabledReason={reason}
            onToggle={(s) => toggleSched.mutate({ id: s.id, enabled: !s.enabled })}
            onEdit={(s) => setScheduleOpen({ mode: 'edit', schedule: s })}
            onDelete={(s) => setScheduleDeleteOpen(s)}
          />
        </section>
      )}

      {/* Recent snapshots section */}
      {veleroReady && (
        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium text-foreground">Recent snapshots</h2>
            <button
              onClick={() => canWrite && setNewSnapshotOpen(true)}
              disabled={!canWrite}
              title={canWrite ? undefined : reason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
                disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Plus className="h-3.5 w-3.5" />
              New Snapshot
            </button>
          </div>
          <SnapshotsTable
            loading={snapsLoading}
            snapshots={snapshots || []}
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
          defaultStorageLocation={veleroStatus?.storageLocation}
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
          schedule={scheduleOpen.mode === 'edit' ? scheduleOpen.schedule : undefined}
          onClose={() => setScheduleOpen(null)}
        />
      )}

      {/* Delete-snapshot confirm */}
      <ConfirmDialog
        open={!!deleteOpen}
        onClose={() => setDeleteOpen(null)}
        onConfirm={() => deleteOpen && deleteSnap.mutate(deleteOpen.id)}
        title="Delete snapshot"
        description={
          deleteOpen
            ? `This initiates a Velero DeleteBackup request for "${deleteOpen.name}". The backup is removed from object storage as well.`
            : ''
        }
        confirmText="Delete"
        variant="destructive"
        loading={deleteSnap.isPending}
      />

      {/* Delete-schedule confirm */}
      <ConfirmDialog
        open={!!scheduleDeleteOpen}
        onClose={() => setScheduleDeleteOpen(null)}
        onConfirm={() => scheduleDeleteOpen && deleteSched.mutate(scheduleDeleteOpen.id)}
        title="Delete schedule"
        description={
          scheduleDeleteOpen
            ? `Delete the snapshot schedule "${scheduleDeleteOpen.name}"? Existing snapshots produced by this schedule are kept.`
            : ''
        }
        confirmText="Delete"
        variant="destructive"
        loading={deleteSched.isPending}
      />
    </div>
  );
}

// ─── Schedules table ────────────────────────────────────────────────────────
function SchedulesTable({
  loading,
  schedules,
  canWrite,
  disabledReason,
  onToggle,
  onEdit,
  onDelete,
}: {
  loading: boolean;
  schedules: SnapshotSchedule[];
  canWrite: boolean;
  disabledReason: string;
  onToggle: (s: SnapshotSchedule) => void;
  onEdit: (s: SnapshotSchedule) => void;
  onDelete: (s: SnapshotSchedule) => void;
}) {
  if (loading) {
    return (
      <div className="rounded-lg border border-border bg-card p-8 flex items-center justify-center">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (schedules.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-card p-8 flex flex-col items-center justify-center text-muted-foreground">
        <CalendarClock className="h-8 w-8 mb-2" />
        <p className="text-sm font-medium text-foreground">No snapshot schedules</p>
        <p className="text-xs mt-1">
          Create a schedule to take cron-driven snapshots of selected namespaces.
        </p>
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/30 text-xs text-muted-foreground">
          <TableRow>
            <TableHead className="text-left font-medium px-4 py-2.5">Name</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Cron</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Namespaces</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Enabled</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Last run</TableHead>
            <TableHead className="text-right font-medium px-4 py-2.5">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className="divide-y divide-border">
          {schedules.map((s) => (
            <TableRow key={s.id} className="hover:bg-accent/30">
              <TableCell className="px-4 py-2.5 font-medium text-foreground">{s.name}</TableCell>
              <TableCell className="px-4 py-2.5 font-mono text-xs text-muted-foreground">{s.cron}</TableCell>
              <TableCell className="px-4 py-2.5">
                <div className="flex flex-wrap gap-1">
                  {(s.spec.includedNamespaces && s.spec.includedNamespaces.length > 0
                    ? s.spec.includedNamespaces
                    : ['(all)']
                  ).map((ns) => (
                    <span
                      key={ns}
                      className="inline-flex items-center px-1.5 py-0.5 rounded text-xs bg-muted text-muted-foreground border border-border"
                    >
                      {ns}
                    </span>
                  ))}
                </div>
              </TableCell>
              <TableCell className="px-4 py-2.5">
                <button
                  type="button"
                  role="switch"
                  aria-checked={s.enabled}
                  disabled={!canWrite}
                  title={canWrite ? undefined : disabledReason}
                  onClick={() => onToggle(s)}
                  className={cn(
                    'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
                    s.enabled ? 'bg-primary' : 'bg-muted',
                    !canWrite && 'opacity-50 cursor-not-allowed',
                  )}
                >
                  <span
                    className={cn(
                      'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
                      s.enabled ? 'translate-x-4' : 'translate-x-0.5',
                    )}
                  />
                </button>
              </TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">{fmt(s.lastRun)}</TableCell>
              <TableCell className="px-4 py-2.5">
                <div className="flex items-center justify-end gap-1.5">
                  <button
                    onClick={() => onEdit(s)}
                    disabled={!canWrite}
                    title={canWrite ? 'Edit' : disabledReason}
                    className="inline-flex items-center justify-center h-7 w-7 rounded text-muted-foreground
                      hover:text-foreground hover:bg-accent transition-colors
                      disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <Pencil className="h-3.5 w-3.5" />
                  </button>
                  <button
                    onClick={() => onDelete(s)}
                    disabled={!canWrite}
                    title={canWrite ? 'Delete' : disabledReason}
                    className="inline-flex items-center justify-center h-7 w-7 rounded text-muted-foreground
                      hover:text-status-error hover:bg-status-error/10 transition-colors
                      disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

// ─── Snapshots table ────────────────────────────────────────────────────────
function SnapshotsTable({
  loading,
  snapshots,
  canWrite,
  disabledReason,
  onRestore,
  onDelete,
}: {
  loading: boolean;
  snapshots: Snapshot[];
  canWrite: boolean;
  disabledReason: string;
  onRestore: (s: Snapshot) => void;
  onDelete: (s: Snapshot) => void;
}) {
  if (loading) {
    return (
      <div className="rounded-lg border border-border bg-card p-8 flex items-center justify-center">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (snapshots.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-card p-8 flex flex-col items-center justify-center text-muted-foreground">
        <Archive className="h-8 w-8 mb-2" />
        <p className="text-sm font-medium text-foreground">No snapshots yet</p>
        <p className="text-xs mt-1">
          Create one on demand, or set up a schedule to capture them automatically.
        </p>
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/30 text-xs text-muted-foreground">
          <TableRow>
            <TableHead className="text-left font-medium px-4 py-2.5">Name</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Source</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Phase</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Started</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">Completed</TableHead>
            <TableHead className="text-left font-medium px-4 py-2.5">W / E</TableHead>
            <TableHead className="text-right font-medium px-4 py-2.5">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className="divide-y divide-border">
          {snapshots.map((s) => (
            <TableRow key={s.id} className="hover:bg-accent/30">
              <TableCell className="px-4 py-2.5 font-medium text-foreground">{s.name}</TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">
                {s.source === 'schedule' ? (
                  <span title={s.scheduleName}>
                    schedule
                    {s.scheduleName ? <span className="ml-1 text-foreground">/ {s.scheduleName}</span> : null}
                  </span>
                ) : (
                  'ad-hoc'
                )}
              </TableCell>
              <TableCell className="px-4 py-2.5"><PhasePill phase={s.phase} /></TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">{fmt(s.startTimestamp)}</TableCell>
              <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">{fmt(s.completionTimestamp)}</TableCell>
              <TableCell className="px-4 py-2.5 text-xs">
                <span className="text-muted-foreground">
                  {s.warnings ?? 0} / <span className={s.errors ? 'text-status-error' : ''}>{s.errors ?? 0}</span>
                </span>
              </TableCell>
              <TableCell className="px-4 py-2.5">
                <div className="flex items-center justify-end gap-1.5">
                  <button
                    onClick={() => onRestore(s)}
                    disabled={!canWrite || (s.phase !== 'Completed' && s.phase !== 'PartiallyFailed')}
                    title={
                      !canWrite
                        ? disabledReason
                        : s.phase !== 'Completed' && s.phase !== 'PartiallyFailed'
                          ? 'Snapshot is not in a restorable state'
                          : 'Restore'
                    }
                    className="inline-flex items-center gap-1 h-7 px-2 rounded text-xs text-muted-foreground
                      hover:text-foreground hover:bg-accent transition-colors
                      disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <RotateCcw className="h-3.5 w-3.5" />
                    Restore
                  </button>
                  <button
                    onClick={() => onDelete(s)}
                    disabled={!canWrite}
                    title={canWrite ? 'Delete' : disabledReason}
                    className="inline-flex items-center justify-center h-7 w-7 rounded text-muted-foreground
                      hover:text-status-error hover:bg-status-error/10 transition-colors
                      disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

// ─── New Snapshot dialog ────────────────────────────────────────────────────
function NewSnapshotDialog({
  clusterId,
  onClose,
  defaultStorageLocation,
}: {
  clusterId: string;
  onClose: () => void;
  defaultStorageLocation?: string;
}) {
  const queryClient = useQueryClient();
  const { data: namespaces } = useClusterNamespaces(clusterId);
  const [selectedNs, setSelectedNs] = useState<string[]>([]);
  const [resources, setResources] = useState<string>('');
  const [ttl, setTtl] = useState<string>('720h');
  const [snapshotVolumes, setSnapshotVolumes] = useState<boolean>(true);

  const mutation = useMutation({
    mutationFn: () => {
      const spec: SnapshotSpec = {
        includedNamespaces: selectedNs.length ? selectedNs : undefined,
        includedResources: resources
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean) || undefined,
        snapshotVolumes,
        ttl: ttl || undefined,
        storageLocation: defaultStorageLocation,
      };
      return createSnapshot(clusterId, { spec });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.snapshots(clusterId) });
      toastSuccess('Snapshot queued');
      onClose();
    },
    onError: (e: Error) => toastApiError('Snapshot failed', e),
  });

  return (
    <Modal onClose={onClose} title="New snapshot" icon={<Archive className="h-4 w-4" />}>
      <NamespaceMultiSelect
        namespaces={namespaces?.map((n) => n.name) || []}
        selected={selectedNs}
        onChange={setSelectedNs}
      />

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Resources (comma-separated)</label>
        <input
          type="text"
          value={resources}
          onChange={(e) => setResources(e.target.value)}
          placeholder="e.g. deployments,configmaps,secrets — leave blank for all"
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm
            placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">TTL</label>
        <input
          type="text"
          value={ttl}
          onChange={(e) => setTtl(e.target.value)}
          placeholder="e.g. 720h (30 days)"
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm font-mono
            placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </div>

      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <input
          type="checkbox"
          checked={snapshotVolumes}
          onChange={(e) => setSnapshotVolumes(e.target.checked)}
          className="h-4 w-4"
        />
        Include PVC snapshots
      </label>

      <ModalFooter
        onCancel={onClose}
        onSubmit={() => mutation.mutate()}
        loading={mutation.isPending}
        submitLabel="Create snapshot"
      />
    </Modal>
  );
}

// ─── Restore dialog ────────────────────────────────────────────────────────
function RestoreSnapshotDialog({
  clusterId,
  snapshot,
  onClose,
}: {
  clusterId: string;
  snapshot: Snapshot;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const { data: clustersPage } = useClusters();
  const [targetClusterId, setTargetClusterId] = useState<string>(clusterId);
  const [includedNs, setIncludedNs] = useState<string>('');
  const [excludedNs, setExcludedNs] = useState<string>('');
  const [restorePVs, setRestorePVs] = useState<boolean>(true);

  const mutation = useMutation({
    mutationFn: () =>
      restoreSnapshot(clusterId, snapshot.id, {
        target_cluster_id: targetClusterId,
        spec: {
          includedNamespaces: parseCsv(includedNs),
          excludedNamespaces: parseCsv(excludedNs),
          restorePVs,
        },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.snapshots(clusterId) });
      toastSuccess('Restore queued');
      onClose();
    },
    onError: (e: Error) => toastApiError('Restore failed', e),
  });

  return (
    <Modal onClose={onClose} title={`Restore from ${snapshot.name}`} icon={<RotateCcw className="h-4 w-4" />}>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Target cluster</label>
        <select
          value={targetClusterId}
          onChange={(e) => setTargetClusterId(e.target.value)}
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm
            focus:outline-none focus:ring-2 focus:ring-ring"
        >
          {(clustersPage?.data || []).map((c) => (
            <option key={c.id} value={c.id}>
              {c.displayName} {c.id === clusterId ? '(this cluster)' : ''}
            </option>
          ))}
        </select>
      </div>
      <details className="rounded-lg border border-border bg-muted/20 px-3 py-2">
        <summary className="text-sm font-medium text-foreground cursor-pointer">
          Advanced
        </summary>
        <div className="pt-3 space-y-3">
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-foreground">Included namespaces (comma-separated)</label>
            <input
              type="text"
              value={includedNs}
              onChange={(e) => setIncludedNs(e.target.value)}
              placeholder="leave blank for all"
              className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-foreground">Excluded namespaces (comma-separated)</label>
            <input
              type="text"
              value={excludedNs}
              onChange={(e) => setExcludedNs(e.target.value)}
              placeholder="e.g. kube-system"
              className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm"
            />
          </div>
          <label className="flex items-center gap-2 text-xs text-foreground cursor-pointer select-none">
            <input
              type="checkbox"
              checked={restorePVs}
              onChange={(e) => setRestorePVs(e.target.checked)}
              className="h-4 w-4"
            />
            Restore PersistentVolumes
          </label>
        </div>
      </details>
      <ModalFooter
        onCancel={onClose}
        onSubmit={() => mutation.mutate()}
        loading={mutation.isPending}
        submitLabel="Restore"
      />
    </Modal>
  );
}

// ─── Schedule create/edit dialog ────────────────────────────────────────────
function ScheduleDialog({
  clusterId,
  mode,
  schedule,
  onClose,
}: {
  clusterId: string;
  mode: 'create' | 'edit';
  schedule?: SnapshotSchedule;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const { data: namespaces } = useClusterNamespaces(clusterId);
  const [name, setName] = useState(schedule?.name || '');
  const [cron, setCron] = useState(schedule?.cron || '0 3 * * *');
  const [enabled, setEnabled] = useState(schedule?.enabled ?? true);
  const [selectedNs, setSelectedNs] = useState<string[]>(schedule?.spec.includedNamespaces || []);
  const [ttl, setTtl] = useState(schedule?.spec.ttl || '720h');
  const [snapshotVolumes, setSnapshotVolumes] = useState(schedule?.spec.snapshotVolumes ?? true);

  const isEdit = mode === 'edit' && !!schedule;

  const mutation = useMutation({
    mutationFn: () => {
      const spec: SnapshotSpec = {
        includedNamespaces: selectedNs.length ? selectedNs : undefined,
        snapshotVolumes,
        ttl: ttl || undefined,
      };
      if (isEdit) {
        return updateSnapshotSchedule(clusterId, schedule.id, { name, cron, enabled, spec });
      }
      return createSnapshotSchedule(clusterId, { name, cron, enabled, spec });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.snapshotSchedules(clusterId) });
      toastSuccess(isEdit ? 'Schedule updated' : 'Schedule created');
      onClose();
    },
    onError: (e: Error) => toastApiError('Schedule failed', e),
  });

  return (
    <Modal
      onClose={onClose}
      title={isEdit ? `Edit schedule — ${schedule?.name}` : 'New snapshot schedule'}
      icon={<CalendarClock className="h-4 w-4" />}
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Name</label>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. nightly-prod"
          disabled={isEdit}
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm
            placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring
            disabled:bg-muted/50 disabled:text-muted-foreground"
        />
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Cron</label>
        <input
          type="text"
          value={cron}
          onChange={(e) => setCron(e.target.value)}
          placeholder="0 3 * * *"
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm font-mono
            placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </div>

      <NamespaceMultiSelect
        namespaces={namespaces?.map((n) => n.name) || []}
        selected={selectedNs}
        onChange={setSelectedNs}
      />

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">TTL</label>
        <input
          type="text"
          value={ttl}
          onChange={(e) => setTtl(e.target.value)}
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm font-mono"
        />
      </div>

      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <input
          type="checkbox"
          checked={snapshotVolumes}
          onChange={(e) => setSnapshotVolumes(e.target.checked)}
          className="h-4 w-4"
        />
        Include PVC snapshots
      </label>
      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <input
          type="checkbox"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
          className="h-4 w-4"
        />
        Enabled
      </label>

      <ModalFooter
        onCancel={onClose}
        onSubmit={() => mutation.mutate()}
        loading={mutation.isPending}
        submitLabel={isEdit ? 'Save' : 'Create schedule'}
        disabled={!name || !cron}
      />
    </Modal>
  );
}

// ─── Namespace multi-select (shared) ────────────────────────────────────────
function NamespaceMultiSelect({
  namespaces,
  selected,
  onChange,
}: {
  namespaces: string[];
  selected: string[];
  onChange: (ns: string[]) => void;
}) {
  const sorted = useMemo(() => [...namespaces].sort(), [namespaces]);
  const [filter, setFilter] = useState('');
  const filtered = sorted.filter((n) => n.toLowerCase().includes(filter.toLowerCase()));

  const toggle = (n: string) =>
    onChange(selected.includes(n) ? selected.filter((x) => x !== n) : [...selected, n]);

  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        Namespaces <span className="text-xs text-muted-foreground font-normal">(leave empty for all)</span>
      </label>
      <input
        type="text"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder="Filter namespaces…"
        className="w-full h-8 px-2.5 rounded-md border border-border bg-background text-xs
          placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
      />
      <div className="rounded-md border border-border bg-background max-h-40 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="text-xs text-muted-foreground px-3 py-2">No namespaces match.</div>
        ) : (
          filtered.map((ns) => (
            <label
              key={ns}
              className="flex items-center gap-2 px-3 py-1 text-xs hover:bg-accent/40 cursor-pointer"
            >
              <input
                type="checkbox"
                checked={selected.includes(ns)}
                onChange={() => toggle(ns)}
                className="h-3.5 w-3.5"
              />
              <span className="font-mono">{ns}</span>
            </label>
          ))
        )}
      </div>
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1 pt-1">
          {selected.map((ns) => (
            <span
              key={ns}
              className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs bg-muted border border-border text-muted-foreground"
            >
              {ns}
              <button onClick={() => toggle(ns)} className="hover:text-foreground" aria-label={`Remove ${ns}`}>
                <XCircle className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

// ─── Modal primitives ───────────────────────────────────────────────────────
function Modal({
  title,
  icon,
  onClose,
  children,
}: {
  title: string;
  icon?: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[90vh] flex flex-col rounded-xl border border-border bg-popover shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <div className="flex items-center gap-3 min-w-0">
            {icon && (
              <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center text-muted-foreground flex-shrink-0">
                {icon}
              </div>
            )}
            <h3 className="text-lg font-semibold text-foreground truncate">{title}</h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors flex-shrink-0"
            aria-label="Close"
          >
            <XCircle className="h-5 w-5" />
          </button>
        </div>
        <div className="p-6 space-y-4 overflow-y-auto">{children}</div>
      </div>
    </OverlayShell>
  );
}

function ModalFooter({
  onCancel,
  onSubmit,
  loading,
  submitLabel,
  disabled,
}: {
  onCancel: () => void;
  onSubmit: () => void;
  loading?: boolean;
  submitLabel: string;
  disabled?: boolean;
}) {
  return (
    <div className="flex items-center justify-end gap-2 pt-2 border-t border-border -mx-6 px-6 pb-0 mt-2">
      <div className="pt-3 flex items-center gap-2">
        <button
          onClick={onCancel}
          disabled={loading}
          className="inline-flex items-center h-9 px-3 rounded text-sm
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          Cancel
        </button>
        <button
          onClick={onSubmit}
          disabled={loading || disabled}
          className="inline-flex items-center gap-2 h-9 px-4 rounded text-sm font-medium
            bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
            disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          {submitLabel}
        </button>
      </div>
    </div>
  );
}

function parseCsv(s: string): string[] | undefined {
  const parts = s
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);
  return parts.length ? parts : undefined;
}

// ============================================================
// Control-plane (etcd) snapshots — flag-gated surface.
//
// Distinct from the Velero workload snapshots above: these capture the
// cluster's control-plane/etcd state and pair with the be-etcd handler under
// /api/v1/clusters/{id}/control-plane-snapshots/*. Restore is NOT automated —
// the "Restore guidance" action opens a read-only runbook from the API.
// ============================================================

// Managed distributions expose no operator-accessible etcd, so control-plane
// snapshots aren't available there. Everything else (k3s / rke2 / k8s /
// openshift / self-managed) is treated as snapshot-capable.
const MANAGED_DISTRIBUTIONS = new Set(['eks', 'aks', 'gke']);
function isManagedControlPlane(distribution?: string): boolean {
  return !!distribution && MANAGED_DISTRIBUTIONS.has(distribution);
}

// Local query key — control-plane snapshots are distinct from the Velero
// workload snapshots keyed under queryKeys.clusterPages.snapshots.
const cpSnapshotsKey = (clusterId: string) =>
  ['clusters', clusterId, 'control-plane-snapshots'] as const;
const cpGuidanceKey = (clusterId: string, snapshotId: string) =>
  ['clusters', clusterId, 'control-plane-snapshots', snapshotId, 'restore-guidance'] as const;

function fmtBytes(bytes?: number): string {
  if (bytes == null) return '—';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let val = bytes / 1024;
  let i = 0;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i += 1;
  }
  return `${val.toFixed(1)} ${units[i]}`;
}

function CPStatusPill({ status }: { status: ControlPlaneSnapshotStatus }) {
  const palette: Record<string, string> = {
    pending: 'bg-muted text-muted-foreground border-border',
    in_progress: 'bg-status-info/10 text-status-info border-status-info/20',
    completed: 'bg-status-success/10 text-status-success border-status-success/20',
    failed: 'bg-status-error/10 text-status-error border-status-error/20',
  };
  const cls = palette[status] ?? 'bg-muted text-muted-foreground border-border';
  return (
    <span
      className={cn(
        'inline-flex items-center px-2 py-0.5 rounded border text-xs font-medium capitalize',
        cls,
      )}
    >
      {String(status).replace(/_/g, ' ')}
    </span>
  );
}

export function ClusterControlPlaneSnapshotsPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const queryClient = useQueryClient();
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const managed = isManagedControlPlane(cluster?.distribution);

  // Only fetch for self-managed control planes, and once the cluster is known —
  // the managed/loading branches never hit the backend.
  const snapshotsQuery = useQuery({
    queryKey: cpSnapshotsKey(clusterId),
    queryFn: () => listControlPlaneSnapshots(clusterId),
    enabled: !!clusterId && !!cluster && !managed,
    refetchInterval: liveFallback(15000),
    refetchIntervalInBackground: false,
  });

  const [guidanceTarget, setGuidanceTarget] = useState<ControlPlaneSnapshot | null>(null);

  const takeSnapshot = useMutation({
    mutationFn: () => createControlPlaneSnapshot(clusterId, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: cpSnapshotsKey(clusterId) });
      toastSuccess('Snapshot requested');
    },
    onError: (e: Error) => toastApiError('Snapshot failed', e),
  });

  if (clusterLoading) {
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

  const header = (
    <div className="flex items-start justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">
          Control-plane snapshots
        </h1>
        <p className="text-sm text-muted-foreground mt-1 max-w-3xl">
          Point-in-time etcd/control-plane snapshots for {cluster.displayName}. Restore is a
          guided, out-of-band procedure — this page surfaces the runbook, it does not perform an
          automated restore.
        </p>
      </div>
      {!managed && (
        <button
          onClick={() => canWrite && takeSnapshot.mutate()}
          disabled={!canWrite || takeSnapshot.isPending}
          title={canWrite ? undefined : reason}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-lg text-sm font-medium
            bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
            disabled:opacity-50 disabled:cursor-not-allowed shrink-0"
        >
          {takeSnapshot.isPending ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Plus className="h-3.5 w-3.5" />
          )}
          Take snapshot
        </button>
      )}
    </div>
  );

  // Managed control planes (eks / aks / gke) have no operator-accessible etcd.
  if (managed) {
    return (
      <div className="space-y-6">
        {header}
        <EmptyState
          icon={Cloud}
          title="Not available for managed control planes"
          description={
            <>
              {cluster.displayName} runs a managed control plane
              {cluster.distribution ? ` (${cluster.distribution})` : ''}, so its etcd is operated
              by the cloud provider and can&apos;t be snapshotted from here. Use the provider&apos;s
              managed backup/restore for control-plane recovery; workload state can still be
              protected with Velero snapshots.
            </>
          }
        />
      </div>
    );
  }

  const columns: Column<ControlPlaneSnapshot>[] = [
    {
      key: 'name',
      header: 'Snapshot',
      accessor: (s) => (
        <div className="min-w-0">
          <div className="font-mono text-xs text-foreground break-all">{s.name || s.id}</div>
          {s.error ? <div className="text-xs text-status-error mt-1">{s.error}</div> : null}
        </div>
      ),
      sortAccessor: (s) => s.name || s.id,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (s) => <CPStatusPill status={s.status} />,
      sortAccessor: (s) => s.status,
      filter: { label: 'Status' },
    },
    {
      key: 'etcdRevision',
      header: 'etcd revision',
      accessor: (s) => (
        <span className="font-mono text-xs text-muted-foreground">
          {s.etcdRevision != null ? s.etcdRevision.toLocaleString() : '—'}
        </span>
      ),
      sortAccessor: (s) => s.etcdRevision ?? 0,
      align: 'right',
    },
    {
      key: 'size',
      header: 'Size',
      accessor: (s) => <span className="text-xs text-muted-foreground">{fmtBytes(s.sizeBytes)}</span>,
      sortAccessor: (s) => s.sizeBytes ?? 0,
      align: 'right',
    },
    {
      key: 'createdBy',
      header: 'Taken by',
      accessor: (s) => <span className="text-xs text-muted-foreground">{s.createdBy || '—'}</span>,
      sortAccessor: (s) => s.createdBy ?? '',
    },
    {
      key: 'createdAt',
      header: 'Created',
      accessor: (s) => <span className="text-xs text-muted-foreground">{fmt(s.createdAt)}</span>,
      sortAccessor: (s) => s.createdAt ?? '',
    },
    {
      key: 'completedAt',
      header: 'Completed',
      accessor: (s) => <span className="text-xs text-muted-foreground">{fmt(s.completedAt)}</span>,
      sortAccessor: (s) => s.completedAt ?? '',
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      align: 'right',
      accessor: (s) => (
        <button
          onClick={() => setGuidanceTarget(s)}
          className="inline-flex items-center gap-1 h-7 px-2 rounded text-xs text-muted-foreground
            hover:text-foreground hover:bg-accent transition-colors"
          title="View restore runbook"
        >
          <BookOpen className="h-3.5 w-3.5" />
          Restore guidance
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {header}

      <DataTable
        data={snapshotsQuery.data ?? []}
        columns={columns}
        keyExtractor={(s) => s.id}
        loading={snapshotsQuery.isLoading}
        isError={snapshotsQuery.isError}
        errorMessage="Failed to load control-plane snapshots"
        onRetry={() => void snapshotsQuery.refetch()}
        emptyMessage="No control-plane snapshots yet. Take one to capture the current etcd state."
        searchPlaceholder="Search snapshots…"
      />

      {guidanceTarget && (
        <RestoreGuidanceModal
          clusterId={clusterId}
          snapshot={guidanceTarget}
          onClose={() => setGuidanceTarget(null)}
        />
      )}
    </div>
  );
}

// ─── Restore guidance (read-only runbook) ───────────────────────────────────
function RestoreGuidanceModal({
  clusterId,
  snapshot,
  onClose,
}: {
  clusterId: string;
  snapshot: ControlPlaneSnapshot;
  onClose: () => void;
}) {
  const guidanceQuery = useQuery({
    queryKey: cpGuidanceKey(clusterId, snapshot.id),
    queryFn: () => getControlPlaneSnapshotRestoreGuidance(clusterId, snapshot.id),
    refetchOnWindowFocus: false,
  });

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-2xl max-h-[90vh] flex flex-col rounded-xl border border-border bg-popover shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <div className="flex items-center gap-3 min-w-0">
            <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center text-muted-foreground flex-shrink-0">
              <BookOpen className="h-4 w-4" />
            </div>
            <div className="min-w-0">
              <h3 className="text-lg font-semibold text-foreground truncate">Restore runbook</h3>
              <p className="text-xs text-muted-foreground font-mono truncate">
                {snapshot.name || snapshot.id}
              </p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors flex-shrink-0"
            aria-label="Close"
          >
            <XCircle className="h-5 w-5" />
          </button>
        </div>

        <div className="p-6 space-y-4 overflow-y-auto">
          <div className="rounded-lg border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning flex items-start gap-2">
            <Lock className="h-3.5 w-3.5 flex-shrink-0 mt-0.5" />
            <span>
              This is guidance only. Restoring a control plane is a manual, out-of-band procedure —
              nothing on this page performs an automated restore.
            </span>
          </div>

          {guidanceQuery.isLoading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground py-8 justify-center">
              <Loader2 className="h-4 w-4 animate-spin" /> Loading runbook…
            </div>
          ) : guidanceQuery.isError ? (
            <div className="text-sm text-status-error py-8 text-center">
              Failed to load restore guidance.{' '}
              <button
                onClick={() => void guidanceQuery.refetch()}
                className="underline hover:text-foreground"
              >
                Retry
              </button>
            </div>
          ) : (
            <div className="space-y-4">
              {guidanceQuery.data?.steps && guidanceQuery.data.steps.length > 0 && (
                <ol className="list-decimal pl-5 space-y-1 text-sm text-foreground">
                  {guidanceQuery.data.steps.map((step, i) => (
                    <li key={i}>{step}</li>
                  ))}
                </ol>
              )}
              <pre className="whitespace-pre-wrap break-words rounded-lg border border-border bg-muted/30 p-3 text-xs font-mono text-foreground">
                {guidanceQuery.data?.guidance || 'No runbook text was provided for this snapshot.'}
              </pre>
              {guidanceQuery.data?.generatedAt && (
                <p className="text-xs text-muted-foreground">
                  Generated {fmt(guidanceQuery.data.generatedAt)}
                </p>
              )}
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-3 border-t border-border flex-shrink-0">
          <button
            onClick={onClose}
            className="inline-flex items-center h-9 px-3 rounded text-sm text-muted-foreground
              hover:text-foreground hover:bg-accent transition-colors"
          >
            Close
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}
