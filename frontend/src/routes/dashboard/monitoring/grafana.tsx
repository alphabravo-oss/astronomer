import { createFileRoute, useLocation } from "@tanstack/react-router";
import { ExternalLink } from "lucide-react";
import { PageHeader, PageShell } from "@/components/ui/page";
import { FLEET_GRAFANA_PROXY_PATH } from "@/components/monitoring/stack-spec";

function SharedGrafanaPage() {
  const search = useLocation({ select: (location) => location.searchStr });
  const src = `${FLEET_GRAFANA_PROXY_PATH}${search || ""}`;

  return (
    <PageShell>
      <PageHeader
        title="Shared Grafana"
        description="Grafana is private to the management cluster and proxied through your Astronomer session and monitoring permissions."
        actions={
          <a
            href={src}
            target="_blank"
            rel="noreferrer"
            className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-foreground hover:bg-accent"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            Open full screen
          </a>
        }
      />
      <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
        <iframe
          title="Shared Grafana"
          src={src}
          className="h-[calc(100vh-12rem)] min-h-[38rem] w-full bg-background"
          referrerPolicy="same-origin"
        />
      </div>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/monitoring/grafana")({
  component: SharedGrafanaPage,
});
