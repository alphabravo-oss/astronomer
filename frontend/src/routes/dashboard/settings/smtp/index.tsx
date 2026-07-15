import { createFileRoute } from '@tanstack/react-router';
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
import { Link } from '@/lib/link';
import { ArrowLeft, Loader2, Mail, Save, Send } from 'lucide-react';
import { toastError } from '@/lib/toast';
import { formatRelativeTime } from '@/lib/utils';
import { useAppForm } from '@/lib/form';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useSentEmails,
  useSmtpConfig,
  useTestSmtp,
  useUpdateSmtpConfig,
} from '@/components/settings/hooks';
import { SMTP_REDACTED_SENTINEL, type SentEmail, type SmtpConfig } from '@/lib/api/settings';

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
  const [testTo, setTestTo] = useState('');
  const update = useUpdateSmtpConfig();
  const testSend = useTestSmtp();

  const form = useAppForm({
    defaultValues: initial,
    onSubmit: async ({ value }) => {
      try {
        // The sentinel stays in the value; updateSmtpConfig strips it before
        // the PUT (strip-in-mutation, unchanged).
        await update.mutateAsync(value);
      } catch {
        // Mutation toasts on error.
      }
    },
  });

  // Post-save invalidation refetches the config — rebase the form on it.
  useEffect(() => {
    form.reset(initial);
  }, [form, initial]);

  const handleTest = async () => {
    if (!testTo) {
      toastError('Recipient required');
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
        <div className="sm:col-span-2">
          <form.AppField name="host">
            {(field) => <field.TextField label="Host" placeholder="smtp.example.com" />}
          </form.AppField>
        </div>
        <form.AppField name="port">{(field) => <field.NumberField label="Port" />}</form.AppField>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <form.AppField name="username">
          {(field) => <field.TextField label="Username" />}
        </form.AppField>
        <form.AppField name="password">
          {(field) => (
            <field.SecretField
              label="Password"
              stored={field.state.value === SMTP_REDACTED_SENTINEL}
            />
          )}
        </form.AppField>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <form.AppField name="fromAddress">
          {(field) => (
            <field.TextField label="From address" type="email" placeholder="no-reply@example.com" />
          )}
        </form.AppField>
        <form.AppField name="fromName">
          {(field) => <field.TextField label="From name" placeholder="Astronomer" />}
        </form.AppField>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <form.AppField name="authMechanism">
          {(field) => (
            <field.SelectField label="Auth mechanism">
              <option value="plain">PLAIN</option>
              <option value="login">LOGIN</option>
              <option value="cram-md5">CRAM-MD5</option>
              <option value="none">None</option>
            </field.SelectField>
          )}
        </form.AppField>
        <form.AppField name="encryption">
          {(field) => (
            <field.SelectField label="Encryption">
              <option value="starttls">STARTTLS</option>
              <option value="tls">TLS</option>
              <option value="none">None</option>
            </field.SelectField>
          )}
        </form.AppField>
        <form.AppField name="timeoutSeconds">
          {(field) => <field.NumberField label="Timeout (s)" min={1} />}
        </form.AppField>
      </div>

      <form.AppField name="requireTls">
        {(field) => (
          <field.SwitchField
            label="Require TLS"
            helper="Reject connections that don't negotiate TLS."
          />
        )}
      </form.AppField>

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
        <form.Subscribe
          selector={(state) =>
            [
              JSON.stringify(state.values) !== JSON.stringify(initial),
              state.isSubmitting,
            ] as const
          }
        >
          {([dirty, saving]) => (
            <button
              type="button"
              onClick={() => void form.handleSubmit()}
              disabled={!dirty || saving}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
              Save changes
            </button>
          )}
        </form.Subscribe>
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

function SmtpSettingsPage() {
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

export const Route = createFileRoute('/dashboard/settings/smtp/')({
  component: SmtpSettingsPage,
});
