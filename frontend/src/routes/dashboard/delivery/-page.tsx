import { useQuery } from "@tanstack/react-query";
import { EstateDeliveryOverview, isForbiddenError } from "./-estate-overview";
import { ProjectDeliveryOverview } from "./-project-overview";
import {
  DeliveryShell,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import { getDeliveryEstate } from "@/lib/api/delivery-system";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";

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
