import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import {
  Activity,
  AlertTriangle,
  Bell,
  Plus,
  Rocket,
  Server,
  ShieldCheck,
  Unplug,
} from "lucide-react";

import { useClusterEstateSummary, useClusters } from "@/lib/hooks/clusters";
import { useActivityFeed } from "@/lib/hooks/audit";
import { queryKeys } from "@/lib/query-keys";
import { useAlertEventSummary } from "@/lib/hooks/alerting";
import { useLatestBackupDrill } from "@/components/settings/backup-drill-hooks";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { getClusterAgents } from "@/lib/api/cluster-agents";
import { getDeliveryEstate } from "@/lib/api/delivery-system";
import { useAuthStore } from "@/lib/store";
import { can } from "@/lib/permissions";
import { formatRelativeTime, cn } from "@/lib/utils";
import { WidgetGrid } from "@/components/dashboards/widget-grid";
import { ExtensionSlot } from "@/components/extensions/ExtensionSlot";
import { renderGlobal } from "@/lib/api/dashboards";
import { EstateClustersTable } from "@/components/clusters/estate-clusters-table";
import { WelcomeBanner } from "@/components/dashboards/welcome-banner";
import { ActionButton } from "@/components/ui/action-button";
import { SkeletonText } from "@/components/ui/skeleton";
import {
  PageHeader,
  PageSection,
  PageShell,
  StatCard,
  StatGrid,
  VerdictBanner,
  type VerdictTone,
} from "@/ui";
import type { Cluster } from "@/types";

/**
 * Fleet-first overview (A1). Every number here reads from the same sources
 * the rest of the product uses: the cluster estate summary (which the
 * Clusters list is paged from), the agents list summary, the alert summary
 * and the delivery fleet — never a second, page-local rollup.
 */
