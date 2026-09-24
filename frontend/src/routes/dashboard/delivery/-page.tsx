import { clusterHref, useEstateFocus } from "./-estate-focus";
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
import type { ReactNode } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import { MetricCard } from "@/components/ui/metric-card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  ErrorMessage,
  deliveryEntityPath,
  projectClusterId,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import { EstateHeader } from "@/components/delivery/estate-header";
import {
  DeliveryUnavailablePanel,
  useDeliveryOverviewHealth,
} from "@/components/delivery/overview-health";
import { listClusterDeployments } from "@/lib/api/delivery-deployments";
import { listComponentBundles } from "@/lib/api/delivery-bundles";
import { listDeliveryRollouts } from "@/lib/api/delivery-rollouts";
import { listDeliverySources } from "@/lib/api/delivery-sources";
import { listDeliveryTargets } from "@/lib/api/delivery-targets";
import {
  getDeliveryEstate,
  getDeliverySystemCompatibility,
  type DeliveryEstate,
  type DeliveryEstateAttention,
  type DeliveryEstateCluster,
  type DeliveryEstateCount,
} from "@/lib/api/delivery-system";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { pageRowCount, pageCountLabel } from "@/lib/api/pagination";
import { cn, formatRelativeTime } from "@/lib/utils";

function isForbiddenError(error: unknown): boolean {
  return Boolean(
    error &&
    typeof error === "object" &&
    "response" in error &&
    (error as { response?: { status?: number } }).response?.status === 403,
  );
}

