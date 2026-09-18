import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { Link as RouterLink } from "@tanstack/react-router";
import { cn, formatPercentage } from "@/lib/utils";
import type { Cluster } from "@/types";

function utilizationTone(value: number | null | undefined): string {
  if (value == null) return "text-muted-foreground";
  if (value >= 90) return "text-status-error";
  if (value >= 75) return "text-status-warning";
  return "text-muted-foreground";
}

export const estateClusterColumns: Column<Cluster>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (cluster) => (
      <RouterLink
        to="/dashboard/clusters/$id" params={{ id: cluster.id }}
        className="font-medium text-foreground hover:underline"
      >
        {cluster.displayName || cluster.name}
      </RouterLink>
    ),
    searchAccessor: (cluster) =>
      `${cluster.displayName || ""} ${cluster.name}`.trim(),
    sortAccessor: (cluster) => cluster.displayName || cluster.name,
  },
  {
    key: "status",
    header: "Status",
    accessor: (cluster) => <StatusBadge status={cluster.status} />,
    searchAccessor: (cluster) => cluster.status,
    sortAccessor: (cluster) => cluster.status,
    filter: { label: "Status" },
    width: "9rem",
  },
  {
    key: "version",
    header: "Version",
    accessor: (cluster) => (
      <span className="font-mono text-xs text-muted-foreground">
        {cluster.kubernetesVersion || "—"}
      </span>
    ),
    searchAccessor: (cluster) => cluster.kubernetesVersion || "",
    sortAccessor: (cluster) => cluster.kubernetesVersion || "",
    width: "8rem",
  },
  {
    key: "nodes",
    header: "Nodes",
    accessor: (cluster) => (
      <span className="font-mono text-xs tabular-nums">
        {cluster.nodeCount}
      </span>
    ),
    sortAccessor: (cluster) => cluster.nodeCount,
    align: "right",
    width: "6rem",
  },
  {
    key: "pods",
    header: "Pods",
    accessor: (cluster) => (
      <span className="font-mono text-xs tabular-nums">{cluster.podCount}</span>
    ),
    sortAccessor: (cluster) => cluster.podCount,
    align: "right",
    width: "6rem",
  },
  {
    key: "cpu",
    header: "CPU",
    accessor: (cluster) => (
      <span
        className={cn(
          "font-mono text-xs tabular-nums",
          utilizationTone(cluster.cpuPercentage),
        )}
      >
        {formatPercentage(cluster.cpuPercentage)}
      </span>
    ),
    sortAccessor: (cluster) => cluster.cpuPercentage ?? -1,
    align: "right",
    width: "6rem",
  },
  {
    key: "memory",
    header: "Memory",
    accessor: (cluster) => (
      <span
        className={cn(
          "font-mono text-xs tabular-nums",
          utilizationTone(cluster.memoryPercentage),
        )}
      >
        {formatPercentage(cluster.memoryPercentage)}
      </span>
    ),
    sortAccessor: (cluster) => cluster.memoryPercentage ?? -1,
    align: "right",
    width: "7rem",
  },
];

interface EstateClustersTableProps {
  clusters: Cluster[];
  loading: boolean;
  isError: boolean;
  error?: unknown;
  onRetry: () => void;
  onRowClick: (cluster: Cluster) => void;
}

export function EstateClustersTable({
  clusters,
  loading,
  isError,
  error,
  onRetry,
  onRowClick,
}: EstateClustersTableProps) {
  return (
    <DataTable
      data={clusters}
      columns={estateClusterColumns}
      keyExtractor={(cluster) => cluster.id}
      density="compact"
      pageSize={10}
      persistKey="dashboard-estate-clusters"
      resizable
      loading={loading}
      isError={isError}
      error={error}
      permission="clusters:read"
      errorMessage="Failed to load cluster health."
      onRetry={onRetry}
      onRowClick={onRowClick}
      searchPlaceholder="Search clusters..."
      emptyState={{
        title: "No clusters available",
        description:
          "Resources will appear here when they are available in this scope.",
      }}
    />
  );
}
