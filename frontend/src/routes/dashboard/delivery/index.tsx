import { createFileRoute } from "@tanstack/react-router";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import {
  AlertTriangle,
  Boxes,
  Crosshair,
  GitBranch,
  Layers,
  Radio,
  Rocket,
  ServerCog,
  Shield,
  Unplug,
  X,
} from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { Link } from "@/lib/link";
import { MetricCard } from "@/components/ui/metric-card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { usePathname, useRouter, useSearchParams } from "@/lib/navigation";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  ErrorMessage,
  clusterDeliveryPath,
  projectClusterId,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import {
  getDeliveryFleet,
  getDeliverySystemCompatibility,
  listClusterDeployments,
  listComponentBundles,
  listDeliveryRollouts,
  listDeliverySources,
  listDeliveryTargets,
  type DeliveryFleet,
  type DeliveryFleetAttention,
  type DeliveryFleetCluster,
  type DeliveryFleetCount,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useClusters, useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { cn, formatRelativeTime } from "@/lib/utils";

function isForbiddenError(error: unknown): boolean {
  return Boolean(
    error &&
      typeof error === "object" &&
      "response" in error &&
      (error as { response?: { status?: number } }).response?.status === 403,
  );
}

function DeliveryOverviewPage() {
  const { projectId, projects, projectQuery, setProjectId } =
    useDeliveryProjectScope();
  const { data: user } = useCurrentUser();
  const canReadFleet = can(user, "delivery_inventory", "read");
  const fleet = useQuery({
    queryKey: queryKeys.delivery.fleet,
    queryFn: getDeliveryFleet,
    enabled: canReadFleet,
    refetchInterval: liveFallback(15_000),
    retry: (failureCount, error) =>
      !isForbiddenError(error) && failureCount < 2,
  });
  useLiveQueryInvalidation(
    [
      "cluster.connected",
      "cluster.disconnected",
      "cluster.deleted",
      "agent.failed",
      "cluster_agents.changed",
      "delivery_rollout.changed",
      "cluster_deployment.changed",
    ],
    [queryKeys.delivery.fleet],
  );
  const showFleet = canReadFleet && !isForbiddenError(fleet.error);
  if (showFleet) {
    return <FleetDeliveryOverview query={fleet} />;
  }
  return (
    <DeliveryShell
      projectId={projectId}
      projects={projects}
      setProjectId={setProjectId}
    >
      <ProjectDeliveryOverview
        projectId={projectId}
        projectQuery={projectQuery}
        projectsCount={projects.length}
      />
    </DeliveryShell>
  );
}

const fleetFocusLabels: Record<string, string> = {
  adopted: "Adopted clusters",
  flux_ready: "Flux-ready clusters",
  incompatible: "Incompatible clusters",
  disconnected: "Disconnected clusters",
  assignments: "Clusters with assignments",
  failed: "Clusters with failed assignments",
  drifted: "Clusters with drift",
};

function clusterHref(clusterId: string): string {
  return clusterDeliveryPath(clusterId);
}

function clusterMatchesFocus(
  cluster: DeliveryFleetCluster,
  focus: string,
): boolean {
  switch (focus) {
    case "adopted":
      return !cluster.isLocal;
    case "flux_ready":
      return (
        !cluster.isLocal &&
        cluster.connected &&
        cluster.inventoryReady &&
        cluster.compatibilityStatus === "compatible"
      );
    case "incompatible":
      return (
        !cluster.isLocal &&
        ["incompatible", "upgrade_required", "degraded"].includes(
          cluster.compatibilityStatus,
        )
      );
    case "disconnected":
      return !cluster.isLocal && !cluster.connected;
    case "assignments":
      return cluster.assignmentCount > 0;
    case "failed":
      return cluster.failedCount > 0;
    case "drifted":
      return cluster.driftedCount > 0;
    default:
      if (focus.startsWith("compatibility:")) {
        return (
          !cluster.isLocal &&
          cluster.compatibilityStatus === focus.slice("compatibility:".length)
        );
      }
      if (focus.startsWith("privilege:")) {
        return (
          !cluster.isLocal &&
          cluster.privilegeProfile === focus.slice("privilege:".length)
        );
      }
      if (focus.startsWith("phase:")) {
        const phase = focus.slice("phase:".length);
        if (phase === "ready") return cluster.readyCount > 0;
        if (phase === "failed") return cluster.failedCount > 0;
        if (phase === "degraded") return cluster.degradedCount > 0;
        return false;
      }
      return true;
  }
}

function FleetDeliveryOverview({
  query,
}: {
  query: UseQueryResult<DeliveryFleet>;
}) {
  const fleet = query.data;
  const summary = fleet?.summary;
  const router = useRouter();
  const pathname = usePathname();
  const search = useSearchParams();
  const focus = search.get("focus") ?? "";
  const clusters = fleet?.clusters ?? [];
  const clusterList = useClusters({ pageSize: 200 });
  const environmentById = useMemo(() => {
    const map = new Map<string, string>();
    for (const item of clusterList.data?.data ?? []) {
      if (item.id && item.environment) map.set(item.id, item.environment);
    }
    return map;
  }, [clusterList.data?.data]);
  const visible = focus
    ? clusters.filter((cluster) => clusterMatchesFocus(cluster, focus))
    : clusters;

  const setFocus = (next: string) => {
    const matches = clusters.filter((cluster) =>
      clusterMatchesFocus(cluster, next),
    );
    if (matches.length === 1) {
      router.push(clusterHref(matches[0].id));
      return;
    }
    const params = new URLSearchParams(search);
    if (next) params.set("focus", next);
    else params.delete("focus");
    router.replace(`${pathname}${params.size ? `?${params.toString()}` : ""}`);
    requestAnimationFrame(() => {
      document
        .getElementById("fleet-clusters")
        ?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  };

  const columns: Column<DeliveryFleetCluster>[] = [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <div>
          <span className="font-medium text-foreground">
            {row.displayName || row.name}
          </span>
          <p className="font-mono text-xs text-muted-foreground">{row.name}</p>
        </div>
      ),
      sortAccessor: (row) => row.displayName || row.name,
    },
    {
      key: "environment",
      header: "Environment",
      accessor: (row) => (
        <span className="text-xs capitalize text-muted-foreground">
          {environmentById.get(row.id) || "—"}
        </span>
      ),
      sortAccessor: (row) => environmentById.get(row.id) || "",
    },
    {
      key: "role",
      header: "Role",
      accessor: (row) =>
        row.isLocal ? (
          <span className="text-xs text-muted-foreground">
            Local host-only
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-xs">
            <Shield className="h-3 w-3" />
            {row.privilegeProfile}
          </span>
        ),
      sortAccessor: (row) => (row.isLocal ? "local" : row.privilegeProfile),
    },
    {
      key: "agent",
      header: "Agent",
      accessor: (row) => (
        <DeliveryPhaseBadge
          value={row.connected ? (row.stale ? "stale" : "connected") : "disconnected"}
        />
      ),
      sortAccessor: (row) =>
        row.connected ? (row.stale ? "stale" : "connected") : "disconnected",
    },
    {
      key: "flux",
      header: "Flux",
      accessor: (row) => (
        <div className="space-y-1">
          <DeliveryPhaseBadge value={row.compatibilityStatus} />
          <p className="font-mono text-xs text-muted-foreground">
            {row.fluxVersion || "—"}
          </p>
        </div>
      ),
      sortAccessor: (row) => row.compatibilityStatus,
    },
    {
      key: "assignments",
      header: "Assignments",
      accessor: (row) => (
        <span className="tabular-nums text-sm">
          {row.readyCount}/{row.assignmentCount}
          {row.failedCount > 0 ? ` · ${row.failedCount} failed` : ""}
          {row.driftedCount > 0 ? ` · ${row.driftedCount} drifted` : ""}
        </span>
      ),
      sortAccessor: (row) => row.failedCount * 1000 + row.assignmentCount,
    },
    {
      key: "heartbeat",
      header: "Last heartbeat",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastHeartbeat ? formatRelativeTime(row.lastHeartbeat) : "—"}
        </span>
      ),
      sortAccessor: (row) => row.lastHeartbeat ?? "",
    },
  ];
  return (
    <PageShell>
      <PageHeader
        eyebrow="Continuous Delivery"
        title="Fleet"
        description="All environments. Click a cluster to open its Flux workspace — Sources, Bundles, Targets, Rollouts, and Deployments live there."
      />
      {query.isError && !isForbiddenError(query.error) && (
        <ErrorMessage error={query.error} />
      )}
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="space-y-2">
          <h2 className="text-sm font-semibold text-foreground">Cluster health</h2>
          <div className="grid grid-cols-2 gap-3">
            <FleetTile
              icon={<Radio className="h-4 w-4" />}
              title="Adopted"
              value={summary?.adoptedClusters ?? "—"}
              active={focus === "adopted"}
              onClick={() => setFocus("adopted")}
            />
            <FleetTile
              icon={<ServerCog className="h-4 w-4" />}
              title="Flux ready"
              value={summary?.fluxReady ?? "—"}
              active={focus === "flux_ready"}
              onClick={() => setFocus("flux_ready")}
            />
            <FleetTile
              icon={<AlertTriangle className="h-4 w-4" />}
              title="Incompatible"
              value={summary?.incompatible ?? "—"}
              active={focus === "incompatible"}
              onClick={() => setFocus("incompatible")}
            />
            <FleetTile
              icon={<Unplug className="h-4 w-4" />}
              title="Disconnected"
              value={summary?.disconnected ?? "—"}
              active={focus === "disconnected"}
              onClick={() => setFocus("disconnected")}
            />
          </div>
        </div>
        <div className="space-y-2">
          <h2 className="text-sm font-semibold text-foreground">Assignments</h2>
          <div className="grid grid-cols-2 gap-3">
            <FleetTile
              icon={<Layers className="h-4 w-4" />}
              title="Assigned"
              value={summary?.assignments ?? "—"}
              active={focus === "assignments"}
              onClick={() => setFocus("assignments")}
            />
            <FleetTile
              icon={<AlertTriangle className="h-4 w-4" />}
              title="Failed"
              value={summary?.failed ?? "—"}
              active={focus === "failed"}
              onClick={() => setFocus("failed")}
            />
            <FleetTile
              icon={<GitBranch className="h-4 w-4" />}
              title="Drifted"
              value={summary?.drifted ?? "—"}
              active={focus === "drifted"}
              onClick={() => setFocus("drifted")}
            />
            <FleetTile
              icon={<Rocket className="h-4 w-4" />}
              title="Active rollouts"
              value={summary?.activeRollouts ?? "—"}
              active={focus === "assignments"}
              onClick={() => setFocus("assignments")}
            />
          </div>
        </div>
      </div>
      <PageSection
        title="Needs attention"
        description="Disconnected agents, failed assignments, incompatible controllers, drift, and stale inventory."
      >
        <AttentionList items={fleet?.attention ?? []} loading={query.isLoading} />
      </PageSection>
      <PageSection
        title="Distributions"
        description="Adopted clusters only. Click a value to filter the table."
      >
        <div className="grid gap-4 md:grid-cols-3">
          <DistributionList
            title="Compatibility"
            items={fleet?.distributions.compatibility ?? []}
            activeKey={focus.startsWith("compatibility:") ? focus.slice("compatibility:".length) : ""}
            onSelect={(key) => setFocus(`compatibility:${key}`)}
          />
          <DistributionList
            title="Privilege"
            items={fleet?.distributions.privilege ?? []}
            activeKey={focus.startsWith("privilege:") ? focus.slice("privilege:".length) : ""}
            onSelect={(key) => setFocus(`privilege:${key}`)}
          />
          <DistributionList
            title="Assignment phases"
            items={fleet?.distributions.assignmentPhases ?? []}
            activeKey={focus.startsWith("phase:") ? focus.slice("phase:".length) : ""}
            onSelect={(key) => setFocus(`phase:${key}`)}
          />
        </div>
      </PageSection>
      <div id="fleet-clusters">
      <PageSection
        title="Clusters"
        description="Click a row to open that cluster's Flux workspace."
        actions={
          focus ? (
            <button
              type="button"
              onClick={() => setFocus("")}
              className="inline-flex h-8 items-center gap-1 rounded-md border border-border px-2 text-xs text-muted-foreground hover:bg-accent"
            >
              <X className="h-3 w-3" />
              {fleetFocusLabels[focus] ?? focus.replaceAll("_", " ")}
            </button>
          ) : null
        }
      >
        <DataTable
          data={visible}
          columns={columns}
          keyExtractor={(row) => row.id}
          loading={query.isLoading}
          isError={query.isError && !isForbiddenError(query.error)}
          onRetry={() => void query.refetch()}
          onRowClick={(row) => router.push(clusterHref(row.id))}
          emptyMessage={
            focus
              ? "No clusters match this filter."
              : "No clusters are registered."
          }
        />
      </PageSection>
      </div>
    </PageShell>
  );
}

