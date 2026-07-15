import { createFileRoute } from '@tanstack/react-router';
/**
 * Restore detail page. Renders one Velero Restore CR — phase, items
 * restored, warnings/errors, and any namespace mapping the user supplied
 * when starting the restore. Linkable from the run detail page after a
 * restore is initiated.
 */

import { useParams, useRouter } from '@/lib/navigation';
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Clock,
  Database,
  Server,
  XCircle,
} from 'lucide-react';
import { useClusters } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { b2Keys, useB2Restore, useB2Run } from '@/components/backups/hooks';
import { PhaseBadge } from '@/components/backups/phase-badge';
import { cn, formatRelativeTime } from '@/lib/utils';

function RestoreDetailPage() {
  const params = useParams();
  const router = useRouter();
  const restoreId = params.restoreId as string;

  const restoreQ = useB2Restore(restoreId);
  const restore = restoreQ.data;
  const runQ = useB2Run(restore?.backupId ?? '');
  const clustersQ = useClusters({ pageSize: 100 });

  useLiveQueryInvalidation(
    ['cluster.k8s_changed'],
    [b2Keys.restoreDetail(restoreId), b2Keys.restores()],
  );

  if (restoreQ.isLoading) {
    return (
      <div className="space-y-3 animate-pulse">
        <div className="h-6 w-48 rounded bg-muted" />
        <div className="h-32 rounded bg-muted" />
      </div>
    );
  }
  if (!restore) {
    return (
      <div className="rounded-xl border border-border bg-card p-6 text-sm text-muted-foreground">
        Restore not found.
      </div>
    );
  }

  const inProgress = restore.phase === 'InProgress' || restore.status === 'in_progress';
  const partial = restore.phase === 'PartiallyFailed';
  const failed =
    restore.phase === 'Failed' ||
    restore.phase === 'FailedValidation' ||
    restore.status === 'failed';

  const cluster = clustersQ.data?.data.find((c) => c.id === restore.clusterId);
  const sourceRun = runQ.data;
  const mapping = restore.namespaceMapping ?? {};
  const mappingEntries = Object.entries(mapping);
  const includedRestore = restore.includedNamespaces ?? [];

  return (
    <div className="space-y-6">
      <div>
        <button
          onClick={() => router.push('/dashboard/backups?tab=runs')}
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors mb-2"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to backups
        </button>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight break-all">
          {restore.veleroRestoreName ?? 'Restore'}
        </h1>
        <p className="text-sm text-muted-foreground mt-1 font-mono break-all">{restore.id}</p>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <div className="flex items-center justify-between gap-4 flex-wrap">
          <PhaseBadge phase={restore.phase} status={restore.status} size="lg" />
          <div className="flex items-center gap-4 text-xs text-muted-foreground">
            <span className="inline-flex items-center gap-1">
              <Clock className="h-3 w-3" />
              Started {restore.startedAt ? formatRelativeTime(restore.startedAt) : '—'}
            </span>
            {restore.completedAt && (
              <span className="inline-flex items-center gap-1">
                <CheckCircle2 className="h-3 w-3" />
                Completed {formatRelativeTime(restore.completedAt)}
              </span>
            )}
          </div>
        </div>

        {inProgress && (
          <div className="h-2 rounded-full bg-muted overflow-hidden">
            <div className="h-full bg-primary animate-pulse" style={{ width: '60%' }} />
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
        <Stat
          icon={Database}
          label="Items restored"
          value={typeof restore.itemsRestored === 'number' ? String(restore.itemsRestored) : '—'}
        />
        <Stat
          icon={AlertTriangle}
          label="Warnings"
          value={typeof restore.warnings === 'number' ? String(restore.warnings) : '—'}
          tone={(restore.warnings ?? 0) > 0 ? 'warning' : 'neutral'}
        />
        <Stat
          icon={XCircle}
          label="Errors"
          value={typeof restore.errors === 'number' ? String(restore.errors) : '—'}
          tone={(restore.errors ?? 0) > 0 ? 'error' : 'neutral'}
        />
      </div>

      {(failed || partial) && restore.errorMessage && (
        <div
          className={cn(
            'rounded-xl border p-4 space-y-1',
            failed
              ? 'border-status-error/30 bg-status-error/5'
              : 'border-status-warning/30 bg-status-warning/5',
          )}
        >
          <div className="flex items-center gap-2">
            {failed ? (
              <XCircle className="h-4 w-4 text-status-error" />
            ) : (
              <AlertTriangle className="h-4 w-4 text-status-warning" />
            )}
            <span className="text-sm font-medium text-foreground">
              {failed ? 'Restore failed' : 'Restore partially completed'}
            </span>
          </div>
          <pre className="text-xs text-muted-foreground font-mono whitespace-pre-wrap break-all">
            {restore.errorMessage}
          </pre>
        </div>
      )}

      <div className="rounded-xl border border-border bg-card p-6 space-y-3">
        <h2 className="text-sm font-semibold text-foreground">Source backup</h2>
        {sourceRun ? (
          <button
            onClick={() => router.push(`/dashboard/backups/runs/${sourceRun.id}`)}
            className="inline-flex items-center gap-2 text-sm text-foreground hover:text-primary transition-colors"
          >
            <Database className="h-4 w-4" />
            {sourceRun.name}
          </button>
        ) : (
          <span className="text-sm text-muted-foreground font-mono break-all">{restore.backupId}</span>
        )}
      </div>

      <div className="rounded-xl border border-border bg-card p-6 space-y-3">
        <h2 className="text-sm font-semibold text-foreground">Restore options</h2>
        <dl className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-2 text-sm">
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
              restore.clusterId ?? '—'
            )}
          </Row>
          <Row k="Velero namespace" mono>
            {restore.veleroNamespace ?? '—'}
          </Row>
          <Row k="Velero CR" mono>
            {restore.veleroRestoreName ?? '—'}
          </Row>
          <Row k="Included namespaces" mono>
            {includedRestore.length > 0 ? includedRestore.join(', ') : 'all'}
          </Row>
          <Row k="Last polled">
            {restore.lastPolledAt ? formatRelativeTime(restore.lastPolledAt) : '—'}
            {typeof restore.pollAttempts === 'number' && restore.pollAttempts > 0
              ? ` (attempt ${restore.pollAttempts})`
              : ''}
          </Row>
          <Row k="Created">{formatRelativeTime(restore.createdAt)}</Row>
        </dl>

        {mappingEntries.length > 0 && (
          <div className="space-y-1.5">
            <p className="text-xs text-muted-foreground">Namespace mapping</p>
            <ul className="space-y-1">
              {mappingEntries.map(([from, to]) => (
                <li key={from} className="text-xs font-mono text-muted-foreground">
                  <span className="text-foreground">{from}</span> →{' '}
                  <span className="text-foreground">{to}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
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

export const Route = createFileRoute('/dashboard/backups/restores/$restoreId/')({
  component: RestoreDetailPage,
});
