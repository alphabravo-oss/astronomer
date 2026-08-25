import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import * as apiClient from "@/lib/api";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { LoggingOperation, LoggingOutput, LoggingPipeline } from "@/types";

// ============================================================
// Logging Hooks
// ============================================================

export function useLoggingOutputs() {
  return useQuery({
    queryKey: queryKeys.logging.outputs,
    queryFn: () => apiClient.getLoggingOutputs(),
  });
}

export function useCreateLoggingOutput() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<LoggingOutput>) =>
      apiClient.createLoggingOutput(data),
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
    queryFn: () => apiClient.getLoggingAttachStatus(clusterId as string),
    enabled: Boolean(clusterId),
  });
}

export function useAttachAstronomerLogs(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rotate?: boolean) =>
      apiClient.attachAstronomerLogs(clusterId, Boolean(rotate)),
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
    mutationFn: (id: string) => apiClient.testLoggingOutput(id),
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
    queryFn: () => apiClient.getLoggingPipelines({ clusterId, limit: 200 }),
  });
}

export function useCreateLoggingPipeline() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: Partial<LoggingPipeline>) =>
      apiClient.createLoggingPipeline(data),
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
  return useQuery<LoggingOperation[]>({
    queryKey: queryKeys.logging.operations(params),
    queryFn: () => apiClient.getLoggingOperations(params),
    // `logging_operation.changed` drives freshness while the stream is open;
    // poll so pending -> running -> completed transitions still appear when
    // it is down.
    refetchInterval: liveFallback(5000),
  });
}

export function useLoggingOperation(id: string) {
  return useQuery<LoggingOperation>({
    queryKey: queryKeys.logging.operation(id),
    queryFn: () => apiClient.getLoggingOperation(id),
    enabled: !!id,
    refetchInterval: liveFallback(5000),
  });
}

export function useRetryLoggingOperation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.retryLoggingOperation(id),
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
