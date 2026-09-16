import { QueryStates } from "@/components/ui/query-states";

interface DataTableQueryErrorProps {
  error?: unknown;
  errorMessage?: string;
  onRetry?: () => void;
  permission?: string;
}

/**
 * Keeps table failures on the same auth/offline/error contract as full-page
 * queries. A table is a presentation primitive; it must not invent a second,
 * less precise interpretation of a failed request.
 */
export function DataTableQueryError({
  error,
  errorMessage,
  onRetry,
  permission,
}: DataTableQueryErrorProps) {
  return (
    <QueryStates
      query={{
        data: undefined,
        error,
        isError: true,
        isLoading: false,
        refetch: onRetry ?? (() => undefined),
      }}
      errorDescription={errorMessage}
      permission={permission}
    >
      {() => null}
    </QueryStates>
  );
}
