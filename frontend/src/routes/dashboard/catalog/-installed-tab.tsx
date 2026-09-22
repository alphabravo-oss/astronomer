import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionMenu, type ActionMenuItem } from "@/components/ui/action-menu";
import { useCluster } from "@/lib/hooks/clusters";
import { formatRelativeTime } from "@/lib/utils";
import type { InstalledChart } from "@/types";
import { ArrowUpCircle, RotateCcw, Trash2 } from "lucide-react";
import { UpgradeChartModal } from "./-upgrade-chart-modal";

export function InstalledTab({
  installed,
  loading,
  onRollback,
  onUninstall,
  uninstallPending,
}: {
  installed: InstalledChart[] | undefined;
  loading: boolean;
  onRollback: (id: string, revision: number) => void;
  onUninstall: (id: string) => void | Promise<void>;
  uninstallPending?: boolean;
}) {
  const [uninstallTarget, setUninstallTarget] = useState<InstalledChart | null>(
    null,
  );
  const [upgradeTarget, setUpgradeTarget] = useState<InstalledChart | null>(null);
  const installedColumns: Column<InstalledChart>[] = [
    {
      key: "release",
      header: "Release",
      accessor: (row) => (
        <span className="font-medium text-foreground font-mono text-xs">
          {row.releaseName}
        </span>
      ),
    },
    {
      key: "chart",
      header: "Chart version",
      accessor: (row) => (
        <span
          className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground font-mono"
          title={row.chartVersionId || undefined}
        >
          {row.chartVersionId ? row.chartVersionId.slice(0, 8) : "managed tool"}
        </span>
      ),
    },
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => <InstalledClusterName clusterId={row.clusterId} />,
    },
    {
      key: "namespace",
      header: "Namespace",
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
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
      key: "revision",
      header: "Rev",
      accessor: (row) => (
        <span className="tabular-nums text-xs text-muted-foreground">
          {row.revision}
        </span>
      ),
      sortAccessor: (row) => row.revision,
      align: "center",
    },
    {
      key: "source",
      header: "Source",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.toolSlug ? `Tool: ${row.toolSlug}` : "Catalog chart"}
        </span>
      ),
    },
    {
      key: "date",
      header: "Date",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => {
        const items: ActionMenuItem[] = [
          {
            label: "Upgrade",
            icon: <ArrowUpCircle className="h-3.5 w-3.5" />,
            onClick: () => setUpgradeTarget(row),
          },
          {
            label: "Rollback",
            icon: <RotateCcw className="h-3.5 w-3.5" />,
            onClick: () => onRollback(row.id, row.revision - 1),
            disabled: row.revision <= 1,
            disabledReason:
              row.revision <= 1 ? "No previous revision" : undefined,
          },
          {
            label: "Uninstall",
            icon: <Trash2 className="h-3.5 w-3.5" />,
            onClick: () => setUninstallTarget(row),
            variant: "destructive",
            separator: true,
          },
        ];
        return (
          <ActionMenu
            items={items}
            ariaLabel={`Actions for ${row.releaseName}`}
          />
        );
      },
      sortable: false,
    },
  ];

  return (
    <>
      <DataTable
        data={installed || []}
        columns={installedColumns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search installed releases..."
        loading={loading}
        emptyState={{
          title: "No charts installed",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
      />
      <ConfirmDialog
        open={uninstallTarget !== null}
        onClose={() => setUninstallTarget(null)}
        onConfirm={async () => {
          if (!uninstallTarget) return;
          await onUninstall(uninstallTarget.id);
          setUninstallTarget(null);
        }}
        title="Uninstall release"
        description="This removes the Helm release from its cluster."
        confirmText="Uninstall"
        confirmValue={uninstallTarget?.releaseName}
        variant="destructive"
        loading={uninstallPending}
        impact={
          uninstallTarget
            ? {
                scope: `${uninstallTarget.releaseName} in ${uninstallTarget.namespace}`,
                consequences: [
                  "The release and its managed Kubernetes resources will be removed.",
                  "Application availability may be interrupted immediately.",
                ],
                recovery:
                  "Reinstall the chart and restore any separately backed-up application data.",
              }
            : undefined
        }
      />
      {upgradeTarget && (
        <UpgradeChartModal
          installation={upgradeTarget}
          onClose={() => setUpgradeTarget(null)}
        />
      )}
    </>
  );
}

function InstalledClusterName({ clusterId }: { clusterId: string }) {
  const { data: cluster } = useCluster(clusterId);
  return (
    <span className="text-sm text-muted-foreground" title={clusterId}>
      {cluster?.displayName || cluster?.name || clusterId.slice(0, 8)}
    </span>
  );
}
