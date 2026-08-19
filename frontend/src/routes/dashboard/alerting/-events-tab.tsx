import { Check, CheckCircle } from 'lucide-react';
import { useAcknowledgeAlert, useAlertEvents, useResolveAlert } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionButton } from '@/components/ui/action-button';
import { cn, formatRelativeTime, statusBgColor } from '@/lib/utils';
import type { AlertEvent } from '@/types';

export function EventsTab({ clusterId }: { clusterId?: string } = {}) {
  const { data: events, isLoading, isError, refetch } = useAlertEvents(
    clusterId ? { clusterId } : undefined,
  );
  const acknowledgeAlert = useAcknowledgeAlert();
  const resolveAlert = useResolveAlert();

  const columns: Column<AlertEvent>[] = [
    {
      key: 'severity',
      header: 'Severity',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded capitalize font-medium', statusBgColor(row.severity))}>
          {row.severity}
        </span>
      ),
    },
    {
      key: 'rule',
      header: 'Rule',
      accessor: (row) => (
        <span className="font-medium text-foreground">{row.ruleName}</span>
      ),
    },
    {
      key: 'message',
      header: 'Message',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground truncate max-w-[300px] block">{row.message}</span>
      ),
      sortable: false,
    },
    ...(clusterId
      ? []
      : [{
          key: 'cluster',
          header: 'Cluster',
          accessor: (row: AlertEvent) => (
            <span className="text-sm text-muted-foreground">{row.clusterName || '--'}</span>
          ),
        } as Column<AlertEvent>]),
    {
      key: 'firedAt',
      header: 'Fired',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.firedAt)}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          {row.status === 'firing' && (
            <>
              <ActionButton
                size="sm"
                intent="ghost"
                title="Acknowledge"
                onClick={() => acknowledgeAlert.mutate(row.id)}
                icon={<Check className="h-3 w-3" />}
                className="h-auto px-2 py-1"
              >
                Ack
              </ActionButton>
              <ActionButton
                size="sm"
                intent="ghost"
                title="Resolve"
                onClick={() => resolveAlert.mutate(row.id)}
                icon={<CheckCircle className="h-3 w-3" />}
                className="h-auto px-2 py-1 hover:text-status-success hover:bg-status-success/10"
              >
                Resolve
              </ActionButton>
            </>
          )}
          {row.status === 'acknowledged' && (
            <ActionButton
              size="sm"
              intent="ghost"
              title="Resolve"
              onClick={() => resolveAlert.mutate(row.id)}
              icon={<CheckCircle className="h-3 w-3" />}
              className="h-auto px-2 py-1 hover:text-status-success hover:bg-status-success/10"
            >
              Resolve
            </ActionButton>
          )}
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <DataTable
      data={events || []}
      columns={columns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search active alerts..."
      loading={isLoading}
      isError={isError}
      onRetry={() => refetch()}
      emptyMessage="No active alerts"
    />
  );
}
