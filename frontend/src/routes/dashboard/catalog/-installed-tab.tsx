import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { formatRelativeTime } from "@/lib/utils";
import type { InstalledChart } from "@/types";
import { RotateCcw, Trash2 } from "lucide-react";

export function InstalledTab({
  installed,
  loading,
  clusterNames,
  onRollback,
  onUninstall,
}: {
  installed: InstalledChart[] | undefined;
  loading: boolean;
  clusterNames: Readonly<Record<string, string>>;
  onRollback: (id: string, revision: number) => void;
  onUninstall: (id: string) => void;
}) {
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
          className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono"
          title={row.chartVersionId || undefined}
        >
          {row.chartVersionId ? row.chartVersionId.slice(0, 8) : "managed tool"}
        </span>
      ),
    },
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <span className="text-sm text-muted-foreground" title={row.clusterId}>
          {clusterNames[row.clusterId] || row.clusterId.slice(0, 8)}
        </span>
      ),
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
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Rollback"
            disabled={row.revision <= 1}
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => {
              if (confirm(`Uninstall release "${row.releaseName}"?`)) {
                onUninstall(row.id);
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
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
    <DataTable
      data={installed || []}
      columns={installedColumns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search installed releases..."
      loading={loading}
      emptyMessage="No charts installed"
    />
  );
}
