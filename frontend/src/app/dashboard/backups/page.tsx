'use client';

/**
 * Phase B2 backups overview. Replaces the Phase B1 stub that displayed
 * fake byte counts and a hard-coded success state. The page is split into
 * three tabs:
 *
 *   - Storage Locations  — Velero BackupStorageLocations
 *   - Schedules          — Velero Schedule CRs
 *   - Runs               — Velero Backup CRs
 *
 * The "Add Storage", "Create Schedule" buttons route to dedicated wizard
 * pages under `/dashboard/backups/storage/new` and `.../schedules/new`.
 * Live updates piggyback on `cluster.k8s_changed` — Velero objects flow
 * through the same agent watchstream as every other K8s resource so the
 * runs table refreshes the moment a new Backup CR's phase changes.
 */

import { useMemo, useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  Archive,
  Clock,
  Database,
  HardDrive,
  Pencil,
  Play,
  Plus,
  RotateCcw,
  Star,
  Trash2,
} from 'lucide-react';
import { useClusters } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionMenu } from '@/components/ui/action-menu';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { EmptyState } from '@/components/ui/empty-state';
import { cn, formatRelativeTime } from '@/lib/utils';
import { PhaseBadge } from '@/components/backups/phase-badge';
import { RestoreModal } from '@/components/backups/restore-modal';
import { cronToHuman } from '@/components/backups/cron';
import { toastApiError, toastError, toastSuccess } from '@/lib/toast';
import {
  b2Keys,
  useB2DeleteSchedule,
  useB2DeleteStorageLocation,
  useB2Runs,
  useB2Schedules,
  useB2StorageLocations,
  useB2TestStorageLocation,
  useB2TriggerScheduleNow,
  useB2UpdateSchedule,
} from '@/components/backups/hooks';
import type {
  BackupRun,
  BackupScheduleRow,
  BackupStorageLocation,
  BackupStorageType,
} from '@/types';

type TabKey = 'storage' | 'schedules' | 'runs';

const STORAGE_TYPE_LABELS: Record<BackupStorageType, string> = {
  s3: 'Amazon S3',
  gcs: 'Google Cloud Storage',
  azure: 'Azure Blob',
  minio: 'MinIO / S3-compatible',
};

const TABS: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'storage', label: 'Storage Locations', icon: HardDrive },
  { key: 'schedules', label: 'Schedules', icon: Clock },
  { key: 'runs', label: 'Runs', icon: Archive },
];

