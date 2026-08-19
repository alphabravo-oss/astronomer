import { Shield } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { Badge } from '@/components/ui/badge';
import { formatRelativeTime } from '@/lib/utils';
import type { ClusterRole, GlobalRole, ProjectRole } from '@/types';

function TypeBadge({ builtin }: { builtin: boolean }) {
  return <Badge variant={builtin ? 'secondary' : 'info'}>{builtin ? 'Built-in' : 'Custom'}</Badge>;
}

const globalRoleColumns: Column<GlobalRole>[] = [
  {
    key: 'name',
    header: 'Role',
    accessor: (row) => (
      <div className="flex items-center gap-2">
        <Shield className="h-4 w-4 text-muted-foreground" />
        <div>
          <p className="font-medium text-foreground">{row.displayName}</p>
          <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
        </div>
      </div>
    ),
  },
  {
    key: 'description',
    header: 'Description',
    accessor: (row) => <span className="text-sm text-muted-foreground">{row.description || '--'}</span>,
    sortable: false,
  },
  {
    key: 'builtin',
    header: 'Type',
    accessor: (row) => <TypeBadge builtin={row.builtin} />,
  },
  {
    key: 'rules',
    header: 'Rules',
    accessor: (row) => <span className="tabular-nums text-sm">{row.rules?.length ?? 0}</span>,
    sortAccessor: (row) => row.rules?.length ?? 0,
    align: 'center',
  },
  {
    key: 'created',
    header: 'Created',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const clusterRoleColumns: Column<ClusterRole>[] = [
  {
    key: 'name',
    header: 'Role',
    accessor: (row) => (
      <div>
        <p className="font-medium text-foreground">{row.displayName}</p>
        <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
      </div>
    ),
  },
  {
    key: 'cluster',
    header: 'Cluster',
    accessor: (row) => <span className="text-sm text-muted-foreground">{row.clusterName}</span>,
  },
  {
    key: 'builtin',
    header: 'Type',
    accessor: (row) => <TypeBadge builtin={row.builtin} />,
  },
  {
    key: 'rules',
    header: 'Rules',
    accessor: (row) => <span className="tabular-nums text-sm">{row.rules?.length ?? 0}</span>,
    align: 'center',
  },
  {
    key: 'created',
    header: 'Created',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const projectRoleColumns: Column<ProjectRole>[] = [
  {
    key: 'name',
    header: 'Role',
    accessor: (row) => (
      <div>
        <p className="font-medium text-foreground">{row.displayName}</p>
        <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
      </div>
    ),
  },
  {
    key: 'project',
    header: 'Project',
    accessor: (row) => <span className="text-sm text-muted-foreground">{row.projectName}</span>,
  },
  {
    key: 'builtin',
    header: 'Type',
    accessor: (row) => <TypeBadge builtin={row.builtin} />,
  },
  {
    key: 'rules',
    header: 'Rules',
    accessor: (row) => <span className="tabular-nums text-sm">{row.rules?.length ?? 0}</span>,
    align: 'center',
  },
  {
    key: 'created',
    header: 'Created',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

interface RolesTabProps<T> {
  data: T[];
  loading: boolean;
  isError: boolean;
  onRetry: () => void;
}

export function GlobalRolesTab({ data, loading, isError, onRetry }: RolesTabProps<GlobalRole>) {
  return (
    <DataTable
      data={data}
      columns={globalRoleColumns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search global roles..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyMessage="No global roles defined"
    />
  );
}

export function ClusterRolesTab({ data, loading, isError, onRetry }: RolesTabProps<ClusterRole>) {
  return (
    <DataTable
      data={data}
      columns={clusterRoleColumns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search cluster roles..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyMessage="No cluster roles defined"
    />
  );
}

export function ProjectRolesTab({ data, loading, isError, onRetry }: RolesTabProps<ProjectRole>) {
  return (
    <DataTable
      data={data}
      columns={projectRoleColumns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search project roles..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyMessage="No project roles defined"
    />
  );
}
