import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/smtp — SMTP configuration + recent sent-email audit.
 *
 * The password column is special: on reads the backend returns the
 * `__redacted__` sentinel. We keep the sentinel in the form state so the
 * input renders something, and `updateSmtpConfig` strips the sentinel
 * before sending the PUT — meaning operators only rotate the password if
 * they actually type a new value.
 */
import { useEffect, useState } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import {
  ArrowLeft,
  Loader2,
  Mail,
  Pencil,
  Plus,
  Save,
  Send,
} from "lucide-react";
import { toastError } from "@/lib/toast";
import { extractApiErrorMessage } from "@/lib/api/errors";
import { formatRelativeTime } from "@/lib/utils";
import { pageCount, pageNumber } from "@/lib/api/pagination";
import { useAppForm } from "@/lib/form";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { StatusBadge } from "@/components/ui/status-badge";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import {
  useSentEmails,
  useSmtpConfig,
  useTestSmtp,
  useUpdateSmtpConfig,
} from "@/components/settings/hooks";
import {
  SMTP_REDACTED_SENTINEL,
  type SentEmail,
  type SmtpConfig,
} from "@/lib/api/settings";

const DEFAULT_CONFIG: SmtpConfig = {
  host: "",
  port: 587,
  username: "",
  password: "",
  fromAddress: "",
  fromName: "",
  authMechanism: "plain",
  encryption: "starttls",
  requireTls: true,
  timeoutSeconds: 30,
};

