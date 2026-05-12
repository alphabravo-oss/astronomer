'use client';

/**
 * /dashboard/settings/backup-drill — surfaced restore-drill results.
 *
 * The platform runs a periodic "restore drill" job: it picks a recent backup,
 * restores it into a scratch namespace, asserts the restored object count
 * matches the captured one, and records a row. This page renders the latest
 * result on top + a paginated history table below.
 */
import { useState } from 'react';
import Link from 'next/link';
import {
  ArrowLeft,
  Loader2,
  ShieldCheck,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { cn, formatRelativeTime } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useBackupDrillHistory,
  useLatestBackupDrill,
} from '@/components/settings/hooks';
import type { BackupDrillResult } from '@/lib/api/settings';

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

function formatAge(ageSeconds: number): string {
  if (ageSeconds < 60) return `${ageSeconds}s ago`;
  if (ageSeconds < 3600) return `${Math.floor(ageSeconds / 60)}m ago`;
  if (ageSeconds < 86400) return `${Math.floor(ageSeconds / 3600)}h ago`;
  return `${Math.floor(ageSeconds / 86400)}d ago`;
}

function LatestCard() {
  const { data, isLoading } = useLatestBackupDrill();

  if (isLoading) {
    return (
      <div className="rounded-xl border border-border bg-card p-6 flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!data) {
    return (
      <div className="rounded-xl border border-dashed border-border bg-card p-6 text-center space-y-2">
        <p className="text-sm text-foreground">No restore drill has run yet.</p>
        <p className="text-xs text-muted-foreground">
          The first scheduled drill will land here once the platform completes one full cycle.
        </p>
      </div>
    );
  }

  const stale = data.ageSeconds > 7 * 24 * 3600;

  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Latest drill</p>
          <div className="flex items-center gap-3">
            <StatusBadge status={statusToVariant(data.status)} label={data.status} size="sm" />
            <span className={cn('text-xs', stale ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground')}>
              {formatAge(data.ageSeconds)}
            </span>
          </div>
          {data.errorMessage && (
            <p className="text-sm text-status-error mt-2">{data.errorMessage}</p>
          )}
        </div>
        <div className="grid grid-cols-2 gap-3 text-xs">
          <div className="rounded-lg border border-border bg-background p-3">
            <p className="text-muted-foreground">Schema version</p>
            <p className="text-sm font-mono text-foreground tabular-nums mt-0.5">{data.schemaVersion}</p>
          </div>
          <div className="rounded-lg border border-border bg-background p-3">
            <p className="text-muted-foreground">Duration</p>
            <p className="text-sm font-mono text-foreground tabular-nums mt-0.5">
              {data.durationSeconds != null ? `${data.durationSeconds}s` : '--'}
            </p>
          </div>
          {data.restoredObjects != null && (
            <div className="rounded-lg border border-border bg-background p-3">
              <p className="text-muted-foreground">Restored objects</p>
              <p className="text-sm font-mono text-foreground tabular-nums mt-0.5">
                {data.restoredObjects.toLocaleString()}
              </p>
            </div>
          )}
          {data.backupId && (
            <div className="rounded-lg border border-border bg-background p-3">
              <p className="text-muted-foreground">Source backup</p>
              <p className="text-2xs font-mono text-foreground truncate mt-0.5">{data.backupId}</p>
            </div>
          )}
        </div>
      </div>
      {stale && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-amber-600 dark:text-amber-400">
          Last drill is over a week old. Restore confidence is decaying — investigate the drill cron.
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
        <span className="text-xs font-mono text-muted-foreground">{row.schemaVersion}</span>
      ),
    },
    {
      key: 'duration',
      header: 'Duration',
      align: 'right',
      accessor: (row) => (
        <span className="text-xs font-mono tabular-nums text-muted-foreground">
          {row.durationSeconds != null ? `${row.durationSeconds}s` : '--'}
        </span>
      ),
    },
    {
      key: 'restored',
      header: 'Objects',
      align: 'right',
      accessor: (row) => (
        <span className="text-xs font-mono tabular-nums text-muted-foreground">
          {row.restoredObjects != null ? row.restoredObjects.toLocaleString() : '--'}
        </span>
      ),
    },
    {
      key: 'error',
      header: 'Error',
      sortable: false,
      accessor: (row) => (
        <span className="text-xs text-status-error truncate max-w-[260px] block">
          {row.errorMessage ?? '--'}
        </span>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <h2 className="text-base font-semibold text-foreground">History</h2>
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

export default function BackupDrillPage() {
  return (
    <SettingsAuthGate>
      <div className="max-w-4xl mx-auto space-y-6">
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Backup drill</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1 flex items-center gap-2">
            <ShieldCheck className="h-5 w-5 text-muted-foreground" />
            Backup restore drill
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Periodic restore checks against a scratch namespace. Latest result up top, full history below.
          </p>
        </div>
        <LatestCard />
        <HistoryTable />
      </div>
    </SettingsAuthGate>
  );
}
