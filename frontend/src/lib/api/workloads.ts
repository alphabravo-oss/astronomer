import { createStreamTicket } from "@/lib/api/auth";
import {
  deleteWorkloadsPodsByClusterIdByNamespaceByPod,
  getClustersByClusterIdEvents,
  getClustersByClusterIdNamespaces,
  getClustersByClusterIdPods,
  getClustersByClusterIdWorkloads,
  getClustersByClusterIdWorkloadsByKindByNamespaceByNamePods,
  getWorkloadsOperationsById,
  getWorkloadsPodsByClusterIdByNamespaceByPodLogs,
  patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale,
  postClustersByClusterIdWorkloadsByKindByNamespaceByNameRestart,
} from "@/lib/api/generated/client";
import {
  createIdempotencyKey,
  type OperationSnapshot,
} from "@/lib/api/operation-polling";
import { mapPage } from "@/lib/api/pagination";
import { wsBase } from "@/lib/env";
import type {
  ClusterEvent,
  Namespace,
  PaginatedResponse,
  Pod,
  PodLog,
  Workload,
} from "@/types";
import type {
  ClusterEvent as ClusterEventWire,
  Namespace as NamespaceWire,
  Pod as PodWire,
  PodLogEntry,
  Workload as WorkloadWire,
  WorkloadOperation,
} from "@/types/openapi.generated";

function requireString(value: string | undefined, field: string): string {
  if (!value) throw new Error(`Workload response omitted ${field}`);
  return value;
}

function mapNamespace(wire: NamespaceWire): Namespace {
  return {
    name: requireString(wire.name, "namespace.name"),
    clusterId: requireString(wire.clusterId, "namespace.clusterId"),
    status: wire.status === "Terminating" ? "Terminating" : "Active",
    labels: wire.labels ?? {},
    annotations: wire.annotations ?? {},
    podCount: wire.podCount ?? 0,
    cpuUsage: wire.cpuUsage ?? 0,
    cpuLimit: wire.cpuLimit ?? 0,
    memoryUsage: wire.memoryUsage ?? 0,
    memoryLimit: wire.memoryLimit ?? 0,
    createdAt: requireString(wire.createdAt, "namespace.createdAt"),
  };
}

function mapClusterEvent(wire: ClusterEventWire): ClusterEvent {
  const object = wire.involvedObject;
  if (!object?.kind || !object.name) {
    throw new Error("Cluster event response omitted involvedObject");
  }
  return {
    id: requireString(wire.id, "event.id"),
    type: wire.type === "Warning" ? "Warning" : "Normal",
    reason: wire.reason ?? "",
    message: wire.message ?? "",
    involvedObject: {
      kind: object.kind,
      name: object.name,
      namespace: object.namespace,
    },
    count: wire.count ?? 0,
    firstTimestamp: wire.firstTimestamp ?? "",
    lastTimestamp: wire.lastTimestamp ?? "",
  };
}

function mapWorkload(wire: WorkloadWire): Workload {
  const kinds = new Set([
    "Deployment",
    "StatefulSet",
    "DaemonSet",
    "Job",
    "CronJob",
    "ReplicaSet",
  ]);
  const statuses = new Set([
    "Running",
    "Pending",
    "Failed",
    "Succeeded",
    "Unknown",
  ]);
  const kind = requireString(wire.kind, "workload.kind");
  const status = wire.status ?? "Unknown";
  if (!kinds.has(kind) || !statuses.has(status)) {
    throw new Error("Workload response contains an unsupported kind or status");
  }
  return {
    name: requireString(wire.name, "workload.name"),
    namespace: requireString(wire.namespace, "workload.namespace"),
    kind: kind as Workload["kind"],
    clusterId: requireString(wire.clusterId, "workload.clusterId"),
    clusterName: wire.clusterName ?? "",
    status: status as Workload["status"],
    ready: wire.ready ?? "0/0",
    upToDate: wire.upToDate ?? 0,
    available: wire.available ?? 0,
    replicas: wire.replicas ?? 0,
    desiredReplicas: wire.desiredReplicas ?? 0,
    images: wire.images ?? [],
    labels: wire.labels ?? {},
    annotations: wire.annotations ?? {},
    createdAt: wire.createdAt ?? "",
    age: wire.age ?? "",
  };
}

function mapPod(wire: PodWire): Pod {
  const phases = new Set([
    "Running",
    "Pending",
    "Succeeded",
    "Failed",
    "Unknown",
  ]);
  const phase = wire.phase ?? "Unknown";
  if (!phases.has(phase)) throw new Error(`Unsupported pod phase: ${phase}`);
  return {
    name: requireString(wire.name, "pod.name"),
    namespace: requireString(wire.namespace, "pod.namespace"),
    clusterId: requireString(wire.clusterId, "pod.clusterId"),
    phase: phase as Pod["phase"],
    status: wire.status ?? phase,
    ready: wire.ready ?? "0/0",
    restarts: wire.restarts ?? 0,
    lastRestartAt: wire.lastRestartAt ?? undefined,
    node: wire.node ?? "",
    ip: wire.ip ?? "",
    images: wire.images ?? [],
    containers: (wire.containers ?? []) as unknown as Pod["containers"],
    conditions: (wire.conditions ?? []) as unknown as Pod["conditions"],
    createdAt: wire.createdAt ?? "",
    age: wire.age ?? "",
  };
}

