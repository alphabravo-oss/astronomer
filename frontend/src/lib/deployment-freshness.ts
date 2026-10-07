import type {
  ClusterDeployment,
  DeliveryConditionView,
} from "@/lib/api/delivery-deployments";
import {
  deliveryObservationFreshness,
  type ObservationFreshness,
} from "./delivery-observation-freshness";
export function deploymentFreshness(
  deployment: Pick<ClusterDeployment, "inventory" | "phase">,
  now: number,
) {
  const freshness = deliveryObservationFreshness(
    deployment.inventory.observation,
    now,
  );
  return {
    ...freshness,
    phase:
      freshness.state === "current" || freshness.state === "unknown"
        ? deployment.phase
        : freshness.state,
  };
}
export function reportedConditionStatus(
  condition: Pick<DeliveryConditionView, "type" | "status">,
) {
  return condition.status === "True"
    ? condition.type === "Ready"
      ? "ready"
      : condition.type.toLowerCase()
    : condition.status.toLowerCase();
}
export function deploymentDrift(
  freshness: ObservationFreshness,
  conditions: DeliveryConditionView[],
) {
  const drifted = conditions.some(
    (condition) => condition.type === "Drifted" && condition.status === "True",
  );
  if (freshness.state === "current")
    return drifted ? "Drift reported" : "No drift reported";
  if (freshness.state === "unknown") return "Source freshness unknown";
  return "Drift evidence " + freshness.state;
}
