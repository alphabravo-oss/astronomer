import { useState } from 'react';
import { useAuditLogs } from '@/lib/hooks';
import { formatDate } from '@/lib/utils';
import type { AuditLogEntry } from '@/types';
import { DataTable, type Column } from '@/components/ui/data-table';
import { Select } from '@/components/ui/select';
import { StatusBadge } from '@/components/ui/status-badge';

type AuditClassFilter = 'all' | 'mutation' | 'read' | 'auth' | 'system';

export function AuditTab() {
  // action_class filter (migration 063). "all" leaves the
  // query unfiltered. "read" surfaces credential-read audit
  // rows specifically; "mutation" hides the read-side noise.
  const [auditClassFilter, setAuditClassFilter] = useState<AuditClassFilter>('all');
  const { data: auditData, isLoading: auditLoading } = useAuditLogs({
    pageSize: 50,
    ...(auditClassFilter !== 'all' ? { action_class: auditClassFilter } : {}),
  });

  const auditLogs = auditData?.data || [];

  const auditColumns: Column<AuditLogEntry>[] = [
    {
      key: 'timestamp',
      header: 'Timestamp',
      accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{formatDate(row.timestamp)}</span>,
    },
    {
      key: 'user',
      header: 'User',
      accessor: (row) => <span className="text-sm text-foreground">{row.user}</span>,
    },
    {
      key: 'action',
      header: 'Action',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">{row.action}</span>
      ),
    },
    {
      key: 'resource',
      header: 'Resource',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">
          {row.resourceType}/{row.resourceName}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge status={row.status === 'success' ? 'active' : 'error'} label={row.status} size="sm" />
      ),
    },
    {
      key: 'source',
      header: 'Source IP',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.sourceIP}</span>,
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <label htmlFor="audit-class-filter" className="text-xs uppercase tracking-wide text-muted-foreground">
          Class
        </label>
        <Select
          id="audit-class-filter"
          value={auditClassFilter}
          onChange={(e) => setAuditClassFilter(e.target.value as AuditClassFilter)}
          className="w-auto"
        >
          <option value="all">All</option>
          <option value="mutation">Mutation</option>
          <option value="read">Read (credential view)</option>
          <option value="auth">Auth</option>
          <option value="system">System</option>
        </Select>
      </div>
      <DataTable
        data={auditLogs}
        columns={auditColumns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search audit logs..."
        loading={auditLoading}
        emptyMessage="No audit log entries"
        pageSize={25}
      />
    </div>
  );
}
