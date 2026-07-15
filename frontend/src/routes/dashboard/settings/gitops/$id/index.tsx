import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * /dashboard/settings/gitops/[id] — single-source detail.
 *
 * Shows the source config, the managed-clusters table, and exposes
 * "Sync now" + "Dry-run preview" + "Save changes" actions.
 */
import { useEffect, useMemo, useState } from 'react';
import { useParams } from '@/lib/navigation';
import { Link } from '@/lib/link';
import { useAppForm, useStore } from '@/lib/form';
import { ArrowLeft, GitBranch, Loader2, Play, RefreshCw } from 'lucide-react';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { StatusBadge } from '@/components/ui/status-badge';
import {
  useGitOpsSource,
  useGitOpsSourceClusters,
  usePreviewGitOpsSource,
  useSyncGitOpsSource,
  useUpdateGitOpsSource,
} from '@/components/settings/hooks';
import type {
  GitOpsPreviewResult,
  GitOpsSourceWriteRequest,
} from '@/lib/api/settings';
import { GITOPS_AUTH_SENTINEL } from '@/lib/api/settings';
import { formatRelativeTime } from '@/lib/utils';

// Snapshot of the source row in write-request shape — the form's baseline;
// the auth column round-trips as the sentinel when a blob is stored.
function toWriteRequest(source: {
  name: string;
  repo_url: string;
  branch?: string;
  path_prefix?: string;
  auth_mode: GitOpsSourceWriteRequest['auth_mode'];
  auth_configured?: boolean;
  sync_mode: GitOpsSourceWriteRequest['sync_mode'];
  sync_interval_seconds?: number;
  on_delete: GitOpsSourceWriteRequest['on_delete'];
  enabled?: boolean;
}): GitOpsSourceWriteRequest {
  return {
    name: source.name,
    repo_url: source.repo_url,
    branch: source.branch,
    path_prefix: source.path_prefix,
    auth_mode: source.auth_mode,
    auth: source.auth_configured ? GITOPS_AUTH_SENTINEL : '',
    sync_mode: source.sync_mode,
    sync_interval_seconds: source.sync_interval_seconds,
    on_delete: source.on_delete,
    enabled: source.enabled,
  };
}

