import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/backup — Astronomer's own management-plane backup.
 *
 * This is the pg_dump CronJob that copies Astronomer's Postgres (clusters,
 * projects, RBAC, audit, …) to object storage. Workload/app snapshots are
 * Velero, live on each cluster, and only appear after Velero is installed
 * there. Restore of this dump is an operator procedure (not a one-click UI).
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import {
  ArrowLeft,
  KeyRound,
  Loader2,
  Pencil,
  Play,
  Plus,
  ShieldAlert,
  ShieldCheck,
  Trash2,
} from 'lucide-react';
import { toastSuccess } from '@/lib/toast';
import { useAppForm } from '@/lib/form';
import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { cn, formatRelativeTime } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { PageHeader, PageShell } from '@/components/ui/page';
import { cronToHuman } from '@/components/backups/cron';
import {
  useBackupDrillHistory,
  useCreateManagementBackupDestination,
  useDeleteManagementBackupDestination,
  useLatestBackupDrill,
  useManagementBackupStatus,
  useRunManagementBackupDestination,
  useTestManagementBackupDestination,
  useUpdateManagementBackupDestination,
} from '@/components/settings/hooks';
import type {
  BackupDrillResult,
  ManagementBackupDestination,
  ManagementBackupStatus,
} from '@/lib/api/settings';
import { MANAGEMENT_BACKUP_SECRET_SENTINEL } from '@/lib/api/settings';

function statusToVariant(status: BackupDrillResult['status']) {
  switch (status) {
    case 'success':
      return 'active' as const;
    case 'partial':
      return 'warning' as const;
    case 'failure':
      return 'error' as const;
    case 'running':
      return 'connecting' as const;
    default:
      return 'disconnected' as const;
  }
}

function durationLabel(startedAt?: string, finishedAt?: string, seconds?: number | null) {
  if (seconds != null) return `${seconds}s`;
  if (!startedAt || !finishedAt) return '—';
  const ms = new Date(finishedAt).getTime() - new Date(startedAt).getTime();
  if (!Number.isFinite(ms) || ms < 0) return '—';
  return `${Math.round(ms / 1000)}s`;
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-background p-3">
      <p className="text-muted-foreground">{label}</p>
      <p className="text-sm font-mono text-foreground truncate mt-0.5">{value || '—'}</p>
    </div>
  );
}

type DestForm = {
  name: string;
  bucket: string;
  prefix: string;
  region: string;
  endpoint_url: string;
  access_key: string;
  secret_key: string;
  schedule: string;
  enabled: boolean;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
};

const emptyForm = (): DestForm => ({
  name: '',
  bucket: '',
  prefix: 'astronomer-pg',
  region: 'us-east-1',
  endpoint_url: '',
  access_key: '',
  secret_key: '',
  schedule: '0 3 * * *',
  enabled: true,
  keep_daily: 30,
  keep_weekly: 12,
  keep_monthly: 6,
});

function formFromDest(row: ManagementBackupDestination): DestForm {
  return {
    name: row.name,
    bucket: row.bucket,
    prefix: row.prefix || 'astronomer-pg',
    region: row.region || 'us-east-1',
    endpoint_url: row.endpoint || '',
    access_key: row.hasCredentials ? MANAGEMENT_BACKUP_SECRET_SENTINEL : '',
    secret_key: row.hasCredentials ? MANAGEMENT_BACKUP_SECRET_SENTINEL : '',
    schedule: row.schedule || '0 3 * * *',
    enabled: row.enabled,
    keep_daily: row.keepDaily || 30,
    keep_weekly: row.keepWeekly || 12,
    keep_monthly: row.keepMonthly || 6,
  };
}

