import { CheckCircle2, XCircle } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { cn, formatBytes } from "@/lib/utils";
import { ImageRefCell, TimestampCell } from "@/components/tables/cells";
import type {
  NodePod,
  NodeEvent,
  NodeImage,
  NodeDetailCondition,
} from "@/types";

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
    kind: "name",
    minSize: 140,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "name",
    grow: false,
    size: 130,
    minSize: 110,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 168,
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "ready",
    header: "Ready",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.ready}</span>
    ),
  },
  {
    key: "restarts",
    header: "Restarts",
    kind: "count",
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
  },
  {
    key: "image",
    header: "Image",
    kind: "text",
    size: 200,
    minSize: 170,
    accessor: (row) => (
      <ImageRefCell
        image={row.images?.[0]}
        extra={Math.max((row.images?.length ?? 0) - 1, 0)}
      />
    ),
    sortable: false,
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    size: 80,
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

const conditionColumns: Column<NodeDetailCondition>[] = [
  {
    key: "type",
    header: "Type",
    kind: "text",
    size: 150,
    minSize: 130,
    accessor: (row) => (
      <span className="font-medium text-foreground text-xs">{row.type}</span>
    ),
  },
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 100,
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
    kind: "text",
    size: 160,
    minSize: 130,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.reason || "-"}</span>
    ),
  },
  {
    key: "message",
    header: "Message",
    kind: "text",
    grow: true,
    minSize: 260,
    maxSize: 900,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.message || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "lastHeartbeat",
    header: "Heartbeat",
    ariaLabel: "Last heartbeat",
    kind: "age",
    size: 119,
    accessor: (row) => (
      <TimestampCell
        value={row.lastHeartbeat}
        fallback="-"
        suffix
        className="text-xs text-muted-foreground"
      />
    ),
  },
  {
    key: "lastTransition",
    header: "Transition",
    ariaLabel: "Last transition",
    kind: "age",
    size: 126,
    accessor: (row) => (
      <TimestampCell
        value={row.lastTransition}
        fallback="-"
        suffix
        className="text-xs text-muted-foreground"
      />
    ),
  },
];

const imageColumns: Column<NodeImage>[] = [
  {
    key: "name",
    header: "Image",
    kind: "text",
    grow: true,
    minSize: 320,
    maxSize: 900,
    accessor: (row) => <ImageRefCell image={row.name} />,
  },
  {
    key: "size",
    header: "Size",
    kind: "bytes",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {row.sizeBytes > 0 ? formatBytes(row.sizeBytes) : "-"}
      </span>
    ),
    sortAccessor: (row) => row.sizeBytes,
  },
];

const eventColumns: Column<NodeEvent>[] = [
  {
    key: "type",
    header: "Type",
    kind: "status",
    size: 100,
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
    kind: "text",
    size: 150,
    minSize: 120,
    accessor: (row) => (
      <span className="font-medium text-foreground text-xs">{row.reason}</span>
    ),
  },
  {
    key: "message",
    header: "Message",
    kind: "text",
    grow: true,
    minSize: 260,
    maxSize: 900,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.message}</span>
    ),
    sortable: false,
  },
  {
    key: "count",
    header: "Count",
    kind: "count",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.count}</span>
    ),
    sortAccessor: (row) => row.count,
  },
  {
    key: "lastSeen",
    header: "Last Seen",
    kind: "age",
    size: 119,
    accessor: (row) => (
      <TimestampCell
        value={row.lastTimestamp}
        fallback="-"
        suffix
        className="text-xs text-muted-foreground"
      />
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
