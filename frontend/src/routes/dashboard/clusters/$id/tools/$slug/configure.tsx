import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { ToolInstallPage } from "@/components/clusters/tool-install-page";
import { useClusterToolActions } from "@/components/clusters/use-cluster-tool-actions";
import { ConfigurationPage } from "@/components/catalog/configuration-page";
import { QueryStates } from "@/components/ui/query-states";
import { useTools, useClusterToolsStatus } from "@/lib/hooks/tools";
import { useCluster } from "@/lib/hooks/clusters";
import type { ClusterTool, ClusterToolStatus } from "@/types";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/tools/$slug/configure",
)({
  validateSearch: (
    search: Record<string, unknown>,
  ): { action: "install" | "upgrade" } => ({
    action: search.action === "upgrade" ? "upgrade" : "install",
  }),
  component: ToolConfigurationRoute,
});
function ToolConfigurationRoute() {
  const { id, slug } = Route.useParams();
  const { action } = Route.useSearch();
  const navigate = useNavigate();
  const tools = useTools();
  const cluster = useCluster(id);
  const statuses = useClusterToolsStatus(id);
  const back = () => void navigate({ to: `/dashboard/clusters/${id}/tools` });
  return (
    <QueryStates
      query={tools}
      permission="catalog:read"
      errorTitle="Tool catalog unavailable"
    >
      {(rows) => (
        <QueryStates
          query={cluster}
          permission="clusters:read"
          errorTitle="Cluster unavailable"
        >
          {(resolved) => (
            <QueryStates
              query={statuses}
              permission="catalog:read"
              errorTitle="Tool status unavailable"
            >
              {(current) => {
                const tool = rows.find((row) => row.slug === slug);
                return tool ? (
                  <ToolConfiguration
                    key={`${id}:${slug}:${action}`}
                    tool={tool}
                    clusterId={id}
                    environment={resolved.environment}
                    statuses={current}
                    action={action}
                    back={back}
                  />
                ) : (
                  <ConfigurationPage title="Tool unavailable" onClose={back}>
                    <p role="alert">This tool is not in the current catalog.</p>
                  </ConfigurationPage>
                );
              }}
            </QueryStates>
          )}
        </QueryStates>
      )}
    </QueryStates>
  );
}
function ToolConfiguration({
  tool,
  clusterId,
  environment,
  statuses,
  action,
  back,
}: {
  tool: ClusterTool;
  clusterId: string;
  environment: string;
  statuses: ClusterToolStatus[];
  action: "install" | "upgrade";
  back: () => void;
}) {
  const navigate = useNavigate();
  const actions = useClusterToolActions({
    clusterId,
    clusterEnvironment: environment,
    tools: [tool],
    statuses,
    initialConfiguration: { kind: action, tool },
    onCloseConfiguration: back,
    onOperationStarted: (operation) =>
      void navigate({
        to: `/dashboard/clusters/${clusterId}/tools?operation=${encodeURIComponent(operation.id)}`,
      }),
  });
  return actions.configurationPage ? (
    <ToolInstallPage {...actions.configurationPage} />
  ) : null;
}