function FleetTile({
  title,
  value,
  icon,
  active,
  onClick,
}: {
  title: string;
  value: string | number;
  icon: ReactNode;
  active?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex items-start justify-between rounded-lg border border-border bg-card p-4 text-left transition-colors hover:bg-accent/40 focus:outline-none focus:ring-2 focus:ring-ring",
        active && "ring-2 ring-ring",
      )}
    >
      <div className="min-w-0">
        <p className="text-xs font-medium text-muted-foreground">{title}</p>
        <p className="mt-1 text-2xl font-semibold tabular-nums tracking-tight text-foreground">
          {value}
        </p>
      </div>
      <div className="rounded-md bg-muted p-2 text-muted-foreground">{icon}</div>
    </button>
  );
}

function AttentionList({
  items,
  loading,
}: {
  items: DeliveryFleetAttention[];
  loading: boolean;
}) {
  if (!loading && items.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-card p-6 text-sm text-muted-foreground">
        No adopted clusters need attention.
      </div>
    );
  }
  return (
    <div className="space-y-2">
      {items.map((item) => (
        <Link
          key={`${item.clusterId}-${item.reason}`}
          href={clusterHref(item.clusterId)}
          className={
            item.severity === "error"
              ? "flex items-center justify-between rounded-md border border-status-error/30 bg-status-error/10 p-3"
              : "flex items-center justify-between rounded-md border border-status-warning/30 bg-status-warning/10 p-3"
          }
        >
          <span className="flex items-center gap-2">
            <AlertTriangle
              className={
                item.severity === "error"
                  ? "h-4 w-4 text-status-error"
                  : "h-4 w-4 text-status-warning"
              }
            />
            <span>
              <strong>{item.clusterName}</strong> — {item.detail}
            </span>
          </span>
          <DeliveryPhaseBadge value={item.reason} />
        </Link>
      ))}
    </div>
  );
}

