import { DeliveryPhaseBadge } from "@/components/delivery/shared";
import type { ClusterDeployment } from "@/lib/api/delivery-deployments";
import { deploymentFreshness } from "@/lib/deployment-freshness";
import { formatRelativeTime } from "@/lib/utils";
export function DeploymentStatus({
  deployment,
  now,
}: {
  deployment: ClusterDeployment;
  now: number;
}) {
  const freshness = deploymentFreshness(deployment, now);
  return (
    <div>
      {freshness.state === "unknown" ? (
        <span>{deployment.phase}</span>
      ) : (
        <DeliveryPhaseBadge value={freshness.phase} />
      )}
      {freshness.state === "unknown" ? (
        <p className="text-xs text-muted-foreground">
          Source freshness unknown
        </p>
      ) : freshness.state !== "current" ? (
        <p className="text-xs text-muted-foreground">
          Reported phase: {deployment.phase}
        </p>
      ) : null}
    </div>
  );
}
export function DeploymentObservationTime({
  deployment,
  now,
}: {
  deployment: ClusterDeployment;
  now: number;
}) {
  const freshness = deploymentFreshness(deployment, now);
  if (freshness.observedAt)
    return (
      <span>Source observed {formatRelativeTime(freshness.observedAt)}</span>
    );
  // An omitted observation can also be a modern local mutation/tombstone, not just a legacy agent.
  if (
    freshness.state === "unknown" &&
    deployment.lastObservedAt &&
    Number.isFinite(Date.parse(deployment.lastObservedAt))
  ) {
    return (
      <span>
        Reported {formatRelativeTime(deployment.lastObservedAt)} · source
        freshness unknown
      </span>
    );
  }
  return <span>Source observation time unknown</span>;
}
