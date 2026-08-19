import { useMemo, useState } from 'react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { useClusters, useEffectivePermissions, useProjects, useUsers } from '@/lib/hooks';
import type {
  EffectivePermissionBinding,
  EffectivePermissionGrant,
  EffectivePermissionSource,
  User,
} from '@/types';
import { clusterLabel, projectLabel, userLabel } from './-utils';

export function EffectiveTab() {
  const { data: usersData } = useUsers({ pageSize: 200 });
  const { data: clustersData } = useClusters({ pageSize: 200 });
  const { data: projectsData } = useProjects({ pageSize: 200 });
  const users = usersData?.data || [];
  const clusters = clustersData?.data || [];
  const projects = projectsData?.data || [];

  const [userId, setUserId] = useState('');
  const [clusterId, setClusterId] = useState('');
  const [projectId, setProjectId] = useState('');
  const [namespace, setNamespace] = useState('');

  const selectedContext = {
    clusterId: clusterId || undefined,
    projectId: projectId || undefined,
    namespace: namespace.trim() || undefined,
  };
  const { data, isLoading, isError, refetch } = useEffectivePermissions(userId || undefined, selectedContext);

  const permissions = data?.permissions ?? [];
  const bindings = data?.bindings ?? [];
  const responseContext = data?.context;
  const superuser = data?.superuser === true;
  const resourceCount = new Set(permissions.map((p) => p.resource)).size;
  const highRiskCount = permissions.filter(isHighRiskGrant).length;
  const applicableCount = permissions.filter((p) => p.appliesToContext !== false).length;

  const clusterNameById = useMemo(() => new Map(clusters.map((c) => [c.id, clusterLabel(c)])), [clusters]);
  const projectNameById = useMemo(() => new Map(projects.map((p) => [p.id, projectLabel(p)])), [projects]);

  const permissionColumns: Column<EffectivePermissionGrant>[] = [
    {
      key: 'applies',
      header: 'Applies',
      accessor: (row) => (
        <Badge variant={row.appliesToContext === false ? 'secondary' : 'success'}>
          {row.appliesToContext === false ? 'No' : 'Yes'}
        </Badge>
      ),
      sortAccessor: (row) => (row.appliesToContext === false ? 0 : 1),
    },
    {
      key: 'resource',
      header: 'Resource',
      accessor: (row) => <span className="font-mono text-sm">{row.resource}</span>,
      sortAccessor: (row) => row.resource,
    },
    {
      key: 'verb',
      header: 'Verb',
      accessor: (row) => <span className="font-mono text-sm">{row.verb}</span>,
      sortAccessor: (row) => row.verb,
    },
    {
      key: 'risk',
      header: 'Risk',
      accessor: (row) => <Badge variant={riskVariant(row)}>{riskLabel(row)}</Badge>,
      sortAccessor: (row) => riskSort(row),
    },
    {
      key: 'sources',
      header: 'Granted By',
      accessor: (row) => sourceSummary(row.sources),
      sortable: false,
    },
    {
      key: 'target',
      header: 'Scope Target',
      accessor: (row) => targetSummary(row.sources, clusterNameById, projectNameById),
      sortable: false,
    },
  ];

  const bindingColumns: Column<EffectivePermissionBinding>[] = [
    {
      key: 'role',
      header: 'Role',
      accessor: (row) => row.roleName || row.roleId || row.bindingId || row.scope,
    },
    {
      key: 'scope',
      header: 'Scope',
      accessor: (row) => (
        <Badge variant={row.superuser ? 'warning' : 'secondary'} className="capitalize">
          {row.superuser ? 'superuser' : row.scope || 'global'}
        </Badge>
      ),
    },
    {
      key: 'target',
      header: 'Target',
      accessor: (row) => namedBindingTarget(row, clusterNameById, projectNameById),
      sortable: false,
    },
    {
      key: 'rules',
      header: 'Rules',
      accessor: (row) => <span className="tabular-nums">{row.rules?.length ?? 0}</span>,
      sortAccessor: (row) => row.rules?.length ?? 0,
      align: 'center',
    },
  ];

  return (
    <div className="space-y-4">
      {superuser && (
        <div className="rounded-lg border border-status-warning/30 bg-status-warning/5 px-4 py-3">
          <p className="text-sm font-medium text-foreground">Superuser</p>
          <p className="mt-0.5 text-sm text-muted-foreground">
            This account bypasses role checks and is granted every permission on the platform.
          </p>
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <MetricTile label="Grants" value={permissions.length} />
        <MetricTile label="Bindings" value={bindings.length} />
        <MetricTile label="Resources" value={resourceCount} />
        <MetricTile label="Applies here" value={applicableCount} />
        <MetricTile label="High risk" value={highRiskCount} tone={highRiskCount > 0 ? 'warning' : 'default'} />
      </div>

      <div className="grid gap-3 rounded-lg border border-border bg-card p-4 md:grid-cols-2 xl:grid-cols-4">
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">User</span>
          <Select value={userId} onChange={(event) => setUserId(event.target.value)}>
            <option value="">Me</option>
            {users.map((user) => (
              <option key={user.id} value={user.id}>
                {userOptionLabel(user)}
              </option>
            ))}
          </Select>
        </label>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Cluster</span>
          <Select value={clusterId} onChange={(event) => setClusterId(event.target.value)}>
            <option value="">All clusters</option>
            {clusters.map((cluster) => (
              <option key={cluster.id} value={cluster.id}>
                {clusterLabel(cluster)}
              </option>
            ))}
          </Select>
        </label>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Project</span>
          <Select value={projectId} onChange={(event) => setProjectId(event.target.value)}>
            <option value="">All projects</option>
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {projectLabel(project)}
              </option>
            ))}
          </Select>
        </label>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Namespace</span>
          <Input
            value={namespace}
            onChange={(event) => setNamespace(event.target.value)}
            placeholder="optional"
            className="font-mono"
          />
        </label>
        {responseContext?.warnings?.length ? (
          <p className="text-xs text-muted-foreground md:col-span-2 xl:col-span-4">
            {responseContext.warnings.join(' ')}
          </p>
        ) : null}
      </div>

      <DataTable
        data={permissions}
        columns={permissionColumns}
        keyExtractor={(row) => `${row.resource}:${row.verb}`}
        searchPlaceholder="Search effective permissions..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No effective permissions found"
        pageSize={25}
      />

      <DataTable
        data={bindings}
        columns={bindingColumns}
        keyExtractor={(row) => row.bindingId || `${row.scope}:${row.roleId || row.roleName || 'binding'}`}
        searchPlaceholder="Search permission sources..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No role bindings contribute permissions"
        pageSize={10}
      />
    </div>
  );
}

