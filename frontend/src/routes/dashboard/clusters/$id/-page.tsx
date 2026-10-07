import { getRouteApi } from "@tanstack/react-router";
import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import {
  useCluster,
  useClusterConditions,
  useClusterEvents,
  useDeleteCluster,
} from "@/lib/hooks/clusters";
import { useClusterMetricsSummary } from "@/lib/hooks/workloads";
import { useClusterKubeconfig } from "@/lib/hooks/kubernetes-proxy";
import { queryKeys } from "@/lib/query-keys";
import { useClustersUpdate } from "@/lib/permission-hooks";
import { useClusterToolsStatus } from "@/lib/hooks/tools";
import { liveFallback } from "@/lib/live/status-store";
import { deriveEffectiveClusterStatus } from "./-cluster-status";
import { ClusterUsageCards } from "./-cluster-usage-cards";
import {
  AnomalyBaselinesPanel,
  ClusterConditionsBar,
  ClusterHealthComponents,
  ClusterPlatformHealthRow,
  ClusterRecentEvents,
  ClusterRemediationFooter,
  MeshHeaderBadge,
  clusterOverviewMetadata,
} from "./-cluster-panels";

import { getImageVulnSummary } from "@/lib/api/cluster-vulnerabilities";
import { vulnerabilityMetric } from "@/components/clusters/vulnerability-metric";
import { toolStatusMetric } from "@/components/clusters/tool-status-metric";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { StatusBadge } from "@/components/ui/status-badge";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { useQuery } from "@tanstack/react-query";
import { getServiceMeshDetection } from "@/lib/api/cluster-service-mesh";
import { ActionMenu } from "@/components/ui/action-menu";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ResourceMasthead, PageShell } from "@/components/ui/page";
import { registrationSearch } from "@/components/clusters/registration-flow";
import { EditClusterModal } from "@/components/clusters/edit-cluster-modal";
import { Server, Download, Terminal, Pencil, Trash2 } from "lucide-react";
import { WidgetGrid } from "@/components/dashboards/widget-grid";
import { ExtensionSlot } from "@/components/extensions/ExtensionSlot";
import { renderForCluster } from "@/lib/api/dashboards";

export {
  AnomalyBaselinesPanel,
  isNoisyCapabilityCondition,
  isRedundantHealthyConnectivityCondition,
} from "./-cluster-panels";

const routeApi = getRouteApi("/dashboard/clusters/$id/");

