import { createFileRoute } from "@tanstack/react-router";
import type { ComponentPropsWithoutRef } from "react";

import { useClusterEstateSummary, useClusters } from "@/lib/hooks/clusters";
import { useActivityFeed } from "@/lib/hooks/audit";
import { queryKeys } from "@/lib/query-keys";
import { useAlertEventSummary } from "@/lib/hooks/alerting";
import { useTools } from "@/lib/hooks/tools";
import { useLatestBackupDrill } from "@/components/settings/hooks";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { formatRelativeTime, cn } from "@/lib/utils";
import { WidgetGrid } from "@/components/dashboards/widget-grid";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ExtensionSlot } from "@/components/extensions/ExtensionSlot";
import { renderGlobal } from "@/lib/api/dashboards";
import {
  Server,
  Activity,
  AlertTriangle,
  WifiOff,
  Loader2,
  ArrowRight,
  PackagePlus,
  TerminalSquare,
  Bell,
  Boxes,
  ShieldCheck,
  Package,
  Layers,
} from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate } from "@tanstack/react-router";
import { EstateClustersTable } from "@/components/clusters/estate-clusters-table";

function DashboardPage() {
  const navigate = useNavigate();
  const clustersQuery = useClusters({
    pageSize: 10,
  });
  const clusterSummaryQuery = useClusterEstateSummary();
  const { data: activityData, isLoading: activityLoading } =
    useActivityFeed(10);
  const { data: alertSummary } = useAlertEventSummary();
  const { data: toolsData } = useTools();
  // T7.3 — backup-drill health row. The CronJob writes one row
  // per drill run; useLatestBackupDrill returns the most recent.
  const { data: latestDrill } = useLatestBackupDrill();

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
    [queryKeys.clusters.all, queryKeys.activity()],
  );

  const clusters = clustersQuery.data?.data || [];
  const clusterSummary = clusterSummaryQuery.data;
  const activity = activityData || [];
  const tools = toolsData || [];

  const activeClusters = clusterSummary?.clustersActive ?? 0;
  const warningClusters = clusterSummary?.clustersWarning ?? 0;
  const disconnectedClusters = clusterSummary?.clustersDisconnected ?? 0;
  const totalNodes = clusterSummary?.nodesTotal ?? 0;
  const totalPods = clusterSummary?.podsTotal ?? 0;
  const criticalAlerts = alertSummary?.firingCritical ?? 0;
  const warningAlerts = alertSummary?.firingWarning ?? 0;
  const totalTools = Array.isArray(tools) ? tools.length : 0;

  return (
    <PageShell>
      <PageHeader
        title="Platform Overview"
        description="Real-time status of your Kubernetes infrastructure"
      />

      {/* At-a-glance metric strip */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
        <MetricTile
          destination="clusters"
          label="Clusters"
          value={clusterSummary?.clustersTotal ?? "—"}
          sublabel={`${activeClusters} active`}
          icon={<Server className="h-4 w-4" />}
          tone="default"
        />
        <MetricTile
          destination="clusters-warning"
          label="Warnings"
          value={warningClusters}
          sublabel="needs attention"
          icon={<AlertTriangle className="h-4 w-4" />}
          tone={warningClusters > 0 ? "warning" : "default"}
        />
        <MetricTile
          destination="clusters-disconnected"
          label="Disconnected"
          value={disconnectedClusters}
          sublabel="agent offline"
          icon={<WifiOff className="h-4 w-4" />}
          tone={disconnectedClusters > 0 ? "error" : "default"}
        />
        <MetricTile
          destination="alerting"
          label="Open Alerts"
          value={alertSummary?.firing ?? "—"}
          sublabel={
            criticalAlerts > 0
              ? `${criticalAlerts} critical`
              : warningAlerts > 0
                ? `${warningAlerts} warning`
                : "all clear"
          }
          icon={<Bell className="h-4 w-4" />}
          tone={
            criticalAlerts > 0
              ? "error"
              : warningAlerts > 0
                ? "warning"
                : "default"
          }
        />
        <MetricTile
          destination="clusters"
          label="Pods"
          value={clusterSummary ? totalPods.toLocaleString() : "—"}
          sublabel={`across ${totalNodes} nodes`}
          icon={<Boxes className="h-4 w-4" />}
          tone="default"
        />
      </div>

      {/* Quick actions */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <ActionCard
          destination="register-cluster"
          icon={<Server className="h-4 w-4" />}
          title="Register cluster"
          description="Generate an install command for a new cluster"
        />
        <ActionCard
          destination="catalog"
          icon={<PackagePlus className="h-4 w-4" />}
          title="Browse catalog"
          description="Install Helm charts across managed clusters"
        />
        <ActionCard
          destination="alerting"
          icon={<Bell className="h-4 w-4" />}
          title="Review alerts"
          description="Acknowledge firing alerts and manage routing"
        />
        <ActionCard
          destination="projects"
          icon={<Layers className="h-4 w-4" />}
          title="Manage projects"
          description="Quotas, members, and project-scoped resources"
        />
      </div>

      {/* Clusters table */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-medium text-foreground">Clusters</h2>
          <RouterLink
            to="/dashboard/clusters"
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            View all
            <ArrowRight className="h-3.5 w-3.5" />
          </RouterLink>
        </div>

        {!clustersQuery.isLoading &&
        !clustersQuery.isError &&
        clusters.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-48 rounded-lg border border-dashed border-border">
            <Server className="h-8 w-8 text-muted-foreground mb-3" />
            <p className="text-sm text-muted-foreground mb-3">
              No clusters registered yet
            </p>
            <RouterLink
              to="/dashboard/clusters/register"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
            >
              Register Cluster
            </RouterLink>
          </div>
        ) : (
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
        )}
      </section>

      {/* Two-column: Recent Activity (wider) + Platform health (signals) */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <section className="lg:col-span-2 space-y-3">
          <h2 className="text-lg font-medium text-foreground">
            Recent Activity
          </h2>
          <div className="rounded-lg border border-border overflow-hidden">
            {activityLoading ? (
              <div className="flex items-center justify-center h-48">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : activity.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-48 text-muted-foreground">
                <Activity className="h-6 w-6 mb-2" />
                <p className="text-sm">No recent activity</p>
              </div>
            ) : (
              <div className="divide-y divide-border max-h-[420px] overflow-y-auto">
                {activity.map((event) => (
                  <div
                    key={event.id}
                    className="px-4 py-3 hover:bg-muted/30 transition-colors"
                  >
                    <div className="flex items-start gap-3">
                      <div
                        className={`mt-0.5 h-2 w-2 rounded-full shrink-0 ${
                          event.type === "cluster"
                            ? "bg-status-info"
                            : event.type === "workload"
                              ? "bg-status-success"
                              : event.type === "deployment"
                                ? "bg-primary"
                                : event.type === "rbac"
                                  ? "bg-status-warning"
                                  : "bg-muted-foreground"
                        }`}
                      />
                      <div className="flex-1 min-w-0">
                        <p className="text-sm text-foreground leading-snug">
                          {event.message}
                        </p>
                        <div className="flex items-center gap-2 mt-1">
                          {event.user && (
                            <span className="text-xs text-muted-foreground">
                              {event.user}
                            </span>
                          )}
                          <span className="text-xs text-muted-foreground/60">
                            {formatRelativeTime(event.timestamp)}
                          </span>
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>

        {/* Platform health — at-a-glance signals + drill-down links */}
        <section className="space-y-3">
          <h2 className="text-lg font-medium text-foreground">
            Platform Health
          </h2>
          <div className="rounded-lg border border-border bg-card divide-y divide-border">
            <HealthRow
              destination="alerting"
              icon={<Bell className="h-4 w-4" />}
              label="Firing alerts"
              value={alertSummary?.firing ?? "—"}
              tone={
                criticalAlerts > 0
                  ? "error"
                  : warningAlerts > 0
                    ? "warning"
                    : "success"
              }
              hint={
                criticalAlerts > 0
                  ? `${criticalAlerts} critical`
                  : warningAlerts > 0
                    ? `${warningAlerts} warning`
                    : "All clear"
              }
            />
            <HealthRow
              destination="clusters-disconnected"
              icon={<WifiOff className="h-4 w-4" />}
              label="Agent offline"
              value={disconnectedClusters}
              tone={disconnectedClusters > 0 ? "error" : "success"}
              hint={
                disconnectedClusters > 0 ? "reconnect needed" : "all reachable"
              }
            />
            <HealthRow
              destination="tools"
              icon={<Package className="h-4 w-4" />}
              label="Tools installed"
              value={totalTools}
              tone="default"
              hint="across all clusters"
            />
            <HealthRow
              destination="backup-settings"
              icon={<ShieldCheck className="h-4 w-4" />}
              label="Astronomer backup"
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
                  ? `last drill ${formatRelativeTime(latestDrill.latest.finishedAt)}`
                  : "drill never run"
              }
            />
          </div>
          <RouterLink
            to="/dashboard/clusters"
            className="block text-center text-xs text-muted-foreground hover:text-foreground py-1"
          >
            <TerminalSquare className="inline h-3 w-3 mr-1" />
            Open kubectl shell on any cluster
          </RouterLink>
        </section>
      </div>

      {/* Custom widgets — only renders when operators have configured them.
          Hidden via hideWhenEmpty so the dashboard stays clean on fresh
          installs. */}
      <section className="space-y-3">
        <WidgetGrid fetcher={renderGlobal} hideWhenEmpty />
      </section>

      {/* §HostMounts mount point 2 — enabled `dashboardWidget` extensions append
          cards here. Renders nothing when no extension declares one. */}
      <ExtensionSlot
        point="dashboardWidget"
        className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3"
      />
    </PageShell>
  );
}

type DashboardDestination =
  | "alerting"
  | "backup-settings"
  | "catalog"
  | "clusters"
  | "clusters-disconnected"
  | "clusters-warning"
  | "projects"
  | "register-cluster"
  | "tools";

function DashboardLink({
  destination,
  ...props
}: { destination: DashboardDestination } & Omit<
  ComponentPropsWithoutRef<"a">,
  "href"
>) {
  switch (destination) {
    case "clusters":
      return <RouterLink to="/dashboard/clusters" {...props} />;
    case "clusters-warning":
      return (
        <RouterLink
          to="/dashboard/clusters"
          search={{ status: "error" }}
          {...props}
        />
      );
    case "clusters-disconnected":
      return (
        <RouterLink
          to="/dashboard/clusters"
          search={{ status: "disconnected" }}
          {...props}
        />
      );
    case "alerting":
      return <RouterLink to="/dashboard/alerting" {...props} />;
    case "backup-settings":
      return <RouterLink to="/dashboard/settings/backup" {...props} />;
    case "catalog":
      return <RouterLink to="/dashboard/catalog" {...props} />;
    case "projects":
      return <RouterLink to="/dashboard/projects" {...props} />;
    case "register-cluster":
      return <RouterLink to="/dashboard/clusters/register" {...props} />;
    case "tools":
      return <RouterLink to="/dashboard/tools" {...props} />;
  }
}

function MetricTile({
  destination,
  label,
  value,
  sublabel,
  icon,
  tone,
}: {
  destination: DashboardDestination;
  label: string;
  value: string | number;
  sublabel?: string;
  icon: React.ReactNode;
  tone: "default" | "warning" | "error";
}) {
  const toneRing =
    tone === "error"
      ? "ring-status-error/20 hover:ring-status-error/40"
      : tone === "warning"
        ? "ring-status-warning/20 hover:ring-status-warning/40"
        : "ring-transparent";
  const toneValue =
    tone === "error"
      ? "text-status-error"
      : tone === "warning"
        ? "text-status-warning"
        : "text-foreground";
  return (
    <DashboardLink
      destination={destination}
      className={cn(
        "block rounded-lg border border-border bg-card p-3 hover:bg-card/80 transition-all ring-2 ring-inset",
        toneRing,
      )}
    >
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        {icon}
        <span>{label}</span>
      </div>
      <div
        className={cn("mt-1 text-2xl font-semibold tabular-nums", toneValue)}
      >
        {value}
      </div>
      {sublabel && (
        <div className="text-xs text-muted-foreground mt-0.5">{sublabel}</div>
      )}
    </DashboardLink>
  );
}

function ActionCard({
  destination,
  icon,
  title,
  description,
}: {
  destination: DashboardDestination;
  icon: React.ReactNode;
  title: string;
  description: string;
}) {
  return (
    <DashboardLink
      destination={destination}
      className="group flex items-start gap-3 rounded-lg border border-border bg-card p-3 hover:bg-card/80 hover:border-foreground/20 transition-colors"
    >
      <div className="shrink-0 w-8 h-8 rounded-md bg-muted flex items-center justify-center text-muted-foreground group-hover:text-foreground transition-colors">
        {icon}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between">
          <p className="text-sm font-medium text-foreground">{title}</p>
          <ArrowRight className="h-3.5 w-3.5 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity" />
        </div>
        <p className="text-xs text-muted-foreground mt-0.5 line-clamp-2">
          {description}
        </p>
      </div>
    </DashboardLink>
  );
}

function HealthRow({
  destination,
  icon,
  label,
  value,
  tone,
  hint,
}: {
  destination: DashboardDestination;
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
    <DashboardLink
      destination={destination}
      className="flex items-center justify-between px-3 py-2.5 hover:bg-muted/30 transition-colors"
    >
      <div className="flex items-center gap-2 min-w-0">
        <span className={cn("h-2 w-2 rounded-full shrink-0", dot)} />
        <span className="text-muted-foreground shrink-0">{icon}</span>
        <span className="text-sm text-foreground truncate">{label}</span>
      </div>
      <div className="flex items-center gap-2 shrink-0">
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
        <span className="text-sm font-medium tabular-nums text-foreground">
          {value}
        </span>
      </div>
    </DashboardLink>
  );
}

export const Route = createFileRoute("/dashboard/")({
  component: DashboardPage,
});
