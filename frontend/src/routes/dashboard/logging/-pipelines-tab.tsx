import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useLoggingPipelines, queryKeys } from '@/lib/hooks';
import { deleteLoggingPipeline, updateLoggingPipeline } from '@/lib/api';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { LoggingPipeline } from '@/types';
import { Trash2 } from 'lucide-react';
import { toastError, toastSuccess } from '@/lib/toast';

export function PipelinesTab({ clusterId }: { clusterId?: string } = {}) {
  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<LoggingPipeline | null>(null);
  const [deleting, setDeleting] = useState(false);
  const { data: pipelines, isLoading, isError, refetch } = useLoggingPipelines(clusterId);

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteLoggingPipeline(deleteTarget.id);
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess('Logging pipeline deleted');
      setDeleteTarget(null);
    } catch (error) {
      toastError(`Failed to delete pipeline: ${error instanceof Error ? error.message : 'Unknown error'}`);
    } finally {
      setDeleting(false);
    }
  };

  const handleToggle = async (pipeline: LoggingPipeline) => {
    try {
      await updateLoggingPipeline(pipeline.id, { enabled: !pipeline.enabled });
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess(`Pipeline ${pipeline.enabled ? 'disabled' : 'enabled'}`);
    } catch (error) {
      toastError(`Failed to update pipeline: ${error instanceof Error ? error.message : 'Unknown error'}`);
    }
  };

  const columns: Column<LoggingPipeline>[] = [
    {
      key: 'name',
      header: 'Pipeline',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          {row.description && (
            <p className="text-xs text-muted-foreground truncate max-w-[300px]">{row.description}</p>
          )}
        </div>
      ),
    },
    ...(clusterId
      ? []
      : [{
          key: 'cluster',
          header: 'Cluster',
          accessor: (row: LoggingPipeline) => (
            <span className="text-sm text-muted-foreground">{row.clusterName || 'All'}</span>
          ),
        } as Column<LoggingPipeline>]),
    {
      key: 'namespaces',
      header: 'Namespaces',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.namespaces.length === 0 ? (
            <span className="text-xs text-muted-foreground">All</span>
          ) : (
            row.namespaces.slice(0, 3).map((ns) => (
              <span key={ns} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                {ns}
              </span>
            ))
          )}
          {row.namespaces.length > 3 && (
            <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              +{row.namespaces.length - 3}
            </span>
          )}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'outputs',
      header: 'Outputs',
      accessor: (row) => (
        <span className="tabular-nums text-sm">{row.outputNames.length}</span>
      ),
      sortAccessor: (row) => row.outputNames.length,
      align: 'center',
    },
    {
      key: 'enabled',
      header: 'Enabled',
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            handleToggle(row);
          }}
          className={cn(
            'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
            row.enabled ? 'bg-primary' : 'bg-muted'
          )}
        >
          <span
            className={cn(
              'inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform',
              row.enabled ? 'translate-x-[18px]' : 'translate-x-[3px]'
            )}
          />
        </button>
      ),
      sortable: false,
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => setDeleteTarget(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete pipeline"
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
        data={pipelines || []}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search logging pipelines..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No logging pipelines configured"
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
