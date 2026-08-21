import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Cluster detail → Network policies tab. Lists every applied
 * NetworkPolicy template for this cluster, grouped by namespace. The
 * "Apply template" action picks a template + one or more namespaces and
 * fires the per-namespace POST. Migration 068.
 */

import { useEffect, useState } from 'react';
import { useParams } from '@/lib/navigation';
import { Link } from '@/lib/link';
import { ArrowLeft, Plus, Trash2, RefreshCw, Loader2 } from 'lucide-react';
import { toastApiError, toastError, toastSuccess } from '@/lib/toast';

import {
  listNetworkPolicyApplications,
  listNetworkPolicyTemplates,
  applyNetworkPolicy,
  deleteNetworkPolicyApplication,
  reapplyNetworkPolicyApplication,
  type NetworkPolicyApplication,
  type NetworkPolicyTemplate,
} from '@/lib/api/settings';

function StatusPill({ status }: { status: NetworkPolicyApplication['status'] }) {
  const palette: Record<string, string> = {
    pending: 'bg-muted text-muted-foreground border-border',
    applied: 'bg-status-success/10 text-status-success border-status-success/30',
    failed: 'bg-status-error/10 text-status-error border-status-error/30',
    drifting: 'bg-status-warning/10 text-status-warning border-status-warning/30',
  };
  return (
    <span className={`text-xs px-2 py-0.5 rounded border font-medium capitalize ${palette[status]}`}>
      {status}
    </span>
  );
}

function ClusterNetworkPoliciesPage() {
  const params = useParams<{ id: string }>();
  const clusterID = params?.id ?? '';

  const [apps, setApps] = useState<NetworkPolicyApplication[]>([]);
  const [templates, setTemplates] = useState<NetworkPolicyTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [openApply, setOpenApply] = useState(false);
  const [pickedTemplate, setPickedTemplate] = useState('');
  const [namespaces, setNamespaces] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const refresh = async () => {
    if (!clusterID) return;
    setLoading(true);
    try {
      const [a, t] = await Promise.all([
        listNetworkPolicyApplications(clusterID),
        listNetworkPolicyTemplates(),
      ]);
      setApps(a);
      setTemplates(t);
    } catch (err: unknown) {
      toastApiError('Load failed', err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterID]);

  const handleApply = async () => {
    if (!pickedTemplate) {
      toastError('Pick a template first');
      return;
    }
    const list = namespaces
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (list.length === 0) {
      toastError('Enter at least one namespace');
      return;
    }
    setSubmitting(true);
    try {
      await applyNetworkPolicy(clusterID, { template_id: pickedTemplate, namespaces: list });
      toastSuccess(`Applied to ${list.length} namespace(s)`);
      setOpenApply(false);
      setPickedTemplate('');
      setNamespaces('');
      await refresh();
    } catch (err: unknown) {
      toastApiError('Apply failed', err);
    } finally {
      setSubmitting(false);
    }
  };

  const handleRevoke = async (app: NetworkPolicyApplication) => {
    if (!confirm(`Revoke ${app.template_slug} from ${app.namespace}?`)) return;
    try {
      await deleteNetworkPolicyApplication(clusterID, app.id);
      toastSuccess('Application revoked');
      await refresh();
    } catch (err: unknown) {
      toastApiError('Revoke failed', err);
    }
  };

  const handleReapply = async (app: NetworkPolicyApplication) => {
    try {
      await reapplyNetworkPolicyApplication(clusterID, app.id);
      toastSuccess('Reapply queued');
      await refresh();
    } catch (err: unknown) {
      toastApiError('Reapply failed', err);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <Link
          href={`/dashboard/clusters/${clusterID}`}
          className="inline-flex items-center text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4 mr-1" /> Back to cluster
        </Link>
        <button
          type="button"
          onClick={() => setOpenApply((v) => !v)}
          className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded border border-border bg-card hover:bg-muted"
        >
          <Plus className="h-4 w-4" /> Apply template
        </button>
      </div>

      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Network policies</h1>
        <p className="text-sm text-muted-foreground mt-1 max-w-3xl">
          NetworkPolicy templates applied to namespaces in this cluster. The reconciler keeps
          each application server-side-applied; drifting rows are re-stamped on the next 5m tick.
        </p>
      </div>

      {openApply && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-3">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <label className="text-sm space-y-1 block">
              <span className="text-muted-foreground">Template</span>
              <select
                className="w-full px-2 py-1 rounded border border-border bg-background text-sm"
                value={pickedTemplate}
                onChange={(e) => setPickedTemplate(e.target.value)}
              >
                <option value="">Pick one...</option>
                {templates
                  .filter((t) => t.enabled)
                  .map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name} ({t.slug})
                    </option>
                  ))}
              </select>
            </label>
            <label className="text-sm space-y-1 block">
              <span className="text-muted-foreground">Namespaces</span>
              <input
                type="text"
                className="w-full px-2 py-1 rounded border border-border bg-background text-sm font-mono"
                placeholder="team-a, team-b"
                value={namespaces}
                onChange={(e) => setNamespaces(e.target.value)}
              />
              <span className="text-xs text-muted-foreground">Comma- or space-separated</span>
            </label>
          </div>
          <button
            type="button"
            onClick={handleApply}
            disabled={submitting}
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded border border-border bg-foreground text-background hover:opacity-90 disabled:opacity-50"
          >
            {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
            Apply
          </button>
        </div>
      )}

      {loading ? (
        <div className="flex items-center text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 mr-2 animate-spin" /> Loading...
        </div>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-border">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted">
              <TableRow className="text-left">
                <TableHead className="px-3 py-2 font-medium">Template</TableHead>
                <TableHead className="px-3 py-2 font-medium">Namespace</TableHead>
                <TableHead className="px-3 py-2 font-medium">Policy name</TableHead>
                <TableHead className="px-3 py-2 font-medium">Status</TableHead>
                <TableHead className="px-3 py-2 font-medium">Last applied</TableHead>
                <TableHead className="px-3 py-2 text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {apps.map((a) => (
                <TableRow key={a.id} className="border-b border-border last:border-0">
                  <TableCell className="px-3 py-2 font-mono text-xs">{a.template_slug ?? a.template_id}</TableCell>
                  <TableCell className="px-3 py-2 font-mono text-xs">{a.namespace}</TableCell>
                  <TableCell className="px-3 py-2 font-mono text-xs">{a.policy_name}</TableCell>
                  <TableCell className="px-3 py-2">
                    <StatusPill status={a.status} />
                    {a.last_error && (
                      <div className="text-xs text-status-error mt-0.5">{a.last_error}</div>
                    )}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-xs text-muted-foreground">{a.last_applied_at ?? '—'}</TableCell>
                  <TableCell className="px-3 py-2 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        type="button"
                        onClick={() => handleReapply(a)}
                        className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded border border-border hover:bg-muted"
                      >
                        <RefreshCw className="h-3 w-3" /> Reapply
                      </button>
                      <button
                        type="button"
                        onClick={() => handleRevoke(a)}
                        className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded border border-status-error/30 text-status-error hover:bg-status-error/10"
                      >
                        <Trash2 className="h-3 w-3" />
                      </button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {apps.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="px-3 py-6 text-center text-sm text-muted-foreground">
                    <div className="space-y-3">
                      <p>No network policies applied yet.</p>
                      <p className="text-xs">
                        Pick a curated template from the{' '}
                        <a href="/dashboard/settings/platform" className="underline">
                          platform network-policy templates
                        </a>{' '}
                        and click <em>Apply template</em> to roll one out.
                      </p>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/network-policies/')({
  component: ClusterNetworkPoliciesPage,
});
