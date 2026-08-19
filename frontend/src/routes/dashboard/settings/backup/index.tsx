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
  Database,
  KeyRound,
  Loader2,
  ShieldAlert,
  ShieldCheck,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { cn, formatRelativeTime } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { PageHeader, PageShell } from '@/components/ui/page';
import { cronToHuman } from '@/components/backups/cron';
import {
  useBackupDrillHistory,
  useLatestBackupDrill,
  useManagementBackupStatus,
} from '@/components/settings/hooks';
import type {
  BackupDrillResult,
  ManagementBackupStatus,
} from '@/lib/api/settings';

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

function BackupStatusCard({ data }: { data: ManagementBackupStatus }) {
  const lastSuccess = data.cronjob?.lastSuccessfulTime;
  const lastJob = data.lastJob;
  const jobTone = lastJob
    ? lastJob.failed > 0
      ? 'error'
      : lastJob.active > 0
        ? 'connecting'
        : lastJob.succeeded > 0
          ? 'active'
          : 'disconnected'
    : 'disconnected';
  const jobLabel = lastJob
    ? lastJob.failed > 0
      ? 'failed'
      : lastJob.active > 0
        ? 'running'
        : lastJob.succeeded > 0
          ? 'succeeded'
          : 'pending'
    : 'never run';

  if (!data.enabled) {
    return (
      <div className="rounded-xl border border-dashed border-border bg-card p-6 space-y-2">
        <p className="text-sm text-foreground">Management-plane backup is not wired.</p>
        <p className="text-xs text-muted-foreground">
          {data.reason ||
            'Set managementBackup.s3.bucket and credentialsSecretRef in Helm values, then upgrade the release.'}
        </p>
      </div>
    );
  }

  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Nightly dump</p>
          <div className="flex items-center gap-3">
            <StatusBadge status="active" label={data.cronjob?.suspended ? 'suspended' : 'scheduled'} size="sm" />
            {lastJob && <StatusBadge status={jobTone} label={jobLabel} size="sm" />}
          </div>
          <p className="text-xs text-muted-foreground">
            {data.cronjob?.schedule
              ? `${cronToHuman(data.cronjob.schedule)} (${data.cronjob.schedule})`
              : 'Schedule unknown'}
          </p>
        </div>
        <div className="grid grid-cols-2 gap-3 text-xs min-w-[16rem]">
          <Stat
            label="Last successful dump"
            value={lastSuccess ? formatRelativeTime(lastSuccess) : 'never'}
          />
          <Stat
            label="Last job"
            value={
              lastJob?.completionTime
                ? formatRelativeTime(lastJob.completionTime)
                : lastJob?.startTime
                  ? formatRelativeTime(lastJob.startTime)
                  : '—'
            }
          />
        </div>
      </div>
      {data.cronjob?.suspended && (
        <div className="rounded-lg border border-status-warning/30 bg-status-warning/5 px-3 py-2 text-xs text-status-warning">
          The backup CronJob is suspended. Nightly dumps will not run until it is resumed.
        </div>
      )}
    </div>
  );
}

function DestinationCard({ data }: { data: ManagementBackupStatus }) {
  if (!data.enabled || !data.destination) return null;
  const dest = data.destination;
  const ret = data.retention;
  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-3">
      <div className="flex items-center gap-2">
        <Database className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-sm font-medium text-foreground">Destination</h2>
      </div>
      <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 text-xs">
        <Stat label="Bucket" value={dest.bucket} />
        <Stat label="Prefix" value={dest.prefix} />
        <Stat label="Region" value={dest.region} />
        {dest.endpoint && <Stat label="Endpoint" value={dest.endpoint} />}
        {ret?.daily && <Stat label="Keep daily" value={ret.daily} />}
        {ret?.weekly && <Stat label="Keep weekly" value={ret.weekly} />}
        {ret?.monthly && <Stat label="Keep monthly" value={ret.monthly} />}
      </div>
    </div>
  );
}

function EncryptionCard({ data }: { data: ManagementBackupStatus }) {
  if (!data.enabled) return null;
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
      <PageShell className="max-w-4xl mx-auto">
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
          description="Nightly dump of Astronomer's own database. Workload snapshots live on each cluster after Velero is installed there. Restoring this dump is an operator procedure, not a one-click action."
        />
        {isLoading || !data ? (
          <div className="rounded-xl border border-border bg-card p-6 flex items-center justify-center h-32">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <>
            <BackupStatusCard data={data} />
            <DestinationCard data={data} />
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
