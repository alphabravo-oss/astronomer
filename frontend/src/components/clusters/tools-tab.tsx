import { Link as RouterLink } from "@tanstack/react-router";
import { useTools, useClusterToolsStatus } from "@/lib/hooks/tools";
import { ToolCard } from "@/components/clusters/tool-card";
import { ToolInstallModal } from "@/components/clusters/tool-install-modal";
import { ToolInstallProgress } from "@/components/clusters/tool-install-progress";
import { useClusterToolActions } from "@/components/clusters/use-cluster-tool-actions";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Loader2, Wrench, Sparkles } from "lucide-react";

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
  const isDisconnected = clusterStatus === "disconnected";
  const { data: tools = [], isLoading: toolsLoading } = useTools();
  const { data: statuses = [] } = useClusterToolsStatus(clusterId);
  const actions = useClusterToolActions({
    clusterId,
    clusterEnvironment,
    tools,
    statuses,
  });

  if (toolsLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
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
  const noToolsInstalled = statuses.every(
    (status) => !["installed", "installing"].includes(status.status),
  );

  return (
    <>
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
            clusterDisconnected={isDisconnected}
          />
        ))}
      </div>
      {actions.installDialog && <ToolInstallModal {...actions.installDialog} />}
      {actions.confirmation && <ConfirmDialog {...actions.confirmation} />}
      {actions.progress && <ToolInstallProgress {...actions.progress} />}
    </>
  );
}