export function DashboardPage() {
  const navigate = useNavigate();
  const user = useAuthStore((state) => state.user);
  const clustersQuery = useClusters({ pageSize: 10 });
  const summaryQuery = useClusterEstateSummary();
  const { data: activityData, isLoading: activityLoading } =
    useActivityFeed(10);
  const { data: alertSummary } = useAlertEventSummary();
  const { data: latestDrill } = useLatestBackupDrill();
  const canReadAgents = can(user, "cluster_agents", "read");
  const agentsQuery = useQuery({
    queryKey: queryKeys.agents.list({ limit: 1, offset: 0, scope: "overview" }),
    queryFn: ({ signal }) => getClusterAgents({ limit: 1, offset: 0 }, { signal }),
    enabled: canReadAgents,
    refetchInterval: liveFallback(30_000),
    throwOnError: false,
  });
  const canReadDelivery = can(user, "delivery_inventory", "read");
  const deliveryQuery = useQuery({
    queryKey: queryKeys.delivery.estate,
    queryFn: ({ signal }) => getDeliveryEstate(signal),
    enabled: canReadDelivery,
    refetchInterval: liveFallback(60_000),
    throwOnError: false,
  });

  useLiveQueryInvalidation(
    [
      "cluster.connected",
      "cluster.disconnected",
      "cluster.created",
      "cluster.updated",
      "cluster.deleted",
      "cluster.status_changed",
      "agent.reconnecting",
      "agent.failed",
    ],
    [queryKeys.clusters.all, queryKeys.activity(), queryKeys.agents.all],
  );

  const clusters = clustersQuery.data?.data ?? [];
  const summary = summaryQuery.data;
  // The list's pagination total and the summary come from the same table;
  // the KPI shows the list total so the tile and the table can never differ.
  const clusterTotal =
    clustersQuery.data?.pagination?.total ?? summary?.clustersTotal ?? clusters.length;
  const active = summary?.clustersActive ?? 0;
  const warning = summary?.clustersWarning ?? 0;
  const disconnected = summary?.clustersDisconnected ?? 0;
  const firing = alertSummary?.firing ?? 0;
  const critical = alertSummary?.firingCritical ?? 0;
  const agents = agentsQuery.data?.summary;
  const delivery = deliveryQuery.data;
  const activity = activityData ?? [];
  const loading = summaryQuery.isLoading && clustersQuery.isLoading;

  const verdict = fleetVerdict({
    total: clusterTotal,
    disconnected,
    warning,
    critical,
    firing,
    loading,
  });

  return (
    <PageShell>
      <PageHeader
        title="Overview"
        description="Fleet health, capacity, agents and delivery across every cluster you manage."
        actions={
          <ActionButton
            intent="primary"
            icon={<Plus className="h-4 w-4" />}
            onClick={() => void navigate({ to: "/dashboard/clusters/register" })}
          >
            Register cluster
          </ActionButton>
        }
      />

      <WelcomeBanner estateEmpty={!loading && clusterTotal === 0} />

      <VerdictBanner
        tone={verdict.tone}
        sentence={verdict.sentence}
        numbers={[
          { label: "clusters", value: loading ? "—" : clusterTotal },
          {
            label: "disconnected",
            value: loading ? "—" : disconnected,
            tone: disconnected > 0 ? "error" : "default",
          },
          {
            label: "firing alerts",
            value: alertSummary ? firing : "—",
            tone: critical > 0 ? "error" : firing > 0 ? "warning" : "default",
          },
          {
            label: "pods",
            value:
              summary?.podsTotal != null
                ? summary.podsTotal.toLocaleString()
                : "—",
          },
        ]}
      />

      {/* Fleet strip: clusters by health, from the estate summary. */}
      <PageSection title="Fleet" description="Clusters by health">
        <StatGrid columns={4}>
          <StatCard
            label="Clusters"
            value={loading ? "—" : clusterTotal}
            hint={summary ? `${active} active` : undefined}
            icon={<Server className="h-4 w-4" />}
            to="/dashboard/clusters"
            loading={loading}
          />
          <StatCard
            label="Needs attention"
            value={summary ? warning : "—"}
            hint="degraded or warning"
            tone={warning > 0 ? "warning" : "default"}
            icon={<AlertTriangle className="h-4 w-4" />}
            to="/dashboard/clusters?status=error"
            loading={loading}
          />
          <StatCard
            label="Disconnected"
            value={summary ? disconnected : "—"}
            hint="agent offline"
            tone={disconnected > 0 ? "error" : "default"}
            icon={<Unplug className="h-4 w-4" />}
            to="/dashboard/clusters?status=disconnected"
            loading={loading}
          />
          <StatCard
            label="Firing alerts"
            value={alertSummary ? firing : "—"}
            hint={
              critical > 0
                ? `${critical} critical`
                : (alertSummary?.firingWarning ?? 0) > 0
                  ? `${alertSummary?.firingWarning} warning`
                  : "all clear"
            }
            tone={critical > 0 ? "error" : firing > 0 ? "warning" : "default"}
            icon={<Bell className="h-4 w-4" />}
            to="/dashboard/alerting"
          />
        </StatGrid>
      </PageSection>

      <div className="grid grid-cols-1 gap-(--gap-section) xl:grid-cols-3">
        {/* Capacity bars per cluster (same rows the Clusters list shows). */}
        <PageSection
          title="Capacity"
          description="CPU and memory per cluster"
          className="xl:col-span-2"
          actions={
            <RouterLink
              to="/dashboard/clusters"
              className="text-meta text-muted-foreground hover:text-foreground"
            >
              All clusters
            </RouterLink>
          }
        >
          <CapacityList clusters={clusters} loading={clustersQuery.isLoading} />
        </PageSection>

        {/* Agent status from the agents list summary. */}
        <PageSection title="Agents" description="Connection health">
          <div className="divide-y divide-border rounded-lg border border-border bg-card">
            <HealthRow
              to="/dashboard/agents"
              icon={<Activity className="h-4 w-4" />}
              label="Connected"
              value={agents ? agents.connected : "—"}
              tone="success"
            />
            <HealthRow
              to="/dashboard/agents"
              icon={<AlertTriangle className="h-4 w-4" />}
              label="Degraded"
              value={agents ? agents.degraded : "—"}
              tone={(agents?.degraded ?? 0) > 0 ? "warning" : "default"}
            />
            <HealthRow
              to="/dashboard/agents"
              icon={<Unplug className="h-4 w-4" />}
              label="Disconnected"
              value={agents ? agents.disconnected : "—"}
              tone={(agents?.disconnected ?? 0) > 0 ? "error" : "default"}
            />
            <HealthRow
              to="/dashboard/settings/backup"
              icon={<ShieldCheck className="h-4 w-4" />}
              label="Management-plane backup"
              value={latestDrill?.latest?.status ?? "—"}
              tone={
                latestDrill?.latest?.status === "success"
                  ? "success"
                  : latestDrill?.latest?.status === "failure"
                    ? "error"
                    : latestDrill?.latest?.status === "partial"
                      ? "warning"
                      : "default"
              }
              hint={
                latestDrill?.latest?.finishedAt
                  ? `drill ${formatRelativeTime(latestDrill.latest.finishedAt)}`
                  : "drill never run"
              }
            />
          </div>
          {agents?.serverVersion ? (
            <p className="text-micro text-muted-foreground">
              Server {agents.serverVersion} · minimum agent{" "}
              {agents.minimumSupportedAgentVersion}
            </p>
          ) : null}
        </PageSection>
      </div>

      {/* Delivery fleet timeline: assignments, rollouts and attention items. */}
      {canReadDelivery ? (
        <PageSection
          title="Delivery"
          description="Fleet assignments and rollouts"
          actions={
            <RouterLink
              to="/dashboard/delivery"
              className="text-meta text-muted-foreground hover:text-foreground"
            >
              Open delivery fleet
            </RouterLink>
          }
        >
          <DeliveryStrip
            summary={delivery?.summary}
            attention={delivery?.attention ?? []}
            loading={deliveryQuery.isLoading}
          />
        </PageSection>
      ) : null}

      <PageSection title="Clusters" description="Most recently registered">
        <EstateClustersTable
          clusters={clusters}
          loading={clustersQuery.isLoading}
          isError={clustersQuery.isError}
          error={clustersQuery.error}
          onRetry={() => void clustersQuery.refetch()}
          onRowClick={(cluster) =>
            void navigate({ to: `/dashboard/clusters/${cluster.id}` })
          }
        />
      </PageSection>

      <PageSection title="Recent activity">
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          {activityLoading ? (
            <div role="status" aria-busy aria-label="Loading activity">
              <SkeletonText lines={5} className="h-48 p-4" />
            </div>
          ) : activity.length === 0 ? (
            <div className="flex h-32 flex-col items-center justify-center text-muted-foreground">
              <Activity className="mb-2 h-5 w-5" />
              <p className="text-sm">No recent activity</p>
            </div>
          ) : (
            <ul className="max-h-105 divide-y divide-border overflow-y-auto">
              {activity.map((event) => (
                <li key={event.id} className="px-4 py-2.5">
                  <p className="text-sm leading-snug text-foreground">
                    {event.message}
                  </p>
                  <p className="mt-0.5 text-meta text-muted-foreground">
                    {event.user ? `${event.user} · ` : ""}
                    {formatRelativeTime(event.timestamp)}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </div>
      </PageSection>

      <section className="space-y-3">
        <WidgetGrid fetcher={renderGlobal} hideWhenEmpty />
      </section>
      <ExtensionSlot
        point="dashboardWidget"
        className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3"
      />
    </PageShell>
  );
}

function fleetVerdict({
  total,
  disconnected,
  warning,
  critical,
  firing,
  loading,
}: {
  total: number;
  disconnected: number;
  warning: number;
  critical: number;
  firing: number;
  loading: boolean;
}): { tone: VerdictTone; sentence: string } {
  if (loading) return { tone: "info", sentence: "Checking the fleet…" };
  if (total === 0)
    return {
      tone: "info",
      sentence: "No clusters registered yet. Register one to start managing it.",
    };
  if (disconnected > 0 || critical > 0)
    return {
      tone: "critical",
      sentence: `${disconnected > 0 ? `${disconnected} of ${total} clusters are disconnected` : `${critical} critical alerts are firing`}. Attention needed.`,
    };
  if (warning > 0 || firing > 0)
    return {
      tone: "degraded",
      sentence: `${warning > 0 ? `${warning} clusters need attention` : `${firing} alerts are firing`}; the rest of the fleet is healthy.`,
    };
  return {
    tone: "ok",
    sentence: `All ${total} clusters are connected and no alerts are firing.`,
  };
}

function CapacityList({
  clusters,
  loading,
}: {
  clusters: Cluster[];
  loading: boolean;
}) {
  if (loading) {
    return (
      <div role="status" aria-busy className="rounded-lg border border-border bg-card p-4">
        <SkeletonText lines={4} />
      </div>
    );
  }
  if (clusters.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        Capacity appears here once a cluster reports metrics.
      </div>
    );
  }
  return (
    <ul className="divide-y divide-border rounded-lg border border-border bg-card">
      {clusters.map((cluster) => (
        <li key={cluster.id} className="grid grid-cols-[minmax(0,1.2fr)_1fr_1fr] items-center gap-4 px-4 py-2.5">
          <div className="min-w-0">
            <RouterLink
              to="/dashboard/clusters/$id"
              params={{ id: cluster.id }}
              className="block truncate text-sm font-medium text-foreground hover:underline"
            >
              {cluster.displayName || cluster.name}
            </RouterLink>
            <p className="text-meta text-muted-foreground">
              {cluster.nodeCount} nodes · {cluster.podCount} pods
            </p>
          </div>
          <CapacityBar label="CPU" value={cluster.cpuPercentage} />
          <CapacityBar label="Memory" value={cluster.memoryPercentage} />
        </li>
      ))}
    </ul>
  );
}

function CapacityBar({
  label,
  value,
}: {
  label: string;
  value: number | null | undefined;
}) {
  const pct = value == null || !Number.isFinite(value) ? null : Math.max(0, Math.min(100, value));
  return (
    <div className="min-w-0">
      <div className="flex items-center justify-between text-meta text-muted-foreground">
        <span>{label}</span>
        <span className="tabular-nums text-foreground">
          {pct == null ? "—" : `${pct < 10 ? pct.toFixed(1) : Math.round(pct)}%`}
        </span>
      </div>
      <div
        className="gauge-bar mt-1"
        role="meter"
        aria-label={`${label} ${pct == null ? "no data" : `${Math.round(pct)}%`}`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct == null ? undefined : Math.round(pct)}
      >
        <div
          className={cn(
            "gauge-bar-fill",
            pct != null && pct >= 90
              ? "bg-status-error"
              : pct != null && pct >= 75
                ? "bg-status-warning"
                : "bg-muted-foreground/60",
          )}
          style={{ width: `${pct ?? 0}%` }}
        />
      </div>
    </div>
  );
}

function DeliveryStrip({
  summary,
  attention,
  loading,
}: {
  summary:
    | {
        managedClusters: number;
        fluxReady: number;
        assignments: number;
        activeRollouts: number;
        failed: number;
        drifted: number;
      }
    | undefined;
  attention: Array<{
    clusterId: string;
    clusterName: string;
    severity: "error" | "warning";
    reason: string;
    detail: string;
  }>;
  loading: boolean;
}) {
  return (
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
      <div className="lg:col-span-2">
        <StatGrid columns={4}>
          <StatCard
            label="Managed clusters"
            value={summary ? summary.managedClusters : "—"}
            hint={summary ? `${summary.fluxReady} Flux-ready` : undefined}
            icon={<Rocket className="h-4 w-4" />}
            to="/dashboard/delivery"
            loading={loading}
          />
          <StatCard
            label="Assignments"
            value={summary ? summary.assignments : "—"}
            to="/dashboard/delivery/targets"
            loading={loading}
          />
          <StatCard
            label="Active rollouts"
            value={summary ? summary.activeRollouts : "—"}
            to="/dashboard/delivery/rollouts"
            loading={loading}
          />
          <StatCard
            label="Failed or drifted"
            value={summary ? (summary.failed ?? 0) + (summary.drifted ?? 0) : "—"}
            hint={
              summary
                ? `${summary.failed ?? 0} failed · ${summary.drifted ?? 0} drifted`
                : undefined
            }
            tone={
              summary && (summary.failed ?? 0) > 0
                ? "error"
                : summary && (summary.drifted ?? 0) > 0
                  ? "warning"
                  : "default"
            }
            to="/dashboard/delivery/deployments"
            loading={loading}
          />
        </StatGrid>
      </div>
      <div className="rounded-lg border border-border bg-card">
        <p className="border-b border-border px-4 py-2 text-meta font-medium text-muted-foreground">
          Needs attention
        </p>
        {loading ? (
          <div role="status" aria-busy className="p-4">
            <SkeletonText lines={3} />
          </div>
        ) : attention.length === 0 ? (
          <p className="px-4 py-6 text-center text-sm text-muted-foreground">
            Nothing needs attention.
          </p>
        ) : (
          <ol className="max-h-56 divide-y divide-border overflow-y-auto">
            {attention.slice(0, 6).map((item, index) => (
              <li key={`${item.clusterId}-${item.reason}-${index}`}>
                <RouterLink
                  to="/dashboard/clusters/$id/delivery"
                  params={{ id: item.clusterId }}
                  className="flex items-start gap-2 px-4 py-2 text-sm hover:bg-accent/40"
                >
                  <span
                    aria-hidden
                    className={cn(
                      "mt-1.5 h-2 w-2 shrink-0 rounded-full",
                      item.severity === "error" ? "bg-status-error" : "bg-status-warning",
                    )}
                  />
                  <span className="min-w-0">
                    <span className="block truncate font-medium text-foreground">
                      {item.clusterName}
                    </span>
                    <span className="block truncate text-meta text-muted-foreground">
                      {item.reason}
                    </span>
                  </span>
                </RouterLink>
              </li>
            ))}
          </ol>
        )}
      </div>
    </div>
  );
}

function HealthRow({
  to,
  icon,
  label,
  value,
  tone,
  hint,
}: {
  to: string;
  icon: React.ReactNode;
  label: string;
  value: string | number;
  tone: "default" | "warning" | "error" | "success";
  hint?: string;
}) {
  const dot =
    tone === "error"
      ? "bg-status-error"
      : tone === "warning"
        ? "bg-status-warning"
        : tone === "success"
          ? "bg-status-success"
          : "bg-muted-foreground";
  return (
    <RouterLink
      to={to}
      className="flex items-center justify-between px-3 py-2.5 transition-colors hover:bg-accent/40"
    >
      <span className="flex min-w-0 items-center gap-2">
        <span aria-hidden className={cn("h-2 w-2 shrink-0 rounded-full", dot)} />
        <span className="shrink-0 text-muted-foreground">{icon}</span>
        <span className="truncate text-sm text-foreground">{label}</span>
      </span>
      <span className="flex shrink-0 items-center gap-2">
        {hint && <span className="text-meta text-muted-foreground">{hint}</span>}
        <span className="text-sm font-medium tabular-nums text-foreground">
          {value}
        </span>
      </span>
    </RouterLink>
  );
}
