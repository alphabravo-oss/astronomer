import { Select } from "@/components/ui/select";
import { createFileRoute } from "@tanstack/react-router";
import { DataTable, type Column } from "@/components/ui/data-table";
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

import { useState, useMemo } from "react";
import { ResourceMasthead } from "@/components/ui/page";
import {
  RefreshCw,
  RotateCw,
  Trash2,
  Activity,
  AlertTriangle,
  CheckCircle2,
  Database,
} from "lucide-react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";
import {
  listQueues,
  listDLQ,
  getDLQOperation,
  retryDLQTask,
  discardDLQTask,
  listTaskOutbox,
  retryTaskOutbox,
  type QueueSummary,
  type DLQEntry,
  type TaskOutboxEntry,
  type TaskOutboxStatus,
} from "@/lib/api/admin-operations";
import { useOperationMutation } from "@/lib/hooks/operation-mutation";
import { QueryStates } from "@/components/ui/query-states";

function OperationsBody() {
  const qc = useQueryClient();

  const queues = useQuery({
    queryKey: queryKeys.adminOperations.queues,
    queryFn: ({ signal }) => listQueues(signal),
    refetchInterval: liveFallback(5_000),
    refetchIntervalInBackground: false,
  });

  // Default to the first queue with non-zero archived count, falling back to
  // the first queue overall so the DLQ panel renders something meaningful on
  // first paint without forcing the operator to click around.
  const queueNames = useMemo(
    () => (queues.data ?? []).map((q) => q.name),
    [queues.data],
  );
  const defaultDLQ = useMemo(() => {
    const withArchived = (queues.data ?? []).find((q) => q.archived > 0);
    return withArchived?.name ?? queueNames[0] ?? "";
  }, [queues.data, queueNames]);
  const [selectedQueue, setSelectedQueue] = useState<string>("");
  const activeQueue = selectedQueue || defaultDLQ;
  const [outboxStatus, setOutboxStatus] = useState<TaskOutboxStatus | "">(
    "dead",
  );

  const dlq = useQuery({
    queryKey: queryKeys.adminOperations.dlq(activeQueue),
    queryFn: ({ signal }) => listDLQ(activeQueue, signal),
    enabled: !!activeQueue,
    refetchInterval: liveFallback(10_000),
  });

  const outbox = useQuery({
    queryKey: queryKeys.adminOperations.outbox(outboxStatus),
    queryFn: ({ signal }) => listTaskOutbox(outboxStatus, signal),
    refetchInterval: liveFallback(10_000),
  });

  const retry = useOperationMutation({
    keyPrefix: "dlq-retry",
    submit: ({ queue, id }: { queue: string; id: string }, context) =>
      retryDLQTask(queue, id, context),
    read: getDLQOperation,
    mutation: {
      onSuccess: (_, vars) => {
        toastSuccess(`Retry completed (${vars.id.slice(0, 8)}…)`);
        qc.invalidateQueries({
          queryKey: queryKeys.adminOperations.dlq(vars.queue),
        });
        qc.invalidateQueries({ queryKey: queryKeys.adminOperations.queues });
      },
      onError: (e) => toastApiError("Retry failed", e),
    },
  });
  const discard = useOperationMutation({
    keyPrefix: "dlq-discard",
    submit: ({ queue, id }: { queue: string; id: string }, context) =>
      discardDLQTask(queue, id, context),
    read: getDLQOperation,
    mutation: {
      onSuccess: (_, vars) => {
        toastSuccess(`Discard completed (${vars.id.slice(0, 8)}…)`);
        qc.invalidateQueries({
          queryKey: queryKeys.adminOperations.dlq(vars.queue),
        });
        qc.invalidateQueries({ queryKey: queryKeys.adminOperations.queues });
      },
      onError: (e) => toastApiError("Discard failed", e),
    },
  });
  const retryOutbox = useMutation({
    mutationFn: (id: string) => retryTaskOutbox(id),
    onSuccess: (row) => {
      toastSuccess(`Task outbox row queued (${row.id.slice(0, 8)}…)`);
      qc.invalidateQueries({
        queryKey: queryKeys.adminOperations.outbox(outboxStatus),
      });
    },
    onError: (e) => toastApiError("Outbox retry failed", e),
  });

  return (
    <div className="space-y-6">
      <p className="sr-only" role="status" aria-live="polite">
        {retry.isPending
          ? `DLQ retry ${retry.operationState.phase}`
          : discard.isPending
            ? `DLQ discard ${discard.operationState.phase}`
            : ""}
      </p>
      <ResourceMasthead
        backTo="/dashboard/settings"
        backLabel="Back to Settings"
        title={
          <span className="inline-flex items-center gap-2">
            <Activity className="h-5 w-5" /> Operations
          </span>
        }
        description="Live view of the asynq worker queues + DLQ. Audited; superuser-only."
      />

      {queues.isError && (
        <QueryStates query={queues} permission="admin_operations:read">
          {() => null}
        </QueryStates>
      )}
      {dlq.isError && (
        <QueryStates query={dlq} permission="admin_operations:read">
          {() => null}
        </QueryStates>
      )}
      {outbox.isError && (
        <QueryStates query={outbox} permission="admin_operations:read">
          {() => null}
        </QueryStates>
      )}

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-foreground">Queues</h2>
          <button
            type="button"
            onClick={() => queues.refetch()}
            className="inline-flex items-center gap-1.5 h-7 px-2 rounded-sm text-xs border border-border hover:bg-accent"
            title="Refresh now"
          >
            <RefreshCw
              className={`h-3 w-3 ${queues.isFetching ? "animate-spin" : ""}`}
            />{" "}
            Refresh
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
              <span className="ml-2 text-xs text-muted-foreground font-mono">
                — {activeQueue}
              </span>
            )}
          </h2>
          <button
            type="button"
            onClick={() => dlq.refetch()}
            disabled={!activeQueue}
            className="inline-flex items-center gap-1.5 h-7 px-2 rounded-sm text-xs border border-border hover:bg-accent disabled:opacity-50"
            title="Refresh DLQ"
          >
            <RefreshCw
              className={`h-3 w-3 ${dlq.isFetching ? "animate-spin" : ""}`}
            />{" "}
            Refresh
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
              Durable DB task intents waiting for Redis delivery or operator
              retry.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Select
              value={outboxStatus}
              onChange={(e) =>
                setOutboxStatus(e.target.value as TaskOutboxStatus | "")
              }
              className="h-8 rounded-sm border border-border bg-background px-2 text-xs"
              title="Filter task outbox rows"
            >
              <option value="dead">Dead</option>
              <option value="failed">Failed</option>
              <option value="pending">Pending</option>
              <option value="delivering">Delivering</option>
              <option value="delivered">Delivered</option>
              <option value="">All</option>
            </Select>
            <button
              type="button"
              onClick={() => outbox.refetch()}
              className="inline-flex items-center gap-1.5 h-7 px-2 rounded-sm text-xs border border-border hover:bg-accent"
              title="Refresh task outbox"
            >
              <RefreshCw
                className={`h-3 w-3 ${outbox.isFetching ? "animate-spin" : ""}`}
              />{" "}
              Refresh
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

