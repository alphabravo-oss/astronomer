import { Trash2 } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { Badge } from '@/components/ui/badge';
import { ActionButton } from '@/components/ui/action-button';
import { formatRelativeTime } from '@/lib/utils';
import type { AccessBinding, Cluster, ClusterRole, GlobalRole, Project, ProjectRole, User } from '@/types';
import { bindingSubject, bindingTarget, roleTitle } from './-utils';

interface BindingsTabProps {
  bindings: AccessBinding[];
  globalRoles: GlobalRole[];
  clusterRoles: ClusterRole[];
  projectRoles: ProjectRole[];
  clusters: Cluster[];
  projects: Project[];
  users: User[];
  loading: boolean;
  isError: boolean;
  onRetry: () => void;
  onRevoke: (binding: AccessBinding) => void;
}

export function BindingsTab({
  bindings,
  globalRoles,
  clusterRoles,
  projectRoles,
  clusters,
  projects,
  users,
  loading,
  isError,
  onRetry,
  onRevoke,
}: BindingsTabProps) {
  const roleName = (binding: AccessBinding) => {
    const roles =
      binding.scope === 'global' ? globalRoles : binding.scope === 'project' ? projectRoles : clusterRoles;
    const role = roles.find((r) => r.id === binding.roleId);
    return role ? roleTitle(role) : binding.roleId;
  };

  const columns: Column<AccessBinding>[] = [
    {
      key: 'subject',
      header: 'Subject',
      accessor: (row) => (
        <span className="font-medium text-foreground">{bindingSubject(row, users)}</span>
      ),
      sortAccessor: (row) => bindingSubject(row, users),
    },
    {
      key: 'scope',
      header: 'Scope',
      accessor: (row) => (
        <Badge variant="secondary" className="capitalize">
          {row.scope}
        </Badge>
      ),
      sortAccessor: (row) => row.scope,
      filter: { label: 'Scope' },
    },
    {
      key: 'role',
      header: 'Role',
      accessor: (row) => <span className="text-sm text-muted-foreground">{roleName(row)}</span>,
      sortAccessor: (row) => roleName(row),
    },
    {
      key: 'target',
      header: 'Applies to',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{bindingTarget(row, clusters, projects)}</span>
      ),
      sortAccessor: (row) => bindingTarget(row, clusters, projects),
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center justify-end" onClick={(e) => e.stopPropagation()}>
          <ActionButton
            size="icon"
            intent="ghost"
            icon={<Trash2 className="h-3.5 w-3.5" />}
            onClick={() => onRevoke(row)}
            title="Revoke binding"
            className="text-muted-foreground hover:text-status-error hover:bg-status-error/10"
          />
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <DataTable
      data={bindings}
      columns={columns}
      keyExtractor={(row) => `${row.scope}:${row.id}`}
      searchPlaceholder="Search bindings..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyMessage="No role bindings found"
    />
  );
}
