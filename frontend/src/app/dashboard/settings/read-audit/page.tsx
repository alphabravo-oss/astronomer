'use client';

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
import Link from 'next/link';
import { ArrowLeft, FileSearch, Loader2, Plus, Trash2 } from 'lucide-react';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  listReadAuditPolicies,
  createReadAuditPolicy,
  updateReadAuditPolicy,
  deleteReadAuditPolicy,
  type ReadAuditPolicy,
} from '@/lib/api/settings';

export default function ReadAuditPoliciesPage() {
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
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <Link
          href="/dashboard/settings"
          className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
        >
          <ArrowLeft className="h-4 w-4" /> Settings
        </Link>
      </div>
      <div className="flex items-start gap-3">
        <div className="flex-shrink-0 w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
          <FileSearch className="h-5 w-5 text-foreground" />
        </div>
        <div className="flex-1">
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">
            Read-side audit policies
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Configure which GET endpoints emit an audit row. HIPAA / PCI compliance
            requires &quot;who saw what credential and when&quot; — the seeded policies
            cover cloud credentials, registry secrets, SSO, webhooks, SIEM auth,
            the audit log itself, support bundles, and admin settings.
          </p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center gap-1 rounded-md border border-border bg-background px-3 py-1.5 text-sm font-medium hover:bg-muted"
        >
          <Plus className="h-4 w-4" /> New policy
        </button>
      </div>

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
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Name</th>
                <th className="px-4 py-2 font-medium">Path pattern</th>
                <th className="px-4 py-2 font-medium">Verbs</th>
                <th className="px-4 py-2 font-medium">Sample</th>
                <th className="px-4 py-2 font-medium">Enabled</th>
                <th className="px-4 py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {(items ?? []).map((p) => (
                <tr key={p.id} className="border-t border-border hover:bg-muted/30">
                  <td className="px-4 py-2 font-mono text-xs">{p.name}</td>
                  <td className="px-4 py-2 font-mono text-xs">{p.path_pattern}</td>
                  <td className="px-4 py-2 text-xs">{p.verbs}</td>
                  <td className="px-4 py-2 text-xs">
                    {Math.round(p.sample_rate * 100)}%
                  </td>
                  <td className="px-4 py-2">
                    <button
                      disabled={busyId === p.id}
                      onClick={() => toggleEnabled(p)}
                      className={`text-xs px-2 py-0.5 rounded-md ${
                        p.enabled
                          ? 'bg-emerald-500/15 text-emerald-600'
                          : 'bg-amber-500/15 text-amber-600'
                      }`}
                    >
                      {p.enabled ? 'enabled' : 'disabled'}
                    </button>
                  </td>
                  <td className="px-4 py-2 text-right">
                    <button
                      disabled={busyId === p.id}
                      onClick={() => remove(p)}
                      className="text-muted-foreground hover:text-destructive"
                      title="Delete policy"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </td>
                </tr>
              ))}
              {items && items.length === 0 && (
                <tr>
                  <td className="px-4 py-6 text-center text-muted-foreground" colSpan={6}>
                    No policies configured. Read-side audit is currently disabled.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
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
    </div>
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
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl p-6 space-y-4">
        <h3 className="text-lg font-semibold text-foreground">New read-audit policy</h3>
        {error && (
          <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-sm text-destructive">
            {error}
          </div>
        )}
        <Field label="Name">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full rounded-md border border-border bg-background px-2 py-1 text-sm"
          />
        </Field>
        <Field label="Description">
          <input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="w-full rounded-md border border-border bg-background px-2 py-1 text-sm"
          />
        </Field>
        <Field label="Path pattern (e.g. /admin/sso or /projects/*/cloud-credentials)">
          <input
            value={pathPattern}
            onChange={(e) => setPathPattern(e.target.value)}
            className="w-full rounded-md border border-border bg-background px-2 py-1 text-sm font-mono"
          />
        </Field>
        <Field label="Verbs (comma-separated or *)">
          <input
            value={verbs}
            onChange={(e) => setVerbs(e.target.value)}
            className="w-full rounded-md border border-border bg-background px-2 py-1 text-sm"
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
        <div className="flex justify-end gap-2 pt-2">
          <button
            onClick={onClose}
            disabled={busy}
            className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
          >
            Cancel
          </button>
          <button
            onClick={submit}
            disabled={busy || !name || !pathPattern}
            className="rounded-md bg-foreground text-background px-3 py-1.5 text-sm font-medium disabled:opacity-50"
          >
            {busy ? 'Creating…' : 'Create'}
          </button>
        </div>
      </div>
    </div>
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
