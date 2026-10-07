import { AlertTriangle, CheckCircle2, RotateCw, Trash2 } from "lucide-react";
import type { Column } from "@/components/ui/data-table";
import { Tooltip } from "@/components/ui/tooltip";
import { BareButton } from "@/components/form/bare-button";
import { RelativeTime } from "@/components/admin/table-cells";
import type {
  QueueSummary,
  DLQEntry,
  TaskOutboxEntry,
  TaskOutboxStatus,
} from "@/lib/api/admin-operations";

export function queueColumns(activeQueue: string): Column<QueueSummary>[] {
  return [
    {
      key: "name",
      header: "Name",
      kind: "name",
      minSize: 160,
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
      kind: "count",
      accessor: (r) => <span className="tabular-nums">{r.pending}</span>,
      sortAccessor: (r) => r.pending,
    },
    {
      key: "active",
      header: "Active",
      kind: "count",
      accessor: (r) => <span className="tabular-nums">{r.active}</span>,
      sortAccessor: (r) => r.active,
    },
    {
      key: "scheduled",
      header: "Scheduled",
      kind: "count",
      accessor: (r) => <span className="tabular-nums">{r.scheduled}</span>,
      sortAccessor: (r) => r.scheduled,
    },
    {
      key: "retry",
      header: "Retry",
      kind: "count",
      accessor: (r) => <span className="tabular-nums">{r.retry}</span>,
      sortAccessor: (r) => r.retry,
    },
    {
      key: "dlq",
      header: "DLQ",
      kind: "count",
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
    },
    {
      key: "completed",
      header: "Completed",
      kind: "count",
      accessor: (r) => (
        <span className="tabular-nums text-muted-foreground">
          {r.completed}
        </span>
      ),
      sortAccessor: (r) => r.completed,
    },
    {
      key: "state",
      header: "State",
      kind: "status",
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
    },
  ];
}

export function dlqColumns(
  onRetry: (id: string) => void,
  onDiscard: (id: string) => void,
  pendingRetry: boolean,
  pendingDiscard: boolean,
): Column<DLQEntry>[] {
  return [
    {
      key: "type",
      header: "Task type",
      kind: "text",
      size: 160,
      minSize: 130,
      accessor: (row) => <span className="font-mono text-xs">{row.type}</span>,
      searchAccessor: (row) => row.type,
      sortAccessor: (row) => row.type,
    },
    {
      key: "id",
      header: "ID",
      kind: "id",
      size: 168,
      minSize: 128,
      accessor: (row) => (
        <span className="font-mono text-11 text-muted-foreground">
          {row.id.length > 16 ? row.id.slice(0, 16) + "…" : row.id}
        </span>
      ),
      searchAccessor: (row) => row.id,
      sortAccessor: (row) => row.id,
    },
    {
      key: "retried",
      header: "Retries",
      kind: "count",
      accessor: (row) => <span className="tabular-nums">{row.retried}</span>,
      sortAccessor: (row) => row.retried,
    },
    {
      key: "last_err",
      header: "Last error",
      kind: "text",
      grow: true,
      minSize: 220,
      maxSize: 720,
      accessor: (row) => (
        <span className="text-xs text-status-error">{row.last_err || "—"}</span>
      ),
      searchAccessor: (row) => row.last_err,
    },
    {
      key: "last_failed_at",
      header: "Failed at",
      kind: "age",
      size: 119,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          <RelativeTime value={row.last_failed_at} fallback="—" />
        </span>
      ),
      sortAccessor: (row) => row.last_failed_at || "",
    },
    {
      key: "actions",
      header: "Actions",
      kind: "actions",
      size: 208,
      maxSize: 208,
      hideable: false,
      accessor: (row) => (
        <div className="inline-flex items-center gap-1">
          <BareButton
            onClick={() => onRetry(row.id)}
            disabled={pendingRetry}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs border border-border hover:bg-muted disabled:opacity-50"
            tooltip="Move this task back to pending"
          >
            <RotateCw className="h-3 w-3" /> Retry
          </BareButton>
          <BareButton
            onClick={() => onDiscard(row.id)}
            disabled={pendingDiscard}
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs border border-border text-status-error hover:bg-status-error/10 disabled:opacity-50"
            tooltip="Permanently delete this task"
          >
            <Trash2 className="h-3 w-3" /> Discard
          </BareButton>
        </div>
      ),
    },
  ];
}

export function taskOutboxColumns(
  onRetry: (id: string) => void,
  pendingRetry: boolean,
): Column<TaskOutboxEntry>[] {
  return [
    {
      key: "task_type",
      header: "Task type",
      kind: "name",
      grow: false,
      size: 200,
      minSize: 160,
      accessor: (row) => (
        <div>
          <div className="font-mono text-xs">{row.task_type}</div>
          {row.dedupe_key && (
            <Tooltip content={row.dedupe_key}>
              <div className="mt-1 max-w-xs truncate font-mono text-11 text-muted-foreground">
                {row.dedupe_key}
              </div>
            </Tooltip>
          )}
        </div>
      ),
      searchAccessor: (row) => `${row.task_type} ${row.dedupe_key ?? ""}`,
      sortAccessor: (row) => row.task_type,
    },
    {
      key: "status",
      header: "Status",
      kind: "status",
      accessor: (row) => (
        <span className={taskOutboxStatusClass(row.status)}>{row.status}</span>
      ),
      searchAccessor: (row) => row.status,
      sortAccessor: (row) => row.status,
      filter: { label: "Status" },
    },
    {
      key: "queue_name",
      header: "Queue",
      kind: "text",
      size: 120,
      minSize: 100,
      accessor: (row) => (
        <span className="font-mono text-xs">{row.queue_name}</span>
      ),
      searchAccessor: (row) => row.queue_name,
      sortAccessor: (row) => row.queue_name,
    },
    {
      key: "attempts",
      header: "Attempts",
      kind: "count",
      accessor: (row) => (
        <span className="tabular-nums">
          {row.attempt_count}/{row.max_delivery_attempts}
        </span>
      ),
      sortAccessor: (row) => row.attempt_count,
    },
    {
      key: "next_attempt_at",
      header: "Next attempt",
      kind: "age",
      size: 140,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          <RelativeTime value={row.next_attempt_at} fallback="—" />
        </span>
      ),
      sortAccessor: (row) => row.next_attempt_at || "",
    },
    {
      key: "last_error",
      header: "Last error",
      kind: "text",
      grow: true,
      minSize: 220,
      maxSize: 720,
      accessor: (row) => (
        <span className="text-xs text-status-error">
          {row.last_error || "—"}
        </span>
      ),
      searchAccessor: (row) => row.last_error || "",
    },
    {
      key: "actions",
      header: "Actions",
      kind: "actions",
      size: 112,
      maxSize: 112,
      hideable: false,
      accessor: (row) => (
        <BareButton
          onClick={() => onRetry(row.id)}
          disabled={pendingRetry || row.status === "delivered"}
          className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs border border-border hover:bg-muted disabled:opacity-50"
          tooltip="Move this task outbox row back to pending"
        >
          <RotateCw className="h-3 w-3" /> Retry
        </BareButton>
      ),
    },
  ];
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
