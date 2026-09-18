import { useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { StatusBadge } from "@/components/ui/status-badge";
import { useCluster } from "@/lib/hooks/clusters";
import { formatRelativeTime } from "@/lib/utils";
import type { InstalledChart } from "@/types";
import { RotateCcw, Trash2 } from "lucide-react";

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
      accessor: (row) => (
        <div className="flex items-center gap-1">
          {/* UX-06: hide Upgrade until an upgrade modal / version picker is wired. */}
          <button
            onClick={() => {
              if (row.revision > 1) {
                onRollback(row.id, row.revision - 1);
              }
            }}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Rollback"
            disabled={row.revision <= 1}
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => setUninstallTarget(row)}
            className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Uninstall"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
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
