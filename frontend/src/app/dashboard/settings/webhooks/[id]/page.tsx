'use client';

/**
 * /dashboard/settings/webhooks/[id] — webhook detail with three tabs:
 *   - Config: edit name / url / filters / secret.
 *   - Deliveries: recent attempts with a per-row retry button.
 *   - Test: synthesise a payload and surface the response.
 */
import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import {
  ArrowLeft,
  Loader2,
  Play,
  RotateCcw,
  Save,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';
import { cn, formatRelativeTime } from '@/lib/utils';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { CodeBlock } from '@/components/ui/code-block';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useDeleteWebhook,
  useRetryWebhookDelivery,
  useTestWebhook,
  useUpdateWebhook,
  useWebhook,
  useWebhookDeliveries,
} from '@/components/settings/hooks';
import type {
  WebhookDelivery,
  WebhookSubscription,
  WebhookTestResult,
} from '@/lib/api/settings';

type Tab = 'config' | 'deliveries' | 'test';

const AVAILABLE_EVENTS = [
  'cluster.unhealthy',
  'cluster.healthy',
  'backup.failed',
  'backup.succeeded',
  'project.created',
  'project.deleted',
  'auth.failed',
  'auth.locked',
  'quota.exceeded',
];

function ConfigTab({ webhook }: { webhook: WebhookSubscription }) {
  const [form, setForm] = useState({
    name: webhook.name,
    url: webhook.url,
    secret: webhook.secret,
    enabled: webhook.enabled,
    events: webhook.filters.events,
    minSeverity: webhook.filters.minSeverity ?? ('' as 'info' | 'warning' | 'critical' | ''),
  });
  const update = useUpdateWebhook();

  useEffect(() => {
    setForm({
      name: webhook.name,
      url: webhook.url,
      secret: webhook.secret,
      enabled: webhook.enabled,
      events: webhook.filters.events,
      minSeverity: webhook.filters.minSeverity ?? '',
    });
  }, [webhook]);

  const handleSave = async () => {
    try {
      await update.mutateAsync({
        id: webhook.id,
        body: {
          name: form.name,
          url: form.url,
          // Secret only sent if it differs from the redacted snapshot.
          ...(form.secret && form.secret !== webhook.secret ? { secret: form.secret } : {}),
          enabled: form.enabled,
          filters: {
            events: form.events,
            ...(form.minSeverity ? { minSeverity: form.minSeverity } : {}),
          },
        },
      });
    } catch {
      // toast on error
    }
  };

  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Name</label>
        <input
          type="text"
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">URL</label>
        <input
          type="url"
          value={form.url}
          onChange={(e) => setForm({ ...form, url: e.target.value })}
          className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm font-mono focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Signing secret</label>
        <input
          type="password"
          value={form.secret}
          onChange={(e) => setForm({ ...form, secret: e.target.value })}
          className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
        />
        <p className="text-xs text-muted-foreground">Stored value preserved — type a new secret to rotate.</p>
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Events</label>
        <div className="flex flex-wrap gap-1.5">
          {AVAILABLE_EVENTS.map((ev) => {
            const checked = form.events.includes(ev);
            return (
              <button
                key={ev}
                type="button"
                onClick={() =>
                  setForm((f) => ({
                    ...f,
                    events: f.events.includes(ev) ? f.events.filter((e) => e !== ev) : [...f.events, ev],
                  }))
                }
                className={cn(
                  'text-2xs px-2 py-1 rounded-full border font-mono transition-colors',
                  checked
                    ? 'border-foreground bg-foreground text-background'
                    : 'border-border text-muted-foreground hover:text-foreground hover:border-foreground/50',
                )}
              >
                {ev}
              </button>
            );
          })}
        </div>
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Minimum severity</label>
        <select
          value={form.minSeverity}
          onChange={(e) => setForm({ ...form, minSeverity: e.target.value as typeof form.minSeverity })}
          className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
        >
          <option value="">No threshold</option>
          <option value="info">Info or higher</option>
          <option value="warning">Warning or higher</option>
          <option value="critical">Critical only</option>
        </select>
      </div>
      <div className="flex items-center justify-between p-3 rounded-lg border border-border">
        <div>
          <p className="text-sm font-medium text-foreground">Enabled</p>
          <p className="text-xs text-muted-foreground">Disabling drops new deliveries silently.</p>
        </div>
        <button
          type="button"
          onClick={() => setForm({ ...form, enabled: !form.enabled })}
          className={cn(
            'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
            form.enabled ? 'bg-status-success' : 'bg-muted',
          )}
        >
          <span
            className={cn(
              'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
              form.enabled ? 'translate-x-6' : 'translate-x-1',
            )}
          />
        </button>
      </div>
      <div className="flex justify-end gap-2 pt-2 border-t border-border">
        <button
          type="button"
          onClick={handleSave}
          disabled={update.isPending}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {update.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
          Save changes
        </button>
      </div>
    </div>
  );
}