function DestinationsSection({ data }: { data: ManagementBackupStatus }) {
  const [editor, setEditor] = useState<ManagementBackupDestination | 'new' | null>(null);
  const [remove, setRemove] = useState<ManagementBackupDestination | null>(null);
  const del = useDeleteManagementBackupDestination();
  const run = useRunManagementBackupDestination();
  const rows = data.destinations ?? [];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold text-foreground">S3 destinations</h2>
          <p className="text-xs text-muted-foreground">
            Each destination gets its own nightly dump CronJob. Add a second bucket for DR or a different schedule.
          </p>
        </div>
        <ActionButton intent="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setEditor('new')}>
          Add destination
        </ActionButton>
      </div>
      {rows.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border bg-card p-6 space-y-2">
          <p className="text-sm text-foreground">No dump destinations yet.</p>
          <p className="text-xs text-muted-foreground">
            {data.reason || 'Add an S3 bucket to start nightly dumps of Astronomer’s database.'}
          </p>
        </div>
      ) : (
        <DataTable
          data={rows}
          columns={[
            {
              key: 'name',
              header: 'Name',
              accessor: (row) => (
                <div>
                  <p className="text-sm text-foreground">{row.name}</p>
                  <p className="text-2xs text-muted-foreground font-mono">{row.bucket}</p>
                </div>
              ),
            },
            {
              key: 'schedule',
              header: 'Schedule',
              accessor: (row) => (
                <span className="text-xs text-muted-foreground">
                  {row.schedule ? cronToHuman(row.schedule) : '—'}
                </span>
              ),
            },
            {
              key: 'status',
              header: 'Status',
              accessor: (row) => (
                <StatusBadge
                  status={row.enabled ? 'active' : 'disconnected'}
                  label={row.enabled ? (row.source === 'helm' ? 'helm' : 'scheduled') : 'paused'}
                  size="sm"
                />
              ),
            },
            {
              key: 'last',
              header: 'Last job',
              accessor: (row) => (
                <span className="text-xs text-muted-foreground">
                  {row.lastJob?.completionTime
                    ? formatRelativeTime(row.lastJob.completionTime)
                    : row.lastJob?.startTime
                      ? formatRelativeTime(row.lastJob.startTime)
                      : 'never'}
                </span>
              ),
            },
            {
              key: 'actions',
              header: '',
              sortable: false,
              accessor: (row) =>
                row.readOnly ? (
                  <span className="text-2xs text-muted-foreground">Helm-managed</span>
                ) : (
                  <div className="flex items-center justify-end gap-1">
                    <ActionButton
                      intent="ghost"
                      size="sm"
                      icon={<Play className="h-3.5 w-3.5" />}
                      onClick={() => run.mutate(row.id)}
                      disabled={run.isPending || !row.enabled}
                    >
                      Run
                    </ActionButton>
                    <ActionButton intent="ghost" size="sm" icon={<Pencil className="h-3.5 w-3.5" />} onClick={() => setEditor(row)}>
                      Edit
                    </ActionButton>
                    <ActionButton intent="ghost" size="sm" icon={<Trash2 className="h-3.5 w-3.5" />} onClick={() => setRemove(row)}>
                      Remove
                    </ActionButton>
                  </div>
                ),
            },
          ]}
          keyExtractor={(row) => row.id}
          emptyMessage="No destinations"
        />
      )}
      {editor && (
        <DestinationModal
          existing={editor === 'new' ? null : editor}
          onClose={() => setEditor(null)}
        />
      )}
      <ConfirmDialog
        open={!!remove}
        onClose={() => setRemove(null)}
        title="Remove destination"
        description={remove ? `Stop dumping to ${remove.bucket} and delete the CronJob.` : ''}
        confirmText="Remove"
        onConfirm={() => {
          if (remove) del.mutate(remove.id);
          setRemove(null);
        }}
      />
    </div>
  );
}