export default function BackupsPage() {
  const router = useRouter();
  const [tab, setTab] = useState<TabKey>('storage');
  const [restoreTarget, setRestoreTarget] = useState<BackupRun | null>(null);
  const [deleteStorage, setDeleteStorage] = useState<BackupStorageLocation | null>(null);
  const [deleteSchedule, setDeleteSchedule] = useState<BackupScheduleRow | null>(null);

  const storageQ = useB2StorageLocations();
  const schedulesQ = useB2Schedules();
  const runsQ = useB2Runs();
  const clustersQ = useClusters({ pageSize: 100 });

  const updateSchedule = useB2UpdateSchedule();
  const triggerSchedule = useB2TriggerScheduleNow();
  const deleteStorageMu = useB2DeleteStorageLocation();
  const deleteScheduleMu = useB2DeleteSchedule();
  const testStorage = useB2TestStorageLocation();

  // Live: refetch the run list whenever K8s state changes on any cluster
  // a backup is targeting. We invalidate the whole `b2-backups` namespace
  // so storage / schedule / run / restore queries all refresh in lockstep.
  useLiveQueryInvalidation(['cluster.k8s_changed'], [b2Keys.all]);

  const clusterById = useMemo(() => {
    const m = new Map<string, string>();
    for (const c of clustersQ.data?.data ?? []) m.set(c.id, c.displayName || c.name);
    return m;
  }, [clustersQ.data]);

  const storageById = useMemo(() => {
    const m = new Map<string, BackupStorageLocation>();
    for (const s of storageQ.data?.data ?? []) m.set(s.id, s);
    return m;
  }, [storageQ.data]);

  const scheduleByStorageId = useMemo(() => {
    const m = new Map<string, BackupScheduleRow[]>();
    for (const s of schedulesQ.data?.data ?? []) {
      const list = m.get(s.storageId) ?? [];
      list.push(s);
      m.set(s.storageId, list);
    }
    return m;
  }, [schedulesQ.data]);

  // ---- Storage columns ----------------------------------------------------
  const storageCols: Column<BackupStorageLocation>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          {row.isDefault && (
            <Star className="h-3.5 w-3.5 text-status-warning fill-status-warning" />
          )}
          <span className="font-medium text-foreground">{row.name}</span>
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
          {STORAGE_TYPE_LABELS[row.storageType] ?? row.storageType}
        </span>
      ),
    },
    {
      key: 'bucket',
      header: 'Bucket',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.bucket}</span>
      ),
    },
    {
      key: 'region',
      header: 'Region',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{row.region || '--'}</span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.clusterId ? (clusterById.get(row.clusterId) ?? row.clusterId.slice(0, 8)) : '--'}
        </span>
      ),
    },
    {
      key: 'default',
      header: 'Default',
      accessor: (row) =>
        row.isDefault ? (
          <Star className="h-3.5 w-3.5 text-status-warning fill-status-warning" />
        ) : (
          <span className="text-muted-foreground/50">--</span>
        ),
      align: 'center',
    },
    {
      key: 'creds',
      header: 'Credentials',
      accessor: (row) =>
        row.hasCredentials ? (
          <StatusBadge status="active" label="Configured" size="sm" />
        ) : (
          <StatusBadge status="warning" label="Missing" size="sm" />
        ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: 'Test Connection',
              icon: <Play className="h-3.5 w-3.5" />,
              onClick: async () => {
                try {
                  const result = await testStorage.mutateAsync(row.id);
                  if (result.success) {
                    toastSuccess(`Reachable: ${result.message}`);
                  } else {
                    toastError(`Unreachable: ${result.message}`);
                  }
                } catch (e) {
                  toastApiError('Test failed', e);
                }
              },
            },
            {
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteStorage(row),
              variant: 'destructive',
              separator: true,
            },
          ]}
        />
      ),
      align: 'center',
      sortable: false,
    },
  ];

  // ---- Schedule columns ---------------------------------------------------
  const scheduleCols: Column<BackupScheduleRow>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => <span className="font-medium text-foreground">{row.name}</span>,
      sortAccessor: (row) => row.name,
    },
    {
      key: 'storage',
      header: 'Storage',
      accessor: (row) => {
        const s = storageById.get(row.storageId);
        return (
          <span className="text-xs text-muted-foreground">{s?.name ?? row.storageId.slice(0, 8)}</span>
        );
      },
    },
    {
      key: 'cron',
      header: 'Schedule',
      accessor: (row) => (
        <div>
          <span className="text-sm text-foreground">{cronToHuman(row.cronExpression)}</span>
          <p className="text-xs text-muted-foreground font-mono">{row.cronExpression}</p>
        </div>
      ),
    },
    {
      key: 'retention',
      header: 'Retention',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground tabular-nums">{row.retentionCount}</span>
      ),
      sortAccessor: (row) => row.retentionCount,
      align: 'center',
    },
    {
      key: 'enabled',
      header: 'Enabled',
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            updateSchedule.mutate({ id: row.id, data: { enabled: !row.enabled } });
          }}
          className="inline-flex items-center gap-1.5 text-xs"
          aria-pressed={row.enabled}
        >
          <span
            className={cn(
              'inline-flex h-4 w-7 rounded-full transition-colors items-center px-0.5',
              row.enabled ? 'bg-status-success/30 justify-end' : 'bg-muted justify-start',
            )}
          >
            <span
              className={cn(
                'h-3 w-3 rounded-full transition-colors',
                row.enabled ? 'bg-status-success' : 'bg-muted-foreground/40',
              )}
            />
          </span>
        </button>
      ),
      sortable: false,
      align: 'center',
    },
    {
      key: 'updated',
      header: 'Updated',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.updatedAt)}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: 'Trigger Now',
              icon: <Play className="h-3.5 w-3.5" />,
              onClick: () => triggerSchedule.mutate(row.id),
            },
            {
              label: 'Edit',
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => router.push(`/dashboard/backups/schedules/new?id=${row.id}`),
            },
            {
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteSchedule(row),
              variant: 'destructive',
              separator: true,
            },
          ]}
        />
      ),
      align: 'center',
      sortable: false,
    },
  ];

  // ---- Run columns --------------------------------------------------------
  const runsCols: Column<BackupRun>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => <span className="font-medium text-foreground">{row.name}</span>,
      sortAccessor: (row) => row.name,
    },
    {
      key: 'schedule',
      header: 'Schedule',
      accessor: (row) => {
        // The Backup row references a storage; we show the schedule that
        // shares that storage when there's exactly one — otherwise we
        // fall back to a dash (manual one-offs and orphaned runs).
        const candidates = scheduleByStorageId.get(row.storageId) ?? [];
        const match = candidates.length === 1 ? candidates[0] : null;
        return (
          <span className="text-xs text-muted-foreground">{match?.name ?? '--'}</span>
        );
      },
    },
    {
      key: 'started',
      header: 'Started',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.startedAt ? formatRelativeTime(row.startedAt) : formatRelativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      key: 'phase',
      header: 'Phase',
      accessor: (row) => <PhaseBadge phase={row.phase} status={row.status} size="sm" />,
    },
    {
      key: 'items',
      header: 'Items',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground tabular-nums">
          {typeof row.itemsBackedUp === 'number' ? row.itemsBackedUp : '--'}
        </span>
      ),
      sortAccessor: (row) => row.itemsBackedUp ?? -1,
      align: 'center',
    },
    {
      key: 'errors',
      header: 'Errors',
      accessor: (row) => (
        <span
          className={cn(
            'text-xs tabular-nums',
            (row.errors ?? 0) > 0 ? 'text-status-error' : 'text-muted-foreground',
          )}
        >
          {typeof row.errors === 'number' ? row.errors : '--'}
        </span>
      ),
      sortAccessor: (row) => row.errors ?? -1,
      align: 'center',
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            setRestoreTarget(row);
          }}
          disabled={row.phase !== 'Completed' && row.status !== 'completed'}
          className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground
            hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
          title="Restore"
        >
          <RotateCcw className="h-3 w-3" />
          Restore
        </button>
      ),
      sortable: false,
      align: 'center',
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Backups</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Velero-backed cluster snapshots, schedules, and restore operations
          </p>
        </div>
        <div className="flex items-center gap-2">
          {tab === 'storage' && (
            <button
              onClick={() => router.push('/dashboard/backups/storage/new')}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Add Storage
            </button>
          )}
          {tab === 'schedules' && (
            <button
              onClick={() => router.push('/dashboard/backups/schedules/new')}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Schedule
            </button>
          )}
        </div>
      </div>

      <div className="border-b border-border">
        <nav className="flex gap-6">
          {TABS.map((t) => {
            const Icon = t.icon;
            return (
              <button
                key={t.key}
                onClick={() => setTab(t.key)}
                className={cn(
                  'flex items-center gap-2 pb-3 text-sm font-medium border-b-2 transition-colors',
                  tab === t.key
                    ? 'border-foreground text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground',
                )}
              >
                <Icon className="h-4 w-4" />
                {t.label}
              </button>
            );
          })}
        </nav>
      </div>

      <div className="animate-fade-in">
        {tab === 'storage' &&
          (!storageQ.isLoading && (storageQ.data?.data ?? []).length === 0 ? (
            <EmptyState
              icon={HardDrive}
              title="No storage locations yet"
              description="Add a Velero BackupStorageLocation pointing at S3, GCS, Azure, or any S3-compatible bucket to capture cluster state."
              actionLabel="Add Storage"
              actionIcon={Plus}
              onAction={() => router.push('/dashboard/backups/storage/new')}
            />
          ) : (
            <DataTable
              data={storageQ.data?.data ?? []}
              columns={storageCols}
              keyExtractor={(r) => r.id}
              loading={storageQ.isLoading}
              searchPlaceholder="Search storage..."
              emptyMessage="No storage locations match your filter"
            />
          ))}
        {tab === 'schedules' &&
          (!schedulesQ.isLoading && (schedulesQ.data?.data ?? []).length === 0 ? (
            <EmptyState
              icon={Clock}
              title="No schedules configured"
              description="Schedules emit Velero Backup CRs on a cron expression. You'll need at least one storage location first."
              actionLabel="Create Schedule"
              actionIcon={Plus}
              onAction={() => router.push('/dashboard/backups/schedules/new')}
              disabled={(storageQ.data?.data ?? []).length === 0}
            />
          ) : (
            <DataTable
              data={schedulesQ.data?.data ?? []}
              columns={scheduleCols}
              keyExtractor={(r) => r.id}
              loading={schedulesQ.isLoading}
              searchPlaceholder="Search schedules..."
              emptyMessage="No schedules match your filter"
            />
          ))}
        {tab === 'runs' &&
          (!runsQ.isLoading && (runsQ.data?.data ?? []).length === 0 ? (
            <EmptyState
              icon={Database}
              title="No backups yet"
              description="Runs appear here as schedules fire (or you trigger one manually). Each row is a Velero Backup CR."
            />
          ) : (
            <DataTable
              data={runsQ.data?.data ?? []}
              columns={runsCols}
              keyExtractor={(r) => r.id}
              loading={runsQ.isLoading}
              searchPlaceholder="Search runs..."
              onRowClick={(row) => router.push(`/dashboard/backups/runs/${row.id}`)}
              emptyMessage="No backup runs match your filter"
            />
          ))}
      </div>

      {restoreTarget && (
        <RestoreModal backup={restoreTarget} onClose={() => setRestoreTarget(null)} />
      )}

      <ConfirmDialog
        open={!!deleteStorage}
        onClose={() => setDeleteStorage(null)}
        onConfirm={async () => {
          if (!deleteStorage) return;
          await deleteStorageMu.mutateAsync(deleteStorage.id);
          setDeleteStorage(null);
        }}
        title="Delete Storage Location"
        description={`This removes "${deleteStorage?.name}" from Astronomer. The underlying bucket and any prior backups remain intact.`}
        confirmText="Delete"
        confirmValue={deleteStorage?.name}
        variant="destructive"
        loading={deleteStorageMu.isPending}
      />

      <ConfirmDialog
        open={!!deleteSchedule}
        onClose={() => setDeleteSchedule(null)}
        onConfirm={async () => {
          if (!deleteSchedule) return;
          await deleteScheduleMu.mutateAsync(deleteSchedule.id);
          setDeleteSchedule(null);
        }}
        title="Delete Schedule"
        description={`Velero will stop emitting Backup CRs for "${deleteSchedule?.name}". Existing runs in object storage are not deleted.`}
        confirmText="Delete"
        confirmValue={deleteSchedule?.name}
        variant="destructive"
        loading={deleteScheduleMu.isPending}
      />
    </div>
  );
}