function mapPodLog(wire: PodLogEntry): PodLog {
  return {
    timestamp: wire.timestamp ?? "",
    message: wire.message ?? "",
    container: wire.container ?? "",
  };
}

function mapOperation(wire: WorkloadOperation | undefined): OperationSnapshot {
  if (!wire?.id || !wire.status) {
    throw new Error("Workload operation receipt is incomplete");
  }
  return {
    id: wire.id,
    status: wire.status,
    errorMessage: wire.errorMessage,
  };
}

export async function getClusterNamespaces(
  clusterId: string,
  signal?: AbortSignal,
): Promise<Namespace[]> {
  const namespaces: Namespace[] = [];
  let offset = 0;
  let hasMore = true;
  while (hasMore) {
    const response = await getClustersByClusterIdNamespaces({
      path: { cluster_id: clusterId },
      query: { limit: 200, offset },
      signal,
    });
    namespaces.push(...(response.data ?? []).map(mapNamespace));
    const nextOffset = response.pagination.next_offset;
    hasMore = response.pagination.has_more && nextOffset !== null;
    if (!hasMore || nextOffset === null) break;
    if (nextOffset <= offset) {
      throw new Error("Namespace pagination did not advance");
    }
    offset = nextOffset;
  }
  return namespaces;
}

export async function getClusterEvents(
  clusterId: string,
  params?: { limit?: number; signal?: AbortSignal },
): Promise<ClusterEvent[]> {
  const response = await getClustersByClusterIdEvents({
    path: { cluster_id: clusterId },
    query: { limit: params?.limit },
    signal: params?.signal,
  });
  return (response.data ?? []).map(mapClusterEvent);
}

export async function getClusterPods(
  clusterId: string,
  params?: {
    namespace?: string;
    limit?: number;
    offset?: number;
    search?: string;
    sort?: PodSort;
    health?: "all" | "attention" | "restarted";
    signal?: AbortSignal;
  },
): Promise<PaginatedResponse<Pod>> {
  const response = await getClustersByClusterIdPods({
    path: { cluster_id: clusterId },
    query: {
      namespace: params?.namespace,
      limit: params?.limit,
      offset: params?.offset,
      search: params?.search,
      sort: params?.sort,
      health: params?.health,
    },
    signal: params?.signal,
  });
  return mapPage(
    {
      data: response.data ?? [],
      pagination: response.pagination,
    },
    mapPod,
  );
}

export type PodSort =
  | "namespace_asc"
  | "namespace_desc"
  | "name_asc"
  | "name_desc"
  | "status_asc"
  | "status_desc"
  | "restarts_asc"
  | "restarts_desc"
  | "node_asc"
  | "node_desc"
  | "age_asc"
  | "age_desc";

export async function deletePod(
  clusterId: string,
  namespace: string,
  pod: string,
  options: { idempotencyKey: string; signal?: AbortSignal },
): Promise<OperationSnapshot> {
  const response = await deleteWorkloadsPodsByClusterIdByNamespaceByPod({
    path: { cluster_id: clusterId, namespace, pod },
    headerParams: { "Idempotency-Key": options.idempotencyKey },
    signal: options.signal,
  });
  return mapOperation(response.data);
}

export async function getWorkloadOperation(
  id: string,
  signal?: AbortSignal,
): Promise<OperationSnapshot> {
  const response = await getWorkloadsOperationsById({ path: { id }, signal });
  return mapOperation(response.data);
}

export async function getWorkloads(
  clusterId: string,
  params?: {
    namespace?: string;
    namespaces?: string[];
    kind?: string;
    search?: string;
    sort?: WorkloadSort;
    page?: number;
    pageSize?: number;
    offset?: number;
    signal?: AbortSignal;
  },
): Promise<PaginatedResponse<Workload>> {
  const page = Math.max(1, params?.page ?? 1);
  const pageSize = Math.max(1, params?.pageSize ?? 20);
  const response = await getClustersByClusterIdWorkloads({
    path: { cluster_id: clusterId },
    query: {
      limit: pageSize,
      offset: params?.offset ?? (page - 1) * pageSize,
      namespace: params?.namespace,
      namespaces: params?.namespaces,
      kind: params?.kind,
      search: params?.search,
      sort: params?.sort,
    },
    signal: params?.signal,
  });
  return mapPage(
    { data: response.data ?? [], pagination: response.pagination },
    mapWorkload,
  );
}

