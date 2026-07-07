'use client';

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
import {
  ArrowLeft,
  Plus,
  Trash2,
  Pencil,
  Send,
  Activity,
  Loader2,
  X,
  ShieldAlert,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { OverlayShell } from '@/components/ui/overlay-shell';
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
} from './hooks';

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
        <button
          onClick={() => {
            setEditing(null);
            setShowModal(true);
          }}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          Add Forwarder
        </button>
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

  const [form, setForm] = useState({
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
  });

  const handleSave = async () => {
    const filters = form.eventFilters
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    const body: SIEMForwarderWriteRequest = {
      name: form.name,
      transport: form.transport,
      endpoint: form.endpoint,
      event_filters: filters,
      format: form.format,
      tls_skip_verify: form.tlsSkipVerify,
      batch_size: form.batchSize,
      flush_interval_ms: form.flushIntervalMs,
      timeout_seconds: form.timeoutSeconds,
      enabled: form.enabled,
    };
    // Only send auth when the operator supplied a new value; on edit an empty
    // field means "keep existing" (we echo the sentinel so a blank PUT doesn't
    // wipe the stored blob).
    if (form.auth.trim()) {
      body.auth = form.auth;
    } else if (isEdit && forwarder?.authConfigured) {
      body.auth = SIEM_AUTH_SENTINEL;
    }
    if (form.caCertPem.trim()) {
      body.ca_cert_pem = form.caCertPem;
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
  };

  const isPending = create.isPending || update.isPending;
  const inputCls =
    'w-full h-9 px-3 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring';

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">
            {isEdit ? 'Edit SIEM Forwarder' : 'Add SIEM Forwarder'}
          </h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="corp-splunk"
              className={inputCls}
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Transport</label>
              <select
                value={form.transport}
                onChange={(e) => setForm((f) => ({ ...f, transport: e.target.value }))}
                className={inputCls}
              >
                {TRANSPORTS.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Format</label>
              <select
                value={form.format}
                onChange={(e) => setForm((f) => ({ ...f, format: e.target.value }))}
                className={inputCls}
              >
                {FORMATS.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Endpoint</label>
            <input
              type="text"
              value={form.endpoint}
              onChange={(e) => setForm((f) => ({ ...f, endpoint: e.target.value }))}
              placeholder="siem.corp.example.com:6514"
              className={`${inputCls} font-mono`}
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              Auth {isEdit && <span className="text-2xs text-muted-foreground font-normal">(leave blank to keep existing)</span>}
            </label>
            <input
              type="password"
              value={form.auth}
              onChange={(e) => setForm((f) => ({ ...f, auth: e.target.value }))}
              placeholder={isEdit && forwarder?.authConfigured ? '•••••••• (configured)' : 'HEC token / bearer / password'}
              className={`${inputCls} font-mono`}
              autoComplete="new-password"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              Event filters <span className="text-2xs text-muted-foreground font-normal">(comma-separated; blank = all)</span>
            </label>
            <input
              type="text"
              value={form.eventFilters}
              onChange={(e) => setForm((f) => ({ ...f, eventFilters: e.target.value }))}
              placeholder="auth.login.failed, admin.*"
              className={`${inputCls} font-mono`}
            />
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Batch size</label>
              <input
                type="number"
                min={1}
                value={form.batchSize}
                onChange={(e) => setForm((f) => ({ ...f, batchSize: parseInt(e.target.value, 10) || 0 }))}
                className={inputCls}
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Flush (ms)</label>
              <input
                type="number"
                min={0}
                value={form.flushIntervalMs}
                onChange={(e) => setForm((f) => ({ ...f, flushIntervalMs: parseInt(e.target.value, 10) || 0 }))}
                className={inputCls}
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Timeout (s)</label>
              <input
                type="number"
                min={1}
                value={form.timeoutSeconds}
                onChange={(e) => setForm((f) => ({ ...f, timeoutSeconds: parseInt(e.target.value, 10) || 0 }))}
                className={inputCls}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              CA certificate (PEM) <span className="text-2xs text-muted-foreground font-normal">(optional; leave blank to keep)</span>
            </label>
            <textarea
              value={form.caCertPem}
              onChange={(e) => setForm((f) => ({ ...f, caCertPem: e.target.value }))}
              placeholder="-----BEGIN CERTIFICATE-----"
              rows={3}
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-none"
            />
          </div>

          <div className="flex items-center justify-between gap-4">
            <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
                className="h-4 w-4 rounded border-border"
              />
              Enabled
            </label>
            <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
              <input
                type="checkbox"
                checked={form.tlsSkipVerify}
                onChange={(e) => setForm((f) => ({ ...f, tlsSkipVerify: e.target.checked }))}
                className="h-4 w-4 rounded border-border"
              />
              <span className="inline-flex items-center gap-1">
                {form.tlsSkipVerify && <ShieldAlert className="h-3.5 w-3.5 text-status-warning" />}
                Skip TLS verify
              </span>
            </label>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={isPending || !form.name || !form.endpoint}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {isEdit ? 'Save Changes' : 'Create Forwarder'}
          </button>
        </div>
      </div>
    </OverlayShell>
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
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <div>
            <h3 className="text-lg font-semibold text-foreground">Forwarder Status</h3>
            <p className="text-xs text-muted-foreground">{forwarder.name}</p>
          </div>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>
        <div className="p-6 space-y-4">
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
        </div>
      </div>
    </OverlayShell>
  );
}

export default function SIEMForwardersPage() {
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
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · SIEM</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">SIEM Forwarders</h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-2xl">
            Stream audit + platform events to external SIEMs over syslog, Splunk HEC, or NDJSON-HTTPS.
            Use Test to ship a synthetic event through the real pipeline and confirm delivery.
          </p>
        </div>
        <SIEMForwardersList />
      </div>
    </SettingsAuthGate>
  );
}
