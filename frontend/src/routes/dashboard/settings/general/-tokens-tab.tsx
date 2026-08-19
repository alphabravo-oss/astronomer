import { Plus, Trash2 } from 'lucide-react';
import { useAPITokens, useDeleteAPIToken } from '@/lib/hooks';
import { formatDate, formatRelativeTime } from '@/lib/utils';
import type { APIToken } from '@/types';
import { ActionButton } from '@/components/ui/action-button';
import { DataTable, type Column } from '@/components/ui/data-table';

export function TokensTab({ onCreate }: { onCreate: () => void }) {
  const { data: tokens, isLoading: tokensLoading } = useAPITokens();
  const deleteToken = useDeleteAPIToken();

  const tokenColumns: Column<APIToken>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          {row.description && <p className="text-xs text-muted-foreground">{row.description}</p>}
        </div>
      ),
    },
    {
      key: 'prefix',
      header: 'Prefix',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.prefix}...</span>,
    },
    {
      key: 'createdBy',
      header: 'Created By',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.createdBy}</span>,
    },
    {
      key: 'expires',
      header: 'Expires',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.expiresAt ? formatDate(row.expiresAt) : 'Never'}
        </span>
      ),
    },
    {
      key: 'lastUsed',
      header: 'Last Used',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastUsedAt ? formatRelativeTime(row.lastUsedAt) : 'Never'}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            if (confirm('Are you sure you want to delete this token?')) {
              deleteToken.mutate(row.id);
            }
          }}
          className="text-muted-foreground hover:text-status-error transition-colors"
          title="Delete token"
        >
          <Trash2 className="h-4 w-4" />
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          API tokens for programmatic access to the Astronomer API.
        </p>
        <ActionButton intent="primary" icon={<Plus className="h-4 w-4" />} onClick={onCreate}>
          Create Token
        </ActionButton>
      </div>

      <DataTable
        data={tokens || []}
        columns={tokenColumns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search tokens..."
        loading={tokensLoading}
        emptyMessage="No API tokens created"
      />
    </div>
  );
}
