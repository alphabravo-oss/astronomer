'use client';

import { useEffect, useRef, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import {
  useCluster,
  useClusterConditions,
  useClusterMetricsSummary,
  useClusterEvents,
  useGenerateKubeconfig,
  useDeleteCluster,
  useUpdateCluster,
  queryKeys,
} from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { MetricCard } from '@/components/ui/metric-card';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionMenu } from '@/components/ui/action-menu';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { RegisterClusterModal } from '@/components/clusters/register-cluster-modal';
import { EditClusterModal } from '@/components/clusters/edit-cluster-modal';
import {
  formatBytes,
  formatCPU,
  formatPercentage,
  formatRelativeTime,
  providerDisplayName,
  distributionDisplayName,
} from '@/lib/utils';
import {
  Cpu,
  MemoryStick,
  Box,
  Server,
  Activity,
  AlertTriangle,
  Loader2,
  Download,
  Terminal,
  Pencil,
  Trash2,
  ChevronDown,
  CheckCircle2,
  XCircle,
  CircleHelp,
} from 'lucide-react';
import type { ClusterCondition } from '@/types';

export default function ClusterDetailPage() {
  const params = useParams();
  const router = useRouter();
  const clusterId = params.id as string;

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const { data: conditions } = useClusterConditions(clusterId);
  const { data: metricsSummary } = useClusterMetricsSummary(clusterId);
  const { data: events } = useClusterEvents(clusterId, { limit: 10 });
  const generateKubeconfig = useGenerateKubeconfig();
  const deleteMutation = useDeleteCluster();
  const updateMutation = useUpdateCluster();

  // Refresh detail + metrics-summary + events lists when any cluster-level
  // event arrives. cluster.metrics is intentionally omitted — the layout
  // merger patches the percentages in place to avoid a refetch storm.
  useLiveQueryInvalidation(
    [
      'cluster.connected',
      'cluster.disconnected',
      'cluster.heartbeat',
      'cluster.status_changed',
      'cluster.updated',
      'cluster.deleted',
      'cluster.k8s_changed',
      'agent.reconnecting',
      'agent.failed',
    ],
    [
      queryKeys.clusters.detail(clusterId),
      queryKeys.clusters.metricsSummary(clusterId),
      queryKeys.clusters.events(clusterId),
      // cluster_conditions uses its own key shape (['clusters', id,
      // 'conditions']) so a heartbeat invalidation refreshes the pills
      // without waiting for the 60s poll interval.
      ['clusters', clusterId, 'conditions'],
    ],
  );

  // Action menu state
  const [showRegister, setShowRegister] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [showDelete, setShowDelete] = useState(false);

  // Kubeconfig download menu + direct-access confirmation
  const [kubeconfigMenuOpen, setKubeconfigMenuOpen] = useState(false);
  const [confirmDirectAccess, setConfirmDirectAccess] = useState(false);
  const kubeconfigMenuRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    function onDocClick(e: MouseEvent) {
      if (kubeconfigMenuRef.current && !kubeconfigMenuRef.current.contains(e.target as Node)) {
        setKubeconfigMenuOpen(false);
      }
    }
    document.addEventListener('mousedown', onDocClick);
    return () => document.removeEventListener('mousedown', onDocClick);
  }, []);

  const downloadKubeconfigFile = async () => {
    try {
      const blob = await generateKubeconfig.mutateAsync(clusterId);
      const url = window.URL.createObjectURL(new Blob([blob]));
      const link = document.createElement('a');
      link.href = url;
      link.setAttribute('download', `${cluster?.name || 'cluster'}-kubeconfig.yaml`);
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
    } catch {
      // Error handled by mutation
    }
  };

  const handleDownloadKubeconfig = downloadKubeconfigFile;

  const handleDirectAccessDownload = async () => {
    setKubeconfigMenuOpen(false);
    if (cluster?.directAccessEnabled) {
      await downloadKubeconfigFile();
    } else {
      setConfirmDirectAccess(true);
    }
  };

  const confirmEnableAndDownload = async () => {
    try {
      await updateMutation.mutateAsync({
        id: clusterId,
        data: { directAccessEnabled: true },
      });
      await downloadKubeconfigFile();
    } finally {
      setConfirmDirectAccess(false);
    }
  };

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(clusterId);
      setShowDelete(false);
      router.push('/dashboard/clusters');
    } catch {
      // Error handled by mutation
    }
  };

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
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-semibold text-foreground tracking-tight">
              {cluster.displayName}
            </h1>
            <StatusBadge status={cluster.status} size="lg" />
          </div>
          <div className="flex items-center gap-4 text-sm text-muted-foreground">
            <span>{distributionDisplayName(cluster.distribution)}</span>
            <span className="text-border">|</span>
            <span>v{cluster.kubernetesVersion}</span>
            <span className="text-border">|</span>
            <span className="capitalize">{cluster.environment}</span>
          </div>
          {conditions && conditions.length > 0 && (
            <ClusterConditionsBar conditions={conditions} />
          )}
        </div>
        <div className="flex items-center gap-2">
          <div ref={kubeconfigMenuRef} className="relative inline-flex">
            <button
              onClick={handleDownloadKubeconfig}
              disabled={generateKubeconfig.isPending}
              className="inline-flex items-center gap-2 h-9 pl-4 pr-3 rounded-l-lg border border-border
                text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent
                transition-colors disabled:opacity-50"
              title="Download kubeconfig (routed through Astronomer)"
            >
              {generateKubeconfig.isPending ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Download className="h-4 w-4" />
              )}
              Kubeconfig
            </button>
            <button
              onClick={() => setKubeconfigMenuOpen((v) => !v)}
              disabled={generateKubeconfig.isPending || updateMutation.isPending}
              aria-label="More kubeconfig options"
              className="inline-flex items-center justify-center h-9 w-8 rounded-r-lg border border-border border-l-0
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            >
              <ChevronDown className="h-4 w-4" />
            </button>
            {kubeconfigMenuOpen && (
              <div
                className="absolute right-0 top-full mt-1 w-72 rounded-md border border-border bg-popover p-1 shadow-lg z-50"
                onClick={(e) => e.stopPropagation()}
              >
                <button
                  onClick={handleDirectAccessDownload}
                  className="w-full flex items-start gap-2 px-2.5 py-2 rounded text-xs text-left text-popover-foreground hover:bg-accent"
                >
                  <AlertTriangle className="h-3.5 w-3.5 text-amber-500 shrink-0 mt-0.5" />
                  <span className="flex-1">
                    <span className="block font-medium">
                      Download with direct access
                      {cluster.directAccessEnabled ? null : <span className="text-muted-foreground"> (enable)</span>}
                    </span>
                    <span className="block text-muted-foreground mt-0.5 leading-snug">
                      Adds a <code className="font-mono">{cluster.name}-direct</code> context that hits the cluster API directly. Break-glass only — not audited.
                    </span>
                  </span>
                </button>
              </div>
            )}
          </div>
          <ActionMenu
            items={[
              {
                label: 'Registration Command',
                icon: <Terminal className="h-3.5 w-3.5" />,
                onClick: () => setShowRegister(true),
              },
              {
                label: 'Edit',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setShowEdit(true),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setShowDelete(true),
                variant: 'destructive',
                separator: true,
              },
            ]}
          />
        </div>
      </div>

      {/* Health Components */}
      {cluster.health?.components && cluster.health.components.length > 0 && (
        <div>
          <h3 className="text-sm font-medium text-muted-foreground mb-3">Health Components</h3>
          <div className="flex flex-wrap gap-2">
            {cluster.health.components.map((comp) => (
              <div
                key={comp.name}
                className="inline-flex items-center gap-2 px-3 py-1.5 rounded-md border border-border bg-card"
              >
                <StatusBadge status={comp.status} size="sm" />
                <span className="text-sm text-foreground">{comp.name}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Metrics Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <MetricCard
          title="CPU Usage"
          value={formatPercentage(metricsSummary?.cpuPercentage ?? cluster.cpuPercentage ?? 0)}
          percentage={metricsSummary?.cpuPercentage ?? cluster.cpuPercentage ?? 0}
          subtitle={`${formatCPU(metricsSummary?.cpuUsage ?? cluster.cpuUsage ?? 0)} / ${formatCPU(metricsSummary?.cpuCapacity ?? cluster.cpuCapacity ?? 0)}`}
          icon={<Cpu className="h-4 w-4" />}
        />
        <MetricCard
          title="Memory Usage"
          value={formatPercentage(metricsSummary?.memoryPercentage ?? cluster.memoryPercentage ?? 0)}
          percentage={metricsSummary?.memoryPercentage ?? cluster.memoryPercentage ?? 0}
          subtitle={`${formatBytes(metricsSummary?.memoryUsage ?? cluster.memoryUsage ?? 0)} / ${formatBytes(metricsSummary?.memoryCapacity ?? cluster.memoryCapacity ?? 0)}`}
          icon={<MemoryStick className="h-4 w-4" />}
        />
        <MetricCard
          title="Nodes"
          value={metricsSummary?.nodeCount ?? cluster.nodeCount ?? 0}
          icon={<Server className="h-4 w-4" />}
        />
        <MetricCard
          title="Pods"
          value={metricsSummary?.podCount ?? cluster.podCount ?? 0}
          subtitle={metricsSummary ? `of ${metricsSummary.podCapacity} capacity` : undefined}
          icon={<Box className="h-4 w-4" />}
        />
      </div>

      {/* Recent Events */}
      <div>
        <h3 className="text-sm font-medium text-muted-foreground mb-3">Recent Events</h3>
        <div className="rounded-lg border border-border overflow-hidden">
          {events && events.length > 0 ? (
            <div className="divide-y divide-border">
              {events.slice(0, 8).map((event) => (
                <div key={event.id} className="flex items-center gap-3 px-4 py-2.5">
                  {event.type === 'Warning' ? (
                    <AlertTriangle className="h-3.5 w-3.5 text-status-warning flex-shrink-0" />
                  ) : (
                    <Activity className="h-3.5 w-3.5 text-status-info flex-shrink-0" />
                  )}
                  <span className="text-sm text-foreground flex-1 truncate">{event.message}</span>
                  <span className="text-xs text-muted-foreground flex-shrink-0">
                    {formatRelativeTime(event.lastTimestamp)}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
              No recent events
            </div>
          )}
        </div>
      </div>

      {/* Registration Command Modal */}
      {showRegister && (
        <RegisterClusterModal
          onClose={() => setShowRegister(false)}
          clusterId={cluster.id}
          clusterName={cluster.name}
        />
      )}

      {/* Edit Modal */}
      {showEdit && (
        <EditClusterModal
          cluster={cluster}
          onClose={() => setShowEdit(false)}
        />
      )}

      {/* Delete Confirmation */}
      <ConfirmDialog
        open={showDelete}
        onClose={() => setShowDelete(false)}
        onConfirm={handleDelete}
        title="Delete Cluster"
        description={`This will remove the cluster "${cluster.displayName}" from Astronomer. The underlying Kubernetes cluster will not be destroyed.`}
        confirmText="Delete"
        confirmValue={cluster.name}
        variant="destructive"
        loading={deleteMutation.isPending}
      />

      {/* Direct-access enable + download confirmation */}
      <ConfirmDialog
        open={confirmDirectAccess}
        onClose={() => setConfirmDirectAccess(false)}
        onConfirm={confirmEnableAndDownload}
        title="Enable direct cluster access?"
        description={`Turns on direct access for "${cluster.displayName}" and downloads a kubeconfig with both the proxy context and a ${cluster.name}-direct context that hits the cluster API directly. Requests via the direct context are NOT audited. Revocation requires rotating the ServiceAccount on the cluster.`}
        confirmText="Enable & Download"
        loading={updateMutation.isPending || generateKubeconfig.isPending}
      />
    </div>
  );
}

// ── Cluster conditions ──────────────────────────────────────────────────────
//
// Renders the kubectl-style condition pills under the cluster header. Each
// chip shows the condition type + a coloured indicator; hover reveals the
// reason, message, and how long the condition has been in its current state.

const CONDITION_LABELS: Record<string, string> = {
  Connected: 'Connected',
  AgentReachable: 'Agent Reachable',
  GatewayAPISupported: 'Gateway API',
};

function relativeAge(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const diff = Math.max(0, Date.now() - t);
  const m = Math.floor(diff / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

function ClusterConditionsBar({ conditions }: { conditions: ClusterCondition[] }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5 pt-1">
      {conditions.map((c) => {
        const label = CONDITION_LABELS[c.type] || c.type;
        let tone = '';
        let Icon = CircleHelp;
        switch (c.status) {
          case 'True':
            tone = 'bg-status-success/10 text-status-success border-status-success/20';
            Icon = CheckCircle2;
            break;
          case 'False':
            tone = 'bg-status-danger/10 text-status-danger border-status-danger/20';
            Icon = XCircle;
            break;
          default:
            tone = 'bg-muted text-muted-foreground border-border';
            Icon = CircleHelp;
        }
        const tooltip = [
          `${c.reason || c.status}`,
          c.message,
          `For ${relativeAge(c.last_transition_time)}`,
        ].filter(Boolean).join(' — ');
        return (
          <span
            key={c.type}
            title={tooltip}
            className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs border ${tone}`}
          >
            <Icon className="h-3 w-3" />
            {label}
          </span>
        );
      })}
    </div>
  );
}
