import { createFileRoute } from '@tanstack/react-router';
/**
 * Cluster Template tab — the applied cluster-template binding and its
 * reconciliation status. A cluster carries at most one template; reapply
 * pushes the canonical spec back through the reconciler. Detach removes
 * just the binding — tools, projects, labels stay (so the operator can
 * inspect drift without nuking the cluster).
 */

import { lazy, Suspense, useMemo, useState } from 'react';
import { useParams } from '@/lib/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  Loader2,
  RefreshCw,
  Server,
  Unlink,
  XCircle,
} from 'lucide-react';

import { queryKeys, useCluster } from '@/lib/hooks';
import { liveFallback } from '@/lib/live/status-store';
import { useClustersUpdate } from '@/lib/permission-hooks';
import {
  bindClusterTemplate,
  detachClusterTemplate,
  getClusterTemplateBinding,
  reapplyClusterTemplate,
  type ClusterTemplateStatus,
} from '@/lib/api/cluster-detail';
import { listClusterTemplates } from '@/lib/api/project-detail';
import { cn } from '@/lib/utils';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';

// Monaco stays a lazy chunk (second of the 2 monaco sites; the first is
// components/ui/yaml-editor.tsx) so the editor bundle loads only when the
// applied-spec panel is opened.
const MonacoEditor = lazy(() => import('@monaco-editor/react'));

function EditorLoading() {
  return (
    <div className="flex items-center justify-center h-full bg-[#1e1e1e]">
      <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
    </div>
  );
}