export function ClusterDetailPage() {
  const params = routeApi.useParams();
  const navigate = useNavigate();
  const clusterId = params.id;

  const clusterQuery = useCluster(clusterId);
  const { data: cluster, isLoading: clusterLoading } = clusterQuery;
  const { data: conditions } = useClusterConditions(clusterId);
  const { data: metricsSummary, isError: metricsError } =
    useClusterMetricsSummary(clusterId);
  const eventsQuery = useClusterEvents(clusterId, { limit: 10 });

  const toolsQuery = useClusterToolsStatus(clusterId);
  const toolsMetric = toolStatusMetric(toolsQuery);
  // Image-vuln severity rollup — same endpoint the Image Scans tab
  // uses, hoisted onto the overview as a top-line card so operators
  // see "you have 47 criticals" at a glance instead of having to
  // navigate two clicks deep.
  const vulnerabilityQuery = useQuery({
    queryKey: queryKeys.clusterPages.vulnerabilitySummary(clusterId),
    queryFn: () => getImageVulnSummary(clusterId),
    enabled: !!clusterId,
    // `image_scan.changed` refreshes this while the stream is open.
    refetchInterval: liveFallback(5 * 60 * 1000),
    refetchIntervalInBackground: false,
    throwOnError: false,
  });
  const criticalMetric = vulnerabilityMetric(vulnerabilityQuery, "critical");
  const highMetric = vulnerabilityMetric(vulnerabilityQuery, "high");
  const directPermission = useClustersUpdate(clusterId);
  const kubeconfig = useClusterKubeconfig(clusterId, cluster, directPermission);
  const deleteMutation = useDeleteCluster();
  // Missing mesh detection renders an unknown state.
  const { data: meshDetection } = useQuery({
    queryKey: queryKeys.clusterPages.serviceMeshHeader(clusterId),
    queryFn: () => getServiceMeshDetection(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(5 * 60 * 1000),
    refetchIntervalInBackground: false,
  });

  // Refresh detail + metrics-summary + events lists when any cluster-level
  // event arrives. cluster.metrics is intentionally omitted — the layout
  // merger patches the percentages in place to avoid a refetch storm.
  useLiveQueryInvalidation(
    [
      "cluster.connected",
      "cluster.disconnected",
      "cluster.heartbeat",
      "cluster.status_changed",
      "cluster.updated",
      "cluster.deleted",
      "cluster.k8s_changed",
      "agent.reconnecting",
      "agent.failed",
    ],
    [
      queryKeys.clusters.detail(clusterId),
      queryKeys.clusters.metricsSummary(clusterId),
      queryKeys.clusters.events(clusterId),
      // cluster_conditions uses its own key shape (['clusters', id,
      // 'conditions']) so a heartbeat invalidation refreshes the pills
      // without waiting for the 60s poll interval.
      ["clusters", clusterId, "conditions"],
    ],
  );

  // Action menu state
  const [showEdit, setShowEdit] = useState(false);
  const [showDelete, setShowDelete] = useState(false);

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(clusterId);
      setShowDelete(false);
      void navigate({ to: "/dashboard/clusters" });
    } catch {
      // Error handled by mutation
    }
  };

  if (clusterLoading || clusterQuery.isError) {
    return (
      <QueryStates
        query={clusterQuery}
        permission="clusters:read"
        notFound={
          <EmptyState
            icon={Server}
            title="Cluster not found"
            description="The cluster may have been deleted or is outside your access scope."
            actionLabel="Back to clusters"
            actionHref="/dashboard/clusters"
          />
        }
      >
        {() => null}
      </QueryStates>
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

  const clusterMeta = clusterOverviewMetadata(cluster);
  const effectiveStatus = deriveEffectiveClusterStatus(cluster);

  return (
    <PageShell>
      <div className="space-y-2">
        <ResourceMasthead
          title={cluster.displayName || cluster.name || cluster.id}
          status={
            <>
              <StatusBadge
                status={effectiveStatus.status}
                label={effectiveStatus.label}
                size="lg"
                className="shrink-0"
              />
              {cluster.badgeText ? (
                <StatusBadge
                  tone={cluster.badgeColor}
                  label={cluster.badgeText}
                  className="shrink-0"
                />
              ) : null}
            </>
          }
          meta={clusterMeta}
          actions={
            <>
              <ActionButton
                tooltip="Download a one-hour, read-only kubeconfig routed and audited through Astronomer"
                onClick={kubeconfig.downloadProxy}
                loading={kubeconfig.proxyPending}
                icon={<Download className="h-4 w-4" />}
              >
                Proxy kubeconfig
              </ActionButton>
              <ActionButton
                tooltip="Download a separately scoped, read-only direct kubeconfig valid for 15 minutes"
                onClick={kubeconfig.downloadDirect}
                loading={kubeconfig.directPending}
                disabled={!!kubeconfig.directDisabledReason}
                disabledReason={kubeconfig.directDisabledReason}
                icon={<Download className="h-4 w-4" />}
              >
                Direct kubeconfig
              </ActionButton>
              <ActionMenu
                items={[
                  {
                    label: "Registration Command",
                    icon: <Terminal className="h-3.5 w-3.5" />,
                    onClick: () =>
                      void navigate({
                        to: "/dashboard/clusters/register",
                        search: registrationSearch(cluster.id),
                      }),
                  },
                  {
                    label: "Edit",
                    icon: <Pencil className="h-3.5 w-3.5" />,
                    onClick: () => setShowEdit(true),
                  },
                  {
                    label: "Delete",
                    icon: <Trash2 className="h-3.5 w-3.5" />,
                    onClick: () => setShowDelete(true),
                    variant: "destructive",
                    separator: true,
                  },
                ]}
              />
            </>
          }
        />
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm text-muted-foreground">
          {meshDetection && (
            <MeshHeaderBadge
              clusterId={clusterId}
              mesh={meshDetection.detectedMesh}
            />
          )}
          {conditions && conditions.length > 0 && (
            <ClusterConditionsBar conditions={conditions} />
          )}
        </div>
        <ClusterRemediationFooter clusterId={clusterId} />
      </div>

      <ClusterHealthComponents cluster={cluster} />

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
      <ClusterUsageCards
        clusterId={clusterId}
        cluster={cluster}
        metricsSummary={metricsSummary}
        metricsError={metricsError}
      />

      {/* Platform health row — image-scan severity + baseline-tool
          installation status + agent freshness. The data here all
          exists today on dedicated tabs (Image Scans / Tools), but
          surfacing the rollup on the overview is what makes the page
          read as a single-pane-of-glass instead of a starting point
          for navigation. */}
      <ClusterPlatformHealthRow
        clusterId={clusterId}
        cluster={cluster}
        criticalMetric={criticalMetric}
        highMetric={highMetric}
        toolsMetric={toolsMetric}
      />

      <ClusterRecentEvents query={eventsQuery} />

      {/* T7.2 — Anomaly baselines surface. The nightly
          nightly baseline job fills these rows but no UI
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
    </PageShell>
  );
}
