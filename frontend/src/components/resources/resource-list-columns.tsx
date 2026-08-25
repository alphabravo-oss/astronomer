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
            className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground"
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
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
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
    key: "node",
    header: "Node",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.node}
      </span>
    ),
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
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
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
      <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">
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
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
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
            className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground"
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
          <span className="px-1.5 py-0.5 rounded text-2xs bg-status-info/10 text-status-info">
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
