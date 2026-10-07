import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  Boxes,
  Crosshair,
  GitBranch,
  Layers,
  Rocket,
  ServerCog,
} from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";
import { MetricCard } from "@/components/ui/metric-card";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  Detail,
  DetailGrid,
  deliveryEntityPath,
  projectClusterId,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import {
  DeliveryUnavailablePanel,
  useDeliveryOverviewHealth,
} from "@/components/delivery/overview-health";
import { listClusterDeployments } from "@/lib/api/delivery-deployments";
import { listComponentBundles } from "@/lib/api/delivery-bundles";
import { listDeliveryRollouts } from "@/lib/api/delivery-rollouts";
import { listDeliverySources } from "@/lib/api/delivery-sources";
import { listDeliveryTargets } from "@/lib/api/delivery-targets";
import { getDeliverySystemCompatibility } from "@/lib/api/delivery-system";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { liveFallback } from "@/lib/live/status-store";
import { pageRowCount, pageCountLabel } from "@/lib/api/pagination";

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

export function ProjectDeliveryOverview({
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
          <SystemCompatibilityMetric
            system={system}
            count={incompatibleClusters}
          />
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
        {system.data && <DeliverySystemSection system={system.data} />}
        <RecentOperatorAttention
          deployments={deployments}
          rollouts={rollouts}
          failures={failures}
          projectId={projectId}
          clusterId={clusterId}
        />
      </PageShell>
    </DeliveryProjectGate>
  );
}

function SystemCompatibilityMetric({
  system,
  count,
}: {
  system: ReturnType<typeof useProjectDeliveryQueries>["system"];
  count?: number;
}) {
  if (!system.data && !system.isFetching && !system.isError) return null;
  return (
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
        value={system.isLoading ? "—" : (count ?? "—")}
      />
    </RouterLink>
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
  const { data: user } = useCurrentUser();
  if (
    !can(user, `delivery_${section}`, "list", {
      type: "project",
      id: projectId,
    })
  )
    return null;
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

function DeliverySystemSection({
  system,
}: {
  system: NonNullable<
    ReturnType<typeof useProjectDeliveryQueries>["system"]["data"]
  >;
}) {
  return (
    <PageSection
      title="Delivery system"
      description="Pinned distribution and exact controller compatibility; workload delivery remains isolated from system upgrades."
    >
      <DetailGrid>
        <Detail
          label="Distribution"
          value={
            stringField(system.currentRelease, "version") ||
            system.contract.fluxVersion
          }
        />
        <Detail label="Flux controllers" value={system.contract.fluxVersion} />
        <Detail
          label="Kubernetes support"
          value={`${system.contract.kubernetesMinimum} – ${system.contract.kubernetesMaximum}`}
        />
        <Detail label="Protocol" value={system.contract.agentProtocol} />
        <Detail
          label="System rollout"
          value={
            <DeliveryPhaseBadge
              value={stringField(system.currentRollout, "state") || "idle"}
            />
          }
        />
        <Detail
          label="Required capabilities"
          value={system.contract.requiredCapabilities.join(", ")}
        />
      </DetailGrid>
      {system.observedInventory.length > 0 && (
        <div
          className="mt-4 flex flex-wrap gap-2"
          aria-label="Observed controller compatibility"
        >
          {system.observedInventory.map((item) => (
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
  );
}

function RecentOperatorAttention({
  deployments,
  rollouts,
  failures,
  projectId,
  clusterId,
}: {
  deployments: ReturnType<typeof useProjectDeliveryQueries>["deployments"];
  rollouts: ReturnType<typeof useProjectDeliveryQueries>["rollouts"];
  failures: ReturnType<typeof useDeliveryOverviewHealth>["failures"];
  projectId: string;
  clusterId?: string;
}) {
  return (
    <PageSection
      title="Recent operator attention"
      description="Failures, degraded convergence, stale state, and rollback failures from the latest server page."
    >
      {!deployments.data && !rollouts.data ? (
        <p className="text-sm text-muted-foreground">
          Recent operator attention is unavailable for the current access or
          while data loads.
        </p>
      ) : deployments.isError || rollouts.isError ? (
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
  );
}
