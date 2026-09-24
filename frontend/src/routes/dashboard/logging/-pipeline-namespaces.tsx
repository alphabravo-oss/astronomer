import { QueryStates } from "@/components/ui/query-states";
import type { useClusterNamespaces } from "@/lib/hooks/clusters";
import { cn } from "@/lib/utils";

export function PipelineNamespaces({
  query,
  selected,
  onToggle,
}: {
  query: ReturnType<typeof useClusterNamespaces>;
  selected: string[];
  onToggle: (namespace: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <p className="text-sm font-medium text-foreground">Namespaces</p>
      <QueryStates query={query} errorTitle="Namespaces unavailable">
        {(namespaces) => (
          <div className="flex flex-wrap gap-1.5 max-h-32 overflow-y-auto p-2 rounded-md border border-border bg-background">
            {namespaces.length === 0 ? (
              <span className="text-xs text-muted-foreground">
                No namespaces found
              </span>
            ) : (
              namespaces.map((ns) => (
                <button
                  key={ns.name}
                  type="button"
                  aria-pressed={selected.includes(ns.name)}
                  onClick={() => onToggle(ns.name)}
                  className={cn(
                    "px-2.5 py-1 rounded-sm text-xs font-medium transition-colors",
                    selected.includes(ns.name)
                      ? "bg-primary text-primary-foreground"
                      : "bg-muted text-muted-foreground hover:text-foreground",
                  )}
                >
                  {ns.name}
                </button>
              ))
            )}
          </div>
        )}
      </QueryStates>
      {selected.length === 0 && (
        <p className="text-xs text-muted-foreground">
          No namespaces selected (will collect from all)
        </p>
      )}
    </div>
  );
}
