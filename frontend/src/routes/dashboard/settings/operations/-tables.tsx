import { useMemo } from "react";
import { DataTable, type DataTableProps } from "@/components/ui/data-table";
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
  isError,
  error,
  onRefetch,
}: {
  loading: boolean;
  rows: QueueSummary[];
  activeQueue: string;
  onSelect: (queue: string) => void;
  isError: boolean;
  error: unknown;
  onRefetch: () => unknown;
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
      isError={isError}
      error={error}
      onRetry={onRefetch}
      permission="admin_operations:read"
      errorMessage="Queue inspection unavailable"
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
  serverSide,
  isError,
  error,
  onRefetch,
}: {
  loading: boolean;
  queue: string;
  rows: DLQEntry[];
  onRetry: (id: string) => void;
  onDiscard: (id: string) => void;
  pendingRetry: boolean;
  pendingDiscard: boolean;
  serverSide: NonNullable<DataTableProps<DLQEntry>["serverSide"]>;
  isError: boolean;
  error: unknown;
  onRefetch: () => unknown;
}) {
  const columns = useMemo(
    () =>
      dlqColumns(onRetry, onDiscard, pendingRetry, pendingDiscard).map(
        (column) => ({ ...column, sortable: false, filter: undefined }),
      ),
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
      isError={isError}
      error={error}
      onRetry={onRefetch}
      permission="admin_operations:read"
      searchable={false}
      serverSide={serverSide}
      searchPlaceholder="Search dead-letter tasks..."
      emptyState={{
        title: "No archived tasks on this page",
        description: `This page of queue ${queue} has no dead-letter entries.`,
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
  serverSide,
  isError,
  error,
  onRefetch,
}: {
  loading: boolean;
  rows: TaskOutboxEntry[];
  status: TaskOutboxStatus | "";
  onRetry: (id: string) => void;
  pendingRetry: boolean;
  serverSide: NonNullable<DataTableProps<TaskOutboxEntry>["serverSide"]>;
  isError: boolean;
  error: unknown;
  onRefetch: () => unknown;
}) {
  const columns = useMemo(
    () =>
      taskOutboxColumns(onRetry, pendingRetry).map((column) => ({
        ...column,
        sortable: false,
        filter: undefined,
      })),
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
      isError={isError}
      error={error}
      onRetry={onRefetch}
      permission="admin_operations:read"
      searchable={false}
      serverSide={serverSide}
      searchPlaceholder="Search task outbox..."
      emptyState={{
        title: "No matching task outbox rows on this page",
        description: `No ${status || "matching"} task outbox rows on this page.`,
      }}
    />
  );
}
