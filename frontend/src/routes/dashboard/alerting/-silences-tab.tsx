import { useAlertSilences } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatRelativeTime } from '@/lib/utils';
import type { AlertSilence } from '@/types';

export function SilencesTab() {
  const { data: silences, isLoading, isError, refetch } = useAlertSilences();

  const columns: Column<AlertSilence>[] = [
    {
      key: 'reason',
      header: 'Reason',
      accessor: (row) => <span className="font-medium text-foreground">{row.reason}</span>,
    },
    {
      key: 'duration',
      header: 'Duration',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.duration}</span>,
    },
    {
      key: 'matchers',
      header: 'Matchers',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {Object.entries(row.matchers).map(([k, v]) => (
            <span key={k} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
              {k}={v}
            </span>
          ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'creator',
      header: 'Creator',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.createdBy}</span>,
    },
    {
      key: 'endsAt',
      header: 'Expires',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.endsAt)}</span>,
    },
  ];

  return (
    <DataTable
      data={silences || []}
      columns={columns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search silences..."
      loading={isLoading}
      isError={isError}
      onRetry={() => refetch()}
      emptyMessage="No active silences"
    />
  );
}
