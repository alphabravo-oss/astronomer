'use client';

/**
 * Run detail page. Renders one Velero Backup CR — phase, item counts,
 * timing, and any error/warning detail surfaced by the reconciler. The
 * "Restore from this Backup" CTA opens the same modal as the overview's
 * runs table so behaviour stays identical.
 */

import { useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Clock,
  Database,
  RotateCcw,
  Server,
  XCircle,
} from 'lucide-react';
import { useClusters } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { b2Keys, useB2Run, useB2StorageLocations } from '@/components/backups/hooks';
import { PhaseBadge } from '@/components/backups/phase-badge';
import { RestoreModal } from '@/components/backups/restore-modal';
import { cn, formatBytes, formatRelativeTime } from '@/lib/utils';

export default function BackupRunDetailPage() {
  const params = useParams();
  const router = useRouter();
  const runId = params.runId as string;

  const runQ = useB2Run(runId);
  const storageQ = useB2StorageLocations();
  const clustersQ = useClusters({ pageSize: 100 });

  // Live: any K8s shape change on the cluster running this backup
  // refetches the run row; the polling refetch in `useB2Run` provides a
  // belt-and-braces fallback when SSE isn't connected.
  useLiveQueryInvalidation(
    ['cluster.k8s_changed'],
    [b2Keys.runDetail(runId), b2Keys.runs()],
  );

  const [showRestore, setShowRestore] = useState(false);

  const run = runQ.data;
  const storage = storageQ.data?.data.find((s) => s.id === run?.storageId);
  const cluster = clustersQ.data?.data.find((c) => c.id === run?.clusterId);

  if (runQ.isLoading) {
    return (
      <div className="space-y-3 animate-pulse">
        <div className="h-6 w-48 rounded bg-muted" />
        <div className="h-32 rounded bg-muted" />
      </div>
    );
  }
  if (!run) {
    return (
      <div className="rounded-xl border border-border bg-card p-6 text-sm text-muted-foreground">
        Backup run not found.
      </div>
    );
  }

  const inProgress = run.phase === 'InProgress' || run.status === 'in_progress';
  const completed = run.phase === 'Completed' || run.status === 'completed';
  const partial = run.phase === 'PartiallyFailed';
  const failed = run.phase === 'Failed' || run.phase === 'FailedValidation' || run.status === 'failed';

  const total = run.totalItems ?? 0;
  const done = run.itemsBackedUp ?? 0;
  const progressPct =
    total > 0 ? Math.min(100, Math.round((done / total) * 100)) : completed ? 100 : 0;

  return (
    <div className="space-y-6">
      <div>
        <button
          onClick={() => router.push('/dashboard/backups?tab=runs')}
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors mb-2"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to runs
        </button>
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-2xl font-semibold text-foreground tracking-tight break-all">
              {run.name}
            </h1>
            <p className="text-sm text-muted-foreground mt-1 font-mono break-all">
              {run.veleroBackupName ?? run.id}
            </p>
          </div>
          <button
            onClick={() => setShowRestore(true)}
            disabled={!completed && !partial}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50 flex-shrink-0"
            title={
              completed || partial
                ? 'Restore resources from this backup'
                : 'Restore is only available for Completed or PartiallyFailed runs'
            }
          >
            <RotateCcw className="h-4 w-4" />
            Restore from this Backup
          </button>
        </div>
      </div>

      {/* Phase + progress card */}
      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <div className="flex items-center justify-between gap-4 flex-wrap">
          <div className="flex items-center gap-3">
            <PhaseBadge phase={run.phase} status={run.status} size="lg" />
            {(inProgress || completed) && total > 0 && (
              <span className="text-xs text-muted-foreground tabular-nums">
                {done}/{total} items
              </span>
            )}
          </div>
          <div className="flex items-center gap-4 text-xs text-muted-foreground">
            <span className="inline-flex items-center gap-1">
              <Clock className="h-3 w-3" />
              Started {run.startedAt ? formatRelativeTime(run.startedAt) : '—'}
            </span>
            {run.completedAt && (
              <span className="inline-flex items-center gap-1">
                <CheckCircle2 className="h-3 w-3" />
                Completed {formatRelativeTime(run.completedAt)}
              </span>
            )}
          </div>
        </div>

        <div className="space-y-1">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted-foreground">Progress</span>
            <span className="text-foreground tabular-nums font-medium">{progressPct}%</span>
          </div>
          <div className="h-2 rounded-full bg-muted overflow-hidden">
            <div
              className={cn(
                'h-full transition-all',
                failed
                  ? 'bg-status-error'
                  : partial
                    ? 'bg-status-warning'
                    : completed
                      ? 'bg-status-success'
                      : 'bg-primary',
                inProgress && 'animate-pulse',
              )}
              style={{ width: `${progressPct}%` }}
            />
          </div>
        </div>
      </div>

      {/* Stat cards */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
        <Stat
          icon={Database}
          label="Items backed up"
          value={typeof run.itemsBackedUp === 'number' ? String(run.itemsBackedUp) : '—'}
        />
        <Stat
          icon={Database}
          label="Total items"
          value={total > 0 ? String(total) : '—'}
        />
        <Stat
          icon={AlertTriangle}
          label="Warnings"
          value={typeof run.warnings === 'number' ? String(run.warnings) : '—'}
          tone={(run.warnings ?? 0) > 0 ? 'warning' : 'neutral'}
        />
        <Stat
          icon={XCircle}
          label="Errors"
          value={typeof run.errors === 'number' ? String(run.errors) : '—'}
          tone={(run.errors ?? 0) > 0 ? 'error' : 'neutral'}
        />
      </div>

      {/* Error message (if any) */}
      {run.errorMessage && (
        <div className="rounded-xl border border-status-error/30 bg-status-error/5 p-4 space-y-1">
          <div className="flex items-center gap-2">
            <XCircle className="h-4 w-4 text-status-error" />
            <span className="text-sm font-medium text-foreground">Backup failed</span>
          </div>
          <pre className="text-xs text-muted-foreground font-mono whitespace-pre-wrap break-all">
            {run.errorMessage}
          </pre>
        </div>
      )}

      {/* Metadata */}
      <div className="rounded-xl border border-border bg-card p-6 space-y-3">
        <h2 className="text-sm font-semibold text-foreground">Details</h2>
        <dl className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          <Row k="Type">{run.backupType}</Row>
          <Row k="Cluster">
            {cluster ? (
              <button
                onClick={() => router.push(`/dashboard/clusters/${cluster.id}`)}
                className="inline-flex items-center gap-1 text-foreground hover:text-primary transition-colors"
              >
                <Server className="h-3 w-3" />
                {cluster.displayName || cluster.name}
              </button>
            ) : (
              run.clusterId ?? '—'
            )}
          </Row>
          <Row k="Storage">{storage?.name ?? run.storageId.slice(0, 8)}</Row>
          <Row k="Velero namespace" mono>
            {run.veleroNamespace ?? '—'}
          </Row>
          <Row k="Velero CR" mono>
            {run.veleroBackupName ?? '—'}
          </Row>
          <Row k="File path" mono>
            {run.filePath ?? '—'}
          </Row>
          <Row k="File size">{run.fileSizeBytes ? formatBytes(run.fileSizeBytes) : '—'}</Row>
          <Row k="Included namespaces" mono>
            {(run.includedNamespaces ?? []).length > 0
              ? (run.includedNamespaces ?? []).join(', ')
              : 'all'}
          </Row>
          <Row k="Excluded namespaces" mono>
            {(run.excludedNamespaces ?? []).length > 0
              ? (run.excludedNamespaces ?? []).join(', ')
              : 'none'}
          </Row>
          <Row k="Last polled">
            {run.lastPolledAt ? formatRelativeTime(run.lastPolledAt) : '—'}
            {typeof run.pollAttempts === 'number' && run.pollAttempts > 0
              ? ` (attempt ${run.pollAttempts})`
              : ''}
          </Row>
          <Row k="Created">{formatRelativeTime(run.createdAt)}</Row>
          <Row k="Updated">{formatRelativeTime(run.updatedAt)}</Row>
        </dl>
      </div>

      {showRestore && <RestoreModal backup={run} onClose={() => setShowRestore(false)} />}
    </div>
  );
}

function Stat({
  icon: Icon,
  label,
  value,
  tone = 'neutral',
}: {
  icon: React.ElementType;
  label: string;
  value: string;
  tone?: 'neutral' | 'warning' | 'error';
}) {
  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Icon
          className={cn(
            'h-3.5 w-3.5',
            tone === 'warning' && 'text-status-warning',
            tone === 'error' && 'text-status-error',
          )}
        />
        {label}
      </div>
      <p
        className={cn(
          'mt-2 text-2xl font-semibold tabular-nums',
          tone === 'warning' && 'text-status-warning',
          tone === 'error' && 'text-status-error',
          tone === 'neutral' && 'text-foreground',
        )}
      >
        {value}
      </p>
    </div>
  );
}

function Row({ k, mono, children }: { k: string; mono?: boolean; children: React.ReactNode }) {
  return (
    <>
      <dt className="text-xs text-muted-foreground self-center">{k}</dt>
      <dd className={cn('text-sm text-foreground break-all', mono && 'font-mono')}>{children}</dd>
    </>
  );
}
