import { pageTableCount } from "@/lib/api/pagination";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useLoggingPipelines } from "@/lib/hooks/logging";
import { queryKeys } from "@/lib/query-keys";
import {
  deleteLoggingPipeline,
  updateLoggingPipeline,
} from "@/lib/api/logging";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Switch } from "@/components/ui/switch";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { formatRelativeTime } from "@/lib/utils";
import type { LoggingPipeline } from "@/types";
import { Trash2 } from "lucide-react";
import { toastError, toastSuccess } from "@/lib/toast";

export function PipelinesTab({ clusterId }: { clusterId?: string } = {}) {
  const queryClient = useQueryClient();
  const [pageIndex, setPageIndex] = useState(0);
  const [toggling, setToggling] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<LoggingPipeline | null>(
    null,
  );
  const [deleting, setDeleting] = useState(false);
  const {
    data: pipelines,
    isLoading,
    isError,
    refetch,
  } = useLoggingPipelines(clusterId, pageIndex * 50);

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteLoggingPipeline(deleteTarget.id);
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess("Logging pipeline deleted");
      setDeleteTarget(null);
    } catch (error) {
      toastError(
        `Failed to delete pipeline: ${error instanceof Error ? error.message : "Unknown error"}`,
      );
    } finally {
      setDeleting(false);
    }
  };

  const handleToggle = async (pipeline: LoggingPipeline) => {
    if (toggling) return;
    setToggling(pipeline.id);
    try {
      await updateLoggingPipeline(pipeline.id, {
        ...pipeline,
        enabled: !pipeline.enabled,
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess(`Pipeline ${pipeline.enabled ? "disabled" : "enabled"}`);
    } catch (error) {
      toastError(
        `Failed to update pipeline: ${error instanceof Error ? error.message : "Unknown error"}`,
      );
    } finally {
      setToggling(null);
    }
  };

  const columns: Column<LoggingPipeline>[] = [
    {
      key: "name",
      header: "Pipeline",
      accessor: (row) => (
        <div>
          <Link
            to={String(`/dashboard/logging/pipelines/${row.id}`)}
            className="font-medium text-foreground hover:underline"
          >
            {row.name}
          </Link>
          {row.description && (
            <p className="text-xs text-muted-foreground truncate max-w-[300px]">
              {row.description}
            </p>
          )}
        </div>
      ),
    },
    ...(clusterId
      ? []
      : [
          {
            key: "cluster",
            header: "Cluster",
            accessor: (row: LoggingPipeline) => (
              <span className="text-sm text-muted-foreground">
                {row.clusterName || "All"}
              </span>
            ),
          } as Column<LoggingPipeline>,
        ]),
    {
      key: "namespaces",
      header: "Namespaces",
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.namespaces.length === 0 ? (
            <span className="text-xs text-muted-foreground">All</span>
          ) : (
            row.namespaces.slice(0, 3).map((ns) => (
              <span
                key={ns}
                className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground font-mono"
              >
                {ns}
              </span>
            ))
          )}
          {row.namespaces.length > 3 && (
            <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground">
              +{row.namespaces.length - 3}
            </span>
          )}
        </div>
      ),
      sortable: false,
    },
    {
      key: "outputs",
      header: "Outputs",
      accessor: (row) => (
        <span className="tabular-nums text-sm">{row.outputNames.length}</span>
      ),
      sortAccessor: (row) => row.outputNames.length,
      align: "center",
    },
    {
      key: "enabled",
      header: "Enabled",
      accessor: (row) => (
        <PipelineToggle
          row={row}
          pending={!!toggling}
          onToggle={() => void handleToggle(row)}
        />
      ),
      sortable: false,
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
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <PipelineDelete row={row} onDelete={() => setDeleteTarget(row)} />
      ),
      sortable: false,
    },
  ];

  return (
    <>
      <DataTable
        data={pipelines?.data || []}
        columns={columns.map((column) => ({ ...column, sortable: false }))}
        keyExtractor={(row) => row.id}
        searchable={false}
        pageSize={50}
        serverSide={{
          ...pageTableCount(pipelines),
          pagination: { pageIndex, pageSize: 50 },
          onPaginationChange: (next) => setPageIndex(next.pageIndex),
        }}
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyState={{
          title: "No logging pipelines configured",
          description: "Create the first item to configure this feature.",
        }}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        title="Delete Logging Pipeline"
        description={`Delete the logging pipeline "${deleteTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleting}
      />
    </>
  );
}

function PipelineToggle({
  row,
  pending,
  onToggle,
}: {
  row: LoggingPipeline;
  pending: boolean;
  onToggle: () => void;
}) {
  const permission = usePermissionDecision("logging", "update", {
    type: "cluster",
    id: row.clusterId,
  });
  return (
    <span
      onClickCapture={(event) => event.stopPropagation()}
      title={permission.reason}
    >
      <Switch
        size="sm"
        checked={row.enabled}
        disabled={!permission.allowed || pending}
        onCheckedChange={onToggle}
      />
    </span>
  );
}
function PipelineDelete({
  row,
  onDelete,
}: {
  row: LoggingPipeline;
  onDelete: () => void;
}) {
  const permission = usePermissionDecision("logging", "delete", {
    type: "cluster",
    id: row.clusterId,
  });
  return (
    <button
      onClick={onDelete}
      disabled={!permission.allowed}
      title={permission.allowed ? "Delete pipeline" : permission.reason}
      className="p-1.5 disabled:opacity-50"
    >
      <Trash2 className="h-3.5 w-3.5" />
    </button>
  );
}
