import { useCatalogApplications } from "@/lib/hooks/catalog";
import { supportForChart } from "@/lib/catalogs/support";
import {
  Link as RouterLink,
  useNavigate,
  useLocation,
} from "@tanstack/react-router";
import { useTools, useClusterToolsStatus } from "@/lib/hooks/tools";
import { ToolCard } from "@/components/clusters/tool-card";
import { ToolInstallProgress } from "@/components/clusters/tool-install-progress";
import { useClusterToolActions } from "@/components/clusters/use-cluster-tool-actions";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Wrench, Sparkles } from "lucide-react";
import { QueryStates } from "@/components/ui/query-states";

interface ToolsTabProps {
  clusterId: string;
  clusterEnvironment: string;
  clusterStatus?: string;
}

export function ToolsTab({
  clusterId,
  clusterEnvironment,
  clusterStatus,
}: ToolsTabProps) {
  const navigate = useNavigate();
  const operationId = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  ).get("operation");
  const isDisconnected = clusterStatus === "disconnected";
  const toolsQuery = useTools();
  const catalog = useCatalogApplications();
  const statusesQuery = useClusterToolsStatus(clusterId);
  const tools = toolsQuery.isError ? [] : (toolsQuery.data ?? []);
  const statuses = statusesQuery.isError ? [] : (statusesQuery.data ?? []);
  const actions = useClusterToolActions({
    clusterId,
    clusterEnvironment,
    tools,
    statuses,
    onConfigure: (action, slug) =>
      void navigate({
        to: `/dashboard/clusters/${clusterId}/tools/${encodeURIComponent(slug)}/configure?action=${action}`,
      }),
  });

  if (
    toolsQuery.isLoading ||
    toolsQuery.isError ||
    toolsQuery.data === undefined
  ) {
    return (
      <QueryStates
        query={toolsQuery}
        permission="tools:read"
        errorTitle="Tool catalog unavailable"
      >
        {null}
      </QueryStates>
    );
  }
  if (
    statusesQuery.isLoading ||
    statusesQuery.isError ||
    statusesQuery.data === undefined
  ) {
    return (
      <QueryStates
        query={statusesQuery}
        permission="tools:read"
        errorTitle="Tool status unavailable"
      >
        {null}
      </QueryStates>
    );
  }
  if (tools.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
        <Wrench className="h-10 w-10 mb-3" />
        <p className="text-sm">No tools available</p>
      </div>
    );
  }

  // Automatic metrics arrive over delivery, not the optional Helm/tool path.
  // See internal/baseline/registry.go for that ownership split.
  const noToolsInstalled = tools.every((tool) =>
    statuses.some(
      (status) =>
        status.slug === tool.slug && status.status === "not_installed",
    ),
  );

  return (
    <>
      <p className="mb-4 text-sm text-muted-foreground">
        Guided cluster capabilities with presets and coordinated releases.
        Support status is independent of installation method. Applications shows
        the shared installed inventory.
      </p>
      {noToolsInstalled && !isDisconnected && (
        <div className="mb-4 flex items-start gap-3 rounded-lg border border-primary/30 bg-primary/5 p-4">
          <Sparkles className="h-5 w-5 mt-0.5 text-primary shrink-0" />
          <div className="flex-1">
            <p className="text-sm font-medium">No add-ons installed yet</p>
            <p className="text-xs text-muted-foreground mt-1">
              Metrics (kube-state-metrics, node-exporter) install automatically
              on every cluster and are managed for you. Everything below is
              optional — enable what this cluster needs: image scanning
              (trivy-operator), log forwarding (fluent-bit), ingress
              (ingress-nginx), TLS (cert-manager), or policy (Gatekeeper). The{" "}
              <strong>Platform Baseline</strong> template installs the
              recommended set in one step.
            </p>
            <RouterLink
              to="/dashboard/clusters/$id/template"
              params={{ id: clusterId }}
              className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
            >
              Install the recommended set →
            </RouterLink>
          </div>
        </div>
      )}
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-4">
        {tools.map((tool) => (
          <ToolCard
            key={tool.slug}
            {...actions.cardProps(tool)}
            support={
              tool.charts.length === 1
                ? supportForChart(
                    catalog.isError ? undefined : catalog.data,
                    tool.charts[0].chartName,
                    tool.charts[0].repoUrl,
                    tool.charts[0].version ?? tool.versionConstraint,
                  )
                : undefined
            }
            clusterDisconnected={isDisconnected}
          />
        ))}
      </div>
      {operationId && (
        <ToolInstallProgress
          operationId={operationId}
          toolName="Platform Tool"
          onClose={() =>
            void navigate({ to: `/dashboard/clusters/${clusterId}/tools` })
          }
        />
      )}
      {actions.confirmation && <ConfirmDialog {...actions.confirmation} />}
      {actions.progress && <ToolInstallProgress {...actions.progress} />}
    </>
  );
}
