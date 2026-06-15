'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
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
  Database,
} from 'lucide-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { queryKeys } from '@/lib/hooks';
import {
  listQueues,
  listDLQ,
  retryDLQTask,
  discardDLQTask,
  listTaskOutbox,
  retryTaskOutbox,
  type QueueSummary,
  type DLQEntry,
  type TaskOutboxEntry,
  type TaskOutboxStatus,
} from '@/lib/api/admin-operations';

function OperationsBody() {
  const qc = useQueryClient();

  const queues = useQuery({
    queryKey: queryKeys.adminOperations.queues,
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
  const [outboxStatus, setOutboxStatus] = useState<TaskOutboxStatus | ''>('dead');

  const dlq = useQuery({
    queryKey: queryKeys.adminOperations.dlq(activeQueue),
    queryFn: () => listDLQ(activeQueue),
    enabled: !!activeQueue,
    refetchInterval: 10_000,
  });

  const outbox = useQuery({
    queryKey: queryKeys.adminOperations.outbox(outboxStatus),
    queryFn: () => listTaskOutbox(outboxStatus),
    refetchInterval: 10_000,
  });

  const retry = useMutation({
    mutationFn: ({ queue, id }: { queue: string; id: string }) => retryDLQTask(queue, id),
    onSuccess: (_, vars) => {
      toastSuccess(`Retry dispatched (${vars.id.slice(0, 8)}…)`);
      qc.invalidateQueries({ queryKey: queryKeys.adminOperations.dlq(vars.queue) });
      qc.invalidateQueries({ queryKey: queryKeys.adminOperations.queues });
    },
    onError: (e) => toastApiError('Retry failed', e),
  });
  const discard = useMutation({
    mutationFn: ({ queue, id }: { queue: string; id: string }) => discardDLQTask(queue, id),
    onSuccess: (_, vars) => {
      toastSuccess(`Discarded (${vars.id.slice(0, 8)}…)`);
      qc.invalidateQueries({ queryKey: queryKeys.adminOperations.dlq(vars.queue) });
      qc.invalidateQueries({ queryKey: queryKeys.adminOperations.queues });
    },
    onError: (e) => toastApiError('Discard failed', e),
  });
  const retryOutbox = useMutation({
    mutationFn: retryTaskOutbox,
    onSuccess: (row) => {
      toastSuccess(`Task outbox row queued (${row.id.slice(0, 8)}…)`);
      qc.invalidateQueries({ queryKey: queryKeys.adminOperations.outbox(outboxStatus) });
    },
    onError: (e) => toastApiError('Outbox retry failed', e),
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

      <section className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="text-sm font-medium text-foreground inline-flex items-center gap-2">
              <Database className="h-4 w-4" />
              Task outbox
            </h2>
            <p className="text-xs text-muted-foreground mt-1">
              Durable DB task intents waiting for Redis delivery or operator retry.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <select
              value={outboxStatus}
              onChange={(e) => setOutboxStatus(e.target.value as TaskOutboxStatus | '')}
              className="h-8 rounded border border-border bg-background px-2 text-xs"
              title="Filter task outbox rows"
            >
              <option value="dead">Dead</option>
              <option value="failed">Failed</option>
              <option value="pending">Pending</option>
              <option value="delivering">Delivering</option>
              <option value="delivered">Delivered</option>
              <option value="">All</option>
            </select>
            <button
              type="button"
              onClick={() => outbox.refetch()}
              className="inline-flex items-center gap-1.5 h-7 px-2 rounded text-xs border border-border hover:bg-accent"
              title="Refresh task outbox"
            >
              <RefreshCw className={`h-3 w-3 ${outbox.isFetching ? 'animate-spin' : ''}`} /> Refresh
            </button>
          </div>
        </div>
        <TaskOutboxTable
          loading={outbox.isLoading}
          rows={outbox.data?.data ?? []}
          status={outboxStatus}
          onRetry={(id) => retryOutbox.mutate(id)}
          pendingRetry={retryOutbox.isPending}
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
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide">
          <TableRow>
            <TableHead className="px-3 py-2">Name</TableHead>
            <TableHead className="px-3 py-2 text-right">Pending</TableHead>
            <TableHead className="px-3 py-2 text-right">Active</TableHead>
            <TableHead className="px-3 py-2 text-right">Scheduled</TableHead>
            <TableHead className="px-3 py-2 text-right">Retry</TableHead>
            <TableHead className="px-3 py-2 text-right">DLQ</TableHead>
            <TableHead className="px-3 py-2 text-right">Completed</TableHead>
            <TableHead className="px-3 py-2">State</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => {
            const isActive = r.name === activeQueue;
            const isStuck = r.archived > 0;
            return (
              <TableRow
                key={r.name}
                onClick={() => onSelect(r.name)}
                className={
                  'border-t border-border cursor-pointer transition-colors ' +
                  (isActive ? 'bg-primary/5' : 'hover:bg-muted/40')
                }
              >
                <TableCell className="px-3 py-2 font-mono">{r.name}</TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums">{r.pending}</TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums">{r.active}</TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums">{r.scheduled}</TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums">{r.retry}</TableCell>
                <TableCell
                  className={
                    'px-3 py-2 text-right tabular-nums ' + (isStuck ? 'text-red-600 font-medium' : '')
                  }
                >
                  {r.archived}
                </TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                  {r.completed}
                </TableCell>
                <TableCell className="px-3 py-2">
                  {r.paused ? (
                    <span className="inline-flex items-center gap-1 text-xs text-amber-600">
                      <AlertTriangle className="h-3 w-3" /> paused
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-xs text-emerald-600">
                      <CheckCircle2 className="h-3 w-3" /> running
                    </span>
                  )}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
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
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide">
          <TableRow>
            <TableHead className="px-3 py-2">Task type</TableHead>
            <TableHead className="px-3 py-2">ID</TableHead>
            <TableHead className="px-3 py-2 text-right">Retries</TableHead>
            <TableHead className="px-3 py-2">Last error</TableHead>
            <TableHead className="px-3 py-2">Failed at</TableHead>
            <TableHead className="px-3 py-2 text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.id} className="border-t border-border hover:bg-muted/40 align-top">
              <TableCell className="px-3 py-2 font-mono text-xs">{row.type}</TableCell>
              <TableCell className="px-3 py-2 font-mono text-[11px] text-muted-foreground">
                {row.id.length > 16 ? row.id.slice(0, 16) + '…' : row.id}
              </TableCell>
              <TableCell className="px-3 py-2 text-right tabular-nums">{row.retried}</TableCell>
              <TableCell className="px-3 py-2 text-xs text-red-600 max-w-md truncate" title={row.last_err}>
                {row.last_err || '—'}
              </TableCell>
              <TableCell className="px-3 py-2 text-xs text-muted-foreground">
                {row.last_failed_at ? new Date(row.last_failed_at).toLocaleString() : '—'}
              </TableCell>
              <TableCell className="px-3 py-2 text-right">
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
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function TaskOutboxTable({
  loading,
  rows,
  status,
  onRetry,
  pendingRetry,
}: {
  loading: boolean;
  rows: TaskOutboxEntry[];
  status: TaskOutboxStatus | '';
  onRetry: (id: string) => void;
  pendingRetry: boolean;
}) {
  if (loading && rows.length === 0) {
    return (
      <div className="flex items-center justify-center h-24 text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin mr-2" /> Loading task outbox…
      </div>
    );
  }
  if (rows.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        No {status || 'matching'} task outbox rows.
      </div>
    );
  }
  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <Table className="w-full text-sm">
        <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide">
          <TableRow>
            <TableHead className="px-3 py-2">Task type</TableHead>
            <TableHead className="px-3 py-2">Status</TableHead>
            <TableHead className="px-3 py-2">Queue</TableHead>
            <TableHead className="px-3 py-2 text-right">Attempts</TableHead>
            <TableHead className="px-3 py-2">Next attempt</TableHead>
            <TableHead className="px-3 py-2">Last error</TableHead>
            <TableHead className="px-3 py-2 text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.id} className="border-t border-border hover:bg-muted/40 align-top">
              <TableCell className="px-3 py-2">
                <div className="font-mono text-xs">{row.task_type}</div>
                {row.dedupe_key && (
                  <div className="mt-1 max-w-xs truncate font-mono text-[11px] text-muted-foreground" title={row.dedupe_key}>
                    {row.dedupe_key}
                  </div>
                )}
              </TableCell>
              <TableCell className="px-3 py-2">
                <span className={taskOutboxStatusClass(row.status)}>{row.status}</span>
              </TableCell>
              <TableCell className="px-3 py-2 font-mono text-xs">{row.queue_name}</TableCell>
              <TableCell className="px-3 py-2 text-right tabular-nums">
                {row.attempt_count}/{row.max_delivery_attempts}
              </TableCell>
              <TableCell className="px-3 py-2 text-xs text-muted-foreground">
                {row.next_attempt_at ? new Date(row.next_attempt_at).toLocaleString() : '—'}
              </TableCell>
              <TableCell className="px-3 py-2 max-w-md truncate text-xs text-red-600" title={row.last_error || ''}>
                {row.last_error || '—'}
              </TableCell>
              <TableCell className="px-3 py-2 text-right">
                <button
                  onClick={() => onRetry(row.id)}
                  disabled={pendingRetry || row.status === 'delivered'}
                  className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs border border-border hover:bg-muted disabled:opacity-50"
                  title="Move this task outbox row back to pending"
                >
                  <RotateCw className="h-3 w-3" /> Retry
                </button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function taskOutboxStatusClass(status: TaskOutboxStatus) {
  const base = 'inline-flex rounded px-1.5 py-0.5 text-xs font-medium';
  switch (status) {
    case 'dead':
      return `${base} bg-red-500/10 text-red-600`;
    case 'failed':
      return `${base} bg-amber-500/10 text-amber-600`;
    case 'delivered':
      return `${base} bg-emerald-500/10 text-emerald-600`;
    case 'delivering':
      return `${base} bg-blue-500/10 text-blue-600`;
    default:
      return `${base} bg-muted text-muted-foreground`;
  }
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
