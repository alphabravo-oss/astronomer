import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { HelmRepository } from '@/types';
import { Globe, RefreshCw, Trash2 } from 'lucide-react';

/**
 * The catalog Repositories table.
 *
 * Split out of the route file so it can be rendered directly by a test: the
 * `-` prefix keeps TanStack from treating this as a route, and the route file
 * keeps `Route` as its only export so the page still code-splits.
 *
 * The columns read camelCase fields. That is correct and deliberate — the API
 * serialises snake_case, and the axios response interceptor (src/lib/camelize.ts)
 * rewrites every key before any component sees it.
 */
export interface RepositoriesTableProps {
  repos: HelmRepository[];
  loading?: boolean;
  onSync: (id: string) => void;
  onDelete: (id: string) => void;
  syncPending?: boolean;
}

export function RepositoriesTable({
  repos,
  loading,
  onSync,
  onDelete,
  syncPending,
}: RepositoriesTableProps) {
  const repoColumns: Column<HelmRepository>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Globe className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
          {row.isDefault && (
            <span className="text-2xs px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">Default</span>
          )}
        </div>
      ),
    },
    {
      key: 'url',
      header: 'URL',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground truncate max-w-[300px] block">{row.url}</span>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground uppercase">
          {row.repoType}
        </span>
      ),
    },
    {
      key: 'charts',
      header: 'Charts',
      // chart_count is enrichment the server computes per response; if it is
      // ever absent, render an explicit 0 rather than an empty cell. React
      // renders `undefined` as nothing at all, which is how this column
      // displayed blank for every repository while the field did not exist.
      accessor: (row) => (
        <span className="tabular-nums text-sm text-muted-foreground">{row.chartCount ?? 0}</span>
      ),
      sortAccessor: (row) => row.chartCount ?? 0,
      align: 'center',
    },
    {
      key: 'lastSynced',
      header: 'Last Synced',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastSyncedAt ? formatRelativeTime(row.lastSyncedAt) : 'Never'}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      // The scheduled sweep isolates failures per repository, so a repo can be
      // Enabled and silently not refreshing. Surface last_sync_error here or
      // the only trace is a worker log line.
      accessor: (row) =>
        row.enabled && row.lastSyncError ? (
          <span title={row.lastSyncError}>
            <StatusBadge status="failed" label="Sync failed" />
          </span>
        ) : (
          <StatusBadge
            status={row.enabled ? 'active' : 'disconnected'}
            label={row.enabled ? 'Enabled' : 'Disabled'}
          />
        ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => onSync(row.id)}
            disabled={syncPending}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground
              hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Sync repository"
          >
            <RefreshCw className={cn('h-3 w-3', syncPending && 'animate-spin')} />
            Sync
          </button>
          <button
            onClick={() => {
              if (confirm(`Delete repository "${row.name}"?`)) {
                onDelete(row.id);
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete repository"
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
      data={repos}
      columns={repoColumns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search repositories..."
      loading={loading}
      emptyMessage="No repositories configured"
    />
  );
}
