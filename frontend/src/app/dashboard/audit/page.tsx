'use client';

import { useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import {
  Download,
  Filter,
  RefreshCw,
  Search,
  TerminalSquare,
  X,
} from 'lucide-react';
import { Link } from '@/lib/link';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActivityDetailsDrawer, type ActivityDetailField } from '@/components/audit/activity-details-drawer';
import { useAuditLogs } from '@/lib/hooks';
import { getAuditLogExportURL, type AuditLogQueryParams } from '@/lib/api';
import { cn, formatDate, formatRelativeTime } from '@/lib/utils';
import type { AuditLogEntry } from '@/types';

const PAGE_SIZE = 50;

type AuditFilters = {
  actor: string;
  target: string;
  action: string;
  actionClass: string;
  result: string;
  clusterId: string;
  projectId: string;
  correlationId: string;
  requestId: string;
  from: string;
  to: string;
};

const emptyFilters: AuditFilters = {
  actor: '',
  target: '',
  action: '',
  actionClass: 'all',
  result: 'all',
  clusterId: '',
  projectId: '',
  correlationId: '',
  requestId: '',
  from: '',
  to: '',
};

export default function AuditLogPage() {
  const [filters, setFilters] = useState<AuditFilters>(emptyFilters);
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<AuditLogEntry | null>(null);

  const queryParams = useMemo(() => buildAuditQuery(filters, page), [filters, page]);
  const auditQuery = useAuditLogs(queryParams);
  const rows = auditQuery.data?.data || [];
  const total = auditQuery.data?.count ?? auditQuery.data?.total ?? rows.length;
  const activeFilterCount = countActiveFilters(filters);
  const exportHref = getAuditLogExportURL({ ...queryParams, limit: 500, offset: 0 });

  const updateFilter = <K extends keyof AuditFilters>(key: K, value: AuditFilters[K]) => {
    setFilters((current) => ({ ...current, [key]: value }));
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
            <div className="truncate text-sm text-foreground">{actorLabel(row)}</div>
            <div className="truncate text-2xs text-muted-foreground">{row.actorAuthMethod || row.source || '-'}</div>
          </div>
        ),
        sortAccessor: actorLabel,
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
            <div className="truncate text-2xs text-muted-foreground">{row.resourceType || '-'}</div>
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
              {scope.length ? scope.map((item) => (
                <div key={item} className="truncate font-mono text-2xs text-muted-foreground">{item}</div>
              )) : <span className="text-xs text-muted-foreground">global</span>}
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
      {
        key: 'correlation',
        header: 'Correlation',
        accessor: (row) => (
          <div className="max-w-52">
            <div className="truncate font-mono text-xs text-foreground">{row.correlationId || '-'}</div>
            <div className="truncate font-mono text-2xs text-muted-foreground">{row.requestId || '-'}</div>
          </div>
        ),
        sortAccessor: (row) => `${row.correlationId || ''} ${row.requestId || ''}`,
      },
    ],
    []
  );

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Audit Log</h1>
          <div className="mt-1 flex items-center gap-3 text-sm text-muted-foreground">
            <span>{total.toLocaleString()} rows</span>
            {activeFilterCount > 0 && <span>{activeFilterCount} filters</span>}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Link
            href="/dashboard/audit/shell-sessions"
            className="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <TerminalSquare className="h-4 w-4" />
            Shell sessions
          </Link>
          <a
            href={exportHref}
            className="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <Download className="h-4 w-4" />
            Export
          </a>
          <button
            type="button"
            onClick={() => auditQuery.refetch()}
            className="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <RefreshCw className={cn('h-4 w-4', auditQuery.isFetching && 'animate-spin')} />
            Refresh
          </button>
        </div>
      </div>

      <div className="space-y-3 border-y border-border py-4">
        <div className="flex items-center gap-2 text-sm font-medium text-foreground">
          <Filter className="h-4 w-4" />
          Filters
        </div>
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <FilterInput icon={<Search className="h-4 w-4" />} label="Actor" value={filters.actor} onChange={(v) => updateFilter('actor', v)} placeholder="email, name, user id" />
          <FilterInput icon={<Search className="h-4 w-4" />} label="Target" value={filters.target} onChange={(v) => updateFilter('target', v)} placeholder="resource, path, name" />
          <FilterInput label="Action" value={filters.action} onChange={(v) => updateFilter('action', v)} placeholder="cluster.delete" />
          <FilterInput label="Correlation" value={filters.correlationId} onChange={(v) => updateFilter('correlationId', v)} placeholder="correlation id" />
          <FilterInput label="Request" value={filters.requestId} onChange={(v) => updateFilter('requestId', v)} placeholder="request id" />
          <FilterInput label="Cluster" value={filters.clusterId} onChange={(v) => updateFilter('clusterId', v)} placeholder="cluster id or name" />
          <FilterInput label="Project" value={filters.projectId} onChange={(v) => updateFilter('projectId', v)} placeholder="project id or name" />
          <label className="space-y-1.5">
            <span className="text-xs font-medium text-muted-foreground">Class</span>
            <select
              value={filters.actionClass}
              onChange={(e) => updateFilter('actionClass', e.target.value)}
              className="h-9 w-full rounded-md border border-border bg-background px-3 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="all">All</option>
              <option value="mutation">Mutation</option>
              <option value="read">Read</option>
              <option value="auth">Auth</option>
              <option value="system">System</option>
            </select>
          </label>
          <label className="space-y-1.5">
            <span className="text-xs font-medium text-muted-foreground">Result</span>
            <select
              value={filters.result}
              onChange={(e) => updateFilter('result', e.target.value)}
              className="h-9 w-full rounded-md border border-border bg-background px-3 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="all">All</option>
              <option value="success">Success</option>
              <option value="failure">Failure</option>
              <option value="error">Error</option>
            </select>
          </label>
          <FilterInput label="From" value={filters.from} onChange={(v) => updateFilter('from', v)} type="datetime-local" />
          <FilterInput label="To" value={filters.to} onChange={(v) => updateFilter('to', v)} type="datetime-local" />
          <div className="flex items-end">
            <button
              type="button"
              onClick={() => {
                setFilters(emptyFilters);
                setPage(0);
              }}
              disabled={activeFilterCount === 0}
              className="inline-flex h-9 w-full items-center justify-center gap-2 rounded-md border border-border px-3 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
            >
              <X className="h-4 w-4" />
              Clear
            </button>
          </div>
        </div>
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
        emptyMessage="No audit rows"
        onRowClick={setSelected}
        serverSide={{
          rowCount: total,
          pagination: { pageIndex: page, pageSize: PAGE_SIZE },
          onPaginationChange: (next) => setPage(next.pageIndex),
        }}
      />

      {selected && <AuditDetailsDrawer row={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}

function buildAuditQuery(filters: AuditFilters, page: number): AuditLogQueryParams {
  const from = toRFC3339(filters.from);
  const to = toRFC3339(filters.to);
  return {
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    actor: filters.actor.trim() || undefined,
    target: filters.target.trim() || undefined,
    action: filters.action.trim() || undefined,
    action_class: filters.actionClass !== 'all' ? filters.actionClass : undefined,
    result: filters.result !== 'all' ? filters.result : undefined,
    cluster_id: filters.clusterId.trim() || undefined,
    project_id: filters.projectId.trim() || undefined,
    correlation_id: filters.correlationId.trim() || undefined,
    request_id: filters.requestId.trim() || undefined,
    from,
    to,
  };
}

function toRFC3339(value: string): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return undefined;
  return date.toISOString();
}

