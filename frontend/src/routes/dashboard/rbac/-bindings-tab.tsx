import { Trash2 } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatRelativeTime } from '@/lib/utils';
import type { Cluster, ClusterRole, ClusterRoleBinding, User } from '@/types';

interface BindingsTabProps {
  bindings: ClusterRoleBinding[];
  clusterRoles: ClusterRole[];
  clusters: Cluster[];
  users: User[];
  loading: boolean;
  isError: boolean;
  onRetry: () => void;
  onRevoke: (binding: ClusterRoleBinding) => void;
}

export function BindingsTab({
  bindings,
  clusterRoles,
  clusters,
  users,
  loading,
  isError,
  onRetry,
  onRevoke,
}: BindingsTabProps) {
  const clusterRoleNameById = new Map(clusterRoles.map((r) => [r.id, r.displayName || r.name]));
  const clusterNameById = new Map(clusters.map((c) => [c.id, c.name]));
  const userLabelById = new Map(users.map((u) => [u.id, u.displayName || u.username]));

  const bindingColumns: Column<ClusterRoleBinding>[] = [
    {
      key: 'subject',
      header: 'Subject',
      accessor: (row) => (
        <span className="font-medium text-foreground">
          {row.user_id ? userLabelById.get(row.user_id) || row.user_id : `group: ${row.group}`}
        </span>
      ),
    },
    {
      key: 'role',
      header: 'Cluster Role',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{clusterRoleNameById.get(row.role_id) || row.role_id}</span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{clusterNameById.get(row.cluster_id) || row.cluster_id}</span>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) =>
        row.namespace ? (
          <span className="font-mono text-xs text-foreground">{row.namespace}</span>
        ) : (
          <span className="text-xs text-muted-foreground">cluster-wide</span>
        ),
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.created_at)}</span>,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => onRevoke(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Revoke binding"
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
      data={bindings}
      columns={bindingColumns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search bindings..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyMessage="No role bindings found"
    />
  );
}
