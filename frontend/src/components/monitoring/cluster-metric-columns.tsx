import { Link } from "@tanstack/react-router";
import { detailHref } from "@/lib/k8s-paths";
import type { Column } from "@/components/ui/data-table";
import { formatBytes, formatCPU, formatPercentage, cn } from "@/lib/utils";
import type { ClusterNode, Namespace } from "@/types";
export function clusterMetricColumns(clusterId: string) {
  const nodeColumns: Column<ClusterNode>[] = [
    {
      key: "name",
      header: "Node",
      accessor: (row) => (
        <Link
          to={detailHref(clusterId, "nodes", undefined, row.name)}
          className="font-mono text-xs text-foreground hover:underline"
        >
          {row.name}
        </Link>
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
            <div className="w-20 gauge-bar">
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
            <span className="text-xs text-muted-foreground tabular-nums w-10">
              {formatPercentage(pct, 0)}
            </span>
          </div>
        );
      },
      sortAccessor: (row) => row.cpuUsage / Math.max(row.cpuCapacity, 1),
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
            <div className="w-20 gauge-bar">
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
            <span className="text-xs text-muted-foreground tabular-nums w-10">
              {formatPercentage(pct, 0)}
            </span>
          </div>
        );
      },
      sortAccessor: (row) => row.memoryUsage / Math.max(row.memoryCapacity, 1),
    },
    {
      key: "pods",
      header: "Pods",
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {row.podCount}/{row.podCapacity}
        </span>
      ),
      sortAccessor: (row) => row.podCount,
      align: "center",
    },
  ];

  const nsColumns: Column<Namespace>[] = [
    {
      key: "name",
      header: "Namespace",
      accessor: (row) => (
        <Link
          to={String(
            `/dashboard/clusters/${clusterId}/pods?namespaces=${encodeURIComponent(row.name)}`,
          )}
          className="font-mono text-xs text-foreground hover:underline"
        >
          {row.name}
        </Link>
      ),
    },
    {
      key: "pods",
      header: "Pods",
      accessor: (row) => (
        <span className="tabular-nums text-xs">{row.podCount}</span>
      ),
      sortAccessor: (row) => row.podCount,
      align: "center",
    },
    {
      key: "cpu",
      header: "CPU Usage",
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {formatCPU(row.cpuUsage)}
        </span>
      ),
      sortAccessor: (row) => row.cpuUsage,
    },
    {
      key: "memory",
      header: "Memory Usage",
      accessor: (row) => (
        <span className="text-xs tabular-nums text-muted-foreground">
          {formatBytes(row.memoryUsage)}
        </span>
      ),
      sortAccessor: (row) => row.memoryUsage,
    },
  ];

  return { nodeColumns, nsColumns };
}