function countActiveFilters(filters: AuditFilters): number {
  return Object.entries(filters).filter(([key, value]) => {
    if (key === 'actionClass' || key === 'result') return value !== 'all';
    return Boolean(String(value).trim());
  }).length;
}

function rowTime(row: AuditLogEntry): string {
  return row.createdAt || row.timestamp;
}

function actorLabel(row: AuditLogEntry): string {
  return row.user || row.userId || 'system';
}

function targetName(row: AuditLogEntry): string {
  return row.resourceName || row.resourceId || row.path || '-';
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
  const cluster = row.resourceType === 'cluster'
    ? row.resourceId || row.resourceName
    : detailString(row, 'cluster_id', 'clusterId', 'cluster', 'cluster_name');
  const project = row.resourceType === 'project'
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

function FilterInput({
  label,
  value,
  onChange,
  placeholder,
  type = 'text',
  icon,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  type?: string;
  icon?: ReactNode;
}) {
  return (
    <label className="space-y-1.5">
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      <span className="relative block">
        {icon && <span className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground">{icon}</span>}
        <input
          type={type}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className={cn(
            'h-9 w-full rounded-md border border-border bg-background px-3 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring',
            icon && 'pl-9'
          )}
        />
      </span>
    </label>
  );
}

function AuditDetailsDrawer({ row, onClose }: { row: AuditLogEntry; onClose: () => void }) {
  const detail = rowDetail(row);
  const fields: ActivityDetailField[] = [
    { label: 'ID', value: row.id },
    { label: 'Time', value: rowTime(row) ? formatDate(rowTime(row)) : '-' },
    { label: 'Actor', value: actorLabel(row) },
    { label: 'Auth', value: row.actorAuthMethod || '-' },
    { label: 'Action', value: row.action },
    { label: 'Class', value: row.actionClass || 'mutation' },
    { label: 'Resource', value: `${row.resourceType || '-'}/${targetName(row)}` },
    { label: 'Method', value: row.httpMethod || '-' },
    { label: 'Status', value: `${row.status || 'success'} (${row.statusCode ?? 0})` },
    { label: 'Duration', value: `${row.durationMs ?? 0}ms` },
    { label: 'Source', value: row.source || '-' },
    { label: 'IP', value: row.sourceIP || row.ipAddress || '-' },
    { label: 'Request', value: row.requestId || '-' },
    { label: 'Correlation', value: row.correlationId || '-' },
    { label: 'Path', value: row.path || '-' },
  ];

  return (
    <ActivityDetailsDrawer
      title={row.action}
      onClose={onClose}
      subtitle={(
        <div className="flex items-center gap-2">
          <StatusBadge status={statusForBadge(row.status)} label={row.status || 'success'} size="sm" />
          <span>{rowTime(row) ? formatRelativeTime(rowTime(row)) : '-'}</span>
        </div>
      )}
      fields={fields}
      detail={detail}
    />
  );
}