function SmtpForm({
  initial,
  onSaved,
}: {
  initial: SmtpConfig;
  onSaved?: () => void;
}) {
  const [testTo, setTestTo] = useState("");
  const update = useUpdateSmtpConfig();
  const testSend = useTestSmtp();

  const form = useAppForm({
    defaultValues: initial,
    onSubmit: async ({ value }) => {
      try {
        // The sentinel stays in the value; updateSmtpConfig strips it before
        // the PUT (strip-in-mutation, unchanged).
        await update.mutateAsync(value);
        onSaved?.();
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
      toastError("Recipient required");
      return;
    }
    try {
      await testSend.mutateAsync({ to: testTo });
    } catch {
      // Mutation toasts.
    }
  };

  return (
    <div className="space-y-5">
      <form.AppForm>
        <form.FormErrorSummary
          serverError={update.error ? extractApiErrorMessage(update.error) : null}
        />
      </form.AppForm>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <div className="sm:col-span-2">
          <form.AppField
            name="host"
            validators={{
              onChange: ({ value }) =>
                !value.trim() ? "Host is required" : undefined,
            }}
          >
            {(field) => (
              <field.TextField label="Host" placeholder="smtp.example.com" />
            )}
          </form.AppField>
        </div>
        <form.AppField
          name="port"
          validators={{
            onChange: ({ value }) =>
              value < 1 || value > 65535
                ? "Port must be between 1 and 65535"
                : undefined,
          }}
        >
          {(field) => <field.NumberField label="Port" />}
        </form.AppField>
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
        <form.AppField
          name="fromAddress"
          validators={{
            onChange: ({ value }) =>
              !value.includes("@")
                ? "From address must be a valid email address"
                : undefined,
          }}
        >
          {(field) => (
            <field.TextField
              label="From address"
              type="email"
              placeholder="no-reply@example.com"
            />
          )}
        </form.AppField>
        <form.AppField name="fromName">
          {(field) => (
            <field.TextField label="From name" placeholder="Astronomer" />
          )}
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
          <Input
            type="email"
            value={testTo}
            onChange={(e) => setTestTo(e.target.value)}
            placeholder="ops@example.com"
            className="flex-1"
          />
          <ActionButton
            type="button"
            onClick={handleTest}
            disabled={testSend.isPending || !testTo}
            loading={testSend.isPending}
            icon={<Send className="h-3.5 w-3.5" />}
          >
            Send test email
          </ActionButton>
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
            <ActionButton
              type="button"
              intent="primary"
              onClick={() => void form.handleSubmit()}
              disabled={!dirty || saving}
              loading={saving}
              icon={<Save className="h-3.5 w-3.5" />}
            >
              Save changes
            </ActionButton>
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
  const currentPage = data ? pageNumber(data.pagination) : page;
  const totalPages = data ? pageCount(data.pagination) : undefined;

  const columns: Column<SentEmail>[] = [
    {
      key: "createdAt",
      header: "Time",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground font-mono">
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      key: "to",
      header: "To",
      accessor: (row) => (
        <span className="text-sm text-foreground">{row.to}</span>
      ),
    },
    {
      key: "template",
      header: "Template",
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground font-mono">
          {row.template}
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) => (
        <StatusBadge
          status={
            row.status === "sent"
              ? "active"
              : row.status === "failed" || row.status === "bounced"
                ? "error"
                : "connecting"
          }
          label={row.status}
          size="sm"
        />
      ),
    },
    {
      key: "attempts",
      header: "Attempts",
      align: "right",
      accessor: (row) => (
        <span className="tabular-nums text-sm">{row.attempts}</span>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-semibold text-foreground">
          Sent email log
        </h2>
        {data && (
          <p className="text-xs text-muted-foreground">
            Page {currentPage}
            {totalPages === undefined ? "" : ` of ${totalPages}`}
            {data.pagination.total === undefined
              ? ""
              : ` · ${data.pagination.total} total`}
          </p>
        )}
      </div>
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        emptyState={{
          title: "No emails sent yet",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        pageSize={25}
      />
      {data && (data.pagination.offset > 0 || data.pagination.has_more) && (
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
            disabled={!data.pagination.has_more}
            className="h-8 px-3 rounded-lg border border-border text-xs font-medium disabled:opacity-50"
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 py-1.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-sm text-foreground font-mono truncate">
        {value || "—"}
      </span>
    </div>
  );
}

function SmtpSummary({
  config,
  onEdit,
}: {
  config: SmtpConfig;
  onEdit: () => void;
}) {
  const configured = !!config.host;
  return (
    <div className="rounded-xl border border-border bg-card p-6 space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">Server</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Connection + authentication for outbound mail.
          </p>
        </div>
        <button
          type="button"
          onClick={onEdit}
          className="inline-flex shrink-0 items-center gap-1.5 h-9 px-3 rounded-lg border border-border text-sm font-medium hover:bg-accent transition-colors"
        >
          {configured ? (
            <Pencil className="h-3.5 w-3.5" />
          ) : (
            <Plus className="h-3.5 w-3.5" />
          )}
          {configured ? "Edit configuration" : "Configure SMTP"}
        </button>
      </div>
      {configured ? (
        <div className="divide-y divide-border/60">
          <SummaryRow label="Host" value={`${config.host}:${config.port}`} />
          <SummaryRow label="Username" value={config.username} />
          <SummaryRow
            label="Password"
            value={config.password ? "Configured" : "Not set"}
          />
          <SummaryRow
            label="From"
            value={
              config.fromName
                ? `${config.fromName} <${config.fromAddress}>`
                : config.fromAddress
            }
          />
          <SummaryRow label="Auth" value={config.authMechanism} />
          <SummaryRow
            label="Encryption"
            value={`${config.encryption}${config.requireTls ? " · require TLS" : ""}`}
          />
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          No mail server configured yet. Configure SMTP to enable outbound email
          and test-sends.
        </p>
      )}
    </div>
  );
}

function SmtpPageInner() {
  const { data, isLoading } = useSmtpConfig();
  const initial = data ?? DEFAULT_CONFIG;
  const [editing, setEditing] = useState(false);
  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  return (
    <div className="space-y-6">
      <SmtpSummary config={initial} onEdit={() => setEditing(true)} />
      <EmailsTable />
      {editing && (
        <ModalShell
          title="SMTP configuration"
          subtitle="Outbound mail server + test-send."
          titleIcon={<Mail className="h-4 w-4" />}
          size="lg"
          onClose={() => setEditing(false)}
        >
          <SmtpForm initial={initial} onSaved={() => setEditing(false)} />
        </ModalShell>
      )}
    </div>
  );
}

function SmtpSettingsPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </RouterLink>
        <PageHeader
          eyebrow="Settings · Email"
          title={
            <span className="inline-flex items-center gap-2">
              <Mail className="h-5 w-5 text-muted-foreground" />
              Email & SMTP
            </span>
          }
          description="Outbound mail server, test-send, and audit log of recent emails."
        />
        <SmtpPageInner />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/smtp/")({
  component: SmtpSettingsPage,
});
