import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import type { ElementType } from 'react';
import {
  useLoggingOutputs,
  useTestLoggingOutput,
  queryKeys,
} from '@/lib/hooks';
import { deleteLoggingOutput, updateLoggingOutput } from '@/lib/api';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { Badge } from '@/components/ui/badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { LoggingOutput } from '@/types';
import {
  FileText,
  Trash2,
  Send,
  Database,
  Cloud,
  HardDrive,
  Server,
} from 'lucide-react';
import { toastError, toastSuccess } from '@/lib/toast';

function outputTypeOf(row: LoggingOutput): string {
  return row.outputType || row.type || '';
}

const outputTypeIcons: Record<string, ElementType> = {
  elasticsearch: Database,
  loki: FileText,
  splunk: Cloud,
  cloudwatch: Cloud,
  datadog: Cloud,
  s3: HardDrive,
  syslog: Server,
};

export function OutputsTab() {
  const queryClient = useQueryClient();
  const [deleteTarget, setDeleteTarget] = useState<LoggingOutput | null>(null);
  const [deleting, setDeleting] = useState(false);
  const { data: outputs, isLoading, isError, refetch } = useLoggingOutputs();
  const testOutput = useTestLoggingOutput();

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteLoggingOutput(deleteTarget.id);
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess('Logging output deleted');
      setDeleteTarget(null);
    } catch (error) {
      toastError(`Failed to delete output: ${error instanceof Error ? error.message : 'Unknown error'}`);
    } finally {
      setDeleting(false);
    }
  };

  const handleToggle = async (output: LoggingOutput) => {
    try {
      await updateLoggingOutput(output.id, { enabled: !output.enabled });
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess(`Output ${output.enabled ? 'disabled' : 'enabled'}`);
    } catch (error) {
      toastError(`Failed to update output: ${error instanceof Error ? error.message : 'Unknown error'}`);
    }
  };

  const columns: Column<LoggingOutput>[] = [
    {
      key: 'name',
      header: 'Output',
      accessor: (row) => {
        const type = outputTypeOf(row);
        const TypeIcon = outputTypeIcons[type] || Database;
        return (
          <div className="flex items-center gap-2">
            <TypeIcon className="h-4 w-4 text-muted-foreground" />
            <div>
              <div className="flex items-center gap-2">
                <p className="font-medium text-foreground">{row.name}</p>
                {row.isSystem ? (
                  <Badge variant="info" data-testid="system-output-badge">
                    System
                  </Badge>
                ) : null}
              </div>
              <p className="text-xs text-muted-foreground capitalize">{type}</p>
            </div>
          </div>
        );
      },
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {outputTypeOf(row)}
        </span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.clusterName || 'All'}</span>
      ),
    },
    {
      key: 'status',
      header: 'Connection',
      accessor: (row) => <StatusBadge status={row.status || 'disconnected'} />,
    },
    {
      key: 'enabled',
      header: 'Enabled',
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            if (row.isSystem) return;
            handleToggle(row);
          }}
          disabled={row.isSystem}
          title={row.isSystem ? 'System destinations are managed with Astronomer Loki' : undefined}
          className={cn(
            'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
            row.enabled ? 'bg-primary' : 'bg-muted',
            row.isSystem && 'cursor-not-allowed opacity-60',
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
            onClick={() => testOutput.mutate(row.id)}
            disabled={testOutput.isPending}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Test Output"
          >
            <Send className="h-3 w-3" />
            Test
          </button>
          {row.isSystem ? null : (
            <button
              onClick={() => setDeleteTarget(row)}
              className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
              title="Delete output"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <>
      <DataTable
        data={outputs || []}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search logging outputs..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No logging outputs configured"
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        title="Delete Logging Output"
        description={`Delete the logging output "${deleteTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleting}
      />
    </>
  );
}
