import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  getWorkloads,
  getWorkloadPods,
  scaleWorkload,
  getWorkloadOperation,
  restartWorkload,
  getPodLogs,
  streamPodLogs,
} from "@/lib/api/workloads";
import {
  getClusterMetrics,
  getClusterMetricsSummary,
  getWorkloadMetrics,
} from "@/lib/api/metrics";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { PodLog } from "@/types";
import { useOperationMutation } from "@/lib/hooks/operation-mutation";
import { k8sQueryKeys } from "@/lib/hooks/kubernetes-proxy";
import type { WorkloadSort } from "@/lib/api/workloads";
import {
  getResourceDef,
  k8sResourcePath,
  kindToResourceType,
} from "@/lib/k8s-paths";

// ============================================================
// Workload Hooks
// ============================================================

export function useWorkloads(
  clusterId: string,
  params?: {
    namespace?: string;
    kind?: string;
    search?: string;
    sort?: WorkloadSort;
    page?: number;
    pageSize?: number;
  },
) {
  return useQuery({
    queryKey: queryKeys.workloads.list(clusterId, params),
    queryFn: ({ signal }) => getWorkloads(clusterId, { ...params, signal }),
    enabled: !!clusterId,
    refetchInterval: liveFallback(15000),
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
      getWorkloadPods(clusterId, kind, namespace, name, signal),
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
      scaleWorkload(
        params.clusterId,
        params.kind,
        params.namespace,
        params.name,
        params.replicas,
        context,
      ),
    read: getWorkloadOperation,
    mutation: {
      onSuccess: (_data, variables) => {
        invalidateWorkloadResource(queryClient, variables);
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
      restartWorkload(
        params.clusterId,
        params.kind,
        params.namespace,
        params.name,
        context,
      ),
    read: getWorkloadOperation,
    mutation: {
      onSuccess: (_data, variables) => {
        invalidateWorkloadResource(queryClient, variables);
        toastSuccess("Workload restart completed");
      },
      onError: (error: Error) => {
        toastApiError("Failed to restart workload", error);
      },
    },
  });
}

function invalidateWorkloadResource(
  queryClient: ReturnType<typeof useQueryClient>,
  workload: {
    clusterId: string;
    kind: string;
    namespace: string;
    name: string;
  },
) {
  const lower = workload.kind.toLowerCase();
  const resourceType = getResourceDef(lower)
    ? lower
    : kindToResourceType(workload.kind);
  const path = k8sResourcePath(resourceType, workload.name, workload.namespace);
  queryClient.invalidateQueries({
    queryKey: k8sQueryKeys.resource(workload.clusterId, path),
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
    previous?: boolean;
  },
) {
  const streamIdentity = `${clusterId}/${namespace}/${pod}/${params?.container ?? ""}`;
  const [stream, setStream] = useState<{
    identity: string;
    logs: PodLog[];
  }>({ identity: streamIdentity, logs: [] });
  // Exposes WS connection state so consumers (e.g. the window-manager tab
  // strip) can show a pill without owning the WS lifecycle themselves.
  const [streamStatus, setStreamStatus] = useState<{
    identity: string;
    value: PodLogsStatus;
  }>({ identity: streamIdentity, value: "idle" });
  const cleanupRef = useRef<(() => void) | null>(null);
  // De-duplicate toasts: if the WS errors mid-stream we only want one toast
  // per (pod, container) selection rather than one per reconnect/dropped
  // frame.
  const errorShownRef = useRef<string | null>(null);

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
      params?.previous ?? false,
    ),
    queryFn: ({ signal }) =>
      getPodLogs(clusterId, namespace, pod, {
        container: params?.container,
        tailLines: effectiveTailLines,
        sinceSeconds: effectiveSinceSeconds,
        previous: params?.previous,
        signal,
      }),
    enabled: !!clusterId && !!namespace && !!pod,
  });

  // Streaming
  useEffect(() => {
    if (
      !params?.follow ||
      params?.previous ||
      !clusterId ||
      !namespace ||
      !pod
    ) {
      return;
    }

    const cleanup = streamPodLogs(
      clusterId,
      namespace,
      pod,
      params?.container || "",
      (log) => {
        setStreamStatus({ identity: streamIdentity, value: "streaming" });
        setStream((previous) =>
          previous.identity === streamIdentity
            ? {
                identity: streamIdentity,
                logs: [...previous.logs.slice(-2000), log],
              }
            : { identity: streamIdentity, logs: [log] },
        );
      },
      (err) => {
        setStreamStatus({ identity: streamIdentity, value: "disconnected" });
        // Surface the first error per stream so the user knows the live
        // tail dropped — but suppress duplicates so a flapping agent
        // doesn't spam the corner of the screen.
        if (errorShownRef.current !== streamIdentity) {
          errorShownRef.current = streamIdentity;
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
    };
  }, [
    clusterId,
    namespace,
    pod,
    params?.container,
    params?.follow,
    params?.previous,
    effectiveTailLines,
    effectiveSinceSeconds,
    streamIdentity,
  ]);

  // Always surface the streamed tail in `allLogs`, even after the caller
  // turned follow off. The previous behavior dropped streamLogs entirely
  // when follow=false, which made the live tail vanish the instant the
  // user clicked Pause — they thought the button had failed. "Pause"
  // should stop *accumulating* new lines (the streaming useEffect tears
  // down when follow is false) without losing what was already on
  // screen.
  const streamLogs = stream.identity === streamIdentity ? stream.logs : [];
  const status: PodLogsStatus =
    !params?.follow || params?.previous || !clusterId || !namespace || !pod
      ? "idle"
      : streamStatus.identity === streamIdentity
        ? streamStatus.value
        : "connecting";
  const allLogs = [...(query.data || []), ...streamLogs];

  const stopStreaming = useCallback(() => {
    cleanupRef.current?.();
    cleanupRef.current = null;
    setStreamStatus({ identity: streamIdentity, value: "disconnected" });
  }, [streamIdentity]);

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
    queryFn: ({ signal }) => getClusterMetrics(clusterId, { range }, signal),
    enabled: !!clusterId,
    // While the stream is open, `cluster.metrics` ticks invalidate the
    // per-cluster metrics prefix (see lib/live/routes.ts).
    refetchInterval: liveFallback(60000),
  });
}

export function useClusterMetricsSummary(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.clusters.metricsSummary(clusterId),
    queryFn: ({ signal }) => getClusterMetricsSummary(clusterId, signal),
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
      getWorkloadMetrics(clusterId, kind, namespace, name, { range }, signal),
    enabled: !!clusterId && !!kind && !!namespace && !!name,
    // No per-workload metrics event exists; while the stream is open this
    // refreshes on the cluster's Pod/workload `cluster.k8s_changed` churn
    // (routed through the per-cluster workloads prefix) and on stream
    // transitions.
    refetchInterval: liveFallback(60000),
  });
}
