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
  Ingress,
  K8sService,
  Namespace,
  NetworkPolicy,
  PersistentVolume,
  PersistentVolumeClaim,
  Pod,
  StorageClass,
  Workload,
} from "@/types";
import {
  ChipsCell,
  ImageRefCell,
  TimestampCell,
} from "@/components/tables/cells";

// ── Column Definitions ──

const nodeColumns: Column<ClusterNode>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    kind: "status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "roles",
    header: "Roles",
    kind: "badge",
    minSize: 140,
    accessor: (row) => <ChipsCell items={row.roles} />,
    sortAccessor: (row) => row.roles.join(","),
    searchAccessor: (row) => row.roles.join(" "),
  },
  {
    key: "cpu",
    header: "CPU",
    kind: "percent",
    size: 200,
    minSize: 190,
    maxSize: 240,
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
    kind: "percent",
    size: 200,
    minSize: 190,
    maxSize: 240,
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
    kind: "count",
    accessor: (row) => (
      <span className="text-muted-foreground tabular-nums text-xs">
        {row.podCount}/{row.podCapacity}
      </span>
    ),
    sortAccessor: (row) => row.podCount,
  },
  {
    key: "age",
    header: "Age",
    kind: "age",
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
    sortAccessor: (row) => row.createdAt ?? "",
  },
];

const nsColumns: Column<Namespace>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 200,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    kind: "status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "pods",
    header: "Pods",
    kind: "count",
    accessor: (row) => <span className="tabular-nums">{row.podCount}</span>,
    sortAccessor: (row) => row.podCount,
  },
  {
    key: "cpu",
    header: "CPU Usage",
    kind: "count",
    size: 130,
    maxSize: 180,
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
    kind: "bytes",
    size: 150,
    maxSize: 200,
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
    kind: "age",
    accessor: (row) => (
      <TimestampCell
        value={row.createdAt}
        className="text-xs text-muted-foreground"
      />
    ),
    sortAccessor: (row) => row.createdAt ?? "",
  },
];

const eventColumns: Column<ClusterEvent>[] = [
  {
    key: "type",
    header: "Type",
    kind: "status",
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
    key: "object",
    header: "Object",
    kind: "text",
    size: 240,
    minSize: 200,
    accessor: (row) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.involvedObject.kind}/{row.involvedObject.name}
      </span>
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
    accessor: (row) => (
      <TimestampCell
        value={row.lastTimestamp}
        className="text-xs text-muted-foreground"
      />
    ),
    sortAccessor: (row) => row.lastTimestamp ?? "",
  },
];

const podColumns: Column<Pod>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 260,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "text",
    size: 150,
    minSize: 120,
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
    kind: "status",
    accessor: (row) => <StatusBadge status={row.status} />,
    sortAccessor: (row) => row.status,
    filter: { label: "Status" },
  },
  {
    key: "images",
    header: "Images",
    kind: "text",
    size: 280,
    minSize: 220,
    hidden: true,
    accessor: (row) => (
      <ImageRefCell
        image={row.images[0]}
        extra={Math.max(row.images.length - 1, 0)}
      />
    ),
    searchAccessor: (row) => row.images.join(" "),
    sortAccessor: (row) => row.images[0] ?? "",
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
    key: "lastRestart",
    header: "Last Restart",
    kind: "age",
    minSize: 100,
    hidden: true,
    accessor: (row) => (
      <TimestampCell
        value={row.lastRestartAt}
        fallback="—"
        className="text-xs text-muted-foreground"
      />
    ),
    sortAccessor: (row) => row.lastRestartAt ?? "",
  },
  {
    key: "ip",
    header: "Pod IP",
    kind: "text",
    size: 130,
    minSize: 120,
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
    kind: "text",
    size: 220,
    minSize: 170,
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
    kind: "age",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.age}</span>
    ),
  },
];

const workloadColumns: Column<Workload>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 220,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
  },
  {
    key: "namespace",
    header: "Namespace",
    kind: "text",
    size: 150,
    minSize: 120,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace}
      </span>
    ),
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
    key: "status",
    header: "Status",
    kind: "status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: "images",
    header: "Image",
    kind: "text",
    size: 280,
    minSize: 220,
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
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.age}</span>
    ),
  },
];

