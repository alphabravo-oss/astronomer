import { createFileRoute, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Boxes } from "lucide-react";
import { PageHeader, PageShell } from "@/components/ui/page";
import {
  DeliveryProjectGate,
  ErrorMessage,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import { getClusterDeliveryInventory } from "@/lib/api/delivery-system";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { liveFallback } from "@/lib/live/status-store";
import { EmptyState, LoadingState } from "@/components/ui/empty-state";
import { SystemComponentContent } from "./-content";

function workloadPath(
  clusterId: string,
  namespace: string | undefined,
  kind: string,
  name: string,
) {
  if (!namespace) return "";
  const segment =
    kind === "Deployment"
      ? "deployments"
      : kind === "StatefulSet"
        ? "statefulsets"
        : kind === "DaemonSet"
          ? "daemonsets"
          : "";
  if (!segment) return "";
  return (
    "/dashboard/clusters/" +
    clusterId +
    "/" +
    segment +
    "/" +
    namespace +
    "/" +
    name
  );
}

function SystemComponentDetailPage() {
  const params = Route.useParams();
  const clusterId = params.id;
  const componentId = decodeURIComponent(params.componentId);
  const { projectId, projects, projectQuery } = useDeliveryProjectScope({
    clusterId,
  });
  const { data: user } = useCurrentUser();
  const allowed = can(user, "delivery_inventory", "read", {
    type: "project",
    id: projectId,
  });
  const query = useQuery({
    queryKey: queryKeys.delivery.clusterInventory(projectId, clusterId),
    queryFn: () => getClusterDeliveryInventory(projectId, clusterId),
    enabled: Boolean(projectId && clusterId && allowed),
    refetchInterval: liveFallback(15_000),
  });
  const inventory = query.data?.controllerInventory;
  const component = inventory?.systemComponents.find(
    (item) => item.id === componentId,
  );
  const workloadHref = component
    ? workloadPath(
        clusterId,
        component.namespace,
        component.kind,
        component.name,
      )
    : "";
  const base =
    "/dashboard/clusters/" + clusterId + "/delivery/system-components";
  return (
    <PageShell>
      <Link
        to={base}
        className="inline-flex items-center gap-1.5 text-xs text-link hover:underline"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        System Components
      </Link>
      <PageHeader
        title={component?.name || "System component"}
        description={
          component?.detail ||
          "Observed ownership, health, resources, and lifecycle evidence."
        }
      />
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_inventory:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        {query.isError && <ErrorMessage error={query.error} />}
        {query.isLoading ? <LoadingState title="Loading component" /> : null}
        {!query.isLoading && !component ? (
          <EmptyState
            icon={Boxes}
            title="Component not found"
            description="The component is no longer present in the latest cluster observation."
            actionLabel="Back to system components"
            actionHref={`/dashboard/clusters/${clusterId}/delivery/system-components`}
          />
        ) : null}
        {component ? (
          <SystemComponentContent
            component={component}
            clusterId={clusterId}
            workloadHref={workloadHref}
          />
        ) : null}
      </DeliveryProjectGate>
    </PageShell>
  );
}

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/system-components/$componentId/",
)({
  component: SystemComponentDetailPage,
});