export type WorkloadSort =
  | "namespace_asc"
  | "namespace_desc"
  | "name_asc"
  | "name_desc"
  | "created_asc"
  | "created_desc";

export async function scaleWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  replicas: number,
  options: { idempotencyKey?: string; signal?: AbortSignal } = {},
): Promise<OperationSnapshot> {
  const response =
    await patchClustersByClusterIdWorkloadsByKindByNamespaceByNameScale({
      path: { cluster_id: clusterId, kind, namespace, name },
      headerParams: {
        "Idempotency-Key":
          options.idempotencyKey ?? createIdempotencyKey("workload-scale"),
      },
      body: { replicas },
      signal: options.signal,
    });
  return mapOperation(response.data);
}

export async function restartWorkload(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  options: { idempotencyKey?: string; signal?: AbortSignal } = {},
): Promise<OperationSnapshot> {
  const response =
    await postClustersByClusterIdWorkloadsByKindByNamespaceByNameRestart({
      path: { cluster_id: clusterId, kind, namespace, name },
      headerParams: {
        "Idempotency-Key":
          options.idempotencyKey ?? createIdempotencyKey("workload-restart"),
      },
      signal: options.signal,
    });
  return mapOperation(response.data);
}

export async function getWorkloadPods(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
  signal?: AbortSignal,
): Promise<Pod[]> {
  const response =
    await getClustersByClusterIdWorkloadsByKindByNamespaceByNamePods({
      path: { cluster_id: clusterId, kind, namespace, name },
      signal,
    });
  return (response.data ?? []).map(mapPod);
}

export async function getPodLogs(
  clusterId: string,
  namespace: string,
  pod: string,
  params?: {
    container?: string;
    tailLines?: number;
    sinceSeconds?: number;
    follow?: boolean;
    previous?: boolean;
    signal?: AbortSignal;
  },
): Promise<PodLog[]> {
  const response = await getWorkloadsPodsByClusterIdByNamespaceByPodLogs({
    path: { cluster_id: clusterId, namespace, pod },
    query: {
      container: params?.container,
      tailLines:
        params?.tailLines && params.tailLines > 0
          ? params.tailLines
          : undefined,
      sinceSeconds:
        params?.sinceSeconds && params.sinceSeconds > 0
          ? params.sinceSeconds
          : undefined,
      follow: params?.follow ? "true" : undefined,
      previous: params?.previous,
    },
    signal: params?.signal,
  });
  return (response.data ?? []).map(mapPodLog);
}

/**
 * Stream pod logs using a one-use ticket because browser WebSockets cannot
 * attach the authenticated transport's custom headers.
 */
export function streamPodLogs(
  clusterId: string,
  namespace: string,
  pod: string,
  container: string,
  onMessage: (log: import("@/types").PodLog) => void,
  onError?: (error: { code?: string; message: string }) => void,
  opts?: { follow?: boolean; tailLines?: number; sinceSeconds?: number },
): () => void {
  const base = wsBase();
  const params = new URLSearchParams();
  if (opts?.follow) params.set("follow", "true");
  if (opts?.tailLines && opts.tailLines > 0)
    params.set("tail_lines", String(opts.tailLines));
  if (opts?.sinceSeconds && opts.sinceSeconds > 0)
    params.set("since_seconds", String(opts.sinceSeconds));

  let closed = false;
  let ws: WebSocket | null = null;

  createStreamTicket("logs", clusterId)
    .then(({ ticket }) => {
      if (closed) return;
      params.set("ticket", ticket);
      const wsUrl =
        `${base}/logs/${clusterId}/${namespace}/${encodeURIComponent(pod)}/${encodeURIComponent(container)}/` +
        (params.toString() ? `?${params.toString()}` : "");
      ws = new WebSocket(wsUrl);

      ws.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          if (data && data.type === "error") {
            onError?.({
              code: data.code,
              message: data.message || "log stream error",
            });
            return;
          }
          onMessage(data);
        } catch {
          onMessage({
            timestamp: new Date().toISOString(),
            message: event.data,
            container,
          });
        }
      };

      ws.onerror = () => {
        if (!closed) onError?.({ message: "WebSocket connection error" });
      };

      ws.onclose = (event) => {
        if (!closed && event.code !== 1000 && event.code !== 1005) {
          onError?.({
            code: String(event.code),
            message: event.reason || `log stream closed (code ${event.code})`,
          });
        }
      };
    })
    .catch((error: Error) => {
      if (!closed) {
        onError?.({
          message: error.message || "Failed to create log stream ticket",
        });
      }
    });

  return () => {
    closed = true;
    try {
      ws?.close(1000, "client unmount");
    } catch {
      // The connection may already be closed during component cleanup.
    }
  };
}