function queueColumns(activeQueue: string): Column<QueueSummary>[] {
  return [
    {
      key: "name",
      header: "Name",
      accessor: (r) => (
        <span
          className={
            "font-mono " +
            (r.name === activeQueue ? "font-semibold text-foreground" : "")
          }
        >
          {r.name === activeQueue ? "▸ " : ""}
          {r.name}
        </span>
      ),
      searchAccessor: (r) => r.name,
      sortAccessor: (r) => r.name,
    },
    {
      key: "pending",
      header: "Pending",
      accessor: (r) => <span className="tabular-nums">{r.pending}</span>,
      sortAccessor: (r) => r.pending,
      align: "right",
      width: "6rem",
    },
    {
      key: "active",
      header: "Active",
      accessor: (r) => <span className="tabular-nums">{r.active}</span>,
      sortAccessor: (r) => r.active,
      align: "right",
      width: "6rem",
    },
    {
      key: "scheduled",
      header: "Scheduled",
      accessor: (r) => <span className="tabular-nums">{r.scheduled}</span>,
      sortAccessor: (r) => r.scheduled,
      align: "right",
      width: "7rem",
    },
    {
      key: "retry",
      header: "Retry",
      accessor: (r) => <span className="tabular-nums">{r.retry}</span>,
      sortAccessor: (r) => r.retry,
      align: "right",
      width: "6rem",
    },
    {
      key: "dlq",
      header: "DLQ",
      accessor: (r) => (
        <span
          className={
            "tabular-nums " +
            (r.archived > 0 ? "text-status-error font-medium" : "")
          }
        >
          {r.archived}
        </span>
      ),
      sortAccessor: (r) => r.archived,
      align: "right",
      width: "6rem",
    },
    {
      key: "completed",
      header: "Completed",
      accessor: (r) => (
        <span className="tabular-nums text-muted-foreground">
          {r.completed}
        </span>
      ),
      sortAccessor: (r) => r.completed,
      align: "right",
      width: "7rem",
    },
    {
      key: "state",
      header: "State",
      accessor: (r) =>
        r.paused ? (
          <span className="inline-flex items-center gap-1 text-xs text-status-warning">
            <AlertTriangle className="h-3 w-3" /> paused
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-xs text-status-success">
            <CheckCircle2 className="h-3 w-3" /> running
          </span>
        ),
      searchAccessor: (r) => (r.paused ? "paused" : "running"),
      sortAccessor: (r) => (r.paused ? "paused" : "running"),
      filter: { label: "State" },
      width: "8rem",
    },
  ];
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
  const columns = useMemo(() => queueColumns(activeQueue), [activeQueue]);
  return (
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={(r) => r.name}
      density="compact"
      loading={loading}
      onRowClick={(r) => onSelect(r.name)}
      searchPlaceholder="Search queues..."
      emptyState={{
        title: "No queues registered",
        description: "The worker may not be running.",
      }}
    />
  );
}

