'use client';

import { useRouter } from '@/lib/navigation';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatRelativeTime } from '@/lib/utils';
import { fleetOpTypeLabel, type FleetOperation } from '@/lib/api/fleet-operations';
import { FleetStatusBadge } from './fleet-status-badge';

interface FleetOperationListProps {
  operations: FleetOperation[];
  loading?: boolean;
  isError?: boolean;
  onRetry?: () => void;
}

export function FleetOperationList({ operations, loading, isError, onRetry }: FleetOperationListProps) {
  const router = useRouter();

  const columns: Column<FleetOperation>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="min-w-0">
          <p className="truncate font-medium text-foreground">{row.name}</p>
          {row.description ? (
            <p className="truncate text-xs text-muted-foreground">{row.description}</p>
          ) : null}
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{fleetOpTypeLabel(row.operation_type)}</span>
      ),
      sortAccessor: (row) => row.operation_type,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <FleetStatusBadge status={row.status} />,
      sortAccessor: (row) => row.status,
    },
    {
      key: 'progress',
      header: 'Progress',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.completed_clusters}/{row.total_clusters} done
          {row.failed_clusters > 0 ? (
            <span className="text-status-error"> · {row.failed_clusters} failed</span>
          ) : null}
          {row.skipped_clusters > 0 ? (
            <span className="text-status-neutral"> · {row.skipped_clusters} skipped</span>
          ) : null}
        </span>
      ),
      sortAccessor: (row) => row.completed_clusters,
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.created_at)}</span>
      ),
      sortAccessor: (row) => row.created_at,
    },
  ];

  return (
    <DataTable
      data={operations}
      columns={columns}
      keyExtractor={(row) => row.id}
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyMessage="No fleet operations yet."
      searchable
      searchPlaceholder="Search operations…"
      onRowClick={(row) => router.push(`/dashboard/fleet/${row.id}`)}
    />
  );
}
