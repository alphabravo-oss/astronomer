'use client';

/**
 * /dashboard/settings/auth/scim-tokens — SCIM provisioning tokens (F-05).
 *
 * Mint, list, and revoke the static bearer tokens that authenticate the
 * /scim/v2/* provisioning chain. The plaintext token is shown exactly once,
 * immediately after creation; list rows only ever carry metadata.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import {
  ArrowLeft,
  Plus,
  Trash2,
  KeyRound,
  Copy,
  Check,
  Loader2,
  X,
  ShieldAlert,
} from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { formatRelativeTime } from '@/lib/utils';
import { toastSuccess } from '@/lib/toast';
import type { SCIMToken, SCIMTokenCreated } from '@/types';
import { useSCIMTokens, useCreateSCIMToken, useRevokeSCIMToken } from './hooks';

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
        <button
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          Mint Token
        </button>
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
  const [name, setName] = useState('');

  const handleSave = async () => {
    try {
      const t = await create.mutateAsync(name.trim());
      onCreated(t);
    } catch {
      /* mutation toasts on error */
    }
  };

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <h3 className="text-lg font-semibold text-foreground">Mint SCIM Token</h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>
        <div className="p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="okta-provisioning"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              autoFocus
            />
            <p className="text-2xs text-muted-foreground">
              A label to recognize this token. The secret is shown once on the next screen.
            </p>
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={create.isPending || !name.trim()}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Mint Token
          </button>
        </div>
      </div>
    </OverlayShell>
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
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <h3 className="text-lg font-semibold text-foreground">Token Created</h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>
        <div className="p-6 space-y-4">
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
              <button
                onClick={copy}
                className="flex-shrink-0 h-9 w-9 inline-flex items-center justify-center rounded-md border border-border text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                title="Copy token"
              >
                {copied ? <Check className="h-4 w-4 text-status-success" /> : <Copy className="h-4 w-4" />}
              </button>
            </div>
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
          >
            Done
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}

export default function SCIMTokensPage() {
  return (
    <SettingsAuthGate>
      <div className="space-y-6">
        <Link
          href="/dashboard/settings/auth"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Auth
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Auth · SCIM</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">SCIM Provisioning Tokens</h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-2xl">
            Bearer tokens that authenticate your IdP&apos;s SCIM 2.0 provisioning requests to
            <code className="mx-1 text-2xs font-mono">/scim/v2</code>. Mint one per IdP; revoke to cut off provisioning.
          </p>
        </div>
        <SCIMTokensList />
      </div>
    </SettingsAuthGate>
  );
}
