import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime } from '@/lib/utils';
import type { InstalledChart } from '@/types';
import { RotateCcw, Trash2 } from 'lucide-react';

export function InstalledTab({
  installed,
  loading,
  onRollback,
  onUninstall,
}: {
  installed: InstalledChart[] | undefined;
  loading: boolean;
  onRollback: (id: string, revision: number) => void;
  onUninstall: (id: string) => void;
}) {
  const installedColumns: Column<InstalledChart>[] = [
    {
      key: 'release',
      header: 'Release',
      accessor: (row) => (
        <span className="font-medium text-foreground font-mono text-xs">{row.releaseName}</span>
      ),
    },
    {
      key: 'chart',
      header: 'Chart',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.chartName}</span>
      ),
    },
    {
      key: 'version',
      header: 'Version',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
          {row.chartVersionLabel}
        </span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.clusterName}</span>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.namespace}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'revision',
      header: 'Rev',
      accessor: (row) => (
        <span className="tabular-nums text-xs text-muted-foreground">{row.revision}</span>
      ),
      sortAccessor: (row) => row.revision,
      align: 'center',
    },
    {
      key: 'installedBy',
      header: 'Installed By',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{row.installedBy}</span>
      ),
    },
    {
      key: 'date',
      header: 'Date',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
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
