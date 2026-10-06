import { useState } from "react";
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Loader2,
  Server,
} from "lucide-react";
import type { SearchClusterError } from "@/lib/api/resource-search";
import { BareButton } from "@/components/form/bare-button";
import type { ClusterResultGroup } from "./-search-grouping";

/** Status line, per-cluster result summary and failed-cluster details. */
export function SearchStatus({
  isFetching,
  loaded,
  resultCount,
  clustersQueried,
  clustersFailed,
  clusterGroups,
  errors,
  groupByCluster,
  onToggleGroup,
}: {
  isFetching: boolean;
  loaded: boolean;
  resultCount: number;
  clustersQueried: number;
  clustersFailed: number;
  clusterGroups: ClusterResultGroup[];
  errors: SearchClusterError[];
  groupByCluster: boolean;
  onToggleGroup: () => void;
}) {
  const [errorsExpanded, setErrorsExpanded] = useState(false);
  const data = loaded;
  const results = { length: resultCount };
  const query = { isFetching };
  return (
    <>
      {/* Status line */}
      <div className="mt-3 flex items-center gap-3 text-xs text-muted-foreground">
        {query.isFetching && (
          <span className="inline-flex items-center gap-1.5">
            <Loader2 className="h-3 w-3 animate-spin" />
            Searching...
          </span>
        )}
        {!query.isFetching && data && (
          <span>
            {results.length} {results.length === 1 ? "result" : "results"} from{" "}
            {clustersQueried} {clustersQueried === 1 ? "cluster" : "clusters"}
          </span>
        )}
        {clustersFailed > 0 && (
          <BareButton
            onClick={() => setErrorsExpanded((v) => !v)}
            className="inline-flex items-center gap-1.5 text-status-warning hover:underline"
          >
            <AlertTriangle className="h-3 w-3" />
            {clustersFailed} {clustersFailed === 1 ? "cluster" : "clusters"}{" "}
            failed
            {errorsExpanded ? (
              <ChevronDown className="h-3 w-3" />
            ) : (
              <ChevronRight className="h-3 w-3" />
            )}
          </BareButton>
        )}
      </div>

      {/* Per-cluster summary: count + status mix, failed clusters flagged */}
      {data && clusterGroups.length > 0 && (
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <BareButton
            onClick={onToggleGroup}
            aria-pressed={groupByCluster}
            className="rounded-sm border border-border px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground aria-pressed:bg-muted aria-pressed:text-foreground"
          >
            Group by cluster
          </BareButton>
          <ul
            aria-label="Results by cluster"
            className="flex flex-wrap items-center gap-2"
          >
            {clusterGroups.map((group) => (
              <li
                key={group.clusterId}
                data-testid="cluster-result-group"
                className="inline-flex items-center gap-1.5 rounded-sm bg-muted px-2 py-0.5 text-xs"
              >
                <Server className="h-3 w-3 text-muted-foreground" />
                <span className="font-medium text-foreground">
                  {group.clusterName}
                </span>
                {group.error ? (
                  <span className="text-status-warning">not searched</span>
                ) : (
                  <span className="text-muted-foreground tabular-nums">
                    {group.total}
                    {" ("}
                    {Object.entries(group.statusCounts)
                      .map(([status, count]) => `${count} ${status}`)
                      .join(", ")}
                    {")"}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Per-cluster errors (collapsible) */}
      {errorsExpanded && errors.length > 0 && (
        <div className="mt-2 rounded-md border border-status-warning/30 bg-status-warning/5 p-3 space-y-1.5">
          {errors.map((err) => (
            <div
              key={err.cluster_id}
              className="flex items-start gap-2 text-xs"
            >
              <AlertTriangle className="h-3.5 w-3.5 text-status-warning shrink-0 mt-0.5" />
              <div className="min-w-0 flex-1">
                <p className="font-medium text-foreground">
                  {err.cluster_name}
                </p>
                <p className="text-muted-foreground truncate font-mono">
                  {err.error}
                </p>
              </div>
            </div>
          ))}
        </div>
      )}
    </>
  );
}
