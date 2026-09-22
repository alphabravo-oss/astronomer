import { useNavigate } from "@tanstack/react-router";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionMenu } from "@/components/ui/action-menu";
import { Terminal, Pencil, Trash2 } from "lucide-react";
import { registrationSearch } from "@/components/clusters/registration-flow";
import {
  formatRelativeTime,
  formatPercentage,
  providerDisplayName,
  distributionDisplayName,
} from "@/lib/utils";
import type { Cluster } from "@/types";
import type { Column } from "@/components/ui/data-table";

export function clusterColumns(
  navigate: ReturnType<typeof useNavigate>,
  setEditCluster: (cluster: Cluster) => void,
  setDeleteTarget: (cluster: Cluster) => void,
): Column<Cluster>[] {
  return [
    {
      key: "name",
      header: "Name",
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.displayName}</p>
          <p className="text-xs text-muted-foreground">{row.name}</p>
        </div>
      ),
      sortAccessor: (row) => row.displayName,
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) =>
        row.decommissioning ? (
          <StatusBadge status="decommissioning" label="Decommissioning" pulse />
        ) : (
          <StatusBadge status={row.status} />
        ),
      sortAccessor: (row) =>
        row.decommissioning ? "decommissioning" : row.status,
    },
    {
      key: "provider",
      header: "Provider",
      accessor: (row) => (
        <span className="text-muted-foreground">
          {providerDisplayName(row.provider)}
        </span>
      ),
      sortAccessor: (row) => row.provider,
    },
    {
      key: "distribution",
      header: "Distribution",
      accessor: (row) => (
        <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground">
          {distributionDisplayName(row.distribution)}
        </span>
      ),
      sortAccessor: (row) => row.distribution,
    },
    {
      key: "version",
      header: "K8s Version",
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.kubernetesVersion}
        </span>
      ),
    },
    {
      key: "nodes",
      header: "Nodes",
      accessor: (row) => <span className="tabular-nums">{row.nodeCount}</span>,
      sortAccessor: (row) => row.nodeCount,
      align: "center",
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
      header: "CPU%",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={`gauge-bar-fill ${
                row.cpuPercentage >= 90
                  ? "bg-status-error"
                  : row.cpuPercentage >= 75
                    ? "bg-status-warning"
                    : "bg-status-success"
              }`}
              style={{ width: `${Math.min(row.cpuPercentage, 100)}%` }}
            />
          </div>
          <span className="text-xs tabular-nums text-muted-foreground w-10">
            {formatPercentage(
              row.cpuPercentage,
              row.cpuPercentage < 10 ? 1 : 0,
            )}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.cpuPercentage,
    },
    {
      key: "mem",
      header: "Mem%",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={`gauge-bar-fill ${
                row.memoryPercentage >= 90
                  ? "bg-status-error"
                  : row.memoryPercentage >= 75
                    ? "bg-status-warning"
                    : "bg-status-success"
              }`}
              style={{ width: `${Math.min(row.memoryPercentage, 100)}%` }}
            />
          </div>
          <span className="text-xs tabular-nums text-muted-foreground w-10">
            {formatPercentage(
              row.memoryPercentage,
              row.memoryPercentage < 10 ? 1 : 0,
            )}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.memoryPercentage,
    },
    {
      key: "heartbeat",
      header: "Last Heartbeat",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastHeartbeat ? formatRelativeTime(row.lastHeartbeat) : "Never"}
        </span>
      ),
      sortAccessor: (row) => row.lastHeartbeat ?? "",
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: "Registration Command",
              icon: <Terminal className="h-3.5 w-3.5" />,
              onClick: () =>
                void navigate({
                  to: "/dashboard/clusters/register",
                  search: registrationSearch(row.id),
                }),
            },
            {
              label: "Edit",
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => setEditCluster(row),
            },
            {
              label: "Delete",
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: "destructive",
              separator: true,
            },
          ]}
        />
      ),
      align: "center",
    },
  ];
}
