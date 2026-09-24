import { QueryStates, type QueryState } from "@/components/ui/query-states";
import { Input } from "@/components/ui/input";
import { ActionButton } from "@/components/ui/action-button";
import type { LoggingOutput } from "@/types";
import { outputsForCluster } from "./-output-scope";

export function PipelineOutputs({
  query,
  clusterId,
  selected,
  onToggle,
}: {
  query: QueryState<LoggingOutput[]> & {
    hasNextPage?: boolean;
    isFetchingNextPage?: boolean;
    fetchNextPage: () => unknown;
  };
  clusterId: string;
  selected: string[];
  onToggle: (id: string) => void;
}) {
  const outputList = outputsForCluster(
    query.isError ? undefined : query.data,
    clusterId,
  );
  return (
    <div className="space-y-1.5">
      <p className="text-sm font-medium text-foreground">Outputs</p>
      <div className="space-y-1.5 max-h-40 overflow-y-auto p-2 rounded-md border border-border bg-background">
        {!clusterId ? (
          <p>Select a cluster to choose outputs.</p>
        ) : (
          <QueryStates
            query={query}
            permission="logging:read"
            loadingTitle="Loading outputs"
            errorTitle="Outputs unavailable"
          >
            {outputList.length === 0 ? (
              <span className="text-xs text-muted-foreground">
                No outputs available for this cluster. Create an output first.
              </span>
            ) : (
              outputList.map((output) => (
                <label
                  key={output.id}
                  className="flex items-center gap-2 px-2 py-1.5 rounded-sm text-sm hover:bg-accent cursor-pointer"
                >
                  <Input
                    type="checkbox"
                    checked={selected.includes(output.id)}
                    onChange={() => onToggle(output.id)}
                    className="rounded-sm border-border text-primary focus:ring-ring"
                  />
                  <span className="text-foreground">{output.name}</span>
                  <span className="text-xs text-muted-foreground capitalize">
                    ({output.type})
                  </span>
                </label>
              ))
            )}
            {query.hasNextPage && (
              <ActionButton
                loading={query.isFetchingNextPage}
                onClick={() => void query.fetchNextPage()}
              >
                Load more outputs
              </ActionButton>
            )}
          </QueryStates>
        )}
      </div>
    </div>
  );
}
