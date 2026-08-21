/**
 * /dashboard/settings/siem — external SIEM forwarders (F-05).
 *
 * List / create / edit / delete syslog / Splunk HEC / NDJSON-HTTPS
 * destinations, a Test button that ships a synthetic event through the real
 * pipeline, and a per-forwarder status drawer (queue depth, dropped /
 * dispatched totals, last error). All endpoints are superuser-gated
 * server-side; SettingsAuthGate mirrors that in the UI.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useAppForm, useStore } from '@/lib/form';
import {
  ArrowLeft,
  Plus,
  Trash2,
  Pencil,
  Send,
  Activity,
  Loader2,
  ShieldAlert,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { ModalShell } from '@/components/ui/modal-shell';
import { PageHeader, PageShell } from '@/components/ui/page';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { formatRelativeTime } from '@/lib/utils';
import type { SIEMForwarder } from '@/types';
import { SIEM_AUTH_SENTINEL, type SIEMForwarderWriteRequest } from '@/lib/api/siem-forwarders';
import {
  useSIEMForwarders,
  useCreateSIEMForwarder,
  useUpdateSIEMForwarder,
  useDeleteSIEMForwarder,
  useTestSIEMForwarder,
  useSIEMForwarderStatus,
} from './-hooks';

const TRANSPORTS: { value: string; label: string }[] = [
  { value: 'syslog_udp', label: 'Syslog (UDP)' },
  { value: 'syslog_tcp', label: 'Syslog (TCP)' },
  { value: 'syslog_tls', label: 'Syslog (TLS)' },
  { value: 'splunk_hec', label: 'Splunk HEC' },
  { value: 'ndjson_https', label: 'NDJSON over HTTPS' },
];

const FORMATS: { value: string; label: string }[] = [
  { value: '', label: 'Auto (derive from transport)' },
  { value: 'rfc5424', label: 'Syslog RFC 5424' },
  { value: 'rfc3164', label: 'Syslog RFC 3164' },
  { value: 'cef', label: 'CEF' },
  { value: 'ndjson', label: 'NDJSON' },
];

function transportLabel(t: string): string {
  return TRANSPORTS.find((x) => x.value === t)?.label ?? t;
}

function SIEMForwardersList() {
  const { data, isLoading, isError, refetch } = useSIEMForwarders();
  const del = useDeleteSIEMForwarder();
  const test = useTestSIEMForwarder();

  const [editing, setEditing] = useState<SIEMForwarder | null>(null);
  const [showModal, setShowModal] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<SIEMForwarder | null>(null);
  const [statusTarget, setStatusTarget] = useState<SIEMForwarder | null>(null);

  const columns: Column<SIEMForwarder>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          <p className="text-2xs font-mono text-muted-foreground truncate max-w-[320px]">{row.endpoint}</p>
        </div>
      ),
    },
    {
      key: 'transport',
      header: 'Transport',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">{transportLabel(row.transport)}</span>
      ),
      sortAccessor: (row) => row.transport,
    },
    {
      key: 'filters',
      header: 'Event filters',
      sortable: false,
      accessor: (row) =>
        row.eventFilters && row.eventFilters.length > 0 ? (
          <div className="flex flex-wrap gap-1 max-w-[240px]">
            {row.eventFilters.slice(0, 3).map((f) => (
              <span key={f} className="text-2xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                {f}
              </span>
            ))}
            {row.eventFilters.length > 3 && (
              <span className="text-2xs text-muted-foreground">+{row.eventFilters.length - 3}</span>
            )}
          </div>
        ) : (
          <span className="text-xs text-muted-foreground">All events</span>
        ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'disconnected'}
          label={row.enabled ? 'Enabled' : 'Disabled'}
          size="sm"
        />
      ),
      sortAccessor: (row) => (row.enabled ? '1' : '0'),
    },
    {
      key: 'updated',
      header: 'Updated',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.updatedAt)}</span>,
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => setStatusTarget(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="View status"
          >
            <Activity className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => test.mutate(row.id)}
            disabled={test.isPending}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Send test event"
          >
            <Send className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => {
              setEditing(row);
              setShowModal(true);
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Edit forwarder"
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => setDeleteTarget(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete forwarder"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
    },
  ];

  return (
    <>
      <div className="flex items-center justify-end">
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() => {
            setEditing(null);
            setShowModal(true);
          }}
        >
          Add Forwarder
        </ActionButton>
      </div>

      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        searchPlaceholder="Search forwarders..."
        emptyMessage="No SIEM forwarders configured"
      />

      {showModal && (
        <SIEMForwarderModal
          forwarder={editing}
          onClose={() => {
            setShowModal(false);
            setEditing(null);
          }}
        />
      )}

      {statusTarget && (
        <SIEMStatusDrawer forwarder={statusTarget} onClose={() => setStatusTarget(null)} />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return;
          await del.mutateAsync(deleteTarget.id);
          setDeleteTarget(null);
        }}
        title="Delete SIEM forwarder?"
        description={`This removes "${deleteTarget?.name}" and drops any queued events for it. This cannot be undone.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={del.isPending}
      />
    </>
  );
}

// ============================================================
// Create / edit modal
// ============================================================

function SIEMForwarderModal({
  forwarder,
  onClose,
}: {
  forwarder: SIEMForwarder | null;
  onClose: () => void;
}) {
  const create = useCreateSIEMForwarder();
  const update = useUpdateSIEMForwarder();
  const isEdit = !!forwarder;

  const form = useAppForm({
    defaultValues: {
      name: forwarder?.name ?? '',
      transport: forwarder?.transport ?? 'syslog_tls',
      endpoint: forwarder?.endpoint ?? '',
      // On edit the real auth is never sent to the client; leave blank and only
      // submit a new value if the operator types one.
      auth: '',
      eventFilters: (forwarder?.eventFilters ?? []).join(', '),
      format: forwarder?.format ?? '',
      tlsSkipVerify: forwarder?.tlsSkipVerify ?? false,
      caCertPem: '',
      batchSize: forwarder?.batchSize ?? 100,
      flushIntervalMs: forwarder?.flushIntervalMs ?? 5000,
      timeoutSeconds: forwarder?.timeoutSeconds ?? 10,
      enabled: forwarder?.enabled ?? true,
    },
    onSubmit: async ({ value }) => {
      const filters = value.eventFilters
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      const body: SIEMForwarderWriteRequest = {
        name: value.name,
        transport: value.transport,
        endpoint: value.endpoint,
        event_filters: filters,
        format: value.format,
        tls_skip_verify: value.tlsSkipVerify,
        batch_size: value.batchSize,
        flush_interval_ms: value.flushIntervalMs,
        timeout_seconds: value.timeoutSeconds,
        enabled: value.enabled,
      };
      // Only send auth when the operator supplied a new value; on edit an empty
      // field means "keep existing" (we echo the sentinel so a blank PUT doesn't
      // wipe the stored blob).
      if (value.auth.trim()) {
        body.auth = value.auth;
      } else if (isEdit && forwarder?.authConfigured) {
        body.auth = SIEM_AUTH_SENTINEL;
      }
      if (value.caCertPem.trim()) {
        body.ca_cert_pem = value.caCertPem;
      }

      try {
        if (forwarder) {
          await update.mutateAsync({ id: forwarder.id, body });
        } else {
          await create.mutateAsync(body);
        }
        onClose();
      } catch {
        /* mutation toasts on error */
      }
    },
  });

  // Old disabled gate (`!form.name || !form.endpoint`), recomputed from form
  // state — the save button below keeps the identical condition.
  const name = useStore(form.store, (s) => s.values.name);
  const endpoint = useStore(form.store, (s) => s.values.endpoint);
  const tlsSkipVerify = useStore(form.store, (s) => s.values.tlsSkipVerify);

  const isPending = create.isPending || update.isPending;
  return (
    <ModalShell
      title={isEdit ? 'Edit SIEM Forwarder' : 'Add SIEM Forwarder'}
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={isPending || !name || !endpoint}
            loading={isPending}
          >
            {isEdit ? 'Save Changes' : 'Create Forwarder'}
          </ActionButton>
        </>
      }
    >
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <form.Field name="name">
              {(field) => (
                <Input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="corp-splunk"
                />
              )}
            </form.Field>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Transport</label>
              <form.Field name="transport">
                {(field) => (
                  <Select
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    >
                    {TRANSPORTS.map((t) => (
                      <option key={t.value} value={t.value}>
                        {t.label}
                      </option>
                    ))}
                  </Select>
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Format</label>
              <form.Field name="format">
                {(field) => (
                  <Select
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    >
                    {FORMATS.map((t) => (
                      <option key={t.value} value={t.value}>
                        {t.label}
                      </option>
                    ))}
                  </Select>
                )}
              </form.Field>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Endpoint</label>
            <form.Field name="endpoint">
              {(field) => (
                <Input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="siem.corp.example.com:6514"
                  className="font-mono"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              Auth {isEdit && <span className="text-2xs text-muted-foreground font-normal">(leave blank to keep existing)</span>}
            </label>
            <form.Field name="auth">
              {(field) => (
                <Input
                  type="password"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder={isEdit && forwarder?.authConfigured ? '•••••••• (configured)' : 'HEC token / bearer / password'}
                  className="font-mono"
                  autoComplete="new-password"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              Event filters <span className="text-2xs text-muted-foreground font-normal">(comma-separated; blank = all)</span>
            </label>
            <form.Field name="eventFilters">
              {(field) => (
                <Input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="auth.login.failed, admin.*"
                  className="font-mono"
                />
              )}
            </form.Field>
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Batch size</label>
              <form.Field name="batchSize">
                {(field) => (
                  <Input
                    type="number"
                    min={1}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(parseInt(e.target.value, 10) || 0)}
                    onBlur={field.handleBlur}
                    />
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Flush (ms)</label>
              <form.Field name="flushIntervalMs">
                {(field) => (
                  <Input
                    type="number"
                    min={0}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(parseInt(e.target.value, 10) || 0)}
                    onBlur={field.handleBlur}
                    />
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Timeout (s)</label>
              <form.Field name="timeoutSeconds">
                {(field) => (
                  <Input
                    type="number"
                    min={1}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(parseInt(e.target.value, 10) || 0)}
                    onBlur={field.handleBlur}
                    />
                )}
              </form.Field>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              CA certificate (PEM) <span className="text-2xs text-muted-foreground font-normal">(optional; leave blank to keep)</span>
            </label>
            <form.Field name="caCertPem">
              {(field) => (
                <Textarea
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="-----BEGIN CERTIFICATE-----"
                  rows={3}
                  className="min-h-0 resize-none"
                />
              )}
            </form.Field>
          </div>

          <div className="flex items-center justify-between gap-4">
            <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
              <form.Field name="enabled">
                {(field) => (
                  <input
                    type="checkbox"
                    checked={field.state.value}
                    onChange={(e) => field.handleChange(e.target.checked)}
                    onBlur={field.handleBlur}
                    className="h-4 w-4 rounded border-border"
                  />
                )}
              </form.Field>
              Enabled
            </label>
            <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
              <form.Field name="tlsSkipVerify">
                {(field) => (
                  <input
                    type="checkbox"
                    checked={field.state.value}
                    onChange={(e) => field.handleChange(e.target.checked)}
                    onBlur={field.handleBlur}
                    className="h-4 w-4 rounded border-border"
                  />
                )}
              </form.Field>
              <span className="inline-flex items-center gap-1">
                {tlsSkipVerify && <ShieldAlert className="h-3.5 w-3.5 text-status-warning" />}
                Skip TLS verify
              </span>
            </label>
          </div>
    </ModalShell>
  );
}

// ============================================================
// Per-forwarder status drawer
// ============================================================

function SIEMStatusDrawer({ forwarder, onClose }: { forwarder: SIEMForwarder; onClose: () => void }) {
  const { data: status, isLoading } = useSIEMForwarderStatus(forwarder.id);

  const metric = (label: string, value: React.ReactNode, tone?: 'error' | 'warning') => (
    <div className="rounded-lg border border-border bg-card p-3">
      <p className="text-2xs uppercase tracking-wide text-muted-foreground">{label}</p>
      <p
        className={`mt-1 text-lg font-semibold tabular-nums ${
          tone === 'error' ? 'text-status-error' : tone === 'warning' ? 'text-status-warning' : 'text-foreground'
        }`}
      >
        {value}
      </p>
    </div>
  );

  return (
    <ModalShell
      title="Forwarder Status"
      subtitle={forwarder.name}
      onClose={onClose}
      size="sm"
    >
          {isLoading && !status ? (
            <div className="flex items-center justify-center py-8 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
            </div>
          ) : (
            <>
              <div className="grid grid-cols-3 gap-3">
                {metric('Queue depth', status?.queueDepth ?? 0, (status?.queueDepth ?? 0) > 0 ? 'warning' : undefined)}
                {metric('Dispatched', status?.dispatchedTotal ?? 0)}
                {metric('Dropped', status?.droppedTotal ?? 0, (status?.droppedTotal ?? 0) > 0 ? 'error' : undefined)}
              </div>
              <div className="space-y-2 text-sm">
                <div className="flex items-center justify-between">
                  <span className="text-muted-foreground">Last sent</span>
                  <span className="text-foreground">
                    {status?.lastSentAt ? formatRelativeTime(status.lastSentAt) : 'Never'}
                  </span>
                </div>
                <div className="flex items-start justify-between gap-4">
                  <span className="text-muted-foreground flex-shrink-0">Last error</span>
                  <span className={`text-right ${status?.lastError ? 'text-status-error' : 'text-muted-foreground'}`}>
                    {status?.lastError || 'None'}
                  </span>
                </div>
              </div>
            </>
          )}
    </ModalShell>
  );
}

export default function SIEMForwardersPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <PageHeader
          eyebrow="Settings · SIEM"
          title="SIEM Forwarders"
          description="Stream audit + platform events to external SIEMs over syslog, Splunk HEC, or NDJSON-HTTPS. Use Test to ship a synthetic event through the real pipeline and confirm delivery."
        />
        <SIEMForwardersList />
      </PageShell>
    </SettingsAuthGate>
  );
}
