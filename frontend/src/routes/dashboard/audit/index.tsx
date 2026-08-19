import { createFileRoute } from '@tanstack/react-router';

import { useEffect, useMemo, useState } from 'react';
import { ChevronDown, Download, Filter, RefreshCw, Search, TerminalSquare, X } from 'lucide-react';
import { Link } from '@/lib/link';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { PageHeader, PageShell } from '@/components/ui/page';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { ActivityDetailsDrawer, type ActivityDetailField } from '@/components/audit/activity-details-drawer';
import { useAuditLogs, useClusters, useProjects, useUsers } from '@/lib/hooks';
import { getAuditLogExportURL } from '@/lib/api';
import { cn, formatDate, formatRelativeTime } from '@/lib/utils';
import { useDebouncedValue } from '@tanstack/react-pacer';
import type { AuditLogEntry } from '@/types';
import {
  PAGE_SIZE,
  auditFilterChips,
  buildAuditQuery,
  clearFilterValue,
  countActiveFilters,
  countAdvancedFilters,
  emptyFilters,
  type AuditFilters,
} from './-filters';

function AuditLogPage() {
  const [filters, setFilters] = useState<AuditFilters>(emptyFilters);
  const [qInput, setQInput] = useState('');
  const [qDebounced] = useDebouncedValue(qInput, { wait: 200 });
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<AuditLogEntry | null>(null);

  const queryParams = useMemo(
    () => buildAuditQuery({ ...filters, q: qDebounced }, page),
    [filters, qDebounced, page],
  );
  const auditQuery = useAuditLogs(queryParams);
  const { data: usersData } = useUsers({ pageSize: 200 });
  const { data: clustersData } = useClusters({ pageSize: 200 });
  const { data: projectsData } = useProjects({ pageSize: 200 });

  const rows = auditQuery.data?.data || [];
  const total = auditQuery.data?.count ?? auditQuery.data?.total ?? rows.length;
  const users = usersData?.data || [];
  const clusters = clustersData?.data || [];
  const projects = projectsData?.data || [];
  const usersById = useMemo(
    () => new Map(users.map((u) => [u.id, u.displayName || u.username || u.email])),
    [users],
  );
  const clusterNames = useMemo(
    () => Object.fromEntries(clusters.map((c) => [c.id, c.displayName || c.name])),
    [clusters],
  );
  const projectNames = useMemo(
    () => Object.fromEntries(projects.map((p) => [p.id, p.displayName || p.name])),
    [projects],
  );

  const activeFilterCount = countActiveFilters({ ...filters, q: qInput });
  const advancedCount = countAdvancedFilters(filters);
  const chips = auditFilterChips(
    { ...filters, q: qInput },
    { clusters: clusterNames, projects: projectNames },
  );
  const exportHref = getAuditLogExportURL({ ...queryParams, limit: 500, offset: 0 });

  useEffect(() => {
    setPage(0);
  }, [qDebounced]);

  const updateFilter = <K extends keyof AuditFilters>(key: K, value: AuditFilters[K]) => {
    if (key === 'q') {
      setQInput(value);
      return;
    }
    setFilters((current) => ({ ...current, [key]: value }));
    setPage(0);
  };

  const clearAll = () => {
    setFilters(emptyFilters);
    setQInput('');
    setPage(0);
  };

  const columns = useMemo<Column<AuditLogEntry>[]>(
    () => [
      {
        key: 'time',
        header: 'Time',
        accessor: (row) => (
          <div className="min-w-36">
            <div className="text-xs font-mono text-foreground">{formatDate(rowTime(row))}</div>
            <div className="text-2xs text-muted-foreground">{formatRelativeTime(rowTime(row))}</div>
          </div>
        ),
        sortAccessor: (row) => rowTime(row),
      },
      {
        key: 'actor',
        header: 'Actor',
        accessor: (row) => (
          <div className="max-w-48">
            <div className="truncate text-sm text-foreground">{actorLabel(row, usersById)}</div>
            <div className="truncate text-2xs text-muted-foreground">{row.actorAuthMethod || row.source || '—'}</div>
          </div>
        ),
        sortAccessor: (row) => actorLabel(row, usersById),
      },
      {
        key: 'action',
        header: 'Action',
        accessor: (row) => (
          <div className="max-w-64">
            <div className="truncate font-mono text-xs text-foreground">{row.action}</div>
            <span className={cn('mt-1 inline-flex rounded px-1.5 py-0.5 text-2xs', actionClassStyle(row.actionClass))}>
              {row.actionClass || 'mutation'}
            </span>
          </div>
        ),
        sortAccessor: (row) => row.action,
      },
      {
        key: 'target',
        header: 'Target',
        accessor: (row) => (
          <div className="max-w-56">
            <div className="truncate text-sm text-foreground">{targetName(row)}</div>
            <div className="truncate text-2xs text-muted-foreground">{row.resourceType || '—'}</div>
          </div>
        ),
        sortAccessor: targetName,
      },
      {
        key: 'scope',
        header: 'Scope',
        accessor: (row) => {
          const scope = scopeLabels(row);
          return (
            <div className="max-w-52 space-y-1">
              {scope.length ? (
                scope.map((item) => (
                  <div key={item} className="truncate font-mono text-2xs text-muted-foreground">
                    {item}
                  </div>
                ))
              ) : (
                <span className="text-xs text-muted-foreground">global</span>
              )}
            </div>
          );
        },
        sortAccessor: (row) => scopeLabels(row).join(' '),
      },
      {
        key: 'result',
        header: 'Result',
        accessor: (row) => (
          <div className="space-y-1">
            <StatusBadge status={statusForBadge(row.status)} label={row.status || 'success'} size="sm" />
            <div className="text-2xs text-muted-foreground">{row.statusCode ?? 0}</div>
          </div>
        ),
        sortAccessor: (row) => row.statusCode ?? 0,
      },
    ],
    [usersById],
  );

  return (
    <PageShell>
      <PageHeader
        title="Audit Log"
        description={
          filters.audience === 'system'
            ? `${total.toLocaleString()} automated system events`
            : filters.audience === 'all'
              ? `${total.toLocaleString()} events`
              : `${total.toLocaleString()} operator actions`
        }
        actions={
          <div className="flex items-center gap-2">
            <Link
              href="/dashboard/audit/shell-sessions"
              className="inline-flex h-9 items-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium text-foreground hover:bg-accent"
            >
              <TerminalSquare className="h-4 w-4" />
              Shell sessions
            </Link>
            <a
              href={exportHref}
              className="inline-flex h-9 items-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium text-foreground hover:bg-accent"
            >
              <Download className="h-4 w-4" />
              Export
            </a>
            <ActionButton
              icon={<RefreshCw className={cn('h-4 w-4', auditQuery.isFetching && 'animate-spin')} />}
              onClick={() => auditQuery.refetch()}
            >
              Refresh
            </ActionButton>
          </div>
        }
      />

      <div className="space-y-2">
        <div className="flex flex-col gap-2 lg:flex-row lg:items-center">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={qInput}
              onChange={(e) => updateFilter('q', e.target.value)}
              placeholder="Search actor, action, or resource…"
              className="pl-9"
              aria-label="Search audit log"
            />
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Select
              value={filters.audience}
              onChange={(e) => updateFilter('audience', e.target.value)}
              className="w-[10.5rem]"
              aria-label="Activity"
            >
              <option value="people">People</option>
              <option value="system">System</option>
              <option value="all">Everything</option>
            </Select>
            <Select
              value={filters.actionClass}
              onChange={(e) => updateFilter('actionClass', e.target.value)}
              className="w-[8.5rem]"
              aria-label="Event class"
            >
              <option value="all">All kinds</option>
              <option value="mutation">Changes</option>
              <option value="auth">Sign-in</option>
              <option value="read">Reads</option>
            </Select>
            <Select
              value={filters.result}
              onChange={(e) => updateFilter('result', e.target.value)}
              className="w-[8.5rem]"
              aria-label="Result"
            >
              <option value="all">Any result</option>
              <option value="success">Success</option>
              <option value="failure">Failure</option>
              <option value="error">Error</option>
            </Select>
            <ActionButton
              intent={advancedOpen || advancedCount > 0 ? 'default' : 'ghost'}
              icon={<Filter className="h-4 w-4" />}
              onClick={() => setAdvancedOpen((open) => !open)}
            >
              Filters
              {advancedCount > 0 ? ` (${advancedCount})` : ''}
              <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', advancedOpen && 'rotate-180')} />
            </ActionButton>
            {activeFilterCount > 0 && (
              <ActionButton intent="ghost" icon={<X className="h-4 w-4" />} onClick={clearAll}>
                Clear
              </ActionButton>
            )}
          </div>
        </div>

        {chips.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {chips.map((chip) => (
              <button
                key={chip.key}
                type="button"
                onClick={() => updateFilter(chip.key, clearFilterValue(chip.key))}
                className="inline-flex items-center gap-1 rounded-full border border-border bg-card px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground"
              >
                {chip.label}
                <X className="h-3 w-3" />
              </button>
            ))}
          </div>
        )}

        {advancedOpen && (
          <div className="grid gap-3 rounded-lg border border-border bg-card p-3 md:grid-cols-2 xl:grid-cols-4">
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Actor</span>
              <Input
                value={filters.actor}
                onChange={(e) => updateFilter('actor', e.target.value)}
                placeholder="email, name, or user id"
              />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Action</span>
              <Input
                value={filters.action}
                onChange={(e) => updateFilter('action', e.target.value)}
                placeholder="auth.login"
                className="font-mono"
              />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Target</span>
              <Input
                value={filters.target}
                onChange={(e) => updateFilter('target', e.target.value)}
                placeholder="resource or path"
              />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Cluster</span>
              <Select value={filters.clusterId} onChange={(e) => updateFilter('clusterId', e.target.value)}>
                <option value="">Any cluster</option>
                {clusters.map((cluster) => (
                  <option key={cluster.id} value={cluster.id}>
                    {cluster.displayName || cluster.name}
                  </option>
                ))}
              </Select>
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Project</span>
              <Select value={filters.projectId} onChange={(e) => updateFilter('projectId', e.target.value)}>
                <option value="">Any project</option>
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.displayName || project.name}
                  </option>
                ))}
              </Select>
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">From</span>
              <Input type="datetime-local" value={filters.from} onChange={(e) => updateFilter('from', e.target.value)} />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">To</span>
              <Input type="datetime-local" value={filters.to} onChange={(e) => updateFilter('to', e.target.value)} />
            </label>
            <label className="space-y-1">
              <span className="text-xs font-medium text-muted-foreground">Correlation ID</span>
              <Input
                value={filters.correlationId}
                onChange={(e) => updateFilter('correlationId', e.target.value)}
                placeholder="optional"
                className="font-mono"
              />
            </label>
            <label className="space-y-1 xl:col-span-2">
              <span className="text-xs font-medium text-muted-foreground">Request ID</span>
              <Input
                value={filters.requestId}
                onChange={(e) => updateFilter('requestId', e.target.value)}
                placeholder="optional"
                className="font-mono"
              />
            </label>
          </div>
        )}
      </div>

      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchable={false}
        pageSize={PAGE_SIZE}
        loading={auditQuery.isLoading}
        isError={auditQuery.isError}
        onRetry={() => auditQuery.refetch()}
        emptyMessage="No audit events match"
        onRowClick={setSelected}
        serverSide={{
          rowCount: total,
          pagination: { pageIndex: page, pageSize: PAGE_SIZE },
          onPaginationChange: (next) => setPage(next.pageIndex),
        }}
      />

      {selected && (
        <AuditDetailsDrawer row={selected} usersById={usersById} onClose={() => setSelected(null)} />
      )}
    </PageShell>
  );
}

