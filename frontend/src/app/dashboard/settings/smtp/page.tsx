'use client';

/**
 * /dashboard/settings/smtp — SMTP configuration + recent sent-email audit.
 *
 * The password column is special: on reads the backend returns the
 * `__redacted__` sentinel. We keep the sentinel in the form state so the
 * input renders something, and `updateSmtpConfig` strips the sentinel
 * before sending the PUT — meaning operators only rotate the password if
 * they actually type a new value.
 */
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { ArrowLeft, Loader2, Mail, Save, Send } from 'lucide-react';
import { toast } from 'sonner';
import { cn, formatRelativeTime } from '@/lib/utils';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useSentEmails,
  useSmtpConfig,
  useTestSmtp,
  useUpdateSmtpConfig,
} from '@/components/settings/hooks';
import {
  SMTP_REDACTED_SENTINEL,
  type SentEmail,
  type SmtpAuth,
  type SmtpConfig,
  type SmtpEncryption,
} from '@/lib/api/settings';

const DEFAULT_CONFIG: SmtpConfig = {
  host: '',
  port: 587,
  username: '',
  password: '',
  fromAddress: '',
  fromName: '',
  authMechanism: 'plain',
  encryption: 'starttls',
  requireTls: true,
  timeoutSeconds: 30,
};

function SmtpForm({ initial }: { initial: SmtpConfig }) {
  const [form, setForm] = useState<SmtpConfig>(initial);
  const [testTo, setTestTo] = useState('');
  const update = useUpdateSmtpConfig();
  const testSend = useTestSmtp();

  useEffect(() => {
    setForm(initial);
  }, [initial]);

  const dirty = JSON.stringify(form) !== JSON.stringify(initial);

  const handleSave = async () => {
    try {
      await update.mutateAsync(form);
    } catch {
      // Mutation toasts on error.
    }
  };

  const handleTest = async () => {
    if (!testTo) {
      toast.error('Recipient required');
      return;
    }
    try {
      await testSend.mutateAsync({ to: testTo });
    } catch {
      // Mutation toasts.
    }
  };

  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-5">
      <div>
        <h2 className="text-base font-semibold text-foreground">Server</h2>
        <p className="text-xs text-muted-foreground mt-0.5">Connection + authentication for outbound mail.</p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <div className="sm:col-span-2 space-y-1.5">
          <label className="text-sm font-medium text-foreground">Host</label>
          <input
            type="text"
            value={form.host}
            onChange={(e) => setForm({ ...form, host: e.target.value })}
            placeholder="smtp.example.com"
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Port</label>
          <input
            type="number"
            value={form.port}
            onChange={(e) => setForm({ ...form, port: Number(e.target.value) })}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Username</label>
          <input
            type="text"
            value={form.username}
            onChange={(e) => setForm({ ...form, username: e.target.value })}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Password</label>
          <input
            type="password"
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          />
          {form.password === SMTP_REDACTED_SENTINEL && (
            <p className="text-xs text-muted-foreground">
              Stored password preserved — type a new value to rotate.
            </p>
          )}
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">From address</label>
          <input
            type="email"
            value={form.fromAddress}
            onChange={(e) => setForm({ ...form, fromAddress: e.target.value })}
            placeholder="no-reply@example.com"
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">From name</label>
          <input
            type="text"
            value={form.fromName}
            onChange={(e) => setForm({ ...form, fromName: e.target.value })}
            placeholder="Astronomer"
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Auth mechanism</label>
          <select
            value={form.authMechanism}
            onChange={(e) => setForm({ ...form, authMechanism: e.target.value as SmtpAuth })}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          >
            <option value="plain">PLAIN</option>
            <option value="login">LOGIN</option>
            <option value="cram-md5">CRAM-MD5</option>
            <option value="none">None</option>
          </select>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Encryption</label>
          <select
            value={form.encryption}
            onChange={(e) => setForm({ ...form, encryption: e.target.value as SmtpEncryption })}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          >
            <option value="starttls">STARTTLS</option>
            <option value="tls">TLS</option>
            <option value="none">None</option>
          </select>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Timeout (s)</label>
          <input
            type="number"
            value={form.timeoutSeconds}
            min={1}
            onChange={(e) => setForm({ ...form, timeoutSeconds: Number(e.target.value) })}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
      </div>

      <div className="flex items-center justify-between p-3 rounded-lg border border-border">
        <div>
          <p className="text-sm font-medium text-foreground">Require TLS</p>
          <p className="text-xs text-muted-foreground">Reject connections that don&apos;t negotiate TLS.</p>
        </div>
        <button
          type="button"
          onClick={() => setForm({ ...form, requireTls: !form.requireTls })}
          className={cn(
            'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
            form.requireTls ? 'bg-status-success' : 'bg-muted',
          )}
        >
          <span
            className={cn(
              'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
              form.requireTls ? 'translate-x-6' : 'translate-x-1',
            )}
          />
        </button>
      </div>

      <div className="flex flex-col sm:flex-row gap-3 pt-2 border-t border-border">
        <div className="flex-1 flex items-center gap-2">
          <input
            type="email"
            value={testTo}
            onChange={(e) => setTestTo(e.target.value)}
            placeholder="ops@example.com"
            className="flex-1 h-9 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
          />
          <button
            type="button"
            onClick={handleTest}
            disabled={testSend.isPending || !testTo}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
          >
            {testSend.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
            Send test email
          </button>
        </div>
        <button
          type="button"
          onClick={handleSave}
          disabled={!dirty || update.isPending}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {update.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
          Save changes
        </button>
      </div>
    </div>
  );
}

function EmailsTable() {
  const [page, setPage] = useState(1);
  const { data, isLoading } = useSentEmails({ page, page_size: 25 });
  const rows = data?.data ?? [];

  const columns: Column<SentEmail>[] = [
    {
      key: 'createdAt',
      header: 'Time',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground font-mono">
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      key: 'to',
      header: 'To',
      accessor: (row) => <span className="text-sm text-foreground">{row.to}</span>,
    },
    {
      key: 'template',
      header: 'Template',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
          {row.template}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={
            row.status === 'sent'
              ? 'active'
              : row.status === 'failed' || row.status === 'bounced'
                ? 'error'
                : 'connecting'
          }
          label={row.status}
          size="sm"
        />
      ),
    },
    {
      key: 'attempts',
      header: 'Attempts',
      align: 'right',
      accessor: (row) => <span className="tabular-nums text-sm">{row.attempts}</span>,
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-semibold text-foreground">Sent email log</h2>
        {data && (
          <p className="text-xs text-muted-foreground">
            Page {data.page} of {data.totalPages || 1} · {data.total} total
          </p>
        )}
      </div>
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        emptyMessage="No emails sent yet"
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

function SmtpPageInner() {
  const { data, isLoading } = useSmtpConfig();
  const initial = data ?? DEFAULT_CONFIG;
  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  return (
    <div className="space-y-6">
      <SmtpForm initial={initial} />
      <EmailsTable />
    </div>
  );
}

export default function SmtpSettingsPage() {
  return (
    <SettingsAuthGate>
      <div className="max-w-4xl mx-auto space-y-6">
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Email</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1 flex items-center gap-2">
            <Mail className="h-5 w-5 text-muted-foreground" />
            Email &amp; SMTP
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Outbound mail server, test-send, and audit log of recent emails.
          </p>
        </div>
        <SmtpPageInner />
      </div>
    </SettingsAuthGate>
  );
}
