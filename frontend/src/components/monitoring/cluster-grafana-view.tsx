import type { ReactNode } from "react";
import { BarChart3, ExternalLink } from "lucide-react";

import { useClusterStackStatus } from "@/components/monitoring/use-cluster-stack-status";
import { clusterGrafanaProxyPath } from "@/components/monitoring/stack-spec";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";

export function ClusterGrafanaView({
  clusterId: id,
  view = "overview",
  actions,
}: {
  clusterId: string;
  view?: "overview" | "metrics" | "logs";
  actions?: ReactNode;
}) {
  const status = useClusterStackStatus(id);
  const base = clusterGrafanaProxyPath(id);
  const suffix =
    view === "logs"
      ? "explore?" +
        new URLSearchParams({
          schemaVersion: "1",
          panes: JSON.stringify({
            logs: {
              datasource: "loki",
              queries: [
                {
                  refId: "A",
                  datasource: { type: "loki", uid: "loki" },
                  expr: '{job=~".+"}',
                  queryType: "range",
                },
              ],
              range: { from: "now-1h", to: "now" },
            },
          }),
        }).toString()
      : view === "metrics"
        ? "dashboards"
        : "";
  const src = base + suffix;

  return (
    <PageShell>
      <PageHeader
        title={
          view === "logs" ? "Logs" : view === "metrics" ? "Metrics" : "Grafana"
        }
        description="Explore this cluster’s metrics, logs, and dashboards in Grafana."
        actions={
          <div className="flex items-center gap-2">
            {actions}
            {status.data?.grafanaAvailable ? (
              <a
                href={src}
                target="_blank"
                rel="noreferrer"
                className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-foreground hover:bg-accent"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                Open full screen
              </a>
            ) : null}
          </div>
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
                src={(data.grafanaProxyPath || base) + suffix}
                className="h-[calc(100vh-12rem)] min-h-[38rem] w-full bg-background"
                referrerPolicy="same-origin"
              />
            </div>
          ) : (
            <EmptyState
              icon={BarChart3}
              title="Cluster Grafana is unavailable"
              description="Install, upgrade, or repair this cluster’s monitoring stack to make its private Grafana dashboard available."
              actionLabel="Open monitoring stack"
              actionHref={`/dashboard/clusters/${id}/monitoring-stack`}
            />
          )
        }
      </QueryStates>
    </PageShell>
  );
}
