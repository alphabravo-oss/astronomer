import type { ReactNode } from "react";

import { apiErrorStatus, extractApiErrorMessage } from "@/lib/api/errors";
import {
  ErrorState,
  LoadingState,
  OfflineState,
  PermissionState,
} from "@/components/ui/empty-state";

export interface QueryState<T> {
  data?: T;
  error?: unknown;
  isError: boolean;
  isLoading: boolean;
  refetch: () => unknown;
}

interface QueryStatesProps<T> {
  query: QueryState<T>;
  children: ReactNode | ((data: T) => ReactNode);
  isEmpty?: (data: T) => boolean;
  empty?: ReactNode;
  notFound?: ReactNode;
  loadingTitle?: string;
  loadingDescription?: ReactNode;
  errorTitle?: string;
  errorDescription?: ReactNode;
  permission?: string;
  permissionDescription?: ReactNode;
  className?: string;
}

/**
 * Exhaustive rendering for query-backed surfaces.
 *
 * Keeping this classification in one place prevents a failed request from
 * being rendered as an empty collection or a missing object. Server failures
 * may still be promoted to the nearest route error boundary by QueryClient;
 * this component owns recoverable 4xx and transport states.
 */
export function QueryStates<T>({
  query,
  children,
  isEmpty,
  empty,
  notFound,
  loadingTitle,
  loadingDescription,
  errorTitle,
  errorDescription,
  permission,
  permissionDescription,
  className,
}: QueryStatesProps<T>) {
  if (query.isLoading && query.data === undefined) {
    return (
      <LoadingState
        title={loadingTitle}
        description={loadingDescription}
        className={className}
      />
    );
  }

  if (query.isError) {
    const status = apiErrorStatus(query.error);
    const retry = () => void query.refetch();

    if (status === 401 || status === 403) {
      return (
        <PermissionState
          permission={permission}
          description={permissionDescription}
          className={className}
        />
      );
    }

    if (status === 404 && notFound !== undefined) {
      return notFound;
    }

    if (status === 0) {
      return (
        <OfflineState
          description={
            errorDescription ??
            "Astronomer could not reach the API. Check your connection and try again."
          }
          onRetry={retry}
          className={className}
        />
      );
    }

    return (
      <ErrorState
        title={errorTitle}
        description={
          errorDescription ??
          extractApiErrorMessage(query.error) ??
          `The API returned status ${status}.`
        }
        onRetry={retry}
        className={className}
      />
    );
  }

  if (query.data === undefined) {
    return (
      <ErrorState
        title={errorTitle}
        description={errorDescription ?? "The response did not contain data."}
        onRetry={() => void query.refetch()}
        className={className}
      />
    );
  }

  if (isEmpty?.(query.data)) {
    return empty ?? null;
  }

  return typeof children === "function" ? children(query.data) : children;
}