const serviceColumns: Column<K8sService>[] = [
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
    key: "type",
    header: "Type",
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
        {row.type}
      </span>
    ),
  },
  {
    key: "clusterIP",
    header: "Cluster IP",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.clusterIP}
      </span>
    ),
  },
  {
    key: "ports",
    header: "Ports",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {row.ports?.map((p) => `${p.port}/${p.protocol}`).join(", ") || "-"}
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

const ingressColumns: Column<Ingress>[] = [
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
    key: "class",
    header: "Class",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.ingressClass || "-"}
      </span>
    ),
  },
  {
    key: "hosts",
    header: "Hosts",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-50 block">
        {row.hosts?.join(", ") || "*"}
      </span>
    ),
    sortable: false,
  },
  {
    key: "tls",
    header: "TLS",
    accessor: (row) => (
      <span
        className={cn(
          "text-xs",
          row.tls ? "text-status-success" : "text-muted-foreground",
        )}
      >
        {row.tls ? "Yes" : "No"}
      </span>
    ),
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

const networkPolicyColumns: Column<NetworkPolicy>[] = [
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
    key: "policyTypes",
    header: "Policy Types",
    accessor: (row) => (
      <div className="flex gap-1">
        {row.policyTypes?.map((t) => (
          <span
            key={t}
            className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground"
          >
            {t}
          </span>
        ))}
      </div>
    ),
    sortable: false,
  },
  {
    key: "ingress",
    header: "Ingress Rules",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.ingressRules}</span>
    ),
    align: "center",
  },
  {
    key: "egress",
    header: "Egress Rules",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.egressRules}</span>
    ),
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

const pvColumns: Column<PersistentVolume>[] = [
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
    key: "capacity",
    header: "Capacity",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {row.capacity}
      </span>
    ),
  },
  {
    key: "accessModes",
    header: "Access Modes",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.accessModes?.join(", ")}
      </span>
    ),
    sortable: false,
  },
  {
    key: "storageClass",
    header: "Storage Class",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.storageClass || "-"}
      </span>
    ),
  },
  {
    key: "claimRef",
    header: "Claim",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.claimRef || "-"}
      </span>
    ),
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

const pvcColumns: Column<PersistentVolumeClaim>[] = [
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
    key: "capacity",
    header: "Capacity",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {row.capacity}
      </span>
    ),
  },
  {
    key: "storageClass",
    header: "Storage Class",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.storageClass || "-"}
      </span>
    ),
  },
  {
    key: "volumeName",
    header: "Volume",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.volumeName || "-"}
      </span>
    ),
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

const storageClassColumns: Column<StorageClass>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <div className="flex items-center gap-2">
        <span className="font-medium text-foreground font-mono text-xs">
          {row.name}
        </span>
        {row.isDefault && (
          <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-status-info/10 text-status-info">
            default
          </span>
        )}
      </div>
    ),
  },
  {
    key: "provisioner",
    header: "Provisioner",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.provisioner}
      </span>
    ),
  },
  {
    key: "reclaimPolicy",
    header: "Reclaim Policy",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.reclaimPolicy}</span>
    ),
  },
  {
    key: "volumeBindingMode",
    header: "Binding Mode",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.volumeBindingMode}
      </span>
    ),
  },
  {
    key: "expansion",
    header: "Expansion",
    accessor: (row) => (
      <span
        className={cn(
          "text-xs",
          row.allowVolumeExpansion
            ? "text-status-success"
            : "text-muted-foreground",
        )}
      >
        {row.allowVolumeExpansion ? "Allowed" : "No"}
      </span>
    ),
    align: "center",
  },
];

// ── Generic resource column definitions ──

export {
  configMapColumns,
  eventColumns,
  genericColumnMap,
  ingressColumns,
  networkPolicyColumns,
  nodeColumns,
  nsColumns,
  podColumns,
  pvColumns,
  pvcColumns,
  serviceColumns,
  storageClassColumns,
  workloadColumns,
};
