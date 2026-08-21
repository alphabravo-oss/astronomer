import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * /dashboard/settings/read-audit — operator UI for the read-side audit
 * policies (migration 063). Each row is a path-prefix + verbs +
 * sample-rate combination that, when matched, fires the read auditor.
 * Default seeds (cloud creds, registry creds, SSO, webhooks, SIEM,
 * audit log, support bundle, admin settings) ship enabled; operators
 * can add their own.
 *
 * Backend: /api/v1/admin/read-audit-policies/. Superuser-gated.
 */
import { useEffect, useState } from 'react';
import { Link } from '@/lib/link';
import { ArrowLeft, FileSearch, Loader2, Plus, Trash2 } from 'lucide-react';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { PageHeader, PageShell } from '@/components/ui/page';
import {
  listReadAuditPolicies,
  createReadAuditPolicy,
  updateReadAuditPolicy,
  deleteReadAuditPolicy,
  type ReadAuditPolicy,
} from '@/lib/api/settings';

function ReadAuditPoliciesPage() {
  return (
    <SettingsAuthGate>
      <ReadAuditPoliciesList />
    </SettingsAuthGate>
  );
}

function ReadAuditPoliciesList() {
  const [items, setItems] = useState<ReadAuditPolicy[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  async function refresh() {
    try {
      const data = await listReadAuditPolicies();
      setItems(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load');
    }
  }

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const data = await listReadAuditPolicies();
        if (!cancelled) setItems(data);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function toggleEnabled(p: ReadAuditPolicy) {
    setBusyId(p.id);
    try {
      await updateReadAuditPolicy(p.id, { enabled: !p.enabled });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Update failed');
    } finally {
      setBusyId(null);
    }
  }

  async function remove(p: ReadAuditPolicy) {
    if (!confirm(`Delete read-audit policy "${p.name}"?`)) return;
    setBusyId(p.id);
    try {
      await deleteReadAuditPolicy(p.id);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setBusyId(null);
    }
  }

  return (
    <PageShell>
      <Link
        href="/dashboard/settings"
        className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
      >
        <ArrowLeft className="h-4 w-4" /> Settings
      </Link>
      <PageHeader
        title="Read-side audit policies"
        description='Configure which GET endpoints emit an audit row. HIPAA / PCI compliance requires "who saw what credential and when" — the seeded policies cover cloud credentials, registry secrets, SSO, webhooks, SIEM auth, the audit log itself, support bundles, and admin settings.'
        actions={
          <ActionButton icon={<Plus className="h-4 w-4" />} onClick={() => setShowCreate(true)}>
            New policy
          </ActionButton>
        }
      />

      {error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}

      {items === null && !error ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading policies…
        </div>
      ) : (
        <div className="rounded-lg border border-border overflow-hidden">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <TableRow>
                <TableHead className="px-4 py-2 font-medium">Name</TableHead>
                <TableHead className="px-4 py-2 font-medium">Path pattern</TableHead>
                <TableHead className="px-4 py-2 font-medium">Verbs</TableHead>
                <TableHead className="px-4 py-2 font-medium">Sample</TableHead>
                <TableHead className="px-4 py-2 font-medium">Enabled</TableHead>
                <TableHead className="px-4 py-2 font-medium" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(items ?? []).map((p) => (
                <TableRow key={p.id} className="border-t border-border hover:bg-muted/30">
                  <TableCell className="px-4 py-2 font-mono text-xs">{p.name}</TableCell>
                  <TableCell className="px-4 py-2 font-mono text-xs">{p.path_pattern}</TableCell>
                  <TableCell className="px-4 py-2 text-xs">{p.verbs}</TableCell>
                  <TableCell className="px-4 py-2 text-xs">
                    {Math.round(p.sample_rate * 100)}%
                  </TableCell>
                  <TableCell className="px-4 py-2">
                    <button
                      disabled={busyId === p.id}
                      onClick={() => toggleEnabled(p)}
                      className={`text-xs px-2 py-0.5 rounded-md ${
                        p.enabled
                          ? 'bg-status-success/15 text-status-success'
                          : 'bg-status-warning/15 text-status-warning'
                      }`}
                    >
                      {p.enabled ? 'enabled' : 'disabled'}
                    </button>
                  </TableCell>
                  <TableCell className="px-4 py-2 text-right">
                    <button
                      disabled={busyId === p.id}
                      onClick={() => remove(p)}
                      className="text-muted-foreground hover:text-destructive"
                      title="Delete policy"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </TableCell>
                </TableRow>
              ))}
              {items && items.length === 0 && (
                <TableRow>
                  <TableCell className="px-4 py-6 text-center text-muted-foreground" colSpan={6}>
                    No policies configured. Read-side audit is currently disabled.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}

      {showCreate && (
        <CreatePolicyModal
          onClose={() => setShowCreate(false)}
          onCreated={async () => {
            setShowCreate(false);
            await refresh();
          }}
        />
      )}
    </PageShell>
  );
}

function CreatePolicyModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [pathPattern, setPathPattern] = useState('');
  const [verbs, setVerbs] = useState('GET');
  const [sampleRate, setSampleRate] = useState(1);
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      await createReadAuditPolicy({
        name,
        description,
        path_pattern: pathPattern,
        verbs,
        sample_rate: sampleRate,
        enabled,
      });
      onCreated();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Create failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell
      title="New read-audit policy"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose} disabled={busy}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={submit}
            disabled={busy || !name || !pathPattern}
            loading={busy}
            loadingLabel="Creating…"
          >
            Create
          </ActionButton>
        </>
      }
    >
        {error && (
          <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-sm text-destructive">
            {error}
          </div>
        )}
        <Field label="Name">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Description">
          <Input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </Field>
        <Field label="Path pattern (e.g. /admin/sso or /projects/*/cloud-credentials)">
          <Input
            value={pathPattern}
            onChange={(e) => setPathPattern(e.target.value)}
            className="font-mono"
          />
        </Field>
        <Field label="Verbs (comma-separated or *)">
          <Input
            value={verbs}
            onChange={(e) => setVerbs(e.target.value)}
          />
        </Field>
        <Field label={`Sample rate: ${Math.round(sampleRate * 100)}%`}>
          <input
            type="range"
            min={0}
            max={1}
            step={0.05}
            value={sampleRate}
            onChange={(e) => setSampleRate(Number(e.target.value))}
            className="w-full"
          />
        </Field>
        <label className="flex items-center gap-2 text-sm text-foreground">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          Enabled
        </label>
    </ModalShell>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <label className="text-xs uppercase tracking-wide text-muted-foreground">{label}</label>
      {children}
    </div>
  );
}

export const Route = createFileRoute('/dashboard/settings/read-audit/')({
  component: ReadAuditPoliciesPage,
});
