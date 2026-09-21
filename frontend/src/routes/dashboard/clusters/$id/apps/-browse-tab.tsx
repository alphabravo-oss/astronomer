import type { useQuery } from "@tanstack/react-query";
import { Input } from "@/components/ui/input";
import { QueryStates } from "@/components/ui/query-states";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import type { PaginatedResponse } from "@/types";
import { Box, ExternalLink, Loader2, Search } from "lucide-react";
import type { ClusterAppRow } from "@/lib/api/cluster-apps";

export function BrowseView({
  q,
  search,
  setSearch,
  installed,
  installDecision,
  onInstall,
}: {
  q: ReturnType<
    typeof useQuery<
      PaginatedResponse<import("@/lib/api/cluster-apps").CatalogChartSummary>
    >
  >;
  search: string;
  setSearch: (s: string) => void;
  installed: ClusterAppRow[];
  installDecision: PermissionDecision;
  onInstall: (chartId: string, chartName: string) => void;
}) {
  const charts = q.data?.data ?? [];

  // Build a name→releases index so each Browse card knows whether
  // it's already on this cluster (and via what install path). This
  // is the cheap version of "drift detection" — we don't reconcile
  // helm releases, we just notice when the catalog browse offers
  // something the cluster already has.
  const installedByChart = new Map<string, ClusterAppRow>();
  for (const r of installed) {
    if (r.chartName) installedByChart.set(r.chartName, r);
    else if (r.toolSlug) installedByChart.set(r.toolSlug, r);
  }

  return (
    <div className="space-y-3">
      <div className="relative max-w-md">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
        <Input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search charts (kube-prometheus, loki, …)"
          className="w-full h-9 pl-8 pr-3 rounded-md border border-border bg-background text-sm
            placeholder:text-muted-foreground focus:outline-hidden focus:ring-1 focus:ring-ring"
        />
      </div>
      {q.isError ? (
        <QueryStates query={q}>{null}</QueryStates>
      ) : q.isLoading ? (
        <div className="flex items-center justify-center h-32 text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin mr-2" /> Loading catalog…
        </div>
      ) : charts.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-6 text-center">
          <p className="text-sm font-medium text-foreground">
            No matching charts
          </p>
          <p className="text-xs text-muted-foreground mt-1">
            Try a broader search, or add a repository on the Repositories tab.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {charts.map((c) => {
            const existing = installedByChart.get(c.name);
            return (
              <article
                key={c.id}
                className="border border-border rounded-lg p-3 flex gap-3 bg-card hover:border-muted-foreground/40 transition-colors"
              >
                <div className="h-10 w-10 shrink-0 rounded-md bg-muted flex items-center justify-center overflow-hidden">
                  {c.iconUrl ? (
                    <img
                      src={c.iconUrl}
                      alt=""
                      className="h-10 w-10 object-contain"
                    />
                  ) : (
                    <Box className="h-5 w-5 text-muted-foreground" />
                  )}
                </div>
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="flex items-start justify-between gap-2">
                    <div className="font-medium text-sm text-foreground truncate">
                      {c.displayName || c.name}
                    </div>
                    {c.deprecated && (
                      <span className="text-[10px] text-status-warning border border-status-warning/40 bg-status-warning/10 px-1.5 py-0.5 rounded-sm">
                        deprecated
                      </span>
                    )}
                  </div>
                  {c.description && (
                    <p className="text-xs text-muted-foreground line-clamp-2">
                      {c.description}
                    </p>
                  )}
                  <div className="flex items-center justify-between gap-2 pt-1">
                    {existing ? (
                      <span className="text-[11px] text-status-success font-medium inline-flex items-center gap-1">
                        Installed
                        {existing.sourceKind === "tool" && (
                          <span className="text-muted-foreground font-normal">
                            (via Tools)
                          </span>
                        )}
                      </span>
                    ) : (
                      <button
                        className="text-[11px] inline-flex items-center gap-1 text-primary hover:underline disabled:cursor-not-allowed disabled:text-muted-foreground disabled:no-underline"
                        disabled={!installDecision.allowed}
                        title={
                          !installDecision.allowed
                            ? permissionDeniedReason(installDecision)
                            : "Install chart"
                        }
                        onClick={() => onInstall(c.id, c.name)}
                      >
                        Install →
                      </button>
                    )}
                    {c.homeUrl && (
                      <a
                        href={c.homeUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-[11px] text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
                      >
                        Docs <ExternalLink className="h-2.5 w-2.5" />
                      </a>
                    )}
                  </div>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </div>
  );
}
