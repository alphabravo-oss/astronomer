'use client';

/**
 * /dashboard/settings/gitops/new — create a new GitOps source.
 * Reused for edit via the [id] page; this is the create-only entrypoint.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import { ArrowLeft } from 'lucide-react';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { useCreateGitOpsSource } from '@/components/settings/hooks';
import type { GitOpsSourceWriteRequest } from '@/lib/api/settings';

function GitOpsForm() {
  const router = useRouter();
  const create = useCreateGitOpsSource();
  const [form, setForm] = useState<GitOpsSourceWriteRequest>({
    name: '',
    repo_url: '',
    branch: 'main',
    path_prefix: 'clusters',
    auth_mode: 'none',
    auth: '',
    sync_mode: 'interval',
    sync_interval_seconds: 60,
    on_delete: 'log',
    enabled: true,
  });

  const update = <K extends keyof GitOpsSourceWriteRequest>(
    key: K,
    value: GitOpsSourceWriteRequest[K],
  ) => setForm((f) => ({ ...f, [key]: value }));

  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        const row = await create.mutateAsync(form);
        router.push(`/dashboard/settings/gitops/${row.id}`);
      }}
      className="space-y-5 max-w-2xl"
    >
      <div className="space-y-1">
        <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Name</label>
        <input
          required
          value={form.name}
          onChange={(e) => update('name', e.target.value)}
          className="w-full h-9 px-3 rounded border bg-background text-sm"
          placeholder="platform-clusters"
        />
      </div>
      <div className="space-y-1">
        <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Repo URL</label>
        <input
          required
          value={form.repo_url}
          onChange={(e) => update('repo_url', e.target.value)}
          className="w-full h-9 px-3 rounded border bg-background text-sm font-mono"
          placeholder="https://github.com/example/clusters.git"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Branch</label>
          <input
            value={form.branch ?? ''}
            onChange={(e) => update('branch', e.target.value)}
            className="w-full h-9 px-3 rounded border bg-background text-sm font-mono"
          />
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Path prefix</label>
          <input
            value={form.path_prefix ?? ''}
            onChange={(e) => update('path_prefix', e.target.value)}
            className="w-full h-9 px-3 rounded border bg-background text-sm font-mono"
          />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Auth mode</label>
          <select
            value={form.auth_mode}
            onChange={(e) => update('auth_mode', e.target.value as 'none' | 'https_token' | 'ssh_key')}
            className="w-full h-9 px-3 rounded border bg-background text-sm"
          >
            <option value="none">None (public repo)</option>
            <option value="https_token">HTTPS token</option>
            <option value="ssh_key">SSH key</option>
          </select>
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Auth token / key</label>
          <input
            type="password"
            value={form.auth ?? ''}
            onChange={(e) => update('auth', e.target.value)}
            disabled={form.auth_mode === 'none'}
            className="w-full h-9 px-3 rounded border bg-background text-sm font-mono disabled:opacity-50"
            placeholder={form.auth_mode === 'none' ? '(not required)' : 'paste secret'}
          />
        </div>
      </div>
      <div className="grid grid-cols-3 gap-4">
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Sync mode</label>
          <select
            value={form.sync_mode}
            onChange={(e) => update('sync_mode', e.target.value as 'manual' | 'interval')}
            className="w-full h-9 px-3 rounded border bg-background text-sm"
          >
            <option value="interval">Interval</option>
            <option value="manual">Manual only</option>
          </select>
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Interval (sec)</label>
          <input
            type="number"
            min={30}
            value={form.sync_interval_seconds ?? 60}
            onChange={(e) => update('sync_interval_seconds', Number(e.target.value))}
            disabled={form.sync_mode === 'manual'}
            className="w-full h-9 px-3 rounded border bg-background text-sm font-mono disabled:opacity-50"
          />
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">On delete</label>
          <select
            value={form.on_delete}
            onChange={(e) => update('on_delete', e.target.value as 'log' | 'tombstone' | 'decommission')}
            className="w-full h-9 px-3 rounded border bg-background text-sm"
          >
            <option value="log">Log only (safe)</option>
            <option value="tombstone">Tombstone (24h grace)</option>
            <option value="decommission">Immediate decommission</option>
          </select>
        </div>
      </div>
      {form.on_delete === 'decommission' && !form.path_prefix ? (
        <p className="text-xs text-status-warning">
          Warning: on_delete=decommission with an empty path_prefix monitors the
          entire repository. A single accidental rm anywhere in the tree will
          trigger a decommission.
        </p>
      ) : null}
      <div className="flex justify-end gap-2 pt-2">
        <Link
          href="/dashboard/settings/gitops"
          className="inline-flex items-center h-9 px-4 rounded-lg border text-sm hover:bg-muted transition-colors"
        >
          Cancel
        </Link>
        <button
          type="submit"
          disabled={create.isPending}
          className="inline-flex items-center h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-60"
        >
          {create.isPending ? 'Creating…' : 'Create source'}
        </button>
      </div>
    </form>
  );
}

export default function NewGitOpsSourcePage() {
  return (
    <SettingsAuthGate>
      <div className="space-y-6">
        <Link
          href="/dashboard/settings/gitops"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to GitOps sources
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · GitOps</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">New GitOps source</h1>
        </div>
        <GitOpsForm />
      </div>
    </SettingsAuthGate>
  );
}
