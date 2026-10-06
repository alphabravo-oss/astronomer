import { StatusBadge } from "@/components/ui/status-badge";
import type { Column } from "@/components/ui/data-table";
import { cn, formatBytes, formatCPU, formatRelativeTime } from "@/lib/utils";
import {
  configMapColumns,
  genericColumnMap,
} from "@/components/resources/resource-generic-columns";
import type {
  ClusterEvent,
  ClusterNode,
  Namespace,
  Pod,
  Workload,
} from "@/types";
import { Tooltip } from "@/components/ui/tooltip";

// ── Column Definitions ──

const nodeColumns: Column<ClusterNode>[] = [
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
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "roles",
    header: "Roles",
    accessor: (row) => (
      <div className="flex gap-1">
        {row.roles.map((role) => (
          <span
            key={role}
            className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground"
          >
            {role}
          </span>
        ))}
      </div>
    ),
  },
  {
    key: "cpu",
    header: "CPU",
    accessor: (row) => {
      const pct =
        row.cpuCapacity > 0 ? (row.cpuUsage / row.cpuCapacity) * 100 : 0;
      return (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={cn(
                "gauge-bar-fill",
                pct >= 90
                  ? "bg-status-error"
                  : pct >= 75
                    ? "bg-status-warning"
                    : "bg-status-success",
              )}
              style={{ width: `${Math.min(pct, 100)}%` }}
            />
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {formatCPU(row.cpuUsage)} / {formatCPU(row.cpuCapacity)}
          </span>
        </div>
      );
    },
    sortAccessor: (row) => row.cpuUsage,
  },
  {
    key: "memory",
    header: "Memory",
    accessor: (row) => {
      const pct =
        row.memoryCapacity > 0
          ? (row.memoryUsage / row.memoryCapacity) * 100
          : 0;
      return (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={cn(
                "gauge-bar-fill",
                pct >= 90
                  ? "bg-status-error"
                  : pct >= 75
                    ? "bg-status-warning"
                    : "bg-status-success",
              )}
              style={{ width: `${Math.min(pct, 100)}%` }}
            />
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {formatBytes(row.memoryUsage)} / {formatBytes(row.memoryCapacity)}
          </span>
        </div>
      );
    },
    sortAccessor: (row) => row.memoryUsage,
  },
  {
    key: "pods",
    header: "Pods",
    accessor: (row) => (
      <span className="text-muted-foreground tabular-nums text-xs">
        {row.podCount}/{row.podCapacity}
      </span>
    ),
    sortAccessor: (row) => row.podCount,
    align: "center",
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

const nsColumns: Column<Namespace>[] = [
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
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "pods",
    header: "Pods",
    accessor: (row) => <span className="tabular-nums">{row.podCount}</span>,
    sortAccessor: (row) => row.podCount,
    align: "center",
  },
  {
    key: "cpu",
    header: "CPU Usage",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {formatCPU(row.cpuUsage)}
        {row.cpuLimit > 0 ? ` / ${formatCPU(row.cpuLimit)}` : ""}
      </span>
    ),
    sortAccessor: (row) => row.cpuUsage,
  },
  {
    key: "memory",
    header: "Memory Usage",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {formatBytes(row.memoryUsage)}
        {row.memoryLimit > 0 ? ` / ${formatBytes(row.memoryLimit)}` : ""}
      </span>
    ),
    sortAccessor: (row) => row.memoryUsage,
  },
  {
    key: "created",
    header: "Created",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.createdAt)}
      </span>
    ),
  },
];

const eventColumns: Column<ClusterEvent>[] = [
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
    key: "object",
    header: "Object",
    accessor: (row) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.involvedObject.kind}/{row.involvedObject.name}
      </span>
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
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.count}</span>
    ),
    sortAccessor: (row) => row.count,
    align: "center",
  },
  {
    key: "lastSeen",
    header: "Last Seen",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(row.lastTimestamp)}
      </span>
    ),
  },
];

const podColumns: Column<Pod>[] = [
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
    sortAccessor: (row) => row.namespace,
    filter: { label: "Namespace" },
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
    sortAccessor: (row) => row.status,
    filter: { label: "Status" },
  },
  {
    key: "images",
    header: "Images",
    hidden: true,
    accessor: (row) => {
      const [first, ...rest] = row.images;
      return (
        <Tooltip content={row.images.join("\n")}>
          <span className="block max-w-64 truncate font-mono text-xs text-muted-foreground">
            {first || "—"}
            {rest.length > 0 ? ` +${rest.length}` : ""}
          </span>
        </Tooltip>
      );
    },
    searchAccessor: (row) => row.images.join(" "),
    sortAccessor: (row) => row.images[0] ?? "",
  },
  {
    key: "ready",
    header: "Ready",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.ready}</span>
    ),
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
    key: "lastRestart",
    header: "Last Restart",
    hidden: true,
    accessor: (row) => (
      <span className="whitespace-nowrap text-xs text-muted-foreground">
        {row.lastRestartAt ? formatRelativeTime(row.lastRestartAt) : "—"}
      </span>
    ),
    sortAccessor: (row) => row.lastRestartAt ?? "",
  },
  {
    key: "ip",
    header: "Pod IP",
    hidden: true,
    accessor: (row) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.ip || "—"}
      </span>
    ),
    sortAccessor: (row) => row.ip,
  },
  {
    key: "node",
    header: "Node",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.node}
      </span>
    ),
    sortAccessor: (row) => row.node,
    filter: { label: "Node" },
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.age}</span>
    ),
  },
];

const workloadColumns: Column<Workload>[] = [
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
    key: "ready",
    header: "Ready",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.ready}</span>
    ),
    align: "center",
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "images",
    header: "Image",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-50 block">
        {row.images?.[0] || "-"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "age",
    header: "Age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.age}</span>
    ),
  },
];

// ── Generic resource column definitions ──

export {
  configMapColumns,
  eventColumns,
  genericColumnMap,
  nodeColumns,
  nsColumns,
  podColumns,
  workloadColumns,
};

export {
  ingressColumns,
  networkPolicyColumns,
  pvColumns,
  pvcColumns,
  serviceColumns,
  storageClassColumns,
} from "@/components/resources/resource-network-storage-columns";
