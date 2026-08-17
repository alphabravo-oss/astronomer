import { createFileRoute } from "@tanstack/react-router";
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
import { Link } from "@/lib/link";
import { MetricCard } from "@/components/ui/metric-card";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  ErrorMessage,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import {
  getDeliverySystemCompatibility,
  listClusterDeployments,
  listComponentBundles,
  listDeliveryRollouts,
  listDeliverySources,
  listDeliveryTargets,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { liveFallback } from "@/lib/live/status-store";

function DeliveryOverviewPage() {
  const { projectId, projects, projectQuery, setProjectId } =
    useDeliveryProjectScope();
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
    <DeliveryShell
      projectId={projectId}
      projects={projects}
      setProjectId={setProjectId}
    >
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
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
              icon={GitBranch}
              label="Sources"
              value={sources.data?.count ?? "—"}
            />
            <MetricLink
              href="bundles"
              projectId={projectId}
              icon={Boxes}
              label="Bundles"
              value={bundles.data?.count ?? "—"}
            />
            <MetricLink
              href="targets"
              projectId={projectId}
              icon={Crosshair}
              label="Targets"
              value={targets.data?.count ?? "—"}
            />
            <MetricLink
              href="rollouts"
              projectId={projectId}
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
                <div className="mt-4 flex flex-wrap gap-2" aria-label="Observed controller compatibility">
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
    </DeliveryShell>
  );
}

function MetricLink({
  href,
  projectId,
  icon: Icon,
  label,
  value,
}: {
  href: string;
  projectId: string;
  icon: typeof ServerCog;
  label: string;
  value: string | number;
}) {
  return (
    <Link
      href={`/dashboard/delivery/${href}?project=${encodeURIComponent(projectId)}`}
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
