import { useState } from 'react';
import { useLoggingOperations, useRetryLoggingOperation } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionButton } from '@/components/ui/action-button';
import { Select } from '@/components/ui/select';
import { capitalize, formatRelativeTime, cn } from '@/lib/utils';
import type { LoggingOperation } from '@/types';
import { X, RotateCcw } from 'lucide-react';
import { mapLoggingOperationStatus, truncate } from './-utils';

export function OperationsTab() {
  const [statusFilter, setStatusFilter] = useState('');
  const [targetFilter, setTargetFilter] = useState('');
  // Server-side params kept narrow so the list query key changes drive the
  // refetch — client-side filtering of the bigger fields happens in DataTable.
  const { data: operations, isLoading, isError, refetch } = useLoggingOperations({
    status: statusFilter || undefined,
    target_type: targetFilter || undefined,
    limit: 100,
  });
  const retryOperation = useRetryLoggingOperation();

  const columns: Column<LoggingOperation>[] = [
    {
      key: 'targetType',
      header: 'Target Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.targetType}
        </span>
      ),
      sortAccessor: (row) => row.targetType,
    },
    {
      key: 'operation',
      header: 'Operation',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.operation}
        </span>
      ),
      sortAccessor: (row) => row.operation,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={mapLoggingOperationStatus(row.status)}
          label={capitalize(row.status)}
          pulse={row.status === 'running'}
        />
      ),
      sortAccessor: (row) => row.status,
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground" title={row.createdAt}>
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
      sortAccessor: (row) => row.createdAt,
    },
    {
      key: 'updated',
      header: 'Age / Updated',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground" title={row.updatedAt}>
          {formatRelativeTime(row.updatedAt)}
        </span>
      ),
      sortAccessor: (row) => row.updatedAt,
    },
    {
      key: 'error',
      header: 'Error',
      accessor: (row) =>
        row.errorMessage ? (
          <span
            className="text-xs text-status-error/80 line-clamp-1 max-w-[260px] block"
            title={row.errorMessage}
          >
            {truncate(row.errorMessage, 80)}
          </span>
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => {
        const retryable = row.status === 'failed' || row.status === 'superseded';
        if (!retryable) {
          return <span className="text-xs text-muted-foreground">—</span>;
        }
        return (
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <button
              onClick={() => retryOperation.mutate(row.id)}
              disabled={retryOperation.isPending}
              className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground
                hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
              title="Retry operation"
            >
              <RotateCcw className={cn('h-3 w-3', retryOperation.isPending && 'animate-spin')} />
              Retry
            </button>
          </div>
        );
      },
      sortable: false,
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <label className="text-xs text-muted-foreground" htmlFor="logging-ops-status">
          Status
        </label>
        <Select
          id="logging-ops-status"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          className="h-8 w-auto text-xs"
        >
          <option value="">All</option>
          <option value="pending">Pending</option>
          <option value="running">Running</option>
          <option value="completed">Completed</option>
          <option value="failed">Failed</option>
          <option value="superseded">Superseded</option>
        </Select>
        <label className="text-xs text-muted-foreground ml-2" htmlFor="logging-ops-target">
          Target
        </label>
        <Select
          id="logging-ops-target"
          value={targetFilter}
          onChange={(e) => setTargetFilter(e.target.value)}
          className="h-8 w-auto text-xs"
        >
          <option value="">All</option>
          <option value="output">Output</option>
          <option value="pipeline">Pipeline</option>
        </Select>
        {(statusFilter || targetFilter) && (
          <ActionButton
            size="sm"
            intent="ghost"
            icon={<X className="h-3 w-3" />}
            onClick={() => {
              setStatusFilter('');
              setTargetFilter('');
            }}
          >
            Clear
          </ActionButton>
        )}
      </div>
      <DataTable
        data={operations || []}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search operations..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No reconciler activity yet."
        pageSize={20}
      />
    </div>
  );
}
