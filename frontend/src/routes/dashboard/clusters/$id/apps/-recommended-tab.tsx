import type { useQuery } from "@tanstack/react-query";
import { QueryStates } from "@/components/ui/query-states";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import { AlertTriangle, Star } from "lucide-react";
import type { ClusterAppRow } from "@/lib/api/cluster-apps";

export function RecommendedView({
  q,
  installed,
  installDecision,
  onInstall,
}: {
  q: ReturnType<
    typeof useQuery<import("@/lib/api/cluster-apps").RecommendedChart[]>
  >;
  installed: ClusterAppRow[];
  installDecision: PermissionDecision;
  onInstall: (chartId: string, chartName: string) => void;
}) {
  const installedByChart = new Set(
    installed.map((r) => r.chartName).filter(Boolean),
  );

  return (
    <QueryStates
      query={q}
      loadingTitle="Loading recommendations…"
      isEmpty={(data) => data.length === 0}
      empty={
        <div className="rounded-lg border border-dashed border-border p-6 text-center">
          <AlertTriangle className="h-6 w-6 mx-auto text-muted-foreground mb-2" />
          <p className="text-sm text-foreground">No recommendations yet</p>
          <p className="text-xs text-muted-foreground mt-1 max-w-sm mx-auto">
            The recommendation engine needs at least a handful of installs
            across managed clusters to surface popular charts. Try the Browse
            tab for the full catalog.
          </p>
        </div>
      }
    >
      {(items) => (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {items.map((c) => {
            const isInstalled = installedByChart.has(c.name);
            return (
              <article
                key={c.chartId || c.name}
                className="border border-border rounded-lg p-3 bg-card space-y-2"
              >
                <div className="flex items-center gap-2">
                  <Star className="h-4 w-4 text-status-warning" />
                  <div className="font-medium text-sm text-foreground">
                    {c.name}
                  </div>
                </div>
                <div className="text-xs text-muted-foreground space-y-0.5">
                  <div>
                    Score:{" "}
                    <span className="tabular-nums text-foreground">
                      {c.score.toFixed(2)}
                    </span>
                  </div>
                  <div>
                    Installs across clusters:{" "}
                    <span className="tabular-nums text-foreground">
                      {c.installCount}
                    </span>
                  </div>
                  {c.ratingAvg > 0 && (
                    <div>
                      Avg rating:{" "}
                      <span className="tabular-nums text-foreground">
                        {c.ratingAvg.toFixed(1)}
                      </span>
                    </div>
                  )}
                </div>
                {isInstalled ? (
                  <span className="text-[11px] text-status-success font-medium">
                    Already installed
                  </span>
                ) : (
                  <button
                    className="text-[11px] text-primary hover:underline disabled:cursor-not-allowed disabled:text-muted-foreground disabled:no-underline"
                    disabled={!installDecision.allowed}
                    title={
                      !installDecision.allowed
                        ? permissionDeniedReason(installDecision)
                        : "Install chart"
                    }
                    onClick={() => onInstall(c.chartId, c.name)}
                  >
                    Install →
                  </button>
                )}
              </article>
            );
          })}
        </div>
      )}
    </QueryStates>
  );
}