function DetailInner({ id }: { id: string }) {
  const { data: source, isLoading } = useGitOpsSource(id);
  const { data: clusters } = useGitOpsSourceClusters(id);
  const update = useUpdateGitOpsSource();
  const sync = useSyncGitOpsSource();
  const preview = usePreviewGitOpsSource();
  const [previewResult, setPreviewResult] = useState<GitOpsPreviewResult | null>(
    null,
  );

  const initial = useMemo(() => (source ? toWriteRequest(source) : null), [source]);

  const form = useAppForm({
    defaultValues: (initial ?? toWriteRequest({
      name: '',
      repo_url: '',
      auth_mode: 'none',
      sync_mode: 'interval',
      on_delete: 'log',
    })) as GitOpsSourceWriteRequest,
    onSubmit: ({ value }) => {
      if (!source || !initial) return;
      // Old behavior: the PUT body carries only the keys the operator
      // actually changed (a Partial), never the whole snapshot.
      const body: Partial<GitOpsSourceWriteRequest> = {};
      for (const k of Object.keys(value) as (keyof GitOpsSourceWriteRequest)[]) {
        if (value[k] !== initial[k]) {
          (body as Record<string, unknown>)[k] = value[k];
        }
      }
      if (Object.keys(body).length === 0) return;
      update.mutate({ id: source.id, body });
    },
  });
  const authMode = useStore(form.store, (s) => s.values.auth_mode);
  const syncMode = useStore(form.store, (s) => s.values.sync_mode);

  // Rebase the form whenever the source snapshot (re)loads.
  useEffect(() => {
    if (initial) form.reset(initial);
  }, [form, initial]);

  if (isLoading || !source) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <GitBranch className="h-5 w-5 text-muted-foreground" />
          <div>
            <h2 className="text-lg font-semibold tracking-tight">
              {source.name}
            </h2>
            <p className="text-xs font-mono text-muted-foreground">
              {source.repo_url} · {source.branch}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={async () => {
              const r = await preview.mutateAsync(source.id);
              setPreviewResult(r);
            }}
            disabled={preview.isPending}
            className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border text-sm hover:bg-muted disabled:opacity-60"
          >
            {preview.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Play className="h-4 w-4" />
            )}
            Dry-run preview
          </button>
          <button
            type="button"
            onClick={() => sync.mutate(source.id)}
            disabled={sync.isPending}
            className="inline-flex items-center gap-2 h-9 px-3 rounded-lg bg-primary text-primary-foreground text-sm hover:opacity-90 disabled:opacity-60"
          >
            {sync.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RefreshCw className="h-4 w-4" />
            )}
            Sync now
          </button>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-3 text-xs">
        <div>
          <p className="text-muted-foreground uppercase tracking-wide">Mode</p>
          <p className="font-mono mt-0.5">
            {source.sync_mode}
            {source.sync_mode === 'interval'
              ? ` · ${source.sync_interval_seconds}s`
              : ''}
          </p>
        </div>
        <div>
          <p className="text-muted-foreground uppercase tracking-wide">On delete</p>
          <p className="font-mono mt-0.5">{source.on_delete}</p>
        </div>
        <div>
          <p className="text-muted-foreground uppercase tracking-wide">Last sync</p>
          <p className="font-mono mt-0.5">
            {source.last_synced_at
              ? formatRelativeTime(source.last_synced_at)
              : 'never'}
          </p>
        </div>
      </div>

      {source.last_error ? (
        <div className="rounded border border-status-error/40 bg-status-error/5 p-3 text-xs">
          <p className="font-semibold text-status-error mb-1">Last sync error</p>
          <pre className="whitespace-pre-wrap font-mono text-status-error/80">
            {source.last_error}
          </pre>
        </div>
      ) : null}

      <form
        onSubmit={(e) => {
          e.preventDefault();
          void form.handleSubmit();
        }}
        className="space-y-5 max-w-2xl"
      >
        <h3 className="text-sm font-semibold text-foreground">Configuration</h3>
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
                  <option value="none">None</option>
                  <option value="https_token">HTTPS token</option>
                  <option value="ssh_key">SSH key</option>
                </select>
              )}
            </form.Field>
          </div>
          <div className="space-y-1">
            <label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Auth blob</label>
            <form.Field name="auth">
              {(field) => (
                <input
                  type="password"
                  value={field.state.value ?? ''}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  disabled={authMode === 'none'}
                  className="w-full h-9 px-3 rounded border bg-background text-sm font-mono disabled:opacity-50"
                  placeholder={authMode === 'none' ? '(not required)' : ''}
                />
              )}
            </form.Field>
            <p className="text-2xs text-muted-foreground">
              The sentinel <code>{GITOPS_AUTH_SENTINEL}</code> means "keep
              existing". Replace it with a new value to rotate.
            </p>
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
                  <option value="log">Log only</option>
                  <option value="tombstone">Tombstone</option>
                  <option value="decommission">Decommission</option>
                </select>
              )}
            </form.Field>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <form.Field name="enabled">
            {(field) => (
              <input
                id="enabled"
                type="checkbox"
                checked={field.state.value ?? true}
                onChange={(e) => field.handleChange(e.target.checked)}
                onBlur={field.handleBlur}
              />
            )}
          </form.Field>
          <label htmlFor="enabled" className="text-sm text-foreground">Enabled</label>
        </div>
        <div className="flex justify-end pt-2">
          <form.Subscribe
            selector={(s) => initial != null && JSON.stringify(s.values) !== JSON.stringify(initial)}
          >
            {(dirty) => (
              <button
                type="submit"
                disabled={!dirty || update.isPending}
                className="inline-flex items-center h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-60"
              >
                {update.isPending ? 'Saving…' : 'Save changes'}
              </button>
            )}
          </form.Subscribe>
        </div>
      </form>

      <div className="space-y-2">
        <h3 className="text-sm font-semibold text-foreground">Managed clusters</h3>
        {(!clusters || clusters.length === 0) ? (
          <p className="text-sm text-muted-foreground">
            No clusters tracked by this source yet.
          </p>
        ) : (
          <Table className="w-full text-sm">
            <TableHeader className="text-xs text-muted-foreground uppercase">
              <TableRow>
                <TableHead className="text-left py-2">Cluster</TableHead>
                <TableHead className="text-left py-2">Path</TableHead>
                <TableHead className="text-left py-2">Last applied</TableHead>
                <TableHead className="text-left py-2">Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {clusters.map((c) => (
                <TableRow key={c.cluster_id} className="border-t">
                  <TableCell className="py-2 font-mono">{c.cluster_name ?? c.cluster_id}</TableCell>
                  <TableCell className="py-2 font-mono text-xs text-muted-foreground">{c.repo_path}</TableCell>
                  <TableCell className="py-2 text-xs text-muted-foreground">
                    {formatRelativeTime(c.last_applied_at)}
                  </TableCell>
                  <TableCell className="py-2">
                    <StatusBadge
                      status={c.status === 'active' ? 'active' : 'warning'}
                      label={c.status}
                      size="sm"
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      {previewResult ? (
        <div className="rounded border bg-muted/30 p-4 text-xs space-y-2">
          <h3 className="text-sm font-semibold">Dry-run preview</h3>
          <p className="font-mono text-muted-foreground">
            HEAD {previewResult.head_sha.slice(0, 12)} ·{' '}
            {previewResult.applies.length} would-apply ·{' '}
            {previewResult.would_miss.length} would-miss ·{' '}
            {previewResult.would_restore.length} would-restore
          </p>
          <pre className="whitespace-pre-wrap font-mono text-2xs">
            {JSON.stringify(previewResult, null, 2)}
          </pre>
        </div>
      ) : null}
    </div>
  );
}

function GitOpsSourceDetailPage() {
  const { id } = useParams<{ id: string }>();
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
        <DetailInner id={id} />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/gitops/$id/')({
  component: GitOpsSourceDetailPage,
});