function dlqColumns(
  onRetry: (id: string) => void,
  onDiscard: (id: string) => void,
  pendingRetry: boolean,
  pendingDiscard: boolean,
): Column<DLQEntry>[] {
  return [
    {
      key: "type",
      header: "Task type",
      accessor: (row) => <span className="font-mono text-xs">{row.type}</span>,
      searchAccessor: (row) => row.type,
      sortAccessor: (row) => row.type,
    },
    {
      key: "id",
      header: "ID",
      accessor: (row) => (
        <span className="font-mono text-[11px] text-muted-foreground">
          {row.id.length > 16 ? row.id.slice(0, 16) + "…" : row.id}
        </span>
      ),
      searchAccessor: (row) => row.id,
      sortAccessor: (row) => row.id,
    },
    {
      key: "retried",
      header: "Retries",
      accessor: (row) => <span className="tabular-nums">{row.retried}</span>,
      sortAccessor: (row) => row.retried,
      align: "right",
      width: "6rem",
    },
    {
      key: "last_err",
      header: "Last error",
      accessor: (row) => (
        <span
          className="text-xs text-status-error block max-w-md truncate"
          title={row.last_err}
        >
          {row.last_err || "—"}
        </span>
      ),
      searchAccessor: (row) => row.last_err,
    },
    {
      key: "last_failed_at",
      header: "Failed at",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.last_failed_at
            ? new Date(row.last_failed_at).toLocaleString()
            : "—"}
        </span>
      ),
      sortAccessor: (row) => row.last_failed_at || "",
      width: "12rem",
    },
    {
      key: "actions",
      header: "Actions",
      hideable: false,
      accessor: (row) => (
        <div className="inline-flex items-center gap-1">
          <button
            onClick={() => onRetry(row.id)}
            disabled={pendingRetry}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs border border-border hover:bg-muted disabled:opacity-50"
            title="Move this task back to pending"
          >
            <RotateCw className="h-3 w-3" /> Retry
          </button>
          <button
            onClick={() => onDiscard(row.id)}
            disabled={pendingDiscard}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs border border-border text-status-error hover:bg-status-error/10 disabled:opacity-50"
            title="Permanently delete this task"
          >
            <Trash2 className="h-3 w-3" /> Discard
          </button>
        </div>
      ),
      align: "right",
      width: "12rem",
    },
  ];
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
  const columns = useMemo(
    () => dlqColumns(onRetry, onDiscard, pendingRetry, pendingDiscard),
    [onRetry, onDiscard, pendingRetry, pendingDiscard],
  );
  if (!queue) {
    return (
      <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        Select a queue above to inspect its dead-letter contents.
      </div>
    );
  }
  return (
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={(row) => row.id}
      density="compact"
      loading={loading}
      searchPlaceholder="Search dead-letter tasks..."
      emptyState={{
        title: "No archived tasks",
        description: `Queue ${queue} has no dead-letter entries.`,
      }}
    />
  );
}

