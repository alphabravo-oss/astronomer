import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { Activity, Plus, Rocket, ShieldCheck } from "lucide-react";

import { useClusters } from "@/lib/hooks/clusters";
import { useActivityFeed } from "@/lib/hooks/audit";
import { queryKeys } from "@/lib/query-keys";
import { useAlertEventSummary } from "@/lib/hooks/alerting";
import { useLatestBackupDrill } from "@/components/settings/backup-drill-hooks";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
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
import {
  FLEET_PAGE_SIZE,
  fleetModel,
  type FleetHealth,
  type FleetModel,
} from "./-fleet-model";

const CAPACITY_ROWS = 8;
const BOARD_NAMES = 6;

/**
 * Fleet-first overview (A1). Every cluster count on this page — verdict
 * strip, health board, agents panel, capacity list and the clusters table —
 * derives from ONE query (the clusters list, one page of up to 200) through
 * the `fleetModel` selector, so no two panels can disagree.
 */
export function DashboardPage() {
  const navigate = useNavigate();
  const user = useAuthStore((state) => state.user);
  const clustersQuery = useClusters({ pageSize: FLEET_PAGE_SIZE });
  const { data: activityData, isLoading: activityLoading } =
    useActivityFeed(10);
  const { data: alertSummary } = useAlertEventSummary();
  const { data: latestDrill } = useLatestBackupDrill();
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
    [queryKeys.clusters.all, queryKeys.activity()],
  );

  const loading = clustersQuery.isLoading;
  const fleet = fleetModel(
    clustersQuery.data?.data,
    clustersQuery.data?.pagination?.total,
  );
  const firing = alertSummary?.firing ?? 0;
  const critical = alertSummary?.firingCritical ?? 0;
  const delivery = deliveryQuery.data;
  const activity = activityData ?? [];
  const verdict = fleetVerdict(fleet, { critical, firing, loading });

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

      <WelcomeBanner estateEmpty={!loading && fleet.total === 0} />

      <div data-testid="fleet-verdict">
        <VerdictBanner
          tone={verdict.tone}
          sentence={verdict.sentence}
          numbers={[
            { id: "clusters", label: "clusters", value: loading ? "—" : fleet.total },
            {
              id: "disconnected",
              label: "disconnected",
              value: loading ? "—" : fleet.counts.disconnected,
              tone: fleet.counts.disconnected > 0 ? "error" : "default",
            },
            {
              id: "alerts",
              label: "firing alerts",
              value: alertSummary ? firing : "—",
              tone: critical > 0 ? "error" : firing > 0 ? "warning" : "default",
            },
            {
              id: "pods",
              label: "pods",
              value: loading ? "—" : fleet.pods.toLocaleString(),
            },
          ]}
        />
      </div>

      {/* Health board: which clusters, not how many (the strip says how many). */}
      <PageSection
        title="Clusters by health"
        actions={
          <RouterLink
            to="/dashboard/clusters"
            className="text-meta text-muted-foreground hover:text-foreground"
          >
            All clusters
          </RouterLink>
        }
      >
        <HealthBoard fleet={fleet} loading={loading} />
      </PageSection>

      <div className="grid grid-cols-1 gap-(--gap-section) xl:grid-cols-3">
        <PageSection
          title="Capacity"
          description={
            fleet.total > CAPACITY_ROWS
              ? `Busiest ${CAPACITY_ROWS} of ${fleet.total} clusters by CPU or memory`
              : "CPU and memory per cluster"
          }
          className="xl:col-span-2"
        >
          <CapacityList fleet={fleet} loading={loading} />
        </PageSection>

        <PageSection title="Agents" description="One agent per cluster">
          <AgentsPanel fleet={fleet} loading={loading} />
          <div className="divide-y divide-border rounded-lg border border-border bg-card">
            <HealthRow
              to="/dashboard/settings/backup"
              icon={<ShieldCheck className="h-4 w-4" />}
              label="Platform backup"
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
                  : "no drill yet"
              }
            />
          </div>
        </PageSection>
      </div>

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
            fleetTotal={fleet.total}
          />
        </PageSection>
      ) : null}

      <PageSection title="Clusters" description="Most recently registered">
        <EstateClustersTable
          clusters={fleet.clusters}
          loading={loading}
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

function plural(count: number, word: string): string {
  return `${count} ${word}${count === 1 ? "" : "s"}`;
}

