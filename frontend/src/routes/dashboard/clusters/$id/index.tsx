import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useEffect, useRef, useState } from 'react';
import { useParams, useRouter } from '@/lib/navigation';
import {
  useCluster,
  useClusterConditions,
  useClusterConditionRemediation,
  useClusterMetricsSummary,
  useClusterEvents,
  useClusterToolsStatus,
  useGenerateKubeconfig,
  useDeleteCluster,
  useUpdateCluster,
  useAnomalyBaselines,
  queryKeys,
} from '@/lib/hooks';
import { liveFallback } from '@/lib/live/status-store';
import {
  getArgoClusterOwnership,
  getRegistrationStatus,
  setArgoClusterOwnershipDecision,
  type RegistrationStatus,
} from '@/lib/api';
import { getImageVulnSummary } from '@/lib/api/cluster-detail';
import { useLiveQueryInvalidation } from '@/lib/live/hooks';
import { MetricCard } from '@/components/ui/metric-card';
import { StatusBadge } from '@/components/ui/status-badge';
import { EmptyState } from '@/components/ui/empty-state';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@/lib/link';
import { getServiceMeshDetection, type ServiceMeshKind } from '@/lib/api/cluster-detail';
import { ActionMenu } from '@/components/ui/action-menu';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
// RegisterClusterModal removed in sprint 22 — the "show install command"
// action now opens wizard step 2 for this cluster.
import { EditClusterModal } from '@/components/clusters/edit-cluster-modal';
import {
  formatBytes,
  formatCPU,
  formatPercentage,
  formatRelativeTime,
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
  ShieldAlert,
  Package,
  GitBranch,
} from 'lucide-react';
import type { ArgoBaselineComponentOwnership, ArgoClusterOwnershipResponse, Cluster, ClusterCondition } from '@/types';
import { toastApiError, toastError, toastSuccess } from '@/lib/toast';
import { WidgetGrid } from '@/components/dashboards/widget-grid';
import { ExtensionSlot } from '@/components/extensions/ExtensionSlot';
import { renderForCluster } from '@/lib/api/dashboards';

