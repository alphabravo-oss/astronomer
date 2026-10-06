import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionMenu, type ActionMenuItem } from "@/components/ui/action-menu";
import { useCluster } from "@/lib/hooks/clusters";
import { AgeCell } from "@/components/ui/age-cell";
import type { InstalledChart } from "@/types";
import { ArrowUpCircle, RotateCcw, Trash2 } from "lucide-react";
import { UpgradeChartModal } from "./-upgrade-chart-modal";
import { Tooltip } from "@/components/ui/tooltip";

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
  const [upgradeTarget, setUpgradeTarget] = useState<InstalledChart | null>(
    null,
  );
  const installedColumns: Column<InstalledChart>[] = [
    {
      key: "release",
      header: "Release",
      kind: "name",
      minSize: 200,
      accessor: (row) => (
        <span className="font-medium text-foreground font-mono text-xs">
          {row.releaseName}
        </span>
      ),
      sortAccessor: (row) => row.releaseName,
    },
    {
      key: "chart",
      header: "Chart version",
      kind: "version",
      size: 140,
      minSize: 140,
      accessor: (row) => (
        <Tooltip content={row.chartVersionId || undefined}>
          <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground font-mono">
            {row.chartVersionId
              ? row.chartVersionId.slice(0, 8)
              : "managed tool"}
          </span>
        </Tooltip>
      ),
    },
    {
      key: "cluster",
      header: "Cluster",
      kind: "text",
      minSize: 160,
      accessor: (row) => <InstalledClusterName clusterId={row.clusterId} />,
    },
    {
      key: "namespace",
      header: "Namespace",
      kind: "text",
      minSize: 160,
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.namespace}
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
      key: "revision",
      header: "Rev",
      kind: "count",
      accessor: (row) => (
        <span className="tabular-nums text-xs text-muted-foreground">
          {row.revision}
        </span>
      ),
      sortAccessor: (row) => row.revision,
    },
    {
      key: "source",
      header: "Source",
      kind: "text",
      minSize: 140,
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.toolSlug ? `Tool: ${row.toolSlug}` : "Catalog chart"}
        </span>
      ),
    },
    {
      key: "date",
      header: "Installed",
      kind: "age",
      accessor: (row) => <AgeCell value={row.createdAt} />,
      sortAccessor: (row) => row.createdAt,
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      rowActions: true,
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
        searchPlaceholder="Filter releases on this page..."
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
    <Tooltip content={clusterId}>
      <span className="text-sm text-muted-foreground">
        {cluster?.displayName || cluster?.name || clusterId.slice(0, 8)}
      </span>
    </Tooltip>
  );
}
