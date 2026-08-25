import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import * as apiClient from "@/lib/api";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { PodLog } from "@/types";
import { useOperationMutation } from "@/lib/hooks/operation-mutation";

// ============================================================
// Workload Hooks
// ============================================================

export function useWorkloads(
  clusterId: string,
  params?: {
    namespace?: string;
    kind?: string;
    search?: string;
    page?: number;
    pageSize?: number;
  },
) {
  return useQuery({
    queryKey: queryKeys.workloads.list(clusterId, params),
    queryFn: ({ signal }) =>
      apiClient.getWorkloads(clusterId, { ...params, signal }),
    enabled: !!clusterId,
    refetchInterval: liveFallback(15000),
  });
}

export function useWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
) {
  return useQuery({
    queryKey: queryKeys.workloads.detail(clusterId, kind, namespace, name),
    queryFn: ({ signal }) =>
      apiClient.getWorkload(clusterId, kind, namespace, name, signal),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    refetchInterval: liveFallback(10000),
  });
}

export function useWorkloadPods(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
) {
  return useQuery({
    queryKey: queryKeys.workloads.pods(clusterId, kind, namespace, name),
    queryFn: ({ signal }) =>
      apiClient.getWorkloadPods(clusterId, kind, namespace, name, signal),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    refetchInterval: liveFallback(10000),
  });
}

export function useScaleWorkload() {
  const queryClient = useQueryClient();
  return useOperationMutation({
    keyPrefix: "workload-scale",
    submit: (
      params: {
        clusterId: string;
        kind: string;
        namespace: string;
        name: string;
        replicas: number;
      },
      context,
    ) =>
      apiClient.scaleWorkload(
        params.clusterId,
        params.kind,
        params.namespace,
        params.name,
        params.replicas,
        context,
      ),
    read: apiClient.getWorkloadOperation,
    mutation: {
      onSuccess: (_data, variables) => {
        queryClient.invalidateQueries({
          queryKey: queryKeys.workloads.detail(
            variables.clusterId,
            variables.kind,
            variables.namespace,
            variables.name,
          ),
        });
        toastSuccess(`Scaled to ${variables.replicas} replicas`);
      },
      onError: (error: Error) => {
        toastApiError("Failed to scale workload", error);
      },
    },
  });
}

export function useRestartWorkload() {
  const queryClient = useQueryClient();
  return useOperationMutation({
    keyPrefix: "workload-restart",
    submit: (
      params: {
        clusterId: string;
        kind: string;
        namespace: string;
        name: string;
      },
      context,
    ) =>
      apiClient.restartWorkload(
        params.clusterId,
        params.kind,
        params.namespace,
        params.name,
        context,
      ),
    read: apiClient.getWorkloadOperation,
    mutation: {
      onSuccess: (_data, variables) => {
        queryClient.invalidateQueries({
          queryKey: queryKeys.workloads.detail(
            variables.clusterId,
            variables.kind,
            variables.namespace,
            variables.name,
          ),
        });
        toastSuccess("Workload restart completed");
      },
      onError: (error: Error) => {
        toastApiError("Failed to restart workload", error);
      },
    },
  });
}

// ============================================================
// Pod Logs Hook (with streaming)
// ============================================================

export type PodLogsStatus =
  "connecting" | "streaming" | "disconnected" | "idle";