function fleetVerdict(
  fleet: FleetModel,
  {
    critical,
    firing,
    loading,
  }: { critical: number; firing: number; loading: boolean },
): { tone: VerdictTone; sentence: string } {
  const { total, counts } = fleet;
  if (loading) return { tone: "info", sentence: "Checking the fleet…" };
  if (total === 0)
    return {
      tone: "info",
      sentence: "No clusters registered yet. Register one to start managing it.",
    };
  if (counts.disconnected > 0 || critical > 0)
    return {
      tone: "critical",
      sentence:
        counts.disconnected > 0
          ? `${counts.disconnected} of ${plural(total, "cluster")} ${counts.disconnected === 1 ? "is" : "are"} disconnected.`
          : `${plural(critical, "critical alert")} firing.`,
    };
  if (counts.attention > 0 || counts.registering > 0 || firing > 0)
    return {
      tone: "degraded",
      sentence:
        counts.attention > 0
          ? `${plural(counts.attention, "cluster")} need${counts.attention === 1 ? "s" : ""} attention; the rest of the fleet is healthy.`
          : counts.registering > 0
            ? `${plural(counts.registering, "cluster")} still registering; the rest of the fleet is healthy.`
            : `${plural(firing, "alert")} firing; every cluster is connected.`,
    };
  return {
    tone: "ok",
    sentence: `All ${plural(total, "cluster")} connected and no alerts firing.`,
  };
}

const boardColumns: Array<{
  key: FleetHealth;
  label: string;
  dot: string;
  status?: string;
}> = [
  { key: "healthy", label: "Healthy", dot: "bg-status-success", status: "active" },
  { key: "attention", label: "Needs attention", dot: "bg-status-warning", status: "error" },
  {
    key: "disconnected",
    label: "Disconnected",
    dot: "bg-status-error",
    status: "disconnected",
  },
  { key: "registering", label: "Registering", dot: "bg-status-info", status: "pending" },
];

function HealthBoard({ fleet, loading }: { fleet: FleetModel; loading: boolean }) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {boardColumns.map((column) => {
        const members = fleet.byHealth[column.key];
        return (
          <div
            key={column.key}
            data-testid={`health-${column.key}`}
            className="rounded-lg border border-border bg-card"
          >
            <div className="flex items-center justify-between border-b border-border px-4 py-2">
              <span className="flex items-center gap-2 text-sm font-semibold text-foreground">
                <span aria-hidden className={cn("h-2 w-2 rounded-full", column.dot)} />
                {column.label}
              </span>
              <span
                data-testid={`health-${column.key}-count`}
                className="text-meta tabular-nums text-muted-foreground"
              >
                {loading ? "—" : members.length}
              </span>
            </div>
            {loading ? (
              <div role="status" aria-busy className="p-4">
                <SkeletonText lines={2} />
              </div>
            ) : members.length === 0 ? (
              <p className="px-4 py-3 text-meta text-muted-foreground">None</p>
            ) : (
              <ul className="divide-y divide-border">
                {members.slice(0, BOARD_NAMES).map((cluster) => (
                  <li key={cluster.id}>
                    <RouterLink
                      to="/dashboard/clusters/$id"
                      params={{ id: cluster.id }}
                      className="flex items-baseline justify-between gap-3 px-4 py-1.5 text-sm transition-colors hover:bg-accent/40"
                    >
                      <span className="min-w-0 truncate font-medium text-foreground">
                        {cluster.displayName || cluster.name}
                      </span>
                      <span className="shrink-0 text-meta text-muted-foreground">
                        {cluster.environment || cluster.region || ""}
                      </span>
                    </RouterLink>
                  </li>
                ))}
                {members.length > BOARD_NAMES ? (
                  <li>
                    <RouterLink
                      to="/dashboard/clusters"
                      search={{ status: column.status }}
                      className="block px-4 py-1.5 text-meta text-muted-foreground hover:text-foreground"
                    >
                      and {members.length - BOARD_NAMES} more
                    </RouterLink>
                  </li>
                ) : null}
              </ul>
            )}
          </div>
        );
      })}
    </div>
  );
}

