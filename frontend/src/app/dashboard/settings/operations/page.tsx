'use client';

/**
 * Operations admin tab (T28b) — surface the asynq queue state + DLQ so on-call
 * can answer "why isn't anything reconciling?" from the UI instead of curl /
 * shelling into a worker pod.
 *
 * Two panels:
 *   1. Queues — depth per queue, refreshed every 5s.
 *   2. Dead-letter — failed tasks per queue with Retry / Discard actions.
 *
 * The retry / discard buttons hit POST /admin/queues/{q}/dlq/{id}/retry/ and
 * DELETE /admin/queues/{q}/dlq/{id}/. Both audited server-side.
 */

import { useState, useMemo } from 'react';
import Link from 'next/link';
import {
  ArrowLeft,
  Loader2,
  RefreshCw,
  RotateCw,
  Trash2,
  Activity,
  AlertTriangle,
  CheckCircle2,
} from 'lucide-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  listQueues,
  listDLQ,
  retryDLQTask,
  discardDLQTask,
  type QueueSummary,
  type DLQEntry,
} from '@/lib/api/admin-operations';

const qk = {
  queues: () => ['admin', 'queues'] as const,
  dlq: (q: string) => ['admin', 'queues', q, 'dlq'] as const,
};

function OperationsBody() {
  const qc = useQueryClient();

  const queues = useQuery({
    queryKey: qk.queues(),
    queryFn: listQueues,
    refetchInterval: 5_000,
    refetchIntervalInBackground: false,
  });

  // Default to the first queue with non-zero archived count, falling back to
  // the first queue overall so the DLQ panel renders something meaningful on
  // first paint without forcing the operator to click around.
  const queueNames = useMemo(() => (queues.data ?? []).map((q) => q.name), [queues.data]);
  const defaultDLQ = useMemo(() => {
    const withArchived = (queues.data ?? []).find((q) => q.archived > 0);
    return withArchived?.name ?? queueNames[0] ?? '';
  }, [queues.data, queueNames]);
  const [selectedQueue, setSelectedQueue] = useState<string>('');
  const activeQueue = selectedQueue || defaultDLQ;

  const dlq = useQuery({
    queryKey: qk.dlq(activeQueue),
    queryFn: () => listDLQ(activeQueue),
    enabled: !!activeQueue,
    refetchInterval: 10_000,
  });

  const retry = useMutation({
    mutationFn: ({ queue, id }: { queue: string; id: string }) => retryDLQTask(queue, id),
    onSuccess: (_, vars) => {
      toast.success(`Retry dispatched (${vars.id.slice(0, 8)}…)`);
      qc.invalidateQueries({ queryKey: qk.dlq(vars.queue) });
      qc.invalidateQueries({ queryKey: qk.queues() });
    },
    onError: (e) => toast.error(`Retry failed: ${(e as Error).message}`),
  });
  const discard = useMutation({
    mutationFn: ({ queue, id }: { queue: string; id: string }) => discardDLQTask(queue, id),
    onSuccess: (_, vars) => {
      toast.success(`Discarded (${vars.id.slice(0, 8)}…)`);
      qc.invalidateQueries({ queryKey: qk.dlq(vars.queue) });
      qc.invalidateQueries({ queryKey: qk.queues() });
    },
    onError: (e) => toast.error(`Discard failed: ${(e as Error).message}`),
  });

  return (
    <div className="max-w-5xl mx-auto space-y-6">
      <Link
        href="/dashboard/settings"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Settings
      </Link>

      <div>
        <h1 className="text-2xl font-semibold flex items-center gap-2">
          <Activity className="h-5 w-5" /> Operations
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Live view of the asynq worker queues + DLQ. Audited; superuser-only.
        </p>
      </div>

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-foreground">Queues</h2>
          <button
            type="button"
            onClick={() => queues.refetch()}
            className="inline-flex items-center gap-1.5 h-7 px-2 rounded text-xs border border-border hover:bg-accent"
            title="Refresh now"
          >
            <RefreshCw className={`h-3 w-3 ${queues.isFetching ? 'animate-spin' : ''}`} /> Refresh
          </button>
        </div>
        <QueueTable
          loading={queues.isLoading}
          rows={queues.data ?? []}
          activeQueue={activeQueue}
          onSelect={setSelectedQueue}
        />
      </section>

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-foreground">
            Dead-letter
            {activeQueue && (
              <span className="ml-2 text-xs text-muted-foreground font-mono">— {activeQueue}</span>
            )}
          </h2>
          <button
            type="button"
            onClick={() => dlq.refetch()}
            disabled={!activeQueue}
            className="inline-flex items-center gap-1.5 h-7 px-2 rounded text-xs border border-border hover:bg-accent disabled:opacity-50"
            title="Refresh DLQ"
          >
            <RefreshCw className={`h-3 w-3 ${dlq.isFetching ? 'animate-spin' : ''}`} /> Refresh
          </button>
        </div>
        <DLQTable
          loading={dlq.isLoading && !!activeQueue}
          queue={activeQueue}
          rows={dlq.data?.dlq ?? []}
          onRetry={(id) => retry.mutate({ queue: activeQueue, id })}
          onDiscard={(id) => discard.mutate({ queue: activeQueue, id })}
          pendingRetry={retry.isPending}
          pendingDiscard={discard.isPending}
        />
      </section>
    </div>
  );
}

