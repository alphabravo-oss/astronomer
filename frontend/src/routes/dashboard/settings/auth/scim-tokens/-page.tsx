/**
 * /dashboard/settings/auth/scim-tokens — SCIM provisioning tokens (F-05).
 *
 * Mint, list, and revoke the static bearer tokens that authenticate the
 * /scim/v2/* provisioning chain. The plaintext token is shown exactly once,
 * immediately after creation; list rows only ever carry metadata.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useAppForm, useStore } from '@/lib/form';
import {
  ArrowLeft,
  Plus,
  Trash2,
  KeyRound,
  Copy,
  Check,
  ShieldAlert,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { PageHeader, PageShell } from '@/components/ui/page';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { formatRelativeTime } from '@/lib/utils';
import { toastSuccess } from '@/lib/toast';
import type { SCIMToken, SCIMTokenCreated } from '@/types';
import { useSCIMTokens, useCreateSCIMToken, useRevokeSCIMToken } from './-hooks';

function SCIMTokensList() {
  const { data, isLoading, isError, refetch } = useSCIMTokens();
  const revoke = useRevokeSCIMToken();

  const [showCreate, setShowCreate] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<SCIMToken | null>(null);
  const [created, setCreated] = useState<SCIMTokenCreated | null>(null);

  const columns: Column<SCIMToken>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <KeyRound className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
        </div>
      ),
    },
    {
      key: 'prefix',
      header: 'Token',
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">{row.prefix}…</span>
      ),
      sortable: false,
    },
    {
      key: 'lastUsedAt',
      header: 'Last used',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastUsedAt ? formatRelativeTime(row.lastUsedAt) : 'Never'}
        </span>
      ),
    },
    {
      key: 'createdAt',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            setRevokeTarget(row);
          }}
          className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
          title="Revoke token"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ),
    },
  ];

  return (
    <>
      <div className="flex items-center justify-end">
        <ActionButton intent="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setShowCreate(true)}>
          Mint Token
        </ActionButton>
      </div>

      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        searchPlaceholder="Search tokens..."
        emptyMessage="No SCIM tokens minted"
      />

      {showCreate && (
        <CreateSCIMTokenModal
          onClose={() => setShowCreate(false)}
          onCreated={(t) => {
            setShowCreate(false);
            setCreated(t);
          }}
        />
      )}

      {created && <RevealTokenModal created={created} onClose={() => setCreated(null)} />}

      <ConfirmDialog
        open={!!revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onConfirm={async () => {
          if (!revokeTarget) return;
          await revoke.mutateAsync(revokeTarget.id);
          setRevokeTarget(null);
        }}
        title="Revoke SCIM token?"
        description={`Any IdP using "${revokeTarget?.name}" will immediately fail to provision. This cannot be undone.`}
        confirmText="Revoke"
        confirmValue={revokeTarget?.name}
        variant="destructive"
        loading={revoke.isPending}
      />
    </>
  );
}

function CreateSCIMTokenModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (t: SCIMTokenCreated) => void;
}) {
  const create = useCreateSCIMToken();

  const form = useAppForm({
    defaultValues: { name: '' },
    onSubmit: async ({ value }) => {
      try {
        const t = await create.mutateAsync(value.name.trim());
        onCreated(t);
      } catch {
        /* mutation toasts on error */
      }
    },
  });
  // Old disabled gate (`!name.trim()`), recomputed from form state.
  const name = useStore(form.store, (s) => s.values.name);

  return (
    <ModalShell
      title="Mint SCIM Token"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={create.isPending || !name.trim()}
            loading={create.isPending}
          >
            Mint Token
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
                  placeholder="okta-provisioning"
                  autoFocus
                />
              )}
            </form.Field>
            <p className="text-2xs text-muted-foreground">
              A label to recognize this token. The secret is shown once on the next screen.
            </p>
          </div>
    </ModalShell>
  );
}

function RevealTokenModal({ created, onClose }: { created: SCIMTokenCreated; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(created.token);
      setCopied(true);
      toastSuccess('Token copied to clipboard');
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard blocked — operator can select the text manually */
    }
  };

  return (
    <ModalShell
      title="Token Created"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <ActionButton intent="primary" onClick={onClose}>
          Done
        </ActionButton>
      }
    >
          <div className="flex items-start gap-2 rounded-lg border border-status-warning/30 bg-status-warning/10 p-3">
            <ShieldAlert className="h-4 w-4 text-status-warning flex-shrink-0 mt-0.5" />
            <p className="text-xs text-foreground">
              Copy this token now — it is shown <b>only once</b>. Only its hash is stored; it cannot be
              recovered later.
            </p>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">{created.name}</label>
            <div className="flex items-center gap-2">
              <code className="flex-1 px-3 py-2 rounded-md border border-border bg-background text-xs font-mono text-foreground break-all">
                {created.token}
              </code>
              <ActionButton
                onClick={copy}
                size="icon"
                title="Copy token"
                icon={copied ? <Check className="h-4 w-4 text-status-success" /> : <Copy className="h-4 w-4" />}
              />
            </div>
          </div>
    </ModalShell>
  );
}

export default function SCIMTokensPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <Link
          href="/dashboard/settings/auth"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Auth
        </Link>
        <PageHeader
          eyebrow="Settings · Auth · SCIM"
          title="SCIM Provisioning Tokens"
          description={
            <>
              Bearer tokens that authenticate your IdP&apos;s SCIM 2.0 provisioning requests to
              <code className="mx-1 text-2xs font-mono">/scim/v2</code>. Mint one per IdP; revoke to cut off provisioning.
            </>
          }
        />
        <SCIMTokensList />
      </PageShell>
    </SettingsAuthGate>
  );
}