function MetricTile({ label, value, tone = 'default' }: { label: string; value: number; tone?: 'default' | 'warning' }) {
  return (
    <div className="rounded-lg border border-border bg-card px-4 py-3">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className={cn('mt-1 text-2xl font-semibold tabular-nums', tone === 'warning' ? 'text-status-warning' : 'text-foreground')}>
        {value}
      </p>
    </div>
  );
}

function userOptionLabel(user: User): string {
  const extra = user.isSuperuser ? ' (superuser)' : '';
  return `${userLabel(user)}${extra}`;
}

function sourceSummary(sources?: EffectivePermissionSource[]): string {
  const labels = (sources ?? []).map((source) => source.roleName || source.roleId || source.bindingId || source.scope || 'binding');
  return unique(labels).join(', ') || '—';
}

function targetSummary(
  sources: EffectivePermissionSource[] | undefined,
  clusters: Map<string, string>,
  projects: Map<string, string>,
): string {
  const labels = (sources ?? []).map((source) => namedSourceTarget(source, clusters, projects));
  return unique(labels).join(', ') || '—';
}

function namedBindingTarget(
  binding: EffectivePermissionBinding,
  clusters: Map<string, string>,
  projects: Map<string, string>,
): string {
  return namedSourceTarget(binding, clusters, projects);
}

function namedSourceTarget(
  source: { clusterId?: string; projectId?: string; namespace?: string; scope?: string },
  clusters: Map<string, string>,
  projects: Map<string, string>,
): string {
  if (source.projectId) return projects.get(source.projectId) || `project:${source.projectId}`;
  if (source.clusterId) {
    const name = clusters.get(source.clusterId) || source.clusterId;
    return source.namespace ? `${name} / ${source.namespace}` : name;
  }
  return source.scope || 'global';
}

function unique(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean)));
}

function isHighRiskGrant(grant: EffectivePermissionGrant): boolean {
  return riskSort(grant) >= 2;
}

function riskSort(grant: EffectivePermissionGrant): number {
  if (grant.resource === '*' || grant.verb === '*') return 3;
  if (grant.resource === 'secrets' && ['read', 'list', 'watch'].includes(grant.verb)) return 3;
  if (['delete', 'manage', 'exec', 'proxy', 'sync'].includes(grant.verb)) return 2;
  if (['create', 'update', 'scale', 'restart'].includes(grant.verb)) return 1;
  return 0;
}

function riskLabel(grant: EffectivePermissionGrant): string {
  const risk = riskSort(grant);
  if (risk >= 3) return 'Critical';
  if (risk === 2) return 'High';
  if (risk === 1) return 'Medium';
  return 'Low';
}

function riskVariant(grant: EffectivePermissionGrant): 'error' | 'warning' | 'info' | 'secondary' {
  const risk = riskSort(grant);
  if (risk >= 3) return 'error';
  if (risk === 2) return 'warning';
  if (risk === 1) return 'info';
  return 'secondary';
}