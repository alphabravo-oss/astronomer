import { CheckCircle2, XCircle } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { cn, formatBytes, formatRelativeTime } from "@/lib/utils";
import type { NodePod, NodeEvent, NodeImage, NodeDetailCondition } from "@/types";

/**
 * The four read-only, filter-free node tabs (Pods / Conditions / Images /
 * Events). Grouped in one sibling because each is a thin DataTable wrapper
 * around a column definition — splitting them into four files each would
 * add navigation overhead without reducing complexity anywhere.
 */

const podColumns: Column<NodePod>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "ready",
    header: "Ready",
    accessor: (row) => <span className="tabular-nums text-xs">{row.ready}</span>,
    align: "center",
  },
  {
    key: "restarts",
    header: "Restarts",
    accessor: (row) => (
      <span
        className={cn(
          "tabular-nums text-xs",
          row.restarts > 0 ? "text-status-warning" : "text-muted-foreground",
        )}
      >
        {row.restarts}
      </span>
    ),
    sortAccessor: (row) => row.restarts,
    align: "center",
  },
  {
    key: "image",
    header: "Image",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[220px] block">
        {row.images?.[0] || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const conditionColumns: Column<NodeDetailCondition>[] = [
  {
    key: "type",
    header: "Type",
    accessor: (row) => (
      <span className="font-medium text-foreground text-xs">{row.type}</span>
    ),
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => {
      const isHealthy =
        (row.type === "Ready" && row.status === "True") ||
        (row.type !== "Ready" && row.status === "False");
      return (
        <div className="flex items-center gap-1.5">
          {isHealthy ? (
            <CheckCircle2 className="h-3.5 w-3.5 text-status-success" />
          ) : (
            <XCircle className="h-3.5 w-3.5 text-status-error" />
          )}
          <span className="text-xs">{row.status}</span>
        </div>
      );
    },
  },
  {
    key: "reason",
    header: "Reason",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.reason || "-"}</span>
    ),
  },
  {
    key: "message",
    header: "Message",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground line-clamp-2">
        {row.message || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "lastHeartbeat",
    header: "Last Heartbeat",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.lastHeartbeat ? formatRelativeTime(row.lastHeartbeat) : "-"}
      </span>
    ),
  },
  {
    key: "lastTransition",
    header: "Last Transition",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.lastTransition ? formatRelativeTime(row.lastTransition) : "-"}
      </span>
    ),
  },
];

const imageColumns: Column<NodeImage>[] = [
  {
    key: "name",
    header: "Image",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs truncate max-w-[500px] block">
        {row.name}
      </span>
    ),
  },
  {
    key: "size",
    header: "Size",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {row.sizeBytes > 0 ? formatBytes(row.sizeBytes) : "-"}
      </span>
    ),
    sortAccessor: (row) => row.sizeBytes,
    align: "right",
  },
];

const eventColumns: Column<NodeEvent>[] = [
  {
    key: "type",
    header: "Type",
    accessor: (row) => (
      <span
        className={cn(
          "text-xs font-medium",
          row.type === "Warning" ? "text-status-warning" : "text-status-info",
        )}
      >
        {row.type}
      </span>
    ),
  },
  {
    key: "reason",
    header: "Reason",
    accessor: (row) => (
      <span className="font-medium text-foreground text-xs">{row.reason}</span>
    ),
  },
  {
    key: "message",
    header: "Message",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground line-clamp-2">
        {row.message}
      </span>
    ),
    sortable: false,
  },
  {
    key: "count",
    header: "Count",
    accessor: (row) => <span className="tabular-nums text-xs">{row.count}</span>,
    sortAccessor: (row) => row.count,
    align: "center",
  },
  {
    key: "lastSeen",
    header: "Last Seen",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.lastTimestamp ? formatRelativeTime(row.lastTimestamp) : "-"}
      </span>
    ),
  },
];

export function PodsTab({ pods }: { pods: NodePod[] }) {
  return (
    <DataTable
      data={pods}
      columns={podColumns}
      keyExtractor={(r) => `${r.namespace}/${r.name}`}
      searchPlaceholder="Search pods..."
      emptyState={{
        title: "No pods running on this node",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}

export function ConditionsTab({
  conditions,
}: {
  conditions: NodeDetailCondition[];
}) {
  return (
    <DataTable
      data={conditions}
      columns={conditionColumns}
      keyExtractor={(r) => r.type}
      emptyState={{
        title: "No conditions reported",
        description: "New observations will appear here as they are reported.",
      }}
    />
  );
}

export function ImagesTab({ images }: { images: NodeImage[] }) {
  return (
    <DataTable
      data={images}
      columns={imageColumns}
      keyExtractor={(r) => r.name}
      searchPlaceholder="Search images..."
      emptyState={{
        title: "No images cached on this node",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}

export function EventsTab({ events }: { events: NodeEvent[] }) {
  return (
    <DataTable
      data={events}
      columns={eventColumns}
      keyExtractor={(r) => `${r.reason}-${r.lastTimestamp}`}
      searchPlaceholder="Search events..."
      emptyState={{
        title: "No events for this node",
        description: "New observations will appear here as they are reported.",
      }}
    />
  );
}
