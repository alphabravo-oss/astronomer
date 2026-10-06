import { usePipelinePageParam } from "./-pipeline-page-param";
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
import {
  CappedChips,
  NameSubCell,
  TimestampCell,
} from "@/components/ui/cell-primitives";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Switch } from "@/components/ui/switch";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import type { LoggingPipeline } from "@/types";
import { Trash2 } from "lucide-react";
import { toastError, toastSuccess } from "@/lib/toast";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";

export function PipelinesTab({ clusterId }: { clusterId?: string } = {}) {
  const queryClient = useQueryClient();
  const { pageIndex, page, setPageIndex } = usePipelinePageParam();
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
      kind: "name",
      minSize: 220,
      accessor: (row) => (
        <NameSubCell
          title={
            <Link
              to={String(`/dashboard/logging/pipelines/${row.id}`)}
              search={{ pipelinePage: page }}
              className="hover:underline"
            >
              {row.name}
            </Link>
          }
          subtitle={row.description}
        />
      ),
    },
    ...(clusterId
      ? []
      : [
          {
            key: "cluster",
            header: "Cluster",
            kind: "text",
            size: 160,
            minSize: 128,
            accessor: (row: LoggingPipeline) => (
              <span className="text-sm text-muted-foreground">
                {row.clusterName || row.clusterId || "Unavailable"}
              </span>
            ),
          } as Column<LoggingPipeline>,
        ]),
    {
      key: "namespaces",
      header: "Namespaces",
      kind: "badge",
      size: 220,
      minSize: 180,
      maxSize: 280,
      accessor: (row) => <CappedChips items={row.namespaces} empty="All" />,
      sortable: false,
    },
    {
      key: "outputs",
      header: "Outputs",
      accessor: (row) => (
        <span className="tabular-nums text-sm">{row.outputNames.length}</span>
      ),
      sortAccessor: (row) => row.outputNames.length,
      kind: "count",
    },
    {
      key: "enabled",
      header: "Enabled",
      kind: "badge",
      size: 88,
      minSize: 80,
      maxSize: 96,
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
      kind: "age",
      accessor: (row) => <TimestampCell value={row.createdAt} />,
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <PipelineDelete row={row} onDelete={() => setDeleteTarget(row)} />
      ),
      sortable: false,
      kind: "actions",
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
    <Tooltip content={permission.reason}>
      <span onClickCapture={(event) => event.stopPropagation()}>
        <Switch
          size="sm"
          checked={row.enabled}
          disabled={!permission.allowed || pending}
          onCheckedChange={onToggle}
        />
      </span>
    </Tooltip>
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
    <ActionButton
      intent="bare"
      size="none"
      onClick={onDelete}
      disabled={!permission.allowed}
      tooltip={permission.allowed ? "Delete pipeline" : undefined}
      disabledReason={permission.allowed ? undefined : permission.reason}
      aria-label="Delete pipeline"
      className="p-1.5 disabled:opacity-50"
    >
      <Trash2 className="h-3.5 w-3.5" />
    </ActionButton>
  );
}
