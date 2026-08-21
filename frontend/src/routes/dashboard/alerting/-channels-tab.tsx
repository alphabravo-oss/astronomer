import type { ElementType } from 'react';
import { AlertCircle, Bell, Hash, Mail, MessageSquare, Send, Webhook } from 'lucide-react';
import { useNotificationChannels, useTestNotificationChannel } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionButton } from '@/components/ui/action-button';
import { formatRelativeTime } from '@/lib/utils';
import type { NotificationChannel } from '@/types';

const channelTypeIcons: Record<string, ElementType> = {
  slack: Hash,
  email: Mail,
  pagerduty: AlertCircle,
  webhook: Webhook,
  msteams: MessageSquare,
};

export function ChannelsTab() {
  const { data: channels, isLoading, isError, refetch } = useNotificationChannels();
  const testChannel = useTestNotificationChannel();

  const columns: Column<NotificationChannel>[] = [
    {
      key: 'name',
      header: 'Channel',
      accessor: (row) => {
        const TypeIcon = channelTypeIcons[row.type] || Bell;
        return (
          <div className="flex items-center gap-2">
            <TypeIcon className="h-4 w-4 text-muted-foreground" />
            <span className="font-medium text-foreground">{row.name}</span>
          </div>
        );
      },
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.type === 'msteams' ? 'MS Teams' : row.type === 'pagerduty' ? 'PagerDuty' : row.type}
        </span>
      ),
    },
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
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <ActionButton
            size="sm"
            intent="ghost"
            title="Test Channel"
            onClick={() => testChannel.mutate(row.id)}
            disabled={testChannel.isPending}
            icon={<Send className="h-3 w-3" />}
            className="h-auto px-2 py-1"
          >
            Test
          </ActionButton>
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <DataTable
      data={channels || []}
      columns={columns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search notification channels..."
      loading={isLoading}
      isError={isError}
      onRetry={() => refetch()}
      emptyMessage="No notification channels configured"
    />
  );
}
