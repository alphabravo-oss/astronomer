import { useMemo } from "react";
import { DataTable } from "@/components/ui/data-table";
import type {
  QueueSummary,
  DLQEntry,
  TaskOutboxEntry,
  TaskOutboxStatus,
} from "@/lib/api/admin-operations";
import { dlqColumns, queueColumns, taskOutboxColumns } from "./-columns";

export function QueueTable({
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
      layout="scroll"
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

export function DLQTable({
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
      layout="scroll"
      loading={loading}
      searchPlaceholder="Search dead-letter tasks..."
      emptyState={{
        title: "No archived tasks",
        description: `Queue ${queue} has no dead-letter entries.`,
      }}
    />
  );
}

export function TaskOutboxTable({
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
      layout="scroll"
      loading={loading}
      searchPlaceholder="Search task outbox..."
      emptyState={{
        title: "No matching task outbox rows",
        description: `No ${status || "matching"} task outbox rows.`,
      }}
    />
  );
}
