import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import {
  getLoggingOutputs,
  createLoggingOutput,
  getLoggingAttachStatus,
  attachAstronomerLogs,
  testLoggingOutput,
  getLoggingPipelines,
  createLoggingPipeline,
  getLoggingOperations,
  getLoggingOperation,
  retryLoggingOperation,
} from "@/lib/api/logging";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { LoggingOperation, LoggingOutput, LoggingPipeline } from "@/types";

// ============================================================
// Logging Hooks
// ============================================================

export function useLoggingOutputs(
  clusterId?: string,
  options?: { enabled?: boolean },
) {
  const query = useInfiniteQuery({
    queryKey: queryKeys.logging.outputPages(clusterId),
    initialPageParam: 0,
    queryFn: ({ pageParam, signal }) =>
      getLoggingOutputs(clusterId, pageParam, signal),
    getNextPageParam: (page) => {
      const { has_more, next_offset, offset } = page.pagination;
      return has_more && next_offset !== null && next_offset > offset
        ? next_offset
        : undefined;
    },
    enabled: options?.enabled,
    throwOnError: false,
  });
  return {
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    refetch: query.refetch,
    hasNextPage: query.hasNextPage,
    isFetchingNextPage: query.isFetchingNextPage,
    fetchNextPage: query.fetchNextPage,
    data: query.isError
      ? undefined
      : query.data?.pages.flatMap((page) => page.data),
  };
}

export function useCreateLoggingOutput() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<LoggingOutput>) => createLoggingOutput(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.outputs });
      toastSuccess("Logging output created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create logging output", error);
    },
  });
}

export function useLoggingAttachStatus(clusterId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.logging.attachStatus(clusterId ?? ""),
    queryFn: () => getLoggingAttachStatus(clusterId as string),
    enabled: Boolean(clusterId),
  });
}

export function useAttachAstronomerLogs(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rotate?: boolean) =>
      attachAstronomerLogs(clusterId, Boolean(rotate)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      queryClient.invalidateQueries({
        queryKey: queryKeys.logging.attachStatus(clusterId),
      });
      toastSuccess("Astronomer logs attached");
    },
    onError: (error: Error) => {
      toastApiError("Failed to attach Astronomer logs", error);
    },
  });
}

export function useTestLoggingOutput() {
  return useMutation({
    mutationFn: (id: string) => testLoggingOutput(id),
    onSuccess: (data) => {
      if (data.success) {
        toastSuccess("Test connection successful");
      } else {
        toastApiError("Test failed", data.message);
      }
    },
    onError: (error: Error) => {
      toastApiError("Test failed", error);
    },
  });
}

export function useLoggingPipelines(clusterId?: string) {
  return useQuery({
    queryKey: queryKeys.logging.pipelines(clusterId),
    queryFn: () => getLoggingPipelines({ clusterId, limit: 200 }),
  });
}

export function useCreateLoggingPipeline() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<LoggingPipeline>) => createLoggingPipeline(data),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.logging.pipelinesAll,
      });
      toastSuccess("Logging pipeline created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create logging pipeline", error);
    },
  });
}

// --- Logging Operations (controller-backed reconciler) ---

export function useLoggingOperations(params?: {
  status?: string;
  target_type?: string;
  limit?: number;
  offset?: number;
}) {
  return useQuery({
    queryKey: queryKeys.logging.operations(params),
    queryFn: ({ signal }) => getLoggingOperations(params, signal),
    throwOnError: false,
    // `logging_operation.changed` drives freshness while the stream is open;
    // poll so pending -> running -> completed transitions still appear when
    // it is down.
    refetchInterval: (query) =>
      query.state.status === "error" ? false : liveFallback(5000)(),
  });
}

export function useLoggingOperation(id: string) {
  return useQuery<LoggingOperation>({
    queryKey: queryKeys.logging.operation(id),
    queryFn: () => getLoggingOperation(id),
    enabled: !!id,
    refetchInterval: liveFallback(5000),
  });
}

export function useRetryLoggingOperation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => retryLoggingOperation(id),
    onSuccess: () => {
      // Invalidate every cached list (parameterized keys) and the detail rows.
      queryClient.invalidateQueries({
        queryKey: queryKeys.logging.operationsAll,
      });
      toastSuccess("Operation retry queued");
    },
    onError: (error: Error) => {
      toastApiError("Failed to retry operation", error);
    },
  });
}