function DeliveriesTab({ webhookId }: { webhookId: string }) {
  const [page, setPage] = useState(1);
  const { data, isLoading } = useWebhookDeliveries(webhookId, { page, page_size: 25 });
  const retry = useRetryWebhookDelivery(webhookId);

  const columns: Column<WebhookDelivery>[] = [
    {
      key: 'createdAt',
      header: 'Time',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground font-mono">{formatRelativeTime(row.createdAt)}</span>
      ),
    },
    {
      key: 'eventType',
      header: 'Event',
      accessor: (row) => (
        <span className="text-xs font-mono px-2 py-0.5 rounded bg-muted text-muted-foreground">{row.eventType}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.status === 'success' ? 'active' : row.status === 'failed' ? 'error' : 'connecting'}
          label={row.status}
          size="sm"
        />
      ),
    },
    {
      key: 'responseCode',
      header: 'HTTP',
      accessor: (row) => (
        <span className="text-xs font-mono tabular-nums text-muted-foreground">
          {row.responseCode ?? '--'}
        </span>
      ),
    },
    {
      key: 'attempts',
      header: 'Attempts',
      align: 'right',
      accessor: (row) => <span className="tabular-nums text-sm">{row.attempts}</span>,
    },
    {
      key: 'durationMs',
      header: 'Duration',
      align: 'right',
      accessor: (row) => (
        <span className="tabular-nums text-xs text-muted-foreground">
          {row.durationMs != null ? `${row.durationMs}ms` : '--'}
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
            retry.mutate(row.id);
          }}
          disabled={retry.isPending || row.status === 'success'}
          className="inline-flex items-center gap-1 h-7 px-2 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-30 transition-colors"
          title="Retry delivery"
        >
          <RotateCcw className="h-3 w-3" />
          Retry
        </button>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <DataTable
        data={data?.data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        emptyMessage="No deliveries yet"
        pageSize={25}
      />
      {data && data.totalPages > 1 && (
        <div className="flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page === 1}
            className="h-8 px-3 rounded-lg border border-border text-xs font-medium disabled:opacity-50"
          >
            Previous
          </button>
          <p className="text-xs text-muted-foreground">
            Page {data.page} of {data.totalPages}
          </p>
          <button
            type="button"
            onClick={() => setPage((p) => p + 1)}
            disabled={page >= data.totalPages}
            className="h-8 px-3 rounded-lg border border-border text-xs font-medium disabled:opacity-50"
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}

function TestTab({ webhookId }: { webhookId: string }) {
  const test = useTestWebhook();
  const [lastResult, setLastResult] = useState<WebhookTestResult | null>(null);

  const handleTest = async () => {
    try {
      const result = await test.mutateAsync(webhookId);
      setLastResult(result);
      if (result.success) {
        toast.success(`Test delivered in ${result.durationMs}ms`);
      } else {
        toast.error(`Test failed: ${result.errorMessage ?? `HTTP ${result.responseCode}`}`);
      }
    } catch {
      // mutation toasts
    }
  };

  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">Send a test payload</h2>
          <p className="text-xs text-muted-foreground mt-0.5 max-w-prose">
            Fires a synthetic <span className="font-mono">webhook.test</span> event using the
            current URL, secret, and template renderer. The response code and body are surfaced
            below.
          </p>
        </div>
        <button
          type="button"
          onClick={handleTest}
          disabled={test.isPending}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {test.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
          Run test
        </button>
      </div>

      {lastResult && (
        <div className="space-y-3">
          <div className="flex items-center gap-3">
            <StatusBadge
              status={lastResult.success ? 'active' : 'error'}
              label={lastResult.success ? 'success' : 'failed'}
              size="sm"
            />
            <span className="text-xs text-muted-foreground">
              HTTP {lastResult.responseCode ?? '—'} · {lastResult.durationMs}ms
            </span>
          </div>
          {lastResult.errorMessage && (
            <p className="text-xs text-status-error">{lastResult.errorMessage}</p>
          )}
          {lastResult.responseBody && (
            <CodeBlock code={lastResult.responseBody} title="Response body" />
          )}
        </div>
      )}
    </div>
  );
}

function WebhookDetail() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  const id = params?.id ?? '';
  const { data, isLoading, error } = useWebhook(id);
  const del = useDeleteWebhook();
  const [tab, setTab] = useState<Tab>('config');
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (error || !data) {
    return (
      <div className="rounded-xl border border-border bg-card p-6">
        <p className="text-sm text-status-error">Failed to load webhook.</p>
      </div>
    );
  }

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <Link
        href="/dashboard/settings/webhooks"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to webhooks
      </Link>

      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Webhooks · {data.template}
          </p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">{data.name}</h1>
          <p className="text-sm text-muted-foreground mt-1 font-mono break-all">{data.url}</p>
        </div>
        <button
          type="button"
          onClick={() => setConfirmDelete(true)}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm font-medium text-status-error hover:bg-status-error/10 transition-colors"
        >
          <Trash2 className="h-3.5 w-3.5" />
          Delete
        </button>
      </div>

      <div className="border-b border-border">
        <nav className="flex gap-6">
          {(['config', 'deliveries', 'test'] as Tab[]).map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => setTab(t)}
              className={cn(
                'pb-3 text-sm font-medium border-b-2 transition-colors capitalize',
                tab === t
                  ? 'border-foreground text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground',
              )}
            >
              {t === 'deliveries' ? 'Recent deliveries' : t}
            </button>
          ))}
        </nav>
      </div>

      <div className="animate-fade-in">
        {tab === 'config' && <ConfigTab webhook={data} />}
        {tab === 'deliveries' && <DeliveriesTab webhookId={id} />}
        {tab === 'test' && <TestTab webhookId={id} />}
      </div>

      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={async () => {
          await del.mutateAsync(id);
          router.push('/dashboard/settings/webhooks');
        }}
        title="Delete webhook?"
        description={`This will remove "${data.name}" and stop further deliveries.`}
        confirmText="Delete"
        variant="destructive"
      />
    </div>
  );
}

export default function WebhookDetailPage() {
  return (
    <SettingsAuthGate>
      <WebhookDetail />
    </SettingsAuthGate>
  );
}