function ClusterDetailPage() {
  const params = useParams();
  const router = useRouter();
  const clusterId = params.id as string;

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const { data: conditions } = useClusterConditions(clusterId);
  const { data: metricsSummary, isError: metricsError } = useClusterMetricsSummary(clusterId);
  const { data: events } = useClusterEvents(clusterId, { limit: 10 });
  const { data: toolsStatus } = useClusterToolsStatus(clusterId);
  // Image-vuln severity rollup — same endpoint the Image Scans tab
  // uses, hoisted onto the overview as a top-line card so operators
  // see "you have 47 criticals" at a glance instead of having to
  // navigate two clicks deep.
  const { data: vulnSummary } = useQuery({
    queryKey: queryKeys.clusterPages.vulnerabilitySummary(clusterId),
    queryFn: () => getImageVulnSummary(clusterId),
    enabled: !!clusterId,
    // `image_scan.changed` refreshes this while the stream is open.
    refetchInterval: liveFallback(5 * 60 * 1000),
    refetchIntervalInBackground: false,
  });
  const generateKubeconfig = useGenerateKubeconfig();
  const deleteMutation = useDeleteCluster();
  const updateMutation = useUpdateCluster();
  // Service-mesh badge data (sprint 071). Cheap query — the row is one
  // SELECT keyed by cluster_id; if no detection has run yet the API
  // returns an "unknown" stub so we can render "—" without a 404 dance.
  const { data: meshDetection } = useQuery({
    queryKey: queryKeys.clusterPages.serviceMeshHeader(clusterId),
    queryFn: () => getServiceMeshDetection(clusterId),
    enabled: !!clusterId,
    // KEEP (P4.9): mesh state is cluster-side truth read through the agent
    // at request time (no server write to publish on) — deliberately NOT
    // converted to liveFallback.
    refetchInterval: 5 * 60 * 1000,
    refetchIntervalInBackground: false,
  });

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
      <EmptyState
        icon={Server}
        title="Cluster not found"
        description="The cluster may have been deleted or you may not have access to it."
        actionLabel="Back to clusters"
        actionHref="/dashboard/clusters"
      />
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
            <RegistrationPhaseHeaderBadge clusterId={clusterId} />
            {meshDetection && <MeshHeaderBadge clusterId={clusterId} mesh={meshDetection.detectedMesh} />}
          </div>
          <div className="flex items-center gap-4 text-sm text-muted-foreground">
            <span>{distributionDisplayName(cluster.distribution)}</span>
            <span className="text-border">|</span>
            <span>v{cluster.kubernetesVersion}</span>
            <span className="text-border">|</span>
            <span className="capitalize">{cluster.environment}</span>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            {conditions && conditions.length > 0 && <ClusterConditionsBar conditions={conditions} />}
            <AgentAccessChip cluster={cluster} />
          </div>
          <ClusterRemediationFooter clusterId={clusterId} />
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
                onClick: () => router.push(`/dashboard/clusters/register/${cluster.id}/connect`),
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

      {/* Custom dashboard widgets (migration 058). Per-cluster scope,
          templated against the cluster's cluster_uid. WidgetGrid owns
          the heading so the section collapses entirely when no widgets
          are configured — otherwise the bare "Widgets" header floats
          above an empty placeholder. */}
      {cluster?.id ? (
        <WidgetGrid
          fetcher={() => renderForCluster(cluster.id)}
          title="Widgets"
          hideWhenEmpty
        />
      ) : null}

      {/* §HostMounts mount point 3 — enabled `clusterTab` extensions append
          here with the current clusterId injected into their context /
          Tier-2 handshake. Renders nothing when no extension declares one. */}
      <ExtensionSlot
        point="clusterTab"
        context={{ clusterId }}
        className="grid grid-cols-1 lg:grid-cols-2 gap-3"
      />

      {/* Metrics Cards. When neither the live summary nor the cached cluster
          row has a usage/percentage value, we render an em-dash so the card
          doesn't lie with a fake 0% — the gauge bar is also suppressed by
          leaving `percentage` undefined. */}
      {(() => {
        const cpuPct = metricsSummary?.cpuPercentage ?? cluster.cpuPercentage ?? null;
        const cpuUsage = metricsSummary?.cpuUsage ?? cluster.cpuUsage ?? null;
        const cpuCap = metricsSummary?.cpuCapacity ?? cluster.cpuCapacity ?? null;
        const memPct = metricsSummary?.memoryPercentage ?? cluster.memoryPercentage ?? null;
        const memUsage = metricsSummary?.memoryUsage ?? cluster.memoryUsage ?? null;
        const memCap = metricsSummary?.memoryCapacity ?? cluster.memoryCapacity ?? null;
        return (
      <div className="space-y-2">
        {/* Metrics are non-optional: the panel always renders. When the
            summary query errors (e.g. Prometheus unreachable) we surface a
            "metrics unavailable" banner rather than hiding the cards, so the
            distinction between "no data" and "couldn't reach metrics" is
            visible to operators. */}
        {metricsError ? (
          <p
            data-testid="metrics-unavailable"
            className="text-sm text-muted-foreground"
          >
            Metrics unavailable
          </p>
        ) : null}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <MetricCard
          title="CPU Usage"
          value={formatPercentage(cpuPct)}
          percentage={cpuPct ?? undefined}
          subtitle={cpuUsage != null && cpuCap != null ? `${formatCPU(cpuUsage)} / ${formatCPU(cpuCap)}` : 'No data'}
          icon={<Cpu className="h-4 w-4" />}
        />
        <MetricCard
          title="Memory Usage"
          value={formatPercentage(memPct)}
          percentage={memPct ?? undefined}
          subtitle={memUsage != null && memCap != null ? `${formatBytes(memUsage)} / ${formatBytes(memCap)}` : 'No data'}
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
      </div>
        );
      })()}

      {/* Platform health row — image-scan severity + baseline-tool
          installation status + agent freshness. The data here all
          exists today on dedicated tabs (Image Scans / Tools), but
          surfacing the rollup on the overview is what makes the page
          read as a single-pane-of-glass instead of a starting point
          for navigation. */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <Link href={`/dashboard/clusters/${clusterId}/image-scans`} className="contents">
          <MetricCard
            title="Critical CVEs"
            value={vulnSummary?.critical ?? 0}
            subtitle={
              vulnSummary?.lastScannedAt
                ? `${vulnSummary.reportCount} reports · last ${formatRelativeTime(vulnSummary.lastScannedAt)}`
                : vulnSummary && vulnSummary.reportCount > 0
                  ? `${vulnSummary.reportCount} reports`
                  : 'no scans yet'
            }
            icon={<ShieldAlert className="h-4 w-4" />}
            className={
              (vulnSummary?.critical ?? 0) > 0
                ? 'cursor-pointer hover:border-status-error/50 transition-colors'
                : 'cursor-pointer hover:border-muted-foreground/50 transition-colors'
            }
          />
        </Link>
        <Link href={`/dashboard/clusters/${clusterId}/image-scans`} className="contents">
          <MetricCard
            title="High CVEs"
            value={vulnSummary?.high ?? 0}
            subtitle={
              vulnSummary
                ? `${vulnSummary.medium} med · ${vulnSummary.low} low`
                : '—'
            }
            icon={<ShieldAlert className="h-4 w-4" />}
            className="cursor-pointer hover:border-muted-foreground/50 transition-colors"
          />
        </Link>
        <Link href={`/dashboard/clusters/${clusterId}/tools`} className="contents">
          <MetricCard
            title="Baseline Tools"
            value={
              toolsStatus
                ? `${toolsStatus.filter((t) => t.status === 'installed').length}/${toolsStatus.length}`
                : '—'
            }
            subtitle={
              toolsStatus
                ? toolsStatus.filter((t) => t.status !== 'installed').length === 0
                  ? 'all installed'
                  : `${toolsStatus.filter((t) => t.status !== 'installed').map((t) => t.slug).join(', ')} pending`
                : undefined
            }
            icon={<Package className="h-4 w-4" />}
            className="cursor-pointer hover:border-muted-foreground/50 transition-colors"
          />
        </Link>
        <MetricCard
          title="Agent"
          value={cluster.agentVersion || '—'}
          subtitle={
            cluster.lastHeartbeat
              ? `heartbeat ${formatRelativeTime(cluster.lastHeartbeat)}`
              : 'never connected'
          }
          icon={<Activity className="h-4 w-4" />}
        />
      </div>

      <ArgoCDOwnershipPanel cluster={cluster} />
      <AgentPrivilegePanel cluster={cluster} />

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

      {/* T7.2 — Anomaly baselines surface. The nightly
          anomaly_baseline_recompute task fills these rows but no UI
          surfaced them until now. Top 5 anomalies sorted by score so
          the operator can sanity-check what the platform considers
          "normal" for this cluster. */}
      <AnomalyBaselinesPanel clusterId={clusterId} />

      {/* Registration Command — opens the wizard step 2 for this
          cluster, which renders the same install command + YAML
          tabs the legacy modal used to. */}

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

// agentAccessSummary derives the agent's access posture once, for both the
// header chip and the full panel.
//
// The local/self-managed cluster runs the in-process management agent (the
// chart's ServiceAccount + management ClusterRole), not a profile-generated
// remote agent — so the viewer/operator/admin model doesn't apply. Its stored
// annotation is empty (which would default to "viewer"), so surface its true
// posture explicitly instead of mislabeling it read-only.
function agentAccessSummary(cluster: Cluster) {
  const isLocal = !!cluster.isLocal;
  const profile = (cluster.agentPrivilegeProfile || 'admin').toLowerCase();
  const isAdmin = !isLocal && profile === 'admin';
  const label = isLocal
    ? 'Management'
    : profile === 'viewer'
      ? 'Viewer'
      : profile === 'operator'
        ? 'Operator'
        : 'Admin';
  const detail = isLocal
    ? 'In-cluster management agent — full management RBAC for platform self-management.'
    : profile === 'viewer'
      ? 'Read-only inventory, logs, health checks, and discovery.'
      : profile === 'operator'
        ? 'Workload operations without ClusterRole or cluster-admin escalation.'
        : 'Full API-group, resource, verb, and non-resource URL access.';
  return { isLocal, profile, isAdmin, label, detail };
}

// AgentAccessChip states the agent's access mode inline with the other cluster
// conditions, where it is scannable. Only `admin` — the posture that warrants a
// warning — escalates to the full AgentPrivilegePanel below; a whole bordered
// card with an icon, heading and docs link is a lot of chrome to say "Operator".
function AgentAccessChip({ cluster }: { cluster: Cluster }) {
  const { isAdmin, isLocal, label, detail } = agentAccessSummary(cluster);
  const tone = isAdmin
    ? 'bg-status-warning/10 text-status-warning border-status-warning/20'
    : isLocal
      ? 'bg-status-info/10 text-status-info border-status-info/20'
      : 'bg-status-success/10 text-status-success border-status-success/20';
  return (
    <span
      title={detail}
      className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs border ${tone}`}
    >
      <ShieldAlert className="h-3 w-3" />
      Access: {label}
    </span>
  );
}

function AgentPrivilegePanel({ cluster }: { cluster: Cluster }) {
  const { isAdmin, label, detail } = agentAccessSummary(cluster);
  // Everything except admin is now carried by the header chip. Admin keeps the
  // full panel: it is the one posture with a caveat worth spelling out and a
  // matrix worth linking.
  if (!isAdmin) return null;
  const tone = 'border-status-warning/30 bg-status-warning/10 text-status-warning';

  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="flex flex-col gap-4 p-4 md:flex-row md:items-center md:justify-between">
        <div className="flex items-start gap-3 min-w-0">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-border bg-background">
            <ShieldAlert className={`h-4 w-4 ${isAdmin ? 'text-status-warning' : 'text-muted-foreground'}`} />
          </div>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-sm font-medium text-foreground">Agent access profile</h3>
              <span className={`inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium ${tone}`}>
                {label}
              </span>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">{detail}</p>
            {isAdmin ? (
              <p className="mt-1 text-xs text-status-warning">
                Full-admin agent access should be reserved for compatibility or break-glass workflows.
              </p>
            ) : null}
          </div>
        </div>
        <Link
          href="/docs/agent-privilege-profiles.md"
          target="_blank"
          rel="noreferrer"
          className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded border border-border px-3 text-xs font-medium text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <CircleHelp className="h-3.5 w-3.5" />
          Profile matrix
        </Link>
      </div>
    </div>
  );
}

type OwnershipDecision = 'adopt' | 'leave_local' | 'replace';

// What each decision actually does to the cluster, in the operator's terms.
// "Leave local" reads harmless and is not: it uninstalls nothing, it just stops
// Astronomer managing the component, so a component that is already running is
// left up with nothing to reconcile it. The server refuses that case outright
// (409); this copy is what stops someone getting there by accident.
function decisionConsequence(option: OwnershipDecision, name: string): string {
  switch (option) {
    case 'leave_local':
      return `Astronomer will stop managing ${name} on this cluster. It is not uninstalled: if it is already running, the workload stays up but nothing will reconcile, upgrade or repair it. To remove it, uninstall it from Tools instead.`;
    case 'adopt':
      return `ArgoCD will take over managing ${name} on this cluster and reconcile it against the baseline from now on.`;
    case 'replace':
      return `The existing ${name} install will be replaced with Astronomer's baseline version. Resources not in the baseline may be removed.`;
  }
}

function ArgoCDOwnershipPanel({ cluster }: { cluster: Cluster }) {
  const queryClient = useQueryClient();
  const [pendingDecision, setPendingDecision] = useState<{
    slug: string;
    name: string;
    option: OwnershipDecision;
  } | null>(null);
  const [decisionReason, setDecisionReason] = useState('');
  const ownershipQuery = useQuery({
    queryKey: queryKeys.argocd.clusterOwnership(cluster.id),
    queryFn: () => getArgoClusterOwnership(cluster.id),
    enabled: !!cluster.id,
    // `argocd.changed` (scope: ownership/health) refreshes this while the
    // stream is open.
    refetchInterval: liveFallback(60_000),
  });
  const decisionMutation = useMutation({
    mutationFn: ({ slug, decision, reason }: { slug: string; decision: 'adopt' | 'leave_local' | 'replace'; reason: string }) =>
      setArgoClusterOwnershipDecision(cluster.id, slug, { decision, reason }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.clusterOwnership(cluster.id) });
      toastSuccess('Ownership decision recorded');
    },
    onError: (err: Error) => {
      toastApiError('Failed to record ownership decision', err);
    },
  });
  const argo = cluster.argocd;
  const ownership = isArgoClusterOwnershipResponse(ownershipQuery.data) ? ownershipQuery.data : undefined;
  const ownershipManagedClusters = Array.isArray(ownership?.managedClusters) ? ownership.managedClusters : [];
  const ownershipComponents = Array.isArray(ownership?.components) ? ownership.components : [];
  const registered = ownership?.registered ?? argo?.registered ?? false;
  const components: ArgoBaselineComponentOwnership[] = ownershipComponents.length > 0 ? ownershipComponents : (argo?.baselineComponents ?? []).map((component) => ({
    slug: component.slug,
    name: component.name,
    namespace: component.namespace,
    applicationSetName: component.applicationSetName,
    desiredOwner: 'argocd',
    observedOwner: component.managedBy,
    state: component.managedBy === 'argocd' ? 'argocd_owned' : component.managedBy === 'argocd_pending' ? 'migration_required' : component.managedBy,
    options: ['adopt', 'leave_local', 'replace'],
  }));
  const owner = ownership
    ? summarizeOwnershipState(ownershipComponents)
    : (argo?.baselineManagedBy ?? 'unknown');
  const drift = argo?.drift;
  const isArgoOwned = owner === 'argocd_owned' || owner === 'argocd';
  const isPending = owner === 'migration_required' || owner === 'argocd_pending';
  const isHelm = owner === 'legacy_helm' || owner === 'helm';
  const resourceCreatedCount = drift?.resourceCreatedCount ?? 0;
  const resourceChangedCount = drift?.resourceChangedCount ?? 0;
  const resourcePrunedCount = drift?.resourcePrunedCount ?? 0;
  const hasResourceDrift = resourceCreatedCount > 0 || resourceChangedCount > 0 || resourcePrunedCount > 0;
  const ownerLabel =
    owner === 'argocd_owned' || owner === 'argocd'
      ? 'ArgoCD'
      : owner === 'migration_required' || owner === 'argocd_pending'
        ? 'Migration required'
        : owner === 'legacy_helm' || owner === 'helm'
          ? 'Helm over tunnel'
          : owner === 'local_manual' || owner === 'local'
            ? 'Local cluster'
            : 'Unknown';
  const tone = isArgoOwned
    ? 'border-status-success/30 bg-status-success/10 text-status-success'
    : isPending
      ? 'border-status-warning/30 bg-status-warning/10 text-status-warning'
      : isHelm
        ? 'border-status-info/30 bg-status-info/10 text-status-info'
        : 'border-border bg-muted/30 text-muted-foreground';
  const driftTone = !drift || drift.appCount === 0
    ? 'border-border bg-muted/30 text-muted-foreground'
    : drift.degradedCount > 0
      ? 'border-status-error/30 bg-status-error/10 text-status-error'
      : drift.outOfSyncCount > 0
        ? 'border-status-warning/30 bg-status-warning/10 text-status-warning'
        : 'border-status-success/30 bg-status-success/10 text-status-success';

  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="flex flex-col gap-4 p-4 md:flex-row md:items-center md:justify-between">
        <div className="flex items-start gap-3 min-w-0">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-border bg-background">
            <GitBranch className="h-4 w-4 text-muted-foreground" />
          </div>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-sm font-medium text-foreground">GitOps ownership</h3>
              <span className={`inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium ${tone}`}>
                {ownerLabel}
              </span>
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span>
                Cluster registration: {registered ? `${ownershipManagedClusters.length || argo?.instanceCount || 0} ArgoCD instance${(ownershipManagedClusters.length || argo?.instanceCount || 0) === 1 ? '' : 's'}` : 'not registered'}
              </span>
              {ownershipManagedClusters.length ? (
                <span className="truncate">
                  Secret: <code className="font-mono">{ownershipManagedClusters.map((row) => row.clusterSecretName).filter(Boolean).join(', ')}</code>
                </span>
              ) : argo?.clusterSecretNames?.length ? (
                <span className="truncate">
                  Secret: <code className="font-mono">{argo.clusterSecretNames.join(', ')}</code>
                </span>
              ) : null}
            </div>
            {drift ? (
              <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span className={`inline-flex items-center rounded border px-2 py-0.5 font-medium ${driftTone}`}>
                  {drift.appCount === 0
                    ? 'No cached apps'
                    : `${drift.syncedCount}/${drift.appCount} synced`}
                </span>
                {drift.appCount > 0 ? (
                  <>
                    <span>
                      Health: {drift.healthyCount} healthy
                      {drift.progressingCount > 0 ? ` · ${drift.progressingCount} progressing` : ''}
                      {drift.degradedCount > 0 ? ` · ${drift.degradedCount} degraded` : ''}
                      {drift.unknownHealthCount > 0 ? ` · ${drift.unknownHealthCount} unknown` : ''}
                    </span>
                    {hasResourceDrift ? (
                      <span>
                        Resources: {resourceCreatedCount} created
                        {` · ${resourceChangedCount} changed`}
                        {` · ${resourcePrunedCount} pruned`}
                      </span>
                    ) : null}
                    {drift.lastSynced ? (
                      <span>Last sync: {formatRelativeTime(drift.lastSynced)}</span>
                    ) : null}
                    {drift.lastError ? (
                      <span className={drift.degradedCount > 0 ? 'text-status-error' : 'text-status-warning'}>
                        {drift.lastError}
                      </span>
                    ) : null}
                  </>
                ) : null}
              </div>
            ) : null}
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Link
            href="/dashboard/argocd"
            className="inline-flex h-8 items-center gap-1.5 rounded border border-border px-3 text-xs font-medium text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <GitBranch className="h-3.5 w-3.5" />
            ArgoCD
          </Link>
          <Link
            href={`/dashboard/clusters/${cluster.id}/tools`}
            className="inline-flex h-8 items-center gap-1.5 rounded border border-border px-3 text-xs font-medium text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <Package className="h-3.5 w-3.5" />
            Components
          </Link>
        </div>
      </div>
      {components.length > 0 ? (
        <div className="border-t border-border px-4 py-3">
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
            {components.map((component) => {
              const componentState = component.state ?? owner;
              const componentTone =
                componentState === 'argocd_owned'
                  ? 'border-status-success/30 bg-status-success/10 text-status-success'
                  : componentState === 'migration_required'
                    ? 'border-status-warning/30 bg-status-warning/10 text-status-warning'
                    : componentState === 'legacy_helm'
                      ? 'border-status-info/30 bg-status-info/10 text-status-info'
                      : 'border-border bg-muted/30 text-muted-foreground';
              const componentOwnerLabel =
                componentState === 'argocd_owned'
                  ? 'ArgoCD'
                  : componentState === 'migration_required'
                    ? 'Migrate'
                    : componentState === 'legacy_helm'
                      ? 'Helm'
                      : componentState === 'local_manual'
                        ? 'Local'
                        : 'Unknown';
              return (
                <div
                  key={component.slug}
                  className="min-w-0 rounded-md border border-border bg-background px-3 py-2"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-xs font-medium text-foreground">{component.name}</span>
                    <span className={`shrink-0 rounded border px-1.5 py-0.5 text-[11px] font-medium ${componentTone}`}>
                      {componentOwnerLabel}
                    </span>
                  </div>
                  <div className="mt-1 truncate text-[11px] text-muted-foreground">
                    {component.namespace}
                    {component.applicationSetName ? ` / ${component.applicationSetName}` : ''}
                  </div>
                  {component.decision?.reason ? (
                    <div className="mt-1 truncate text-[11px] text-muted-foreground">
                      {component.decision.reason}
                    </div>
                  ) : null}
                  {ownership && component.options?.length ? (
                    <div className="mt-2 flex flex-wrap gap-1">
                      {component.options.map((option) => (
                        <button
                          key={option}
                          onClick={() => {
                            setDecisionReason('');
                            setPendingDecision({
                              slug: component.slug,
                              name: component.name,
                              option: option as OwnershipDecision,
                            });
                          }}
                          disabled={decisionMutation.isPending}
                          className="rounded border border-border px-1.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
                        >
                          {decisionLabel(option)}
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        </div>
      ) : null}

      <ConfirmDialog
        open={!!pendingDecision}
        onClose={() => setPendingDecision(null)}
        onConfirm={() => {
          if (!pendingDecision) return;
          // Every decision here is consequential enough to explain, and the
          // server requires a reason for leave_local and replace.
          if (decisionReason.trim() === '') {
            toastError(`${decisionLabel(pendingDecision.option)} decisions require a reason`);
            return;
          }
          decisionMutation.mutate(
            { slug: pendingDecision.slug, decision: pendingDecision.option, reason: decisionReason.trim() },
            { onSuccess: () => setPendingDecision(null) },
          );
        }}
        title={pendingDecision ? `${decisionLabel(pendingDecision.option)} ${pendingDecision.name}?` : ''}
        description={pendingDecision ? decisionConsequence(pendingDecision.option, pendingDecision.name) : ''}
        confirmText={pendingDecision ? decisionLabel(pendingDecision.option) : 'Confirm'}
        variant={pendingDecision?.option === 'adopt' ? undefined : 'destructive'}
        loading={decisionMutation.isPending}
      >
        <div className="space-y-1.5">
          <label htmlFor="ownership-reason" className="text-sm font-medium text-foreground">
            Reason
          </label>
          <textarea
            id="ownership-reason"
            value={decisionReason}
            onChange={(e) => setDecisionReason(e.target.value)}
            rows={3}
            placeholder="Recorded in the audit log — why is this the right call?"
            className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm
              placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-none"
          />
        </div>
      </ConfirmDialog>
    </div>
  );
}

function isArgoClusterOwnershipResponse(value: unknown): value is ArgoClusterOwnershipResponse {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const candidate = value as Partial<ArgoClusterOwnershipResponse>;
  return (
    typeof candidate.registered === 'boolean' &&
    Array.isArray(candidate.managedClusters) &&
    Array.isArray(candidate.components)
  );
}

function summarizeOwnershipState(components: ArgoBaselineComponentOwnership[] = []): string {
  if (components.length === 0) return 'unknown';
  if (components.some((component) => component.state === 'migration_required')) return 'migration_required';
  if (components.every((component) => component.state === 'argocd_owned')) return 'argocd_owned';
  if (components.every((component) => component.state === 'local_manual')) return 'local_manual';
  return 'mixed';
}

function decisionLabel(value: string): string {
  if (value === 'leave_local') return 'Leave local';
  return value.charAt(0).toUpperCase() + value.slice(1);
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
            tone = 'bg-status-error/10 text-status-error border-status-error/20';
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

// ClusterRemediationFooter — shows the most recent action the
// cluster-condition reconciler took for this cluster. Hidden when
// there's no history yet (the common case for green clusters).
function ClusterRemediationFooter({ clusterId }: { clusterId: string }) {
  const { data } = useClusterConditionRemediation(clusterId);
  if (!data || data.length === 0) return null;
  const latest = data[0];
  const tone =
    latest.outcome === 'success'
      ? 'text-status-success'
      : latest.outcome === 'failed'
        ? 'text-status-error'
        : 'text-muted-foreground';
  return (
    <div
      className="text-[11px] text-muted-foreground pt-1"
      title={latest.error || latest.action}
    >
      Last remediation: <span className={tone}>{latest.action} — {latest.outcome}</span>
      <span className="text-border"> · </span>
      <span>{relativeAge(latest.attempted_at)} ago</span>
    </div>
  );
}

// MeshHeaderBadge — compact "Istio" / "Linkerd" / "—" pill rendered next
// to the cluster status badge. Links to the per-cluster service-mesh
// tab so a single click drills into the full tile. When the detector
// reports "unknown" / "none" the badge collapses to "—" so it never
// claims a mesh we don't have signal for.
function MeshHeaderBadge({ clusterId, mesh }: { clusterId: string; mesh: ServiceMeshKind }) {
  const label =
    mesh === 'istio'
      ? 'Istio'
      : mesh === 'linkerd'
        ? 'Linkerd'
        : mesh === 'kuma'
          ? 'Kuma'
          : mesh === 'cilium'
            ? 'Cilium'
            : '—';
  const tone =
    mesh === 'istio'
      ? 'border-blue-500/30 text-blue-500 bg-blue-500/10'
      : mesh === 'linkerd'
        ? 'border-emerald-500/30 text-emerald-500 bg-emerald-500/10'
        : 'border-border text-muted-foreground bg-muted/30';
  return (
    <Link
      href={`/dashboard/clusters/${clusterId}/service-mesh/`}
      title="Service mesh detection"
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${tone} hover:opacity-80 transition-opacity`}
    >
      mesh: {label}
    </Link>
  );
}

// Compact pill showing the cluster's adoption phase next to its status badge.
// Yellow spinner on awaiting_agent + baseline apply, green check on ready,
// red X on failed. Links to the Adoption tab so one click drills into the
// full timeline. Hidden when the cluster has no registration record.
function RegistrationPhaseHeaderBadge({ clusterId }: { clusterId: string }) {
  const { data } = useQuery<RegistrationStatus | null>({
    queryKey: queryKeys.clusterPages.registrationStatus(clusterId),
    queryFn: async () => {
      try {
        return await getRegistrationStatus(clusterId);
      } catch {
        return null;
      }
    },
    // `cluster.registration.step`/`.phase` events refresh this while the
    // stream is open.
    refetchInterval: liveFallback(5000),
  });
  const phase = data?.phase;
  if (!phase || phase === 'ready') return null; // collapse when done
  const tone =
    phase === 'failed'
      ? 'border-red-500/30 text-red-500 bg-red-500/10'
      : phase === 'provisioning' || phase === 'awaiting_agent' || phase === 'connected'
        ? 'border-yellow-500/30 text-yellow-500 bg-yellow-500/10'
        : 'border-border text-muted-foreground bg-muted/30';
  const label =
    phase === 'awaiting_agent' ? 'waiting for agent' :
    phase === 'provisioning' ? 'applying baseline' :
    phase === 'connected' ? 'connected' :
    phase === 'failed' ? 'failed' :
    phase;
  return (
    <Link
      href={`/dashboard/clusters/${clusterId}/adoption`}
      title="Adoption phase - click for step timeline"
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium border ${tone} hover:opacity-80 transition-opacity`}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current animate-pulse" />
      {label}
    </Link>
  );
}

// ── Anomaly baselines panel (T7.2) ───────────────────────────────────────
//
// The nightly anomaly_baseline_recompute task computes a per-metric
// rolling mean + stddev per cluster. Until now those rows lived in
// the DB with nothing rendering them. The panel surfaces the top 5
// metrics by sample count (the most-observed → most-trustworthy)
// with mean ± stddev so an operator can sanity-check what the
// platform considers "normal" for the cluster. Read-only.
function AnomalyBaselinesPanel({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useAnomalyBaselines({ clusterId, limit: 5 });
  if (isLoading) {
    return (
      <div>
        <h3 className="text-sm font-medium text-muted-foreground mb-3">Anomaly Baselines</h3>
        <div className="rounded-lg border border-border p-4 text-xs text-muted-foreground">
          Loading baselines…
        </div>
      </div>
    );
  }
  const rows = (data ?? []).slice(0, 5);
  if (rows.length === 0) {
    return (
      <div>
        <h3 className="text-sm font-medium text-muted-foreground mb-3">Anomaly Baselines</h3>
        <div className="rounded-lg border border-dashed border-border p-4 text-xs text-muted-foreground">
          No baselines computed yet. The nightly anomaly_baseline_recompute
          task fills these in once the cluster has at least 24h of metric
          samples.
        </div>
      </div>
    );
  }
  return (
    <div>
      <h3 className="text-sm font-medium text-muted-foreground mb-3">Anomaly Baselines</h3>
      <div className="rounded-lg border border-border overflow-hidden">
        <Table className="w-full text-sm">
          <TableHeader className="bg-muted/30 text-xs text-muted-foreground">
            <TableRow>
              <TableHead className="px-3 py-2 text-left font-medium">Metric</TableHead>
              <TableHead className="px-3 py-2 text-right font-medium">Mean</TableHead>
              <TableHead className="px-3 py-2 text-right font-medium">Stddev</TableHead>
              <TableHead className="px-3 py-2 text-right font-medium">Samples</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className="divide-y divide-border">
            {rows.map((b) => (
              <TableRow key={b.id}>
                <TableCell className="px-3 py-2 font-mono text-xs text-foreground">{b.metric}</TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums text-foreground">
                  {b.mean.toFixed(2)}
                </TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                  ±{b.stddev.toFixed(2)}
                </TableCell>
                <TableCell className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                  {b.sampleCount}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/')({
  component: ClusterDetailPage,
});
