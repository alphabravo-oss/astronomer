import { createFileRoute } from "@tanstack/react-router";
import { BarChart3, ExternalLink } from "lucide-react";

import { useClusterStackStatus } from "@/components/monitoring/use-cluster-stack-status";
import { clusterGrafanaProxyPath } from "@/components/monitoring/stack-spec";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";

function ClusterGrafanaPage() {
  const { id } = Route.useParams();
  const status = useClusterStackStatus(id);
  const src = clusterGrafanaProxyPath(id);

  return (
    <PageShell>
      <PageHeader
        title="Grafana"
        description="This cluster’s Grafana stays private and is proxied through your Astronomer session and cluster-scoped monitoring permissions."
        actions={
          status.data?.grafanaAvailable ? (
            <a
              href={src}
              target="_blank"
              rel="noreferrer"
              className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-foreground hover:bg-accent"
            >
              <ExternalLink className="h-3.5 w-3.5" />
              Open full screen
            </a>
          ) : null
        }
      />
      <QueryStates
        query={status}
        loadingTitle="Checking cluster Grafana"
        permission="monitoring:read"
        errorTitle="Cluster Grafana is unavailable"
      >
        {(data) =>
          data.grafanaAvailable ? (
            <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
              <iframe
                title="Cluster Grafana"
                src={data.grafanaProxyPath || src}
                className="h-[calc(100vh-12rem)] min-h-[38rem] w-full bg-background"
                referrerPolicy="same-origin"
              />
            </div>
          ) : (
            <EmptyState
              icon={BarChart3}
              title="Cluster Grafana is not installed"
              description="Enable Grafana in this cluster’s monitoring stack to add its private dashboard here."
              actionLabel="Open monitoring stack"
              actionHref={`/dashboard/clusters/${id}/monitoring-stack`}
            />
          )
        }
      </QueryStates>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/grafana")({
  component: ClusterGrafanaPage,
});
