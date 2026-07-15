import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/webhooks — webhook subscriptions index.
 *
 * Each row links to the detail page; the enabled column has an inline
 * toggle so on/off doesn't require entering the detail view. Last-delivery
 * status / time come from the backend joined to the subscription row.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import {
  ArrowLeft,
  Plus,
  Trash2,
  Webhook,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { cn, formatRelativeTime } from '@/lib/utils';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useDeleteWebhook,
  useUpdateWebhook,
  useWebhooks,
} from '@/components/settings/hooks';
import type { WebhookSubscription } from '@/lib/api/settings';

function EnabledToggle({ row }: { row: WebhookSubscription }) {
  const update = useUpdateWebhook();
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        update.mutate({ id: row.id, body: { enabled: !row.enabled } });
      }}
      disabled={update.isPending}
      className={cn(
        'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
        row.enabled ? 'bg-status-success' : 'bg-muted',
      )}
      title={row.enabled ? 'Disable' : 'Enable'}
    >
      <span
        className={cn(
          'inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform',
          row.enabled ? 'translate-x-5' : 'translate-x-1',
        )}
      />
    </button>
  );
}

function WebhooksList() {
  const router = useRouter();
  const { data, isLoading } = useWebhooks();
  const del = useDeleteWebhook();
  const [confirmDelete, setConfirmDelete] = useState<WebhookSubscription | null>(null);

  const columns: Column<WebhookSubscription>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Webhook className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{row.name}</p>
            <p className="text-2xs font-mono text-muted-foreground uppercase">{row.template}</p>
          </div>
        </div>
      ),
    },
    {
      key: 'url',
      header: 'URL',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground font-mono truncate max-w-[360px] block">
          {row.url}
        </span>
      ),
      sortable: false,
    },
    {
      key: 'enabled',
      header: 'Enabled',
      align: 'center',
      sortable: false,
      accessor: (row) => <EnabledToggle row={row} />,
    },
    {
      key: 'lastDeliveryStatus',
      header: 'Last delivery',
      accessor: (row) => {
        if (!row.lastDeliveryStatus) {
          return <span className="text-xs text-muted-foreground">Never delivered</span>;
        }
        return (
          <StatusBadge
            status={row.lastDeliveryStatus === 'success' ? 'active' : 'error'}
            label={row.lastDeliveryStatus}
            size="sm"
          />
        );
      },
    },
    {
      key: 'lastDeliveryAt',
      header: 'When',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastDeliveryAt ? formatRelativeTime(row.lastDeliveryAt) : '--'}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setConfirmDelete(row);
          }}
          className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
          title="Delete webhook"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ),
    },
  ];

  return (
    <>
      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        onRowClick={(row) => router.push(`/dashboard/settings/webhooks/${row.id}`)}
        emptyMessage="No webhooks configured"
        searchPlaceholder="Search webhooks..."
      />
      <ConfirmDialog
        open={!!confirmDelete}
        onClose={() => setConfirmDelete(null)}
        onConfirm={async () => {
          if (!confirmDelete) return;
          await del.mutateAsync(confirmDelete.id);
          setConfirmDelete(null);
        }}
        title="Delete webhook?"
        description={`This will remove "${confirmDelete?.name}" and stop further deliveries. Already-queued deliveries are dropped.`}
        confirmText="Delete"
        variant="destructive"
      />
    </>
  );
}

function WebhooksPage() {
  return (
    <SettingsAuthGate>
      <div className="space-y-6">
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <div className="flex items-center justify-between">
          <div>
            <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Webhooks</p>
            <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">Webhooks</h1>
            <p className="text-sm text-muted-foreground mt-1">
              Outbound HTTP subscribers for platform events. Slack / PagerDuty / generic JSON.
            </p>
          </div>
          <Link
            href="/dashboard/settings/webhooks/new"
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <Plus className="h-4 w-4" />
            New webhook
          </Link>
        </div>
        <WebhooksList />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/webhooks/')({
  component: WebhooksPage,
});
