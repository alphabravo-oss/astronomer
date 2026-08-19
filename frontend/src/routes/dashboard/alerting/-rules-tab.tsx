import { useState } from 'react';
import { Pencil, Trash2 } from 'lucide-react';
import { useAlertRules, useDeleteAlertRule } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionButton } from '@/components/ui/action-button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { cn, statusBgColor } from '@/lib/utils';
import type { AlertRule } from '@/types';

export function RulesTab({
  onEdit,
  clusterId,
}: {
  onEdit: (rule: AlertRule) => void;
  clusterId?: string;
}) {
  const { data: rules, isLoading, isError, refetch } = useAlertRules(clusterId);
  const deleteRule = useDeleteAlertRule();
  const [deleteRuleTarget, setDeleteRuleTarget] = useState<AlertRule | null>(null);

  const columns: Column<AlertRule>[] = [
    {
      key: 'name',
      header: 'Rule',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          {row.description && (
            <p className="text-xs text-muted-foreground truncate max-w-[300px]">{row.description}</p>
          )}
        </div>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.type}
        </span>
      ),
    },
    {
      key: 'severity',
      header: 'Severity',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded capitalize font-medium', statusBgColor(row.severity))}>
          {row.severity}
        </span>
      ),
    },
    ...(clusterId
      ? []
      : [{
          key: 'cluster',
          header: 'Cluster',
          accessor: (row: AlertRule) => (
            <span className="text-sm text-muted-foreground">{row.clusterName || 'All'}</span>
          ),
        } as Column<AlertRule>]),
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'disconnected'}
          label={row.enabled ? 'Enabled' : 'Disabled'}
        />
      ),
    },
    {
      key: 'activeAlerts',
      header: 'Active',
      accessor: (row) => (
        <span className={cn('tabular-nums text-sm font-medium', row.activeAlerts > 0 ? 'text-status-error' : 'text-muted-foreground')}>
          {row.activeAlerts}
        </span>
      ),
      sortAccessor: (row) => row.activeAlerts,
      align: 'center',
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <ActionButton
            size="icon"
            intent="ghost"
            title="Edit rule"
            onClick={() => onEdit(row)}
            icon={<Pencil className="h-3.5 w-3.5" />}
          />
          <ActionButton
            size="icon"
            intent="ghost"
            title="Delete rule"
            onClick={() => setDeleteRuleTarget(row)}
            icon={<Trash2 className="h-3.5 w-3.5" />}
            className="hover:text-status-error hover:bg-status-error/10"
          />
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <>
      <DataTable
        data={rules || []}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search alert rules..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No alert rules configured"
      />
      <ConfirmDialog
        open={!!deleteRuleTarget}
        onClose={() => setDeleteRuleTarget(null)}
        onConfirm={() => {
          if (!deleteRuleTarget) return;
          deleteRule.mutate(deleteRuleTarget.id, {
            onSuccess: () => setDeleteRuleTarget(null),
          });
        }}
        title="Delete Alert Rule"
        description={`Delete the alert rule "${deleteRuleTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleteRule.isPending}
      />
    </>
  );
}