export function DeliveryOverviewPage() {
  const { projectId, projects, projectQuery, setProjectId } =
    useDeliveryProjectScope();
  const { data: user } = useCurrentUser();
  const canReadEstate = can(user, "delivery_inventory", "read");
  const estate = useQuery({
    queryKey: queryKeys.delivery.estate,
    queryFn: ({ signal }) => getDeliveryEstate(signal),
    enabled: canReadEstate,
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
    [queryKeys.delivery.estate],
  );
  const showEstate = canReadEstate && !isForbiddenError(estate.error);
  if (showEstate) {
    return <EstateDeliveryOverview query={estate} />;
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

const estateFocusLabels: Record<string, string> = {
  adopted: "Adopted clusters",
  flux_ready: "Flux-ready clusters",
  incompatible: "Incompatible clusters",
  disconnected: "Disconnected clusters",
  assignments: "Clusters with assignments",
  failed: "Clusters with failed assignments",
  drifted: "Clusters with drift",
};

function EstateDeliveryOverview({
  query,
}: {
  query: UseQueryResult<DeliveryEstate>;
}) {
  const summary = query.data?.summary;
  const { focus, visible, setFocus, navigate } = useEstateFocus(
    query.data?.clusters ?? [],
  );

  const columns: Column<DeliveryEstateCluster>[] = [
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
          {row.environment || "—"}
        </span>
      ),
      sortAccessor: (row) => row.environment || "",
    },
    {
      key: "role",
      header: "Role",
      accessor: (row) =>
        row.isLocal ? (
          <span className="text-xs text-muted-foreground">Local host-only</span>
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
          value={
            row.connected ? (row.stale ? "stale" : "connected") : "disconnected"
          }
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
      <EstateHeader />
      {query.isError && !isForbiddenError(query.error) && (
        <ErrorMessage error={query.error} />
      )}
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="space-y-2">
          <h2 className="text-sm font-semibold text-foreground">
            Cluster health
          </h2>
          <div className="grid grid-cols-2 gap-3">
            <EstateTile
              icon={<Radio className="h-4 w-4" />}
              title="Adopted"
              value={summary?.managedClusters ?? "—"}
              active={focus === "adopted"}
              onClick={() => setFocus("adopted")}
            />
            <EstateTile
              icon={<ServerCog className="h-4 w-4" />}
              title="Flux ready"
              value={summary?.fluxReady ?? "—"}
              active={focus === "flux_ready"}
              onClick={() => setFocus("flux_ready")}
            />
            <EstateTile
              icon={<AlertTriangle className="h-4 w-4" />}
              title="Incompatible"
              value={summary?.incompatible ?? "—"}
              active={focus === "incompatible"}
              onClick={() => setFocus("incompatible")}
            />
            <EstateTile
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
            <EstateTile
              icon={<Layers className="h-4 w-4" />}
              title="Assigned"
              value={summary?.assignments ?? "—"}
              active={focus === "assignments"}
              onClick={() => setFocus("assignments")}
            />
            <EstateTile
              icon={<AlertTriangle className="h-4 w-4" />}
              title="Failed"
              value={summary?.failed ?? "—"}
              active={focus === "failed"}
              onClick={() => setFocus("failed")}
            />
            <EstateTile
              icon={<GitBranch className="h-4 w-4" />}
              title="Drifted"
              value={summary?.drifted ?? "—"}
              active={focus === "drifted"}
              onClick={() => setFocus("drifted")}
            />
            <EstateTile
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
        <AttentionList
          items={query.data?.attention ?? []}
          loading={query.isLoading}
        />
      </PageSection>
      <PageSection
        title="Distributions"
        description="Adopted clusters only. Click a value to filter the table."
      >
        <div className="grid gap-4 md:grid-cols-3">
          <DistributionList
            title="Compatibility"
            items={query.data?.distributions.compatibility ?? []}
            activeKey={
              focus.startsWith("compatibility:")
                ? focus.slice("compatibility:".length)
                : ""
            }
            onSelect={(key) => setFocus(`compatibility:${key}`)}
          />
          <DistributionList
            title="Privilege"
            items={query.data?.distributions.privilege ?? []}
            activeKey={
              focus.startsWith("privilege:")
                ? focus.slice("privilege:".length)
                : ""
            }
            onSelect={(key) => setFocus(`privilege:${key}`)}
          />
          <DistributionList
            title="Assignment phases"
            items={query.data?.distributions.assignmentPhases ?? []}
            activeKey={
              focus.startsWith("phase:") ? focus.slice("phase:".length) : ""
            }
            onSelect={(key) => setFocus(`phase:${key}`)}
          />
        </div>
      </PageSection>
      <div id="estate-clusters">
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
                {estateFocusLabels[focus] ?? focus.replaceAll("_", " ")}
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
            onRowClick={(row) => void navigate({ to: clusterHref(row.id) })}
            filtersActive={!!focus}
            onClearFilters={() => setFocus("")}
            emptyState={{
              title: "No clusters registered",
              description:
                "Register a cluster to inspect its delivery readiness.",
              action: {
                label: "Register cluster",
                href: "/dashboard/clusters/register",
              },
            }}
          />
        </PageSection>
      </div>
    </PageShell>
  );
}

function EstateTile({
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
        "flex items-start justify-between rounded-lg border border-border bg-card p-4 text-left transition-colors hover:bg-accent/40 focus:outline-hidden focus:ring-2 focus:ring-ring",
        active && "ring-2 ring-ring",
      )}
    >
      <div className="min-w-0">
        <p className="text-xs font-medium text-muted-foreground">{title}</p>
        <p className="mt-1 text-2xl font-semibold tabular-nums tracking-tight text-foreground">
          {value}
        </p>
      </div>
      <div className="rounded-md bg-muted p-2 text-muted-foreground">
        {icon}
      </div>
    </button>
  );
}

function AttentionList({
  items,
  loading,
}: {
  items: DeliveryEstateAttention[];
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
        <RouterLink
          key={`${item.clusterId}-${item.reason}`}
          to={clusterHref(item.clusterId)}
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
        </RouterLink>
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
  items: DeliveryEstateCount[];
  activeKey: string;
  onSelect: (key: string) => void;
}) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <h3 className="text-sm font-medium text-foreground">{title}</h3>
      {items.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">
          No adopted clusters.
        </p>
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

function useProjectDeliveryQueries(projectId: string) {
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed =
    can(user, "delivery_targets", "list", scope) ||
    can(user, "delivery_deployments", "list", scope) ||
    can(user, "delivery_sources", "list", scope);
  const sources = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, { limit: 1 }),
    queryFn: ({ signal }) =>
      listDeliverySources(projectId, { limit: 1 }, signal),
    enabled: Boolean(projectId && can(user, "delivery_sources", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const unhealthySources = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, {
      limit: 1,
      status: "degraded",
    }),
    queryFn: ({ signal }) =>
      listDeliverySources(projectId, { limit: 1, status: "degraded" }, signal),
    enabled: Boolean(projectId && can(user, "delivery_sources", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const bundles = useQuery({
    queryKey: queryKeys.delivery.bundles(projectId, { limit: 1 }),
    queryFn: ({ signal }) =>
      listComponentBundles(projectId, { limit: 1 }, signal),
    enabled: Boolean(projectId && can(user, "delivery_bundles", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const targets = useQuery({
    queryKey: queryKeys.delivery.targets(projectId, { limit: 1 }),
    queryFn: ({ signal }) =>
      listDeliveryTargets(projectId, { limit: 1 }, signal),
    enabled: Boolean(projectId && can(user, "delivery_targets", "list", scope)),
    refetchInterval: liveFallback(30_000),
  });
  const rollouts = useQuery({
    queryKey: queryKeys.delivery.rollouts(projectId, { limit: 10 }),
    queryFn: ({ signal }) =>
      listDeliveryRollouts(projectId, { limit: 10 }, signal),
    enabled: Boolean(
      projectId && can(user, "delivery_rollouts", "list", scope),
    ),
    refetchInterval: liveFallback(10_000),
  });
  const deployments = useQuery({
    queryKey: queryKeys.delivery.deployments(projectId, { limit: 10 }),
    queryFn: ({ signal }) =>
      listClusterDeployments(projectId, { limit: 10 }, signal),
    enabled: Boolean(
      projectId && can(user, "delivery_deployments", "list", scope),
    ),
    refetchInterval: liveFallback(10_000),
  });
  const system = useQuery({
    queryKey: queryKeys.delivery.system,
    queryFn: ({ signal }) => getDeliverySystemCompatibility(signal),
    enabled: can(user, "delivery_platform", "read"),
    refetchInterval: liveFallback(30_000),
  });
  return {
    allowed,
    sources,
    unhealthySources,
    bundles,
    targets,
    rollouts,
    deployments,
    system,
  };
}

function ProjectDeliveryOverview({
  projectId,
  projectQuery,
  projectsCount,
}: {
  projectId: string;
  projectQuery: {
    isLoading: boolean;
    isError: boolean;
    refetch: () => unknown;
  };
  projectsCount: number;
}) {
  const {
    allowed,
    sources,
    unhealthySources,
    bundles,
    targets,
    rollouts,
    deployments,
    system,
  } = useProjectDeliveryQueries(projectId);
  const { projects } = useDeliveryProjectScope();
  const clusterId = projectClusterId(
    projects.find((project) => project.id === projectId) ?? {},
  );
  const {
    failedQueries,
    failures,
    drifted,
    activeRollouts,
    incompatibleClusters,
  } = useDeliveryOverviewHealth({
    sources,
    unhealthySources,
    bundles,
    targets,
    rollouts,
    deployments,
    system,
  });
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
        <DeliveryUnavailablePanel failedQueries={failedQueries} />
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
          <MetricLink
            section="sources"
            projectId={projectId}
            clusterId={clusterId}
            icon={GitBranch}
            label="Sources"
            value={
              sources.isError
                ? "—"
                : sources.data
                  ? pageCountLabel(sources.data)
                  : "—"
            }
            unavailable={sources.isError}
          />
          <MetricLink
            section="bundles"
            projectId={projectId}
            clusterId={clusterId}
            icon={Boxes}
            label="Bundles"
            value={
              bundles.isError
                ? "—"
                : bundles.data
                  ? pageCountLabel(bundles.data)
                  : "—"
            }
            unavailable={bundles.isError}
          />
          <MetricLink
            section="targets"
            projectId={projectId}
            clusterId={clusterId}
            icon={Crosshair}
            label="Targets"
            value={
              targets.isError
                ? "—"
                : targets.data
                  ? pageCountLabel(targets.data)
                  : "—"
            }
            unavailable={targets.isError}
          />
          <MetricLink
            section="rollouts"
            projectId={projectId}
            clusterId={clusterId}
            icon={Rocket}
            label="Active (latest 10)"
            value={activeRollouts ?? "—"}
            unavailable={rollouts.isError}
          />
          <MetricLink
            section="deployments"
            projectId={projectId}
            clusterId={clusterId}
            icon={Layers}
            label="Drifted (loaded page)"
            value={drifted ?? "—"}
            unavailable={deployments.isError}
          />
          <RouterLink
            to="/dashboard/agents"
            className="block rounded-lg focus:outline-hidden focus:ring-2 focus:ring-ring"
            aria-label={
              system.isError ? "Incompatible clusters unavailable" : undefined
            }
          >
            <MetricCard
              icon={<ServerCog className="h-4 w-4" />}
              title="Incompatible clusters"
              value={system.isLoading ? "—" : (incompatibleClusters ?? "—")}
            />
          </RouterLink>
        </div>
        {unhealthySources.isSuccess &&
          pageRowCount(unhealthySources.data) > 0 && (
            <RouterLink
              to="/dashboard/delivery/sources"
              search={{ project: projectId, status: "degraded" }}
              className="flex items-center justify-between rounded-md border border-status-warning/30 bg-status-warning/10 p-3 text-sm"
            >
              <span className="flex items-center gap-2">
                <AlertTriangle className="h-4 w-4 text-status-warning" />
                {pageCountLabel(unhealthySources.data)} degraded delivery source
                {pageRowCount(unhealthySources.data) === 1 ? "" : "s"}
              </span>
              <DeliveryPhaseBadge value="degraded" />
            </RouterLink>
          )}
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
                      stringField(system.data.currentRollout, "state") || "idle"
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
          {deployments.isError || rollouts.isError ? (
            <div
              className="rounded-lg border border-dashed border-border bg-card p-6 text-sm text-muted-foreground"
              aria-label="unavailable"
            >
              Recent operator attention is unavailable while some delivery data
              failed to load — see the banner above.
            </div>
          ) : failures.length === 0 &&
            !(rollouts.data?.data ?? []).some(
              (item) => item.state === "rollback_failed",
            ) ? (
            <div className="rounded-lg border border-border bg-card p-6 text-sm text-muted-foreground">
              No recent delivery failures are visible in this page.
            </div>
          ) : (
            <div className="space-y-2">
              {failures.map((deployment) => (
                <RouterLink
                  key={deployment.id}
                  to={deliveryEntityPath("deployments", deployment.id, {
                    clusterId: deployment.clusterId || clusterId,
                    projectId,
                  })}
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
                </RouterLink>
              ))}
              {(rollouts.data?.data ?? [])
                .filter((item) => item.state === "rollback_failed")
                .map((rollout) => (
                  <RouterLink
                    key={rollout.id}
                    to={deliveryEntityPath("rollouts", rollout.id, {
                      clusterId,
                      projectId,
                    })}
                    className="flex items-center justify-between rounded-md border border-status-error/30 bg-status-error/10 p-3"
                  >
                    <span className="flex items-center gap-2">
                      <AlertTriangle className="h-4 w-4 text-status-error" />{" "}
                      Rollback failed for rollout{" "}
                      <code className="text-xs">{rollout.id}</code>
                    </span>
                    <DeliveryPhaseBadge value={rollout.state} />
                  </RouterLink>
                ))}
            </div>
          )}
        </PageSection>
      </PageShell>
    </DeliveryProjectGate>
  );
}

function MetricLink({
  section,
  projectId,
  clusterId,
  icon: Icon,
  label,
  value,
  unavailable,
}: {
  section: "sources" | "bundles" | "targets" | "rollouts" | "deployments";
  projectId: string;
  clusterId?: string;
  icon: typeof ServerCog;
  label: string;
  value: string | number;
  unavailable?: boolean;
}) {
  const card = (
    <MetricCard
      icon={<Icon className="h-4 w-4" />}
      title={label}
      value={value}
    />
  );
  const className =
    "block rounded-lg focus:outline-hidden focus:ring-2 focus:ring-ring";
  const ariaLabel = unavailable ? `${label} unavailable` : undefined;

  const clusterRoutes = {
    sources: "/dashboard/clusters/$id/delivery/sources",
    bundles: "/dashboard/clusters/$id/delivery/bundles",
    targets: "/dashboard/clusters/$id/delivery/targets",
    rollouts: "/dashboard/clusters/$id/delivery/rollouts",
    deployments: "/dashboard/clusters/$id/delivery/deployments",
  } as const;
  const projectRoutes = {
    sources: "/dashboard/delivery/sources",
    bundles: "/dashboard/delivery/bundles",
    targets: "/dashboard/delivery/targets",
    rollouts: "/dashboard/delivery/rollouts",
    deployments: "/dashboard/delivery/deployments",
  } as const;

  if (clusterId) {
    return (
      <RouterLink
        to={clusterRoutes[section]}
        params={{ id: clusterId }}
        search={{ project: projectId }}
        className={className}
        aria-label={ariaLabel}
      >
        {card}
      </RouterLink>
    );
  }

  return (
    <RouterLink
      to={projectRoutes[section]}
      search={{ project: projectId }}
      className={className}
      aria-label={ariaLabel}
    >
      {card}
    </RouterLink>
  );
}
function stringField(
  value: Record<string, unknown> | null,
  field: string,
): string {
  const item = value?.[field];
  return typeof item === "string" ? item : "";
}
