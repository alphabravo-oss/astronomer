import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { Link as RouterLink } from "@tanstack/react-router";
import {
  cn,
  distributionDisplayName,
  formatPercentage,
  providerDisplayName,
} from "@/lib/utils";
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
    kind: "name",
    minSize: 200,
    accessor: (cluster) => (
      <RouterLink
        to="/dashboard/clusters/$id"
        params={{ id: cluster.id }}
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
    kind: "status",
    size: 120,
    accessor: (cluster) => <StatusBadge status={cluster.status} />,
    searchAccessor: (cluster) => cluster.status,
    sortAccessor: (cluster) => cluster.status,
    filter: { label: "Status" },
  },
  {
    key: "provider",
    header: "Provider",
    kind: "badge",
    size: 150,
    accessor: (cluster) => (
      <div className="space-y-0.5">
        <span className="block text-xs text-muted-foreground">
          {providerDisplayName(cluster.provider)}
        </span>
        <span className="inline-block rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground">
          {distributionDisplayName(cluster.distribution)}
        </span>
      </div>
    ),
    searchAccessor: (cluster) =>
      `${cluster.provider} ${cluster.distribution}`.trim(),
    sortAccessor: (cluster) => cluster.provider,
  },
  {
    key: "version",
    header: "Version",
    kind: "version",
    size: 120,
    accessor: (cluster) => (
      <span className="font-mono text-xs text-muted-foreground">
        {cluster.kubernetesVersion || "—"}
      </span>
    ),
    searchAccessor: (cluster) => cluster.kubernetesVersion || "",
    sortAccessor: (cluster) => cluster.kubernetesVersion || "",
  },
  {
    key: "nodes",
    header: "Nodes",
    kind: "count",
    accessor: (cluster) => (
      <span className="font-mono text-xs tabular-nums">
        {cluster.nodeCount}
      </span>
    ),
    sortAccessor: (cluster) => cluster.nodeCount,
  },
  {
    key: "pods",
    header: "Pods",
    kind: "count",
    accessor: (cluster) => (
      <span className="font-mono text-xs tabular-nums">{cluster.podCount}</span>
    ),
    sortAccessor: (cluster) => cluster.podCount,
  },
  {
    key: "cpu",
    header: "CPU",
    kind: "percent",
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
  },
  {
    key: "memory",
    header: "Memory",
    kind: "percent",
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