function fmt(iso?: string) {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function TemplateStatusBadge({ status }: { status: ClusterTemplateStatus }) {
  const cfg = (() => {
    switch (status) {
      case 'applied':
        return {
          tone: 'bg-status-success/10 text-status-success border-status-success/20',
          Icon: CheckCircle2,
          label: 'Applied',
        };
      case 'pending':
        return {
          tone: 'bg-status-info/10 text-status-info border-status-info/20',
          Icon: Loader2,
          label: 'Pending',
          spin: true,
        };
      case 'applying':
        return {
          tone: 'bg-status-info/10 text-status-info border-status-info/20',
          Icon: Loader2,
          label: 'Applying',
          spin: true,
        };
      case 'failed':
        return {
          tone: 'bg-status-error/10 text-status-error border-status-error/20',
          Icon: XCircle,
          label: 'Failed',
        };
      default:
        return {
          tone: 'bg-muted text-muted-foreground border-border',
          Icon: AlertTriangle,
          label: status || 'Unknown',
        };
    }
  })();
  const { tone, Icon, label, spin } = cfg as { tone: string; Icon: typeof CheckCircle2; label: string; spin?: boolean };
  return (
    <span className={cn('inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs border font-medium', tone)}>
      <Icon className={cn('h-3 w-3', spin && 'animate-spin')} />
      {label}
    </span>
  );
}

function ClusterTemplatePage() {
  const params = useParams();
  const clusterId = params.id as string;
  const queryClient = useQueryClient();
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);

  const { data: templatesPage, isLoading: tplsLoading } = useQuery({
    queryKey: queryKeys.clusterPages.templates,
    queryFn: () => listClusterTemplates({ pageSize: 200 }),
    staleTime: 60_000,
  });
  const templates = templatesPage?.data;

  const { data: binding, isLoading: bindingLoading } = useQuery({
    queryKey: queryKeys.clusterPages.templateBinding(clusterId),
    queryFn: () => getClusterTemplateBinding(clusterId),
    enabled: !!clusterId,
    // While-pending wrap (P4.5): `template_binding.changed` drives freshness
    // when the stream is open; the 5s poll only runs for in-flight applies
    // during a stream drop, and stops entirely once settled.
    refetchInterval: (q) => {
      const status = q.state.data?.status;
      return status === 'pending' || status === 'applying' ? liveFallback(5000)() : false;
    },
    refetchIntervalInBackground: false,
  });

  const [selectedTemplateId, setSelectedTemplateId] = useState<string>('');
  const [specOpen, setSpecOpen] = useState(true);
  const [confirmReapply, setConfirmReapply] = useState(false);
  const [confirmDetach, setConfirmDetach] = useState(false);

  const bindMutation = useMutation({
    mutationFn: (templateId: string) => bindClusterTemplate(clusterId, { template_id: templateId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.templateBinding(clusterId) });
      toastSuccess('Template applied');
    },
    onError: (e: Error) => toastApiError('Apply failed', e),
  });
  const reapplyMutation = useMutation({
    mutationFn: () => reapplyClusterTemplate(clusterId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.templateBinding(clusterId) });
      toastSuccess('Reapply queued');
      setConfirmReapply(false);
    },
    onError: (e: Error) => toastApiError('Reapply failed', e),
  });
  const detachMutation = useMutation({
    mutationFn: () => detachClusterTemplate(clusterId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.templateBinding(clusterId) });
      toastSuccess('Template detached');
      setConfirmDetach(false);
    },
    onError: (e: Error) => toastApiError('Detach failed', e),
  });

  const specJson = useMemo(() => {
    if (!binding?.spec) return '';
    try {
      return JSON.stringify(binding.spec, null, 2);
    } catch {
      return String(binding.spec);
    }
  }, [binding?.spec]);

  if (clusterLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Template</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Cluster template applied to {cluster.displayName}.
          </p>
        </div>
      </div>

      {bindingLoading ? (
        <div className="rounded-lg border border-border bg-card p-12 flex items-center justify-center">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : !binding ? (
        /* Empty state */
        <div className="rounded-lg border border-border bg-card p-12 flex flex-col items-center justify-center text-muted-foreground">
          <ClipboardList className="h-10 w-10 mb-3" />
          <p className="text-sm font-medium text-foreground">No template applied</p>
          <p className="text-xs mt-1 max-w-md text-center">
            Apply a cluster template to install a curated set of tools, policies, and labels.
          </p>
          <div className="mt-4 flex items-center gap-2">
            <select
              value={selectedTemplateId}
              onChange={(e) => setSelectedTemplateId(e.target.value)}
              disabled={tplsLoading || !canWrite}
              className="h-8 px-2 rounded-md border border-border bg-background text-xs
                focus:outline-none focus:ring-1 focus:ring-ring
                disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <option value="">Select a template…</option>
              {(templates || []).map((t) => (
                <option key={t.id} value={t.id}>
                  {t.displayName}
                </option>
              ))}
            </select>
            <button
              onClick={() => selectedTemplateId && bindMutation.mutate(selectedTemplateId)}
              disabled={!selectedTemplateId || bindMutation.isPending || !canWrite}
              title={canWrite ? undefined : reason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
                disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {bindMutation.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
              Apply Template
            </button>
          </div>
        </div>
      ) : (
        <>
          {/* Applied template card */}
          <div className="rounded-lg border border-border bg-card p-5">
            <div className="flex items-start justify-between gap-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <h2 className="text-lg font-semibold text-foreground truncate">
                    {binding.templateDisplayName || binding.templateName}
                  </h2>
                  <TemplateStatusBadge status={binding.status} />
                </div>
                <div className="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1 text-xs">
                  <div className="flex gap-2">
                    <span className="text-muted-foreground">Template:</span>
                    <span className="font-mono text-foreground">{binding.templateName}</span>
                  </div>
                  <div className="flex gap-2">
                    <span className="text-muted-foreground">Applied at:</span>
                    <span className="text-foreground">{fmt(binding.appliedAt)}</span>
                  </div>
                </div>
                {binding.lastError && (
                  <div className="mt-3 rounded-md border border-status-error/30 bg-status-error/10 p-2.5 flex items-start gap-2">
                    <AlertTriangle className="h-3.5 w-3.5 text-status-error flex-shrink-0 mt-0.5" />
                    <pre className="text-xs text-status-error whitespace-pre-wrap break-words font-mono">
                      {binding.lastError}
                    </pre>
                  </div>
                )}
              </div>
              <div className="flex items-center gap-2 flex-shrink-0">
                <button
                  onClick={() => canWrite && setConfirmReapply(true)}
                  disabled={!canWrite || binding.status === 'applying' || binding.status === 'pending'}
                  title={canWrite ? undefined : reason}
                  className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                    border border-border text-foreground hover:bg-accent transition-colors
                    disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <RefreshCw className="h-3.5 w-3.5" />
                  Reapply
                </button>
                <button
                  onClick={() => canWrite && setConfirmDetach(true)}
                  disabled={!canWrite}
                  title={canWrite ? undefined : reason}
                  className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                    border border-border text-foreground hover:text-status-error hover:border-status-error/40 transition-colors
                    disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <Unlink className="h-3.5 w-3.5" />
                  Detach
                </button>
              </div>
            </div>
          </div>

          {/* Spec snapshot */}
          <div className="rounded-lg border border-border bg-card overflow-hidden">
            <button
              onClick={() => setSpecOpen((v) => !v)}
              className="w-full flex items-center justify-between px-5 py-3 hover:bg-accent/40 transition-colors"
            >
              <div className="flex items-center gap-2">
                {specOpen ? (
                  <ChevronDown className="h-4 w-4 text-muted-foreground" />
                ) : (
                  <ChevronRight className="h-4 w-4 text-muted-foreground" />
                )}
                <span className="text-sm font-medium text-foreground">Applied spec</span>
                <span className="text-xs text-muted-foreground">(read-only snapshot)</span>
              </div>
            </button>
            {specOpen && (
              <div className="border-t border-border">
                {specJson ? (
                  <div style={{ height: 360 }}>
                    <Suspense fallback={<EditorLoading />}>
                      <MonacoEditor
                        height="100%"
                        defaultLanguage="json"
                        value={specJson}
                        theme="vs-dark"
                        options={{
                          readOnly: true,
                          minimap: { enabled: false },
                          fontSize: 12,
                          lineNumbers: 'on',
                          scrollBeyondLastLine: false,
                          wordWrap: 'on',
                        }}
                      />
                    </Suspense>
                  </div>
                ) : (
                  <pre className="p-4 text-xs text-muted-foreground">(no spec recorded)</pre>
                )}
              </div>
            )}
          </div>
        </>
      )}

      <ConfirmDialog
        open={confirmReapply}
        onClose={() => setConfirmReapply(false)}
        onConfirm={() => reapplyMutation.mutate()}
        title="Reapply template?"
        description={`This pushes the canonical template spec back through the reconciler. Any drift on managed tools, labels, or policies will be reverted.`}
        confirmText="Reapply"
        loading={reapplyMutation.isPending}
      />

      <ConfirmDialog
        open={confirmDetach}
        onClose={() => setConfirmDetach(false)}
        onConfirm={() => detachMutation.mutate()}
        title="Detach template?"
        description={`Removes the template binding from this cluster. Tools, projects, and labels stay in place — only the binding is removed. You can re-apply later.`}
        confirmText="Detach"
        variant="destructive"
        loading={detachMutation.isPending}
      />
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/template/')({
  component: ClusterTemplatePage,
});