function taskOutboxColumns(
  onRetry: (id: string) => void,
  pendingRetry: boolean,
): Column<TaskOutboxEntry>[] {
  return [
    {
      key: "task_type",
      header: "Task type",
      accessor: (row) => (
        <div>
          <div className="font-mono text-xs">{row.task_type}</div>
          {row.dedupe_key && (
            <div
              className="mt-1 max-w-xs truncate font-mono text-[11px] text-muted-foreground"
              title={row.dedupe_key}
            >
              {row.dedupe_key}
            </div>
          )}
        </div>
      ),
      searchAccessor: (row) => `${row.task_type} ${row.dedupe_key ?? ""}`,
      sortAccessor: (row) => row.task_type,
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) => (
        <span className={taskOutboxStatusClass(row.status)}>
          {row.status}
        </span>
      ),
      searchAccessor: (row) => row.status,
      sortAccessor: (row) => row.status,
      filter: { label: "Status" },
      width: "9rem",
    },
    {
      key: "queue_name",
      header: "Queue",
      accessor: (row) => (
        <span className="font-mono text-xs">{row.queue_name}</span>
      ),
      searchAccessor: (row) => row.queue_name,
      sortAccessor: (row) => row.queue_name,
    },
    {
      key: "attempts",
      header: "Attempts",
      accessor: (row) => (
        <span className="tabular-nums">
          {row.attempt_count}/{row.max_delivery_attempts}
        </span>
      ),
      sortAccessor: (row) => row.attempt_count,
      align: "right",
      width: "7rem",
    },
    {
      key: "next_attempt_at",
      header: "Next attempt",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.next_attempt_at
            ? new Date(row.next_attempt_at).toLocaleString()
            : "—"}
        </span>
      ),
      sortAccessor: (row) => row.next_attempt_at || "",
      width: "12rem",
    },
    {
      key: "last_error",
      header: "Last error",
      accessor: (row) => (
        <span
          className="block max-w-md truncate text-xs text-status-error"
          title={row.last_error || ""}
        >
          {row.last_error || "—"}
        </span>
      ),
      searchAccessor: (row) => row.last_error || "",
    },
    {
      key: "actions",
      header: "Actions",
      hideable: false,
      accessor: (row) => (
        <button
          onClick={() => onRetry(row.id)}
          disabled={pendingRetry || row.status === "delivered"}
          className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs border border-border hover:bg-muted disabled:opacity-50"
          title="Move this task outbox row back to pending"
        >
          <RotateCw className="h-3 w-3" /> Retry
        </button>
      ),
      align: "right",
      width: "8rem",
    },
  ];
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
  status: TaskOutboxStatus | "";
  onRetry: (id: string) => void;
  pendingRetry: boolean;
}) {
  const columns = useMemo(
    () => taskOutboxColumns(onRetry, pendingRetry),
    [onRetry, pendingRetry],
  );
  return (
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={(row) => row.id}
      density="compact"
      loading={loading}
      searchPlaceholder="Search task outbox..."
      emptyState={{
        title: "No matching task outbox rows",
        description: `No ${status || "matching"} task outbox rows.`,
      }}
    />
  );
}

function taskOutboxStatusClass(status: TaskOutboxStatus) {
  const base = "inline-flex rounded-sm px-1.5 py-0.5 text-xs font-medium";
  switch (status) {
    case "dead":
      return `${base} bg-status-error/10 text-status-error`;
    case "failed":
      return `${base} bg-status-warning/10 text-status-warning`;
    case "delivered":
      return `${base} bg-status-success/10 text-status-success`;
    case "delivering":
      return `${base} bg-status-info/10 text-status-info`;
    default:
      return `${base} bg-muted text-muted-foreground`;
  }
}

function OperationsPage() {
  return (
    <SettingsAuthGate>
      <div className="p-6">
        <OperationsBody />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/operations/")({
  component: OperationsPage,
});