function QueueTable({
  loading,
  rows,
  activeQueue,
  onSelect,
}: {
  loading: boolean;
  rows: QueueSummary[];
  activeQueue: string;
  onSelect: (queue: string) => void;
}) {
  if (loading && rows.length === 0) {
    return (
      <div className="flex items-center justify-center h-24 text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin mr-2" /> Loading queues…
      </div>
    );
  }
  if (rows.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        No queues registered. The worker may not be running.
      </div>
    );
  }
  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-muted/50 text-left text-xs uppercase tracking-wide">
          <tr>
            <th className="px-3 py-2">Name</th>
            <th className="px-3 py-2 text-right">Pending</th>
            <th className="px-3 py-2 text-right">Active</th>
            <th className="px-3 py-2 text-right">Scheduled</th>
            <th className="px-3 py-2 text-right">Retry</th>
            <th className="px-3 py-2 text-right">DLQ</th>
            <th className="px-3 py-2 text-right">Completed</th>
            <th className="px-3 py-2">State</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const isActive = r.name === activeQueue;
            const isStuck = r.archived > 0;
            return (
              <tr
                key={r.name}
                onClick={() => onSelect(r.name)}
                className={
                  'border-t border-border cursor-pointer transition-colors ' +
                  (isActive ? 'bg-primary/5' : 'hover:bg-muted/40')
                }
              >
                <td className="px-3 py-2 font-mono">{r.name}</td>
                <td className="px-3 py-2 text-right tabular-nums">{r.pending}</td>
                <td className="px-3 py-2 text-right tabular-nums">{r.active}</td>
                <td className="px-3 py-2 text-right tabular-nums">{r.scheduled}</td>
                <td className="px-3 py-2 text-right tabular-nums">{r.retry}</td>
                <td
                  className={
                    'px-3 py-2 text-right tabular-nums ' + (isStuck ? 'text-red-600 font-medium' : '')
                  }
                >
                  {r.archived}
                </td>
                <td className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                  {r.completed}
                </td>
                <td className="px-3 py-2">
                  {r.paused ? (
                    <span className="inline-flex items-center gap-1 text-xs text-amber-600">
                      <AlertTriangle className="h-3 w-3" /> paused
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-xs text-emerald-600">
                      <CheckCircle2 className="h-3 w-3" /> running
                    </span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function DLQTable({
  loading,
  queue,
  rows,
  onRetry,
  onDiscard,
  pendingRetry,
  pendingDiscard,
}: {
  loading: boolean;
  queue: string;
  rows: DLQEntry[];
  onRetry: (id: string) => void;
  onDiscard: (id: string) => void;
  pendingRetry: boolean;
  pendingDiscard: boolean;
}) {
  if (!queue) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        Select a queue above to inspect its dead-letter contents.
      </div>
    );
  }
  if (loading) {
    return (
      <div className="flex items-center justify-center h-24 text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin mr-2" /> Loading DLQ…
      </div>
    );
  }
  if (rows.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        No archived tasks in <code className="font-mono">{queue}</code>.
      </div>
    );
  }
  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <table className="w-full text-sm">
        <thead className="bg-muted/50 text-left text-xs uppercase tracking-wide">
          <tr>
            <th className="px-3 py-2">Task type</th>
            <th className="px-3 py-2">ID</th>
            <th className="px-3 py-2 text-right">Retries</th>
            <th className="px-3 py-2">Last error</th>
            <th className="px-3 py-2">Failed at</th>
            <th className="px-3 py-2 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.id} className="border-t border-border hover:bg-muted/40 align-top">
              <td className="px-3 py-2 font-mono text-xs">{row.type}</td>
              <td className="px-3 py-2 font-mono text-[11px] text-muted-foreground">
                {row.id.length > 16 ? row.id.slice(0, 16) + '…' : row.id}
              </td>
              <td className="px-3 py-2 text-right tabular-nums">{row.retried}</td>
              <td className="px-3 py-2 text-xs text-red-600 max-w-md truncate" title={row.last_err}>
                {row.last_err || '—'}
              </td>
              <td className="px-3 py-2 text-xs text-muted-foreground">
                {row.last_failed_at ? new Date(row.last_failed_at).toLocaleString() : '—'}
              </td>
              <td className="px-3 py-2 text-right">
                <div className="inline-flex items-center gap-1">
                  <button
                    onClick={() => onRetry(row.id)}
                    disabled={pendingRetry}
                    className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs border border-border hover:bg-muted disabled:opacity-50"
                    title="Move this task back to pending"
                  >
                    <RotateCw className="h-3 w-3" /> Retry
                  </button>
                  <button
                    onClick={() => onDiscard(row.id)}
                    disabled={pendingDiscard}
                    className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs border border-border text-red-600 hover:bg-red-500/10 disabled:opacity-50"
                    title="Permanently delete this task"
                  >
                    <Trash2 className="h-3 w-3" /> Discard
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default function OperationsPage() {
  return (
    <SettingsAuthGate>
      <div className="p-6">
        <OperationsBody />
      </div>
    </SettingsAuthGate>
  );
}