function DestinationModal({
  existing,
  onClose,
}: {
  existing: ManagementBackupDestination | null;
  onClose: () => void;
}) {
  const create = useCreateManagementBackupDestination();
  const update = useUpdateManagementBackupDestination();
  const test = useTestManagementBackupDestination();

  const form = useAppForm({
    defaultValues: existing ? formFromDest(existing) : emptyForm(),
    onSubmit: async ({ value }) => {
      const body = {
        name: value.name,
        bucket: value.bucket,
        prefix: value.prefix,
        region: value.region,
        endpoint_url: value.endpoint_url,
        access_key: value.access_key,
        secret_key: value.secret_key,
        schedule: value.schedule,
        enabled: value.enabled,
        keep_daily: value.keep_daily,
        keep_weekly: value.keep_weekly,
        keep_monthly: value.keep_monthly,
      };
      if (existing) {
        await update.mutateAsync({ id: existing.id, body });
      } else {
        await create.mutateAsync(body);
      }
      onClose();
    },
  });

  return (
    <ModalShell
      title={existing ? 'Edit destination' : 'Add S3 destination'}
      subtitle="Credentials are stored encrypted. The dump CronJob starts as soon as you save."
      onClose={onClose}
      size="lg"
      footer={
        <div className="flex justify-end gap-2">
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton intent="primary" onClick={() => void form.handleSubmit()}>
            {existing ? 'Save' : 'Add destination'}
          </ActionButton>
        </div>
      }
    >
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          void form.handleSubmit();
        }}
      >
        <form.AppField name="name">
          {(field) => <field.TextField label="Name" required placeholder="primary" />}
        </form.AppField>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <form.AppField name="bucket">
            {(field) => <field.TextField label="Bucket" required placeholder="astronomer-backups" />}
          </form.AppField>
          <form.AppField name="prefix">
            {(field) => <field.TextField label="Prefix" helper="Object key prefix inside the bucket" />}
          </form.AppField>
          <form.AppField name="region">
            {(field) => <field.TextField label="Region" />}
          </form.AppField>
          <form.AppField name="endpoint_url">
            {(field) => (
              <field.TextField
                label="Endpoint"
                helper="Leave blank for AWS. Set for MinIO or other S3-compatible stores."
                placeholder="https://minio.example.com"
              />
            )}
          </form.AppField>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <form.AppField name="access_key">
            {(field) => (
              <field.SecretField label="Access key" required stored={!!existing?.hasCredentials} revealable />
            )}
          </form.AppField>
          <form.AppField name="secret_key">
            {(field) => (
              <field.SecretField label="Secret key" required stored={!!existing?.hasCredentials} revealable />
            )}
          </form.AppField>
        </div>
        <form.AppField name="schedule">
          {(field) => (
            <field.TextField label="Cron schedule" helper="UTC. Default is 03:00 every day." />
          )}
        </form.AppField>
        <div className="grid grid-cols-3 gap-3">
          <form.AppField name="keep_daily">
            {(field) => <field.NumberField label="Keep daily" min={1} max={365} />}
          </form.AppField>
          <form.AppField name="keep_weekly">
            {(field) => <field.NumberField label="Keep weekly" min={1} max={52} />}
          </form.AppField>
          <form.AppField name="keep_monthly">
            {(field) => <field.NumberField label="Keep monthly" min={1} max={36} />}
          </form.AppField>
        </div>
        <form.AppField name="enabled">
          {(field) => (
            <field.SwitchField
              label="Enabled"
              helper="When on, Astronomer writes a CronJob that dumps to this bucket."
            />
          )}
        </form.AppField>
        {existing && (
          <ActionButton
            onClick={async () => {
              const result = await test.mutateAsync(existing.id);
              if (result.success) toastSuccess(result.message);
            }}
            disabled={test.isPending}
          >
            Test connection
          </ActionButton>
        )}
      </form>
    </ModalShell>
  );
}

function EncryptionCard({ data }: { data: ManagementBackupStatus }) {
  if (!(data.destinations ?? []).length) return null;
  const wrapped = data.encryptionKeyBackup?.wrappingConfigured;
  return (
    <div
      className={cn(
        'rounded-xl border p-6 space-y-2',
        wrapped
          ? 'border-border bg-card'
          : 'border-status-warning/30 bg-status-warning/5',
      )}
    >
      <div className="flex items-center gap-2">
        {wrapped ? (
          <KeyRound className="h-4 w-4 text-muted-foreground" />
        ) : (
          <ShieldAlert className="h-4 w-4 text-status-warning" />
        )}
        <h2 className="text-sm font-medium text-foreground">Encryption key backup</h2>
      </div>
      {wrapped ? (
        <p className="text-xs text-muted-foreground">
          The platform encryption key is wrapped and stored with each dump. A restore onto a
          new cluster can decrypt agent tokens and SSO secrets.
        </p>
      ) : (
        <p className="text-xs text-status-warning">
          Dumps are running without a wrapped copy of the encryption key. Restoring onto a new
          cluster would leave encrypted columns undecryptable. Set
          managementBackup.encryptionKeyBackup.wrappingSecretRef in Helm values.
        </p>
      )}
    </div>
  );
}

