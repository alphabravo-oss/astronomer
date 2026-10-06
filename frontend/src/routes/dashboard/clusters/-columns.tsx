import { useNavigate } from "@tanstack/react-router";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionMenu } from "@/components/ui/action-menu";
import { Terminal, Pencil, Trash2 } from "lucide-react";
import { registrationSearch } from "@/components/clusters/registration-flow";
import {
  formatPercentage,
  providerDisplayName,
  distributionDisplayName,
} from "@/lib/utils";
import {
  EntityCell,
  TimestampCell,
  UsageGauge,
} from "@/components/tables/cells";
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
      // Flexible column: every other column is kind-sized, so Name absorbs
      // the remaining space instead of truncating.
      header: "Name",
      kind: "name",
      minSize: 110,
      accessor: (row) => (
        <EntityCell primary={row.displayName} secondary={row.name} />
      ),
      sortAccessor: (row) => row.displayName,
    },
    {
      key: "status",
      header: "Status",
      kind: "status",
      size: 136,
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
      key: "distribution",
      header: "Platform",
      ariaLabel: "Distribution / provider",
      kind: "badge",
      size: 112,
      accessor: (row) => (
        <div className="space-y-0.5">
          <span className="rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground">
            {distributionDisplayName(row.distribution)}
          </span>
          <p className="text-xs text-muted-foreground">
            {providerDisplayName(row.provider)}
          </p>
        </div>
      ),
      sortAccessor: (row) => row.distribution,
      searchAccessor: (row) =>
        `${distributionDisplayName(row.distribution)} ${providerDisplayName(row.provider)}`,
    },
    {
      key: "version",
      header: "K8s",
      ariaLabel: "Kubernetes version",
      kind: "version",
      size: 112,
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.kubernetesVersion}
        </span>
      ),
    },
    {
      key: "nodes",
      header: "Nodes",
      kind: "count",
      accessor: (row) => <span className="tabular-nums">{row.nodeCount}</span>,
      sortAccessor: (row) => row.nodeCount,
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
      header: "CPU%",
      kind: "percent",
      size: 104,
      accessor: (row) => (
        <UsageGauge
          pct={row.cpuPercentage}
          label={formatPercentage(
            row.cpuPercentage,
            row.cpuPercentage < 10 ? 1 : 0,
          )}
        />
      ),
      sortAccessor: (row) => row.cpuPercentage,
    },
    {
      key: "mem",
      header: "Mem%",
      kind: "percent",
      size: 104,
      accessor: (row) => (
        <UsageGauge
          pct={row.memoryPercentage}
          label={formatPercentage(
            row.memoryPercentage,
            row.memoryPercentage < 10 ? 1 : 0,
          )}
        />
      ),
      sortAccessor: (row) => row.memoryPercentage,
    },
    {
      key: "heartbeat",
      header: "Seen",
      ariaLabel: "Last heartbeat",
      kind: "age",
      size: 84,
      accessor: (row) => (
        <TimestampCell
          value={row.lastHeartbeat}
          className="text-xs text-muted-foreground"
        />
      ),
      sortAccessor: (row) => row.lastHeartbeat ?? "",
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      rowActions: true,
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
    },
  ];
}
