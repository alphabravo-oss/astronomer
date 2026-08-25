import type { OwnershipTransferResult } from "@/lib/api/ownership";
import {
  getClustersByClusterIdNodes,
  getClustersByClusterIdNodesByNodeName,
  getClustersByIdConditionRemediation,
  getClustersByIdConditions,
  getNodeOperation,
  postClustersByIdOwnershipTakeover,
  postNodesByClusterIdByNodeNameAnnotations,
  postNodesByClusterIdByNodeNameAnnotationsRemove,
  postNodesByClusterIdByNodeNameCordon,
  postNodesByClusterIdByNodeNameDrain,
  postNodesByClusterIdByNodeNameLabels,
  postNodesByClusterIdByNodeNameLabelsRemove,
  postNodesByClusterIdByNodeNameTaints,
  postNodesByClusterIdByNodeNameTaintsRemove,
  postNodesByClusterIdByNodeNameUncordon,
} from "@/lib/api/generated/client";
import type { OperationSnapshot } from "@/lib/api/operation-polling";
import type {
  ClusterCondition,
  ClusterConditionRemediationAttemptView,
  ClusterNode,
  NodeAddress,
  NodeCondition,
  NodeDetail,
  NodeDetailCondition,
  NodeEvent,
  NodeImage,
  NodeInfo,
  NodePod,
  NodeTaint,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type RequireKeys<T, K extends keyof T> = Omit<T, K> & {
  [P in K]-?: Exclude<T[P], undefined>;
};

export interface NodeReadOptions {
  signal?: AbortSignal;
}

type ClusterConditionWire =
  OpenAPIComponents["schemas"]["ClusterConditionResponse"];
type ClusterConditionRemediationWire =
  OpenAPIComponents["schemas"]["ClusterConditionRemediationAttempt"];
type ClusterNodeWire = OpenAPIComponents["schemas"]["NodeSummary"];
type NodeDetailWire = OpenAPIComponents["schemas"]["NodeDetail"];

function requireData<T>(value: T | undefined, operation: string): T {
  if (value === undefined) {
    throw new Error(`${operation} returned no data`);
  }
  return value;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function stringValue(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  return typeof value === "string" ? value : "";
}

function numberValue(record: Record<string, unknown>, key: string): number {
  const value = record[key];
  return typeof value === "number" ? value : 0;
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}

function recordArray(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.map(asRecord) : [];
}

function stringRecord(value: unknown): Record<string, string> {
  return Object.fromEntries(
    Object.entries(asRecord(value)).filter(
      (entry): entry is [string, string] => typeof entry[1] === "string",
    ),
  );
}

function nodeStatus(value: unknown): ClusterNode["status"] {
  if (
    value === "Ready" ||
    value === "NotReady" ||
    value === "SchedulingDisabled"
  ) {
    return value;
  }
  return "NotReady";
}

function conditionFromWire(wire: ClusterConditionWire): ClusterCondition {
  return {
    type: wire.type,
    status: wire.status,
    reason: wire.reason,
    message: wire.message,
    last_transition_time: wire.last_transition_time,
    last_probe_time: wire.last_probe_time,
  };
}

function remediationFromWire(
  wire: ClusterConditionRemediationWire,
): ClusterConditionRemediationAttemptView {
  return {
    id: wire.id,
    cluster_id: wire.cluster_id,
    condition_type: wire.condition_type,
    action: wire.action,
    outcome: wire.outcome,
    error: wire.error || null,
    detail: wire.detail,
    attempted_at: wire.attempted_at,
  };
}

function summaryConditionFromWire(
  record: Record<string, unknown>,
): NodeCondition {
  return {
    type: stringValue(record, "type"),
    status: stringValue(record, "status"),
    reason: stringValue(record, "reason") || undefined,
    message: stringValue(record, "message") || undefined,
    lastTransition: stringValue(record, "lastTransition"),
  };
}

function clusterNodeFromWire(wire: ClusterNodeWire): ClusterNode {
  return {
    name: wire.name ?? "",
    status: nodeStatus(wire.status),
    roles: wire.roles ?? [],
    kubernetesVersion: wire.kubernetesVersion ?? "",
    os: wire.os ?? "",
    architecture: wire.architecture ?? "",
    containerRuntime: wire.containerRuntime ?? "",
    cpuCapacity: wire.cpuCapacity ?? 0,
    cpuUsage: wire.cpuUsage ?? 0,
    memoryCapacity: wire.memoryCapacity ?? 0,
    memoryUsage: wire.memoryUsage ?? 0,
    podCapacity: wire.podCapacity ?? 0,
    podCount: wire.podCount ?? 0,
    conditions: (wire.conditions ?? []).map((item) =>
      summaryConditionFromWire(asRecord(item)),
    ),
    createdAt: wire.createdAt ?? "",
  };
}

function nodeInfoFromWire(value: unknown): NodeInfo {
  const wire = asRecord(value);
  return {
    machineId: stringValue(wire, "machineID"),
    systemUuid: stringValue(wire, "systemUUID"),
    bootId: stringValue(wire, "bootID"),
    kernelVersion: stringValue(wire, "kernelVersion"),
    osImage: stringValue(wire, "osImage"),
    containerRuntimeVersion: stringValue(wire, "containerRuntimeVersion"),
    kubeletVersion: stringValue(wire, "kubeletVersion"),
    kubeProxyVersion: stringValue(wire, "kubeProxyVersion"),
    operatingSystem: stringValue(wire, "operatingSystem"),
    architecture: stringValue(wire, "architecture"),
  };
}

function detailConditionFromWire(
  wire: Record<string, unknown>,
): NodeDetailCondition {
  return {
    type: stringValue(wire, "type"),
    status: stringValue(wire, "status"),
    reason: stringValue(wire, "reason") || undefined,
    message: stringValue(wire, "message") || undefined,
    lastHeartbeat: stringValue(wire, "lastHeartbeatTime"),
    lastTransition: stringValue(wire, "lastTransitionTime"),
  };
}

function addressFromWire(wire: Record<string, unknown>): NodeAddress {
  return {
    type: stringValue(wire, "type"),
    address: stringValue(wire, "address"),
  };
}

function taintFromWire(wire: Record<string, unknown>): NodeTaint {
  const effect = stringValue(wire, "effect");
  return {
    key: stringValue(wire, "key"),
    value: stringValue(wire, "value"),
    effect:
      effect === "NoExecute" || effect === "PreferNoSchedule"
        ? effect
        : "NoSchedule",
  };
}

function imageFromWire(wire: Record<string, unknown>): NodeImage {
  return {
    name: stringValue(wire, "name"),
    sizeBytes: numberValue(wire, "sizeBytes"),
  };
}

function podFromWire(wire: Record<string, unknown>): NodePod {
  return {
    name: stringValue(wire, "name"),
    namespace: stringValue(wire, "namespace"),
    status: stringValue(wire, "status"),
    ready: stringValue(wire, "ready"),
    restarts: numberValue(wire, "restarts"),
    createdAt: stringValue(wire, "createdAt"),
    images: stringArray(wire.images),
  };
}

function eventFromWire(wire: Record<string, unknown>): NodeEvent {
  return {
    type: stringValue(wire, "type"),
    reason: stringValue(wire, "reason"),
    message: stringValue(wire, "message"),
    count: numberValue(wire, "count"),
    firstTimestamp: stringValue(wire, "firstTimestamp"),
    lastTimestamp: stringValue(wire, "lastTimestamp"),
  };
}

function nodeDetailFromWire(wire: NodeDetailWire): NodeDetail {
  return {
    name: wire.name ?? "",
    status: nodeStatus(wire.status),
    roles: wire.roles ?? [],
    labels: stringRecord(wire.labels),
    annotations: stringRecord(wire.annotations),
    createdAt: wire.createdAt ?? "",
    nodeInfo: nodeInfoFromWire(wire.nodeInfo),
    cpuCapacity: wire.cpuCapacity ?? 0,
    cpuUsage: wire.cpuUsage ?? 0,
    memoryCapacity: wire.memoryCapacity ?? 0,
    memoryUsage: wire.memoryUsage ?? 0,
    podCapacity: wire.podCapacity ?? 0,
    podCount: wire.podCount ?? 0,
    addresses: recordArray(wire.addresses).map(addressFromWire),
    conditions: recordArray(wire.conditions).map(detailConditionFromWire),
    taints: recordArray(wire.taints).map(taintFromWire),
    images: recordArray(wire.images).map(imageFromWire),
    pods: recordArray(wire.pods).map(podFromWire),
    events: recordArray(wire.events).map(eventFromWire),
    unschedulable: wire.unschedulable ?? false,
  };
}

export async function takeoverClusterOwnership(
  id: string,
  options: NodeReadOptions = {},
): Promise<OwnershipTransferResult> {
  const response = await postClustersByIdOwnershipTakeover({
    path: { id },
    signal: options.signal,
  });
  const wire = requireData(response.data, "Cluster ownership takeover");
  return {
    id: wire.id,
    managedBy: wire.managed_by,
    transferred: wire.transferred,
  };
}

export async function getClusterConditions(
  clusterId: string,
  options: NodeReadOptions = {},
): Promise<ClusterCondition[]> {
  const response = await getClustersByIdConditions({
    path: { id: clusterId },
    signal: options.signal,
  });
  return response.data.map(conditionFromWire);
}

export async function getClusterConditionRemediation(
  clusterId: string,
  options: NodeReadOptions = {},
): Promise<ClusterConditionRemediationAttemptView[]> {
  const response = await getClustersByIdConditionRemediation({
    path: { id: clusterId },
    signal: options.signal,
  });
  return response.data.map(remediationFromWire);
}

export async function getClusterNodes(
  clusterId: string,
  options: NodeReadOptions = {},
): Promise<ClusterNode[]> {
  const items: ClusterNode[] = [];
  let offset = 0;
  let hasMore = true;
  while (hasMore) {
    const response = await getClustersByClusterIdNodes({
      path: { cluster_id: clusterId },
      query: { limit: 200, offset },
      signal: options.signal,
    });
    items.push(...response.data.map(clusterNodeFromWire));
    const nextOffset = response.pagination.next_offset;
    hasMore = response.pagination.has_more && nextOffset !== null;
    if (!hasMore || nextOffset === null) break;
    if (nextOffset <= offset) {
      throw new Error("Node pagination did not advance");
    }
    offset = nextOffset;
  }
  return items;
}

export async function getNodeDetail(
  clusterId: string,
  nodeName: string,
  options: NodeReadOptions = {},
): Promise<NodeDetail> {
  const response = await getClustersByClusterIdNodesByNodeName({
    path: { cluster_id: clusterId, node_name: nodeName },
    signal: options.signal,
  });
  return nodeDetailFromWire(requireData(response.data, "Cluster node detail"));
}

export type DrainNodeRequest = OpenAPIComponents["schemas"]["NodeDrainRequest"];
export type DrainNodePodRef = RequireKeys<
  OpenAPIComponents["schemas"]["NodeDrainPodRef"],
  "namespace" | "name"
>;
export type DrainNodeResponse = Omit<
  RequireKeys<
    OpenAPIComponents["schemas"]["NodeDrainResponse"],
    "node" | "status" | "message" | "evicted" | "skipped" | "failed"
  >,
  "evicted" | "skipped" | "failed"
> & {
  evicted: DrainNodePodRef[];
  skipped: DrainNodePodRef[];
  failed: DrainNodePodRef[];
};

export interface NodeOperationSnapshot extends OperationSnapshot {
  clusterId: string;
  nodeName: string;
  action: string;
  progress: Record<string, unknown>;
}

type NodeOperationWire = OpenAPIComponents["schemas"]["NodeOperation"];

function nodeOperationFromWire(
  operation: NodeOperationWire,
): NodeOperationSnapshot {
  const blockers = Array.isArray(operation.progress?.blockers)
    ? operation.progress.blockers.filter(
        (blocker): blocker is string => typeof blocker === "string",
      )
    : [];
  return {
    id: operation.id,
    status: operation.status,
    errorMessage:
      blockers.length > 0
        ? `Drain blocked by ${blockers.join(", ")}`
        : operation.error_code,
    clusterId: operation.cluster_id,
    nodeName: operation.node_name,
    action: operation.action,
    progress: operation.progress,
  };
}

export type NodeOperationAction =
  | "cordon"
  | "uncordon"
  | "drain"
  | "set_label"
  | "remove_label"
  | "set_annotation"
  | "remove_annotation"
  | "add_taint"
  | "remove_taint";

export interface NodeOperationRequest {
  clusterId: string;
  nodeName: string;
  action: NodeOperationAction;
  body?:
    | DrainNodeRequest
    | NodeKeyValueRequest
    | NodeKeyRequest
    | NodeTaintRequest
    | NodeTaintRemoveRequest;
}

export async function submitNodeOperation(
  request: NodeOperationRequest,
  options: { idempotencyKey: string; signal?: AbortSignal },
): Promise<NodeOperationSnapshot> {
  const common = {
    path: { cluster_id: request.clusterId, node_name: request.nodeName },
    headerParams: { "Idempotency-Key": options.idempotencyKey },
    signal: options.signal,
  };
  let response: { data: NodeOperationWire };
  switch (request.action) {
    case "cordon":
      response = await postNodesByClusterIdByNodeNameCordon(common);
      break;
    case "uncordon":
      response = await postNodesByClusterIdByNodeNameUncordon(common);
      break;
    case "drain":
      {
        const drainResponse = await postNodesByClusterIdByNodeNameDrain({
          ...common,
          body: request.body as DrainNodeRequest,
        });
        if (!drainResponse.data || !("id" in drainResponse.data)) {
          throw new Error(
            "Node drain did not return a durable operation receipt",
          );
        }
        response = { data: drainResponse.data };
      }
      break;
    case "set_label":
      response = await postNodesByClusterIdByNodeNameLabels({
        ...common,
        body: request.body as NodeKeyValueRequest,
      });
      break;
    case "remove_label":
      response = await postNodesByClusterIdByNodeNameLabelsRemove({
        ...common,
        body: request.body as NodeKeyRequest,
      });
      break;
    case "set_annotation":
      response = await postNodesByClusterIdByNodeNameAnnotations({
        ...common,
        body: request.body as NodeKeyValueRequest,
      });
      break;
    case "remove_annotation":
      response = await postNodesByClusterIdByNodeNameAnnotationsRemove({
        ...common,
        body: request.body as NodeKeyRequest,
      });
      break;
    case "add_taint":
      response = await postNodesByClusterIdByNodeNameTaints({
        ...common,
        body: request.body as NodeTaintRequest,
      });
      break;
    case "remove_taint":
      response = await postNodesByClusterIdByNodeNameTaintsRemove({
        ...common,
        body: request.body as NodeTaintRemoveRequest,
      });
      break;
  }
  return nodeOperationFromWire(response.data);
}

export async function getNodeOperationStatus(
  id: string,
  signal?: AbortSignal,
  operation?: NodeOperationSnapshot,
): Promise<NodeOperationSnapshot> {
  if (!operation) throw new Error("Node operation target is missing");
  const response = await getNodeOperation({
    path: {
      cluster_id: operation.clusterId,
      node_name: operation.nodeName,
      id,
    },
    signal,
  });
  return nodeOperationFromWire(response.data);
}

export type NodeKeyValueRequest =
  OpenAPIComponents["schemas"]["NodeKeyValueRequest"];
export type NodeKeyRequest = OpenAPIComponents["schemas"]["NodeKeyRequest"];
export type NodeTaintRequest = OpenAPIComponents["schemas"]["NodeTaintRequest"];
export type NodeTaintRemoveRequest =
  OpenAPIComponents["schemas"]["NodeTaintRemoveRequest"];
