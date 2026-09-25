import { ErrorState } from "@/components/ui/empty-state";
import { apiErrorStatus, extractApiErrorMessage } from "@/lib/api/errors";
import type { QueryState } from "@/components/ui/query-states";

export function MetricsReadError({
  query,
  title,
}: {
  query: QueryState<unknown>;
  title: string;
}) {
  return (
    <div className="space-y-2">
      <ErrorState
        title={title}
        description={
          apiErrorStatus(query.error) === 403
            ? "Permission to read these metrics was denied. Monitoring availability could not be determined."
            : extractApiErrorMessage(query.error)
        }
        onRetry={() => void query.refetch()}
      />
      {query.data !== undefined && (
        <p className="text-sm text-muted-foreground">
          Previously loaded metrics may be stale; the latest read failed.
        </p>
      )}
    </div>
  );
}
