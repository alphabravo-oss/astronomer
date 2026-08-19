import { Shield } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { Badge } from '@/components/ui/badge';
import { formatRelativeTime } from '@/lib/utils';
import type { ClusterRole, GlobalRole, ProjectRole } from '@/types';
import { crdGrantCount, isBuiltinRole, roleTitle, type RoleLike } from './-utils';

function TypeBadge({ builtin }: { builtin: boolean }) {
  return <Badge variant={builtin ? 'secondary' : 'info'}>{builtin ? 'Built-in' : 'Custom'}</Badge>;
}

function roleColumns<T extends RoleLike>(): Column<T>[] {
  return [
    {
      key: 'name',
      header: 'Role',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Shield className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{roleTitle(row)}</p>
            <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
          </div>
        </div>
      ),
      sortAccessor: (row) => roleTitle(row),
    },
    {
      key: 'description',
      header: 'Description',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.description || '—'}</span>,
      sortable: false,
    },
    {
      key: 'builtin',
      header: 'Type',
      accessor: (row) => <TypeBadge builtin={isBuiltinRole(row)} />,
      sortAccessor: (row) => (isBuiltinRole(row) ? 'Built-in' : 'Custom'),
      filter: { label: 'Type' },
    },
    {
      key: 'rules',
      header: 'Rules',
      accessor: (row) => <span className="tabular-nums text-sm">{row.rules?.length ?? 0}</span>,
      sortAccessor: (row) => row.rules?.length ?? 0,
      align: 'center',
    },
    {
      key: 'crd',
      header: 'CRD grants',
      accessor: (row) => {
        const count = crdGrantCount(row.rules);
        return count > 0 ? (
          <span className="tabular-nums text-sm">{count}</span>
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        );
      },
      sortAccessor: (row) => crdGrantCount(row.rules),
      align: 'center',
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.createdAt ? formatRelativeTime(row.createdAt) : '—'}
        </span>
      ),
      sortAccessor: (row) => row.createdAt || '',
    },
  ];
}

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
      columns={roleColumns<GlobalRole>()}
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
      columns={roleColumns<ClusterRole>()}
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
      columns={roleColumns<ProjectRole>()}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search project roles..."
      loading={loading}
      isError={isError}
      onRetry={onRetry}
      emptyMessage="No project roles defined"
    />
  );
}