export function usePodLogs(
  clusterId: string,
  namespace: string,
  pod: string,
  params?: {
    container?: string;
    tailLines?: number;
    // sinceSeconds enables Rancher-style time-window queries. When set, it
    // takes precedence over tailLines (both can be passed, but the UI
    // picker chooses exactly one mode).
    sinceSeconds?: number;
    // noTail disables both the line-count and time-window limits ("All").
    // We need an explicit flag because `tailLines === undefined` already
    // means "use the default of 500"; without this we can't represent
    // "give me everything."
    noTail?: boolean;
    follow?: boolean;
  },
) {
  const [streamLogs, setStreamLogs] = useState<PodLog[]>([]);
  // Exposes WS connection state so consumers (e.g. the window-manager tab
  // strip) can show a pill without owning the WS lifecycle themselves.
  const [status, setStatus] = useState<PodLogsStatus>("idle");
  const cleanupRef = useRef<(() => void) | null>(null);
  // De-duplicate toasts: if the WS errors mid-stream we only want one toast
  // per (pod, container) selection rather than one per reconnect/dropped
  // frame.
  const errorShownRef = useRef(false);

  // Resolve the effective query params once so the initial fetch, the
  // streaming useEffect, and the queryKey all agree. The precedence is:
  //   noTail  -> no limit at all
  //   sinceSeconds set -> time-window mode (kubelet sinceSeconds)
  //   tailLines set -> line-count mode
  //   otherwise -> default 500 lines
  const effectiveTailLines = params?.noTail
    ? undefined
    : params?.sinceSeconds && params.sinceSeconds > 0
      ? undefined
      : (params?.tailLines ?? 500);
  const effectiveSinceSeconds = params?.noTail
    ? undefined
    : params?.sinceSeconds && params.sinceSeconds > 0
      ? params.sinceSeconds
      : undefined;

  // Initial fetch — include tail/since in the queryKey so flipping modes
  // refetches instead of serving the previous mode's cache.
  const query = useQuery({
    queryKey: queryKeys.podLogsFetch(
      clusterId,
      namespace,
      pod,
      params?.container,
      effectiveTailLines ?? "no-tail",
      effectiveSinceSeconds ?? "no-since",
    ),
    queryFn: ({ signal }) =>
      apiClient.getPodLogs(clusterId, namespace, pod, {
        container: params?.container,
        tailLines: effectiveTailLines,
        sinceSeconds: effectiveSinceSeconds,
        signal,
      }),
    enabled: !!clusterId && !!namespace && !!pod,
  });

  // Reset the streaming buffer when the pod or container changes. Without
  // this, switching pods would show the old pod's tail of stream lines
  // prepended to the new pod's REST fetch — a real correctness bug that
  // also leaked memory across switches.
  useEffect(() => {
    setStreamLogs([]);
    errorShownRef.current = false;
  }, [clusterId, namespace, pod, params?.container]);

  // Streaming
  useEffect(() => {
    if (!params?.follow || !clusterId || !namespace || !pod) {
      setStatus("idle");
      return;
    }

    setStatus("connecting");
    const cleanup = apiClient.streamPodLogs(
      clusterId,
      namespace,
      pod,
      params?.container || "",
      (log) => {
        setStatus("streaming");
        setStreamLogs((prev) => [...prev.slice(-2000), log]);
      },
      (err) => {
        setStatus("disconnected");
        // Surface the first error per stream so the user knows the live
        // tail dropped — but suppress duplicates so a flapping agent
        // doesn't spam the corner of the screen.
        if (!errorShownRef.current) {
          errorShownRef.current = true;
          toastApiError("Log stream", err);
        }
      },
      {
        follow: true,
        // The REST query above already returned the historical tail (up to
        // 500 lines / the since-window). Opening the follow stream with the
        // same tail/since would replay all of those lines again, duplicating
        // the whole buffer on every open. Ask the backend for NO backfill
        // (tail_lines=0) and only lines newer than ~now (since=1s) so the
        // stream contributes strictly NEW output on top of the REST fetch.
        tailLines: 0,
        sinceSeconds: 1,
      },
    );

    cleanupRef.current = cleanup;

    return () => {
      cleanup();
      cleanupRef.current = null;
      setStatus("idle");
    };
  }, [
    clusterId,
    namespace,
    pod,
    params?.container,
    params?.follow,
    effectiveTailLines,
    effectiveSinceSeconds,
  ]);

  // Always surface the streamed tail in `allLogs`, even after the caller
  // turned follow off. The previous behavior dropped streamLogs entirely
  // when follow=false, which made the live tail vanish the instant the
  // user clicked Pause — they thought the button had failed. "Pause"
  // should stop *accumulating* new lines (the streaming useEffect tears
  // down when follow is false) without losing what was already on
  // screen.
  const allLogs = [...(query.data || []), ...streamLogs];

  const stopStreaming = useCallback(() => {
    cleanupRef.current?.();
    cleanupRef.current = null;
  }, []);

  // Forward query fields explicitly rather than spreading the whole result —
  // spreading a TanStack Query result subscribes the consumer to every field
  // and defeats its fine-grained re-render tracking (@tanstack/query/no-rest-destructuring).
  return {
    data: allLogs,
    isLoading: query.isLoading,
    isError: query.isError,
    error: query.error,
    refetch: query.refetch,
    streamLogs,
    stopStreaming,
    status,
  };
}

// ============================================================
// Metrics Hooks
// ============================================================

export function useClusterMetrics(clusterId: string, range?: string) {
  return useQuery({
    queryKey: queryKeys.clusters.metrics(clusterId, range),
    queryFn: ({ signal }) =>
      apiClient.getClusterMetrics(clusterId, { range }, signal),
    enabled: !!clusterId,
    // While the stream is open, `cluster.metrics` ticks invalidate the
    // per-cluster metrics prefix (see lib/live/routes.ts).
    refetchInterval: liveFallback(60000),
  });
}

export function useClusterMetricsSummary(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.metricsSummary(clusterId),
    queryFn: ({ signal }) => apiClient.getClusterMetricsSummary(clusterId, signal),
    enabled: !!clusterId,
    refetchInterval: liveFallback(30000),
  });
}

export function useWorkloadMetrics(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  range?: string,
) {
  return useQuery({
    queryKey: queryKeys.workloads.metrics(
      clusterId,
      kind,
      namespace,
      name,
      range,
    ),
    queryFn: ({ signal }) =>
      apiClient.getWorkloadMetrics(clusterId, kind, namespace, name, { range }, signal),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    // No per-workload metrics event exists; while the stream is open this
    // refreshes on the cluster's Pod/workload `cluster.k8s_changed` churn
    // (routed through the per-cluster workloads prefix) and on stream
    // transitions.
    refetchInterval: liveFallback(60000),
  });
}