function DistributionList({
  title,
  items,
  activeKey,
  onSelect,
}: {
  title: string;
  items: DeliveryFleetCount[];
  activeKey: string;
  onSelect: (key: string) => void;
}) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <h3 className="text-sm font-medium text-foreground">{title}</h3>
      {items.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">No adopted clusters.</p>
      ) : (
        <ul className="mt-3 space-y-2" aria-label={title}>
          {items.map((item) => (
            <li key={item.key}>
              <button
                type="button"
                onClick={() => onSelect(item.key)}
                className={cn(
                  "flex w-full items-center justify-between rounded-md px-1 py-1 text-sm hover:bg-accent",
                  activeKey === item.key && "bg-accent",
                )}
              >
                <DeliveryPhaseBadge value={item.key} />
                <span className="tabular-nums text-muted-foreground">
                  {item.count}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ProjectDeliveryOverview({
  projectId,
  projectQuery,
  projectsCount,
}: {
  projectId: string;
  projectQuery: { isLoading: boolean; isError: boolean; refetch: () => unknown };
  projectsCount: number;
}) {
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed =
    can(user, "delivery_targets", "list", scope) ||
    can(user, "delivery_deployments", "list", scope) ||
    can(user, "delivery_sources", "list", scope);
  const sources = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, { limit: 1 }),
    queryFn: () => listDeliverySources(projectId, { limit: 1 }),
    enabled: Boolean(projectId && can(user, "delivery_sources", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const unhealthySources = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, {
      limit: 1,
      status: "degraded",
    }),
    queryFn: () =>
      listDeliverySources(projectId, { limit: 1, status: "degraded" }),
    enabled: Boolean(projectId && can(user, "delivery_sources", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const bundles = useQuery({
    queryKey: queryKeys.delivery.bundles(projectId, { limit: 1 }),
    queryFn: () => listComponentBundles(projectId, { limit: 1 }),
    enabled: Boolean(projectId && can(user, "delivery_bundles", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const targets = useQuery({
    queryKey: queryKeys.delivery.targets(projectId, { limit: 1 }),
    queryFn: () => listDeliveryTargets(projectId, { limit: 1 }),
    enabled: Boolean(projectId && can(user, "delivery_targets", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const rollouts = useQuery({
    queryKey: queryKeys.delivery.rollouts(projectId, { limit: 10 }),
    queryFn: () => listDeliveryRollouts(projectId, { limit: 10 }),
    enabled: Boolean(
      projectId && can(user, "delivery_rollouts", "list", scope),
    ),
    refetchInterval: liveFallback(10_000),
  });
  const deployments = useQuery({
    queryKey: queryKeys.delivery.deployments(projectId, { limit: 10 }),
    queryFn: () => listClusterDeployments(projectId, { limit: 10 }),
    enabled: Boolean(
      projectId && can(user, "delivery_deployments", "list", scope),
    ),
    refetchInterval: liveFallback(10_000),
  });
  const system = useQuery({
    queryKey: queryKeys.delivery.system,
    queryFn: getDeliverySystemCompatibility,
    enabled: can(user, "delivery_platform", "read"),
    refetchInterval: liveFallback(30_000),
  });
  const { projects } = useDeliveryProjectScope();
  const clusterId = projectClusterId(
    projects.find((project) => project.id === projectId) ?? {},
  );
  const deploymentRows = deployments.data?.data ?? [];
  const failures = deploymentRows.filter(
    (item) =>
      item.phase === "failed" ||
      item.phase === "degraded" ||
      item.phase === "unknown",
  );
  const drifted = deploymentRows.filter((item) =>
    item.conditions.some(
      (condition) =>
        condition.type === "Drifted" && condition.status === "True",
    ),
  ).length;
  const incompatibleClusters = (system.data?.observedInventory ?? [])
    .filter((item) => item.compatibilityStatus !== "compatible")
    .reduce((total, item) => total + item.clusterCount, 0);
  return (
    <DeliveryProjectGate
      projectId={projectId}
      loading={projectQuery.isLoading}
      error={projectQuery.isError}
      projectsCount={projectsCount}
      permission="delivery resources:list"
      allowed={allowed}
      onRetry={() => void projectQuery.refetch()}
    >
      <PageShell>
        <PageHeader
          eyebrow="Continuous Delivery"
          title="Delivery overview"
          description="Astronomer-owned intent and rollout policy with local, pull-based convergence on managed clusters."
        />
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
          <MetricLink
            href="sources"
            projectId={projectId}
            clusterId={clusterId}
            icon={GitBranch}
            label="Sources"
            value={sources.data?.count ?? "—"}
          />
          <MetricLink
            href="bundles"
            projectId={projectId}
            clusterId={clusterId}
            icon={Boxes}
            label="Bundles"
            value={bundles.data?.count ?? "—"}
          />
          <MetricLink
            href="targets"
            projectId={projectId}
            clusterId={clusterId}
            icon={Crosshair}
            label="Targets"
            value={targets.data?.count ?? "—"}
          />
          <MetricLink
            href="rollouts"
            projectId={projectId}
            clusterId={clusterId}
            icon={Rocket}
            label="Active (latest 10)"
            value={
              (rollouts.data?.data ?? []).filter((row) =>
                [
                  "queued",
                  "progressing",
                  "paused",
                  "awaiting_approval",
                  "rolling_back",
                ].includes(row.state),
              ).length
            }
          />
          <MetricLink
            href="deployments"
            projectId={projectId}
            clusterId={clusterId}
            icon={Layers}
            label="Drifted (loaded page)"
            value={drifted}
          />
          <Link
            href="/dashboard/agents"
            className="block rounded-lg focus:outline-none focus:ring-2 focus:ring-ring"
          >
            <MetricCard
              icon={<ServerCog className="h-4 w-4" />}
              title="Incompatible clusters"
              value={system.isLoading ? "—" : incompatibleClusters}
            />
          </Link>
        </div>
        {unhealthySources.data && unhealthySources.data.count > 0 && (
          <Link
            href={`/dashboard/delivery/sources?project=${encodeURIComponent(projectId)}&status=degraded`}
            className="flex items-center justify-between rounded-md border border-status-warning/30 bg-status-warning/10 p-3 text-sm"
          >
            <span className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-status-warning" />
              {unhealthySources.data.count} degraded delivery source
              {unhealthySources.data.count === 1 ? "" : "s"}
            </span>
            <DeliveryPhaseBadge value="degraded" />
          </Link>
        )}
        {system.isError && <ErrorMessage error={system.error} />}
        {system.data && (
          <PageSection
            title="Delivery system"
            description="Pinned distribution and exact controller compatibility; workload delivery remains isolated from system upgrades."
          >
            <DetailGrid>
              <Detail
                label="Distribution"
                value={
                  stringField(system.data.currentRelease, "version") ||
                  system.data.contract.fluxVersion
                }
              />
              <Detail
                label="Flux controllers"
                value={system.data.contract.fluxVersion}
              />
              <Detail
                label="Kubernetes support"
                value={`${system.data.contract.kubernetesMinimum} – ${system.data.contract.kubernetesMaximum}`}
              />
              <Detail
                label="Protocol"
                value={system.data.contract.agentProtocol}
              />
              <Detail
                label="System rollout"
                value={
                  <DeliveryPhaseBadge
                    value={
                      stringField(system.data.currentRollout, "state") ||
                      "idle"
                    }
                  />
                }
              />
              <Detail
                label="Required capabilities"
                value={system.data.contract.requiredCapabilities.join(", ")}
              />
            </DetailGrid>
            {system.data.observedInventory.length > 0 && (
              <div
                className="mt-4 flex flex-wrap gap-2"
                aria-label="Observed controller compatibility"
              >
                {system.data.observedInventory.map((item) => (
                  <span
                    key={item.compatibilityStatus}
                    className="inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-sm"
                  >
                    <DeliveryPhaseBadge value={item.compatibilityStatus} />
                    <span className="tabular-nums">{item.clusterCount}</span>
                  </span>
                ))}
              </div>
            )}
          </PageSection>
        )}
        <PageSection
          title="Recent operator attention"
          description="Failures, degraded convergence, stale state, and rollback failures from the latest server page."
        >
          {failures.length === 0 &&
          !(rollouts.data?.data ?? []).some(
            (item) => item.state === "rollback_failed",
          ) ? (
            <div className="rounded-lg border border-border bg-card p-6 text-sm text-muted-foreground">
              No recent delivery failures are visible in this page.
            </div>
          ) : (
            <div className="space-y-2">
              {failures.map((deployment) => (
                <Link
                  key={deployment.id}
                  href={`/dashboard/delivery/deployments/${deployment.id}?project=${encodeURIComponent(projectId)}`}
                  className="flex items-center justify-between rounded-md border border-status-warning/30 bg-status-warning/10 p-3"
                >
                  <span className="flex items-center gap-2">
                    <AlertTriangle className="h-4 w-4 text-status-warning" />
                    <span>
                      <strong>{deployment.phase}</strong> on cluster{" "}
                      <code className="text-xs">{deployment.clusterId}</code>
                    </span>
                  </span>
                  <DeliveryPhaseBadge value={deployment.phase} />
                </Link>
              ))}
              {(rollouts.data?.data ?? [])
                .filter((item) => item.state === "rollback_failed")
                .map((rollout) => (
                  <Link
                    key={rollout.id}
                    href={`/dashboard/delivery/rollouts/${rollout.id}?project=${encodeURIComponent(projectId)}`}
                    className="flex items-center justify-between rounded-md border border-status-error/30 bg-status-error/10 p-3"
                  >
                    <span className="flex items-center gap-2">
                      <AlertTriangle className="h-4 w-4 text-status-error" />{" "}
                      Rollback failed for rollout{" "}
                      <code className="text-xs">{rollout.id}</code>
                    </span>
                    <DeliveryPhaseBadge value={rollout.state} />
                  </Link>
                ))}
            </div>
          )}
        </PageSection>
      </PageShell>
    </DeliveryProjectGate>
  );
}

function MetricLink({
  href,
  projectId,
  clusterId,
  icon: Icon,
  label,
  value,
}: {
  href: string;
  projectId: string;
  clusterId?: string;
  icon: typeof ServerCog;
  label: string;
  value: string | number;
}) {
  const target = clusterId
    ? `${clusterDeliveryPath(clusterId, href)}?project=${encodeURIComponent(projectId)}`
    : `/dashboard/delivery/${href}?project=${encodeURIComponent(projectId)}`;
  return (
    <Link
      href={target}
      className="block rounded-lg focus:outline-none focus:ring-2 focus:ring-ring"
    >
      <MetricCard
        icon={<Icon className="h-4 w-4" />}
        title={label}
        value={value}
      />
    </Link>
  );
}
function stringField(
  value: Record<string, unknown> | null,
  field: string,
): string {
  const item = value?.[field];
  return typeof item === "string" ? item : "";
}
export const Route = createFileRoute("/dashboard/delivery/")({
  component: DeliveryOverviewPage,
});