function AgentsPanel({ fleet, loading }: { fleet: FleetModel; loading: boolean }) {
  const versions = new Map<string, number>();
  for (const cluster of fleet.clusters) {
    if (!cluster.agentVersion) continue;
    versions.set(cluster.agentVersion, (versions.get(cluster.agentVersion) ?? 0) + 1);
  }
  const rows: Array<{ key: FleetHealth; label: string; tone: "success" | "warning" | "error" | "default" }> = [
    { key: "healthy", label: "Connected", tone: "success" },
    { key: "attention", label: "Connected, degraded", tone: "warning" },
    { key: "disconnected", label: "Disconnected", tone: "error" },
    { key: "registering", label: "Never connected", tone: "default" },
  ];
  return (
    <div className="space-y-2">
      <div
        data-testid="agents-panel"
        className="divide-y divide-border rounded-lg border border-border bg-card"
      >
        {rows.map((row) => (
          <HealthRow
            key={row.key}
            to="/dashboard/agents"
            label={row.label}
            value={loading ? "—" : fleet.counts[row.key]}
            tone={fleet.counts[row.key] > 0 ? row.tone : "default"}
            testId={`agents-${row.key}`}
          />
        ))}
      </div>
      {versions.size > 0 ? (
        <p className="text-micro text-muted-foreground">
          Agent versions:{" "}
          {[...versions.entries()]
            .sort((a, b) => b[1] - a[1])
            .map(([version, count]) => `${version} × ${count}`)
            .join(" · ")}
        </p>
      ) : null}
    </div>
  );
}

function CapacityList({ fleet, loading }: { fleet: FleetModel; loading: boolean }) {
  if (loading) {
    return (
      <div role="status" aria-busy className="rounded-lg border border-border bg-card p-4">
        <SkeletonText lines={4} />
      </div>
    );
  }
  if (fleet.clusters.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
        Capacity appears here once a cluster reports metrics.
      </div>
    );
  }
  const load = (cluster: Cluster) =>
    Math.max(cluster.cpuPercentage ?? 0, cluster.memoryPercentage ?? 0);
  const rows = [...fleet.clusters]
    .sort((a, b) => load(b) - load(a))
    .slice(0, CAPACITY_ROWS);
  return (
    <ul
      data-testid="capacity-list"
      className="divide-y divide-border rounded-lg border border-border bg-card"
    >
      {rows.map((cluster) => (
        <li
          key={cluster.id}
          className="grid grid-cols-[minmax(0,1.2fr)_1fr_1fr] items-center gap-4 px-4 py-2.5"
        >
          <div className="min-w-0">
            <RouterLink
              to="/dashboard/clusters/$id"
              params={{ id: cluster.id }}
              className="block truncate text-sm font-medium text-foreground hover:underline"
            >
              {cluster.displayName || cluster.name}
            </RouterLink>
            <p className="text-meta text-muted-foreground">
              {plural(cluster.nodeCount ?? 0, "node")} · {plural(cluster.podCount ?? 0, "pod")}
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
  const pct =
    value == null || !Number.isFinite(value) ? null : Math.max(0, Math.min(100, value));
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
  fleetTotal,
}: {
  summary:
    | {
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
  fleetTotal: number;
}) {
  const failed = summary?.failed ?? 0;
  const drifted = summary?.drifted ?? 0;
  return (
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
      <div className="lg:col-span-2">
        <StatGrid columns={4}>
          <StatCard
            label="Flux-ready"
            value={summary ? summary.fluxReady : "—"}
            hint={`of ${plural(fleetTotal, "cluster")}`}
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
            value={summary ? failed + drifted : "—"}
            hint={summary ? `${failed} failed · ${drifted} drifted` : undefined}
            tone={failed > 0 ? "error" : drifted > 0 ? "warning" : "default"}
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
  testId,
}: {
  to: string;
  icon?: React.ReactNode;
  label: string;
  value: string | number;
  tone: "default" | "warning" | "error" | "success";
  hint?: string;
  testId?: string;
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
      data-testid={testId}
      className="flex items-center justify-between px-3 py-2.5 transition-colors hover:bg-accent/40"
    >
      <span className="flex min-w-0 items-center gap-2">
        <span aria-hidden className={cn("h-2 w-2 shrink-0 rounded-full", dot)} />
        {icon ? <span className="shrink-0 text-muted-foreground">{icon}</span> : null}
        <span className="truncate text-sm text-foreground">{label}</span>
      </span>
      <span className="flex shrink-0 items-center gap-2">
        {hint && <span className="text-meta text-muted-foreground">{hint}</span>}
        <span
          data-value=""
          className="text-sm font-medium tabular-nums text-foreground"
        >
          {value}
        </span>
      </span>
    </RouterLink>
  );
}
