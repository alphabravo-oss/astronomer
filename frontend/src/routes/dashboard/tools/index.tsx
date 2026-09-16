import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { Server } from "lucide-react";

import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";
import { useClusters } from "@/lib/hooks/clusters";
import { useClusterToolsStatus, useTools } from "@/lib/hooks/tools";
import { normalizeToolStatus } from "@/lib/tool-status";
import type { Cluster, ClusterTool, ToolStatus } from "@/types";
import { cn } from "@/lib/utils";

const toolStatusDotColor: Record<ToolStatus, string> = {
  installed: "bg-status-success",
  installing: "bg-status-warning",
  upgrading: "bg-status-warning",
  uninstalling: "bg-status-warning",
  failed: "bg-status-error",
  not_installed: "bg-muted-foreground/30",
  installed_unmanaged: "bg-status-info",
  unknown: "bg-status-warning/50",
};

const toolStatusLabel: Record<ToolStatus, string> = {
  installed: "Installed",
  installing: "Installing",
  upgrading: "Upgrading",
  uninstalling: "Uninstalling",
  failed: "Failed",
  not_installed: "Not Installed",
  installed_unmanaged: "Unmanaged",
  unknown: "Unknown",
};

function ToolStatusCell({
  clusterId,
  tool,
}: {
  clusterId: string;
  tool: ClusterTool;
}) {
  const statusQuery = useClusterToolsStatus(clusterId);
  const status = normalizeToolStatus(
    statusQuery.data?.find((item) => item.slug === tool.slug)?.status,
  );
  const working = ["installing", "upgrading", "uninstalling"].includes(status);

  return (
    <span
      className="flex items-center gap-2"
      aria-label={`${tool.name}: ${toolStatusLabel[status]}`}
    >
      <span className="relative flex h-2.5 w-2.5" aria-hidden="true">
        {working ? (
          <span
            className={cn(
              "absolute inline-flex h-full w-full animate-ping rounded-full opacity-75",
              toolStatusDotColor[status],
            )}
          />
        ) : null}
        <span
          className={cn(
            "relative inline-flex h-2.5 w-2.5 rounded-full",
            toolStatusDotColor[status],
          )}
        />
      </span>
      <span className="text-xs text-muted-foreground">
        {toolStatusLabel[status]}
      </span>
    </span>
  );
}

function ManagedToolsTable({
  clusters,
  tools,
}: {
  clusters: Cluster[];
  tools: ClusterTool[];
}) {
  const navigate = useNavigate();
  const columns = useMemo<Column<Cluster>[]>(
    () => [
      {
        key: "cluster",
        header: "Cluster",
        accessor: (cluster) => (
          <div className="flex items-center gap-3">
            <Server className="h-4 w-4 shrink-0 text-muted-foreground" />
            <div>
              <p className="font-medium text-foreground">
                {cluster.displayName}
              </p>
              <p className="text-xs text-muted-foreground">{cluster.name}</p>
            </div>
          </div>
        ),
        sortAccessor: (cluster) => cluster.displayName || cluster.name,
      },
      {
        key: "environment",
        header: "Environment",
        accessor: (cluster) => (
          <span className="capitalize">{cluster.environment}</span>
        ),
        sortAccessor: (cluster) => cluster.environment,
      },
      ...tools.map<Column<Cluster>>((tool) => ({
        key: tool.slug,
        header: tool.name,
        accessor: (cluster) => (
          <ToolStatusCell clusterId={cluster.id} tool={tool} />
        ),
      })),
    ],
    [tools],
  );

  return (
    <DataTable
      data={clusters}
      columns={columns}
      keyExtractor={(cluster) => cluster.id}
      persistKey="estate-tools"
      searchPlaceholder="Filter clusters…"
      onRowClick={(cluster) =>
        void navigate({
          to: "/dashboard/clusters/$id/tools",
          params: { id: cluster.id },
        })
      }
    />
  );
}

function ManagedToolsPage() {
  const clustersQuery = useClusters({ pageSize: 100 });
  const toolsQuery = useTools();

  return (
    <PageShell>
      <PageHeader
        title="Cluster Tools"
        description="Manage operational tools across your clusters"
      />
      <QueryStates
        query={clustersQuery}
        permission="clusters:read"
        isEmpty={(page) => page.data.length === 0}
        empty={
          <EmptyState
            icon={Server}
            title="No clusters registered"
            description="Register a cluster to start managing tools."
          />
        }
      >
        {(page) => (
          <QueryStates
            query={toolsQuery}
            permission="tools:read"
            isEmpty={(tools) => tools.length === 0}
            empty={
              <EmptyState
                icon={Server}
                title="No tools available"
                description="No cluster tools are configured for this installation."
              />
            }
          >
            {(tools) => (
              <ManagedToolsTable clusters={page.data} tools={tools} />
            )}
          </QueryStates>
        )}
      </QueryStates>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/tools/")({
  component: ManagedToolsPage,
});
