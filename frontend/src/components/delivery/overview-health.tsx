import type { UseQueryResult } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import type { ClusterDeployment } from "@/lib/api/delivery-deployments";
import type { DeliveryRollout } from "@/lib/api/delivery-rollouts";
import type { DeliverySystemCompatibility } from "@/lib/api/delivery-system";
import type { PaginatedResponse } from "@/types";

export type DeliveryQueryHealthEntry = { label: string; query: UseQueryResult<unknown> };

// Pure post-processing over the seven overview queries — which failed (danger
// panel + tile dashes) and the derived counts that must never fall back to
// `?? []` on a failed query (that would silently read an outage as "zero").
export function useDeliveryOverviewHealth(queries: {
  sources: UseQueryResult<unknown>;
  unhealthySources: UseQueryResult<unknown>;
  bundles: UseQueryResult<unknown>;
  targets: UseQueryResult<unknown>;
  rollouts: UseQueryResult<PaginatedResponse<DeliveryRollout>>;
  deployments: UseQueryResult<PaginatedResponse<ClusterDeployment>>;
  system: UseQueryResult<DeliverySystemCompatibility>;
}) {
  const { sources, unhealthySources, bundles, targets, rollouts, deployments, system } =
    queries;
  const failedQueries: DeliveryQueryHealthEntry[] = [
    { label: "Sources", query: sources },
    { label: "Degraded sources", query: unhealthySources },
    { label: "Bundles", query: bundles },
    { label: "Targets", query: targets },
    { label: "Rollouts", query: rollouts },
    { label: "Deployments", query: deployments },
    { label: "Delivery system", query: system },
  ].filter((entry) => entry.query.isError);
  const deploymentRows = deployments.isError
    ? []
    : (deployments.data?.data ?? []);
  const failures = deploymentRows.filter(
    (item) =>
      item.phase === "failed" ||
      item.phase === "degraded" ||
      item.phase === "unknown",
  );
  const drifted = deployments.isError
    ? undefined
    : deploymentRows.filter((item) =>
        item.conditions.some(
          (c) => c.type === "Drifted" && c.status === "True",
        ),
      ).length;
  const activeRollouts = rollouts.isError
    ? undefined
    : (rollouts.data?.data ?? []).filter((row) =>
        ["queued", "progressing", "paused", "awaiting_approval", "rolling_back"].includes(
          row.state,
        ),
      ).length;
  const incompatibleClusters = system.isError
    ? undefined
    : (system.data?.observedInventory ?? [])
        .filter((item) => item.compatibilityStatus !== "compatible")
        .reduce((total, item) => total + item.clusterCount, 0);
  return { failedQueries, failures, drifted, activeRollouts, incompatibleClusters };
}

export function DeliveryUnavailablePanel({
  failedQueries,
}: {
  failedQueries: DeliveryQueryHealthEntry[];
}) {
  if (failedQueries.length === 0) return null;
  return (
    <StatePanel
      icon={AlertTriangle}
      tone="danger"
      role="alert"
      title="Delivery status unavailable"
      description={`${failedQueries.map((entry) => entry.label).join(", ")} could not be loaded. Counts below are hidden until this recovers.`}
      actionLabel="Retry"
      onAction={() => failedQueries.forEach((entry) => void entry.query.refetch())}
    />
  );
}