function LatestDrillCard() {
  const { data, isLoading } = useLatestBackupDrill();

  if (isLoading) {
    return (
      <div className="rounded-xl border border-border bg-card p-6 flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  const latest = data?.latest ?? null;
  if (!latest) {
    return (
      <div className="rounded-xl border border-dashed border-border bg-card p-6 text-center space-y-2">
        <p className="text-sm text-foreground">No restore drill has run yet.</p>
        <p className="text-xs text-muted-foreground">
          The weekly drill restores the latest dump into a scratch Postgres and records the result here.
        </p>
      </div>
    );
  }

  const age = data?.latestSuccessAgeSeconds;
  const stale = age != null && age > 7 * 24 * 3600;

  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Latest restore drill</p>
          <div className="flex items-center gap-3">
            <StatusBadge status={statusToVariant(latest.status)} label={latest.status} size="sm" />
            <span className={cn('text-xs', stale ? 'text-status-warning' : 'text-muted-foreground')}>
              {formatRelativeTime(latest.finishedAt ?? latest.startedAt)}
            </span>
          </div>
          {latest.errorMessage && (
            <p className="text-sm text-status-error mt-2">{latest.errorMessage}</p>
          )}
        </div>
        <div className="grid grid-cols-2 gap-3 text-xs">
          <Stat
            label="Schema version"
            value={latest.schemaVersion != null ? String(latest.schemaVersion) : '—'}
          />
          <Stat label="Duration" value={durationLabel(latest.startedAt, latest.finishedAt)} />
          {latest.backupKey && <Stat label="Source dump" value={latest.backupKey} />}
        </div>
      </div>
      {stale && (
        <div className="rounded-lg border border-status-warning/30 bg-status-warning/5 px-3 py-2 text-xs text-status-warning">
          Last successful drill is over a week old. Restore confidence is decaying — check the drill CronJob.
        </div>
      )}
    </div>
  );
}

function HistoryTable() {
  const [page, setPage] = useState(1);
  const { data, isLoading } = useBackupDrillHistory({ page, page_size: 25 });
  const rows = data?.data ?? [];

  const columns: Column<BackupDrillResult>[] = [
    {
      key: 'startedAt',
      header: 'Started',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground font-mono">{formatRelativeTime(row.startedAt)}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={statusToVariant(row.status)} label={row.status} size="sm" />,
    },
    {
      key: 'schemaVersion',
      header: 'Schema',
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">
          {row.schemaVersion != null ? row.schemaVersion : '—'}
        </span>
      ),
    },
    {
      key: 'duration',
      header: 'Duration',
      align: 'right',
      accessor: (row) => (
        <span className="text-xs font-mono tabular-nums text-muted-foreground">
          {durationLabel(row.startedAt, row.finishedAt)}
        </span>
      ),
    },
    {
      key: 'error',
      header: 'Error',
      sortable: false,
      accessor: (row) => (
        <span className="text-xs text-status-error truncate max-w-[260px] block">
          {row.errorMessage || '—'}
        </span>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <h2 className="text-base font-semibold text-foreground">Restore drill history</h2>
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        emptyMessage="No drills recorded"
        pageSize={25}
      />
      {data && data.totalPages > 1 && (
        <div className="flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page === 1}
            className="h-8 px-3 rounded-lg border border-border text-xs font-medium disabled:opacity-50"
          >
            Previous
          </button>
          <p className="text-xs text-muted-foreground">
            Page {data.page} of {data.totalPages}
          </p>
          <button
            type="button"
            onClick={() => setPage((p) => p + 1)}
            disabled={page >= data.totalPages}
            className="h-8 px-3 rounded-lg border border-border text-xs font-medium disabled:opacity-50"
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}

function AstronomerBackupPage() {
  const { data, isLoading } = useManagementBackupStatus();

  return (
    <SettingsAuthGate>
      <PageShell>
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <PageHeader
          eyebrow="Settings · Backup"
          title={
            <span className="flex items-center gap-2">
              <ShieldCheck className="h-5 w-5 text-muted-foreground" />
              Astronomer backup
            </span>
          }
          description="Nightly dump of Astronomer's own database to one or more S3 buckets. Workload snapshots live on each cluster after Velero is installed there."
        />
        {isLoading || !data ? (
          <div className="rounded-xl border border-border bg-card p-6 flex items-center justify-center h-32">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <>
            <DestinationsSection data={data} />
            <EncryptionCard data={data} />
          </>
        )}
        <LatestDrillCard />
        <HistoryTable />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/backup/')({
  component: AstronomerBackupPage,
});
