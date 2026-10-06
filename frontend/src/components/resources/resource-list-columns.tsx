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
import { Tooltip } from "@/components/ui/tooltip";
import {
  AccessModesCell,
  abbreviateAccessModes,
  ageColumn,
  chWidth,
  chipColumn,
  countColumn,
  namespaceColumn,
  plainNameColumn,
  textColumn,
} from "@/components/resources/resource-column-kit";

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

const serviceColumns: Column<K8sService>[] = [
  plainNameColumn<K8sService>(),
  namespaceColumn<K8sService>(),
  {
    key: "type",
    header: "Type",
    kind: "badge",
    size: 104,
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
        {row.type}
      </span>
    ),
    searchAccessor: (row) => row.type,
  },
  {
    key: "clusterIP",
    header: "Cluster IP",
    kind: "text",
    size: 132,
    minSize: 112,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.clusterIP}
      </span>
    ),
    searchAccessor: (row) => row.clusterIP ?? "",
  },
  chipColumn<K8sService>(
    "ports",
    "Ports",
    (row) => row.ports?.map((p) => `${p.port}/${p.protocol}`),
    { size: 180, minSize: chWidth(16), maxSize: 280 },
  ),
  ageColumn<K8sService>((row) => row.createdAt),
];

const ingressColumns: Column<Ingress>[] = [
  plainNameColumn<Ingress>(),
  namespaceColumn<Ingress>(),
  textColumn<Ingress>("class", "Class", (row) => row.ingressClass, {
    size: 110,
    minSize: 96,
  }),
  chipColumn<Ingress>(
    "hosts",
    "Hosts",
    (row) => (row.hosts?.length ? row.hosts : ["*"]),
    { size: 200, minSize: chWidth(22), maxSize: 400, max: 1 },
  ),
  {
    key: "tls",
    header: "TLS",
    kind: "status",
    size: 72,
    minSize: 64,
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
    searchAccessor: (row) => (row.tls ? "Yes" : "No"),
  },
  ageColumn<Ingress>((row) => row.createdAt),
];

const networkPolicyColumns: Column<NetworkPolicy>[] = [
  plainNameColumn<NetworkPolicy>(),
  namespaceColumn<NetworkPolicy>(),
  chipColumn<NetworkPolicy>(
    "policyTypes",
    "Policy Types",
    (row) => row.policyTypes,
    { mono: false, size: 150 },
  ),
  countColumn<NetworkPolicy>(
    "ingress",
    "Ingress Rules",
    (row) => row.ingressRules,
    { size: 104 },
  ),
  countColumn<NetworkPolicy>(
    "egress",
    "Egress Rules",
    (row) => row.egressRules,
    { size: 104 },
  ),
  ageColumn<NetworkPolicy>((row) => row.createdAt),
];

const pvColumns: Column<PersistentVolume>[] = [
  plainNameColumn<PersistentVolume>(),
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 100,
    accessor: (row) => <StatusBadge status={row.status} />,
    searchAccessor: (row) => row.status,
  },
  {
    key: "capacity",
    header: "Capacity",
    kind: "bytes",
    size: 84,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.capacity}</span>
    ),
    searchAccessor: (row) => row.capacity ?? "",
  },
  {
    key: "accessModes",
    header: "Access Modes",
    kind: "text",
    size: 96,
    minSize: 92,
    accessor: (row) => <AccessModesCell modes={row.accessModes} />,
    searchAccessor: (row) => abbreviateAccessModes(row.accessModes).join(" "),
    sortable: false,
  },
  textColumn<PersistentVolume>(
    "storageClass",
    "Storage Class",
    (row) => row.storageClass,
    { size: 124, minSize: 112 },
  ),
  textColumn<PersistentVolume>("claimRef", "Claim", (row) => row.claimRef, {
    mono: true,
    size: 170,
    minSize: chWidth(18),
  }),
  ageColumn<PersistentVolume>((row) => row.createdAt),
];

const pvcColumns: Column<PersistentVolumeClaim>[] = [
  plainNameColumn<PersistentVolumeClaim>(),
  namespaceColumn<PersistentVolumeClaim>(),
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 104,
    accessor: (row) => <StatusBadge status={row.status} />,
    searchAccessor: (row) => row.status,
  },
  {
    key: "capacity",
    header: "Capacity",
    kind: "bytes",
    size: 88,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">{row.capacity}</span>
    ),
    searchAccessor: (row) => row.capacity ?? "",
  },
  textColumn<PersistentVolumeClaim>(
    "storageClass",
    "Storage Class",
    (row) => row.storageClass,
    { size: 124, minSize: 112 },
  ),
  {
    key: "volumeName",
    header: "Volume",
    kind: "id",
    size: 170,
    minSize: 130,
    maxSize: 320,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.volumeName || "-"}
      </span>
    ),
    searchAccessor: (row) => row.volumeName ?? "",
  },
  ageColumn<PersistentVolumeClaim>((row) => row.createdAt),
];

const storageClassColumns: Column<StorageClass>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    accessor: (row) => (
      <div className="flex min-w-0 items-center gap-2">
        <span className="min-w-0 truncate font-medium text-foreground font-mono text-xs">
          {row.name}
        </span>
        {row.isDefault && (
          <span className="shrink-0 px-1.5 py-0.5 rounded-sm text-2xs bg-status-info/10 text-status-info">
            default
          </span>
        )}
      </div>
    ),
    searchAccessor: (row) => row.name,
  },
  textColumn<StorageClass>(
    "provisioner",
    "Provisioner",
    (row) => row.provisioner,
    { mono: true, size: 200, minSize: chWidth(22) },
  ),
  textColumn<StorageClass>(
    "reclaimPolicy",
    "Reclaim Policy",
    (row) => row.reclaimPolicy,
    { size: 112, minSize: 100 },
  ),
  textColumn<StorageClass>(
    "volumeBindingMode",
    "Binding Mode",
    (row) => row.volumeBindingMode,
    { size: 170, minSize: chWidth(20) },
  ),
  {
    key: "expansion",
    header: "Expansion",
    kind: "status",
    size: 96,
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
    searchAccessor: (row) => (row.allowVolumeExpansion ? "Allowed" : "No"),
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