function rowTime(row: AuditLogEntry): string {
  return row.createdAt || row.timestamp;
}

function actorLabel(row: AuditLogEntry, usersById: Map<string, string>): string {
  const id = row.userId || row.user;
  if (id && usersById.has(id)) return usersById.get(id) || id;
  return row.user || row.userId || 'system';
}

function targetName(row: AuditLogEntry): string {
  return row.resourceName || row.resourceId || row.path || '—';
}

function rowDetail(row: AuditLogEntry): Record<string, unknown> {
  const detail = row.detail || row.details;
  return detail && typeof detail === 'object' ? detail : {};
}

function detailString(row: AuditLogEntry, ...keys: string[]): string {
  const detail = rowDetail(row);
  for (const key of keys) {
    const value = detail[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return '';
}

function scopeLabels(row: AuditLogEntry): string[] {
  const out = new Set<string>();
  const cluster =
    row.resourceType === 'cluster'
      ? row.resourceId || row.resourceName
      : detailString(row, 'cluster_id', 'clusterId', 'cluster', 'cluster_name');
  const project =
    row.resourceType === 'project'
      ? row.resourceId || row.resourceName
      : detailString(row, 'project_id', 'projectId', 'project', 'project_name');
  if (cluster) out.add(`cluster:${cluster}`);
  if (project) out.add(`project:${project}`);
  return Array.from(out);
}

function actionClassStyle(actionClass?: string): string {
  switch (actionClass) {
    case 'read':
      return 'bg-info/10 text-info';
    case 'auth':
      return 'bg-status-warning/10 text-status-warning';
    case 'system':
      return 'bg-muted text-muted-foreground';
    default:
      return 'bg-primary/10 text-primary';
  }
}

function statusForBadge(status?: string): string {
  if (status === 'error' || status === 'failure') return 'error';
  return 'active';
}

function AuditDetailsDrawer({
  row,
  usersById,
  onClose,
}: {
  row: AuditLogEntry;
  usersById: Map<string, string>;
  onClose: () => void;
}) {
  const detail = rowDetail(row);
  const fields: ActivityDetailField[] = [
    { label: 'ID', value: row.id },
    { label: 'Time', value: rowTime(row) ? formatDate(rowTime(row)) : '—' },
    { label: 'Actor', value: actorLabel(row, usersById) },
    { label: 'Auth', value: row.actorAuthMethod || '—' },
    { label: 'Action', value: row.action },
    { label: 'Class', value: row.actionClass || 'mutation' },
    { label: 'Resource', value: `${row.resourceType || '—'}/${targetName(row)}` },
    { label: 'Method', value: row.httpMethod || '—' },
    { label: 'Status', value: `${row.status || 'success'} (${row.statusCode ?? 0})` },
    { label: 'Duration', value: `${row.durationMs ?? 0}ms` },
    { label: 'Source', value: row.source || '—' },
    { label: 'IP', value: row.sourceIP || row.ipAddress || '—' },
    { label: 'Request', value: row.requestId || '—' },
    { label: 'Correlation', value: row.correlationId || '—' },
    { label: 'Path', value: row.path || '—' },
  ];

  return (
    <ActivityDetailsDrawer
      title={row.action}
      onClose={onClose}
      subtitle={
        <div className="flex items-center gap-2">
          <StatusBadge status={statusForBadge(row.status)} label={row.status || 'success'} size="sm" />
          <span>{rowTime(row) ? formatRelativeTime(rowTime(row)) : '—'}</span>
        </div>
      }
      fields={fields}
      detail={detail}
    />
  );
}

export const Route = createFileRoute('/dashboard/audit/')({
  component: AuditLogPage,
});
