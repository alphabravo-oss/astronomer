import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/gitops/new — create a new GitOps source.
 * Reused for edit via the [id] page; this is the create-only entrypoint.
 */
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import { ArrowLeft } from 'lucide-react';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { useAppForm, useStore } from '@/lib/form';
import { useCreateGitOpsSource } from '@/components/settings/hooks';
import type { GitOpsSourceWriteRequest } from '@/lib/api/settings';

function GitOpsForm() {
  const router = useRouter();
  const create = useCreateGitOpsSource();

  const form = useAppForm({
    defaultValues: {
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
    } as GitOpsSourceWriteRequest,
    onSubmit: async ({ value }) => {
      const row = await create.mutateAsync(value);
      router.push(`/dashboard/settings/gitops/${row.id}`);
    },
  });
  // Cross-field UI state: auth input disables on mode none, interval input on
  // manual sync, and the decommission warning reads on_delete + path_prefix.
  const authMode = useStore(form.store, (s) => s.values.auth_mode);
  const syncMode = useStore(form.store, (s) => s.values.sync_mode);
  const onDelete = useStore(form.store, (s) => s.values.on_delete);
  const pathPrefix = useStore(form.store, (s) => s.values.path_prefix);

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void form.handleSubmit();
      }}
      className="space-y-5 max-w-2xl"
    >
      <div className="space-y-1">
        <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Name</label>
        <form.Field name="name">
          {(field) => (
            <input
              required
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              className="w-full h-9 px-3 rounded border bg-background text-sm"
              placeholder="platform-clusters"
            />
          )}
        </form.Field>
      </div>
      <div className="space-y-1">
        <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Repo URL</label>
        <form.Field name="repo_url">
          {(field) => (
            <input
              required
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              className="w-full h-9 px-3 rounded border bg-background text-sm font-mono"
              placeholder="https://github.com/example/clusters.git"
            />
          )}
        </form.Field>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Branch</label>
          <form.Field name="branch">
            {(field) => (
              <input
                value={field.state.value ?? ''}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="w-full h-9 px-3 rounded border bg-background text-sm font-mono"
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Path prefix</label>
          <form.Field name="path_prefix">
            {(field) => (
              <input
                value={field.state.value ?? ''}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="w-full h-9 px-3 rounded border bg-background text-sm font-mono"
              />
            )}
          </form.Field>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Auth mode</label>
          <form.Field name="auth_mode">
            {(field) => (
              <select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value as 'none' | 'https_token' | 'ssh_key')}
                onBlur={field.handleBlur}
                className="w-full h-9 px-3 rounded border bg-background text-sm"
              >
                <option value="none">None (public repo)</option>
                <option value="https_token">HTTPS token</option>
                <option value="ssh_key">SSH key</option>
              </select>
            )}
          </form.Field>
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Auth token / key</label>
          <form.Field name="auth">
            {(field) => (
              <input
                type="password"
                value={field.state.value ?? ''}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                disabled={authMode === 'none'}
                className="w-full h-9 px-3 rounded border bg-background text-sm font-mono disabled:opacity-50"
                placeholder={authMode === 'none' ? '(not required)' : 'paste secret'}
              />
            )}
          </form.Field>
        </div>
      </div>
      <div className="grid grid-cols-3 gap-4">
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Sync mode</label>
          <form.Field name="sync_mode">
            {(field) => (
              <select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value as 'manual' | 'interval')}
                onBlur={field.handleBlur}
                className="w-full h-9 px-3 rounded border bg-background text-sm"
              >
                <option value="interval">Interval</option>
                <option value="manual">Manual only</option>
              </select>
            )}
          </form.Field>
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Interval (sec)</label>
          <form.Field name="sync_interval_seconds">
            {(field) => (
              <input
                type="number"
                min={30}
                value={field.state.value ?? 60}
                onChange={(e) => field.handleChange(Number(e.target.value))}
                onBlur={field.handleBlur}
                disabled={syncMode === 'manual'}
                className="w-full h-9 px-3 rounded border bg-background text-sm font-mono disabled:opacity-50"
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1">
          <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">On delete</label>
          <form.Field name="on_delete">
            {(field) => (
              <select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value as 'log' | 'tombstone' | 'decommission')}
                onBlur={field.handleBlur}
                className="w-full h-9 px-3 rounded border bg-background text-sm"
              >
                <option value="log">Log only (safe)</option>
                <option value="tombstone">Tombstone (24h grace)</option>
                <option value="decommission">Immediate decommission</option>
              </select>
            )}
          </form.Field>
        </div>
      </div>
      {onDelete === 'decommission' && !pathPrefix ? (
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

function NewGitOpsSourcePage() {
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

export const Route = createFileRoute('/dashboard/settings/gitops/new/')({
  component: NewGitOpsSourcePage,
});
