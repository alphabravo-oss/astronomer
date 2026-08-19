import { useState } from 'react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import { useMyEffectivePermissions } from '@/lib/hooks';
import type { EffectivePermissionBinding, EffectivePermissionGrant, EffectivePermissionSource } from '@/types';

export function EffectiveTab() {
  const [context, setContext] = useState({ clusterId: '', projectId: '', namespace: '' });
  const selectedContext = {
    clusterId: context.clusterId.trim() || undefined,
    projectId: context.projectId.trim() || undefined,
    namespace: context.namespace.trim() || undefined,
  };
  const { data, isLoading, isError, refetch } = useMyEffectivePermissions(selectedContext);
  const permissions = data?.permissions || [];
  const bindings = data?.bindings || [];
  const responseContext = data?.context;
  const resourceCount = new Set(permissions.map((p) => p.resource)).size;
  const highRiskCount = permissions.filter(isHighRiskGrant).length;
  const applicableCount = permissions.filter((p) => p.appliesToContext !== false).length;

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
      accessor: (row) => row.resource,
      sortAccessor: (row) => row.resource,
    },
    {
      key: 'verb',
      header: 'Verb',
      accessor: (row) => row.verb,
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
      accessor: (row) => targetSummary(row.sources),
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
      accessor: (row) => row.scope || 'global',
    },
    {
      key: 'target',
      header: 'Target',
      accessor: (row) => bindingTarget(row),
      sortable: false,
    },
    {
      key: 'rules',
      header: 'Rules',
      accessor: (row) => <span className="tabular-nums">{row.rules.length}</span>,
      sortAccessor: (row) => row.rules.length,
      align: 'center',
    },
  ];

  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-4">
        <MetricTile label="Grants" value={permissions.length} />
        <MetricTile label="Bindings" value={bindings.length} />
        <MetricTile label="Resources" value={resourceCount} />
        <MetricTile label="Applies Here" value={applicableCount} />
        <MetricTile label="High Risk" value={highRiskCount} tone={highRiskCount > 0 ? 'warning' : 'default'} />
      </div>

      <div className="grid gap-3 rounded-lg border border-border bg-card p-4 md:grid-cols-3">
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Cluster ID</span>
          <Input
            value={context.clusterId}
            onChange={(event) => setContext((current) => ({ ...current, clusterId: event.target.value }))}
          />
        </label>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Project ID</span>
          <Input
            value={context.projectId}
            onChange={(event) => setContext((current) => ({ ...current, projectId: event.target.value }))}
          />
        </label>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Namespace</span>
          <Input
            value={context.namespace}
            onChange={(event) => setContext((current) => ({ ...current, namespace: event.target.value }))}
          />
        </label>
        {responseContext?.warnings?.length ? (
          <p className="text-xs text-muted-foreground md:col-span-3">{responseContext.warnings.join(' ')}</p>
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
        keyExtractor={(row) => row.bindingId || `${row.scope}:${row.roleId}:${row.roleName}`}
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

function sourceSummary(sources: EffectivePermissionSource[]): string {
  const labels = sources.map((source) => source.roleName || source.roleId || source.bindingId || source.scope || 'binding');
  return unique(labels).join(', ');
}

function targetSummary(sources: EffectivePermissionSource[]): string {
  const labels = sources.map((source) => {
    if (source.clusterId) return `cluster:${source.clusterId}`;
    if (source.projectId) return `project:${source.projectId}`;
    return source.scope || 'global';
  });
  return unique(labels).join(', ');
}

function bindingTarget(binding: EffectivePermissionBinding): string {
  if (binding.clusterId) return `cluster:${binding.clusterId}`;
  if (binding.projectId) return `project:${binding.projectId}`;
  return binding.scope || 'global';
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
