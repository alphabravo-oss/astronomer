import {
  getClusterAgents as getClusterAgentsOperation,
  getClusterAgentsByClusterIdDiagnostics,
  getClusterAgentsByClusterIdDiagnosticsBundle,
  getClusterAgentsByClusterIdOperations,
  postClusterAgentsByClusterIdSelfTest,
  postClusterAgentsByClusterIdUpgrade,
  postClusterAgentsByClusterIdUpgradePlan,
  postClustersByIdRegister,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import { camelizeKeys } from "@/lib/camelize";
import type {
  AgentDiagnosticsResponse,
  AgentLifecycleOperationsResponse,
  AgentSelfTestResponse,
  AgentUpgradeOperationResponse,
  AgentUpgradePlanRequest,
  AgentUpgradePlanResponse,
  ClusterAgentResponse,
} from "@/types";
import type {
  AgentDiagnostics,
  AgentLifecycleOperationsResponse as AgentLifecycleOperationsWire,
  AgentSelfTest,
  AgentUpgradeOperationResponse as AgentUpgradeOperationWire,
  AgentUpgradePlan,
  AgentUpgradePlanRequest as AgentUpgradePlanWireRequest,
  ClusterAgentResponse as ClusterAgentWireResponse,
} from "@/types/openapi.generated";

export interface AgentRequestOptions {
  signal?: AbortSignal;
}

export interface ClusterRegistrationTokenReceiptView {
  id: string;
  clusterId: string;
  token: string;
  expiresAt: string;
}

function requireData<T>(data: T | undefined, operation: string): T {
  if (data === undefined) throw new Error(`${operation} response omitted data`);
  return data;
}

function mapWire<TWire, TView>(wire: TWire): TView {
  return camelizeKeys(wire) as unknown as TView;
}

function mapUpgradeRequest(
  body: AgentUpgradePlanRequest,
): AgentUpgradePlanWireRequest {
  return {
    target_version: body.targetVersion,
    target_image: body.targetImage,
    strategy: body.strategy,
    canary_cluster_ids: body.canaryClusterIds,
    batch_size: body.batchSize,
    max_unavailable: body.maxUnavailable,
    rollback_image: body.rollbackImage,
  };
}

export async function getClusterAgents(
  params?: { limit?: number; offset?: number },
  options: AgentRequestOptions = {},
): Promise<ClusterAgentResponse> {
  const response = await getClusterAgentsOperation({
    query: params,
    signal: options.signal,
  });
  return mapWire<ClusterAgentWireResponse, ClusterAgentResponse>(
    requireData(response.data, "List cluster agents"),
  );
}

export async function getAgentDiagnostics(
  clusterId: string,
  options: AgentRequestOptions = {},
): Promise<AgentDiagnosticsResponse> {
  const response = await getClusterAgentsByClusterIdDiagnostics({
    path: { cluster_id: clusterId },
    signal: options.signal,
  });
  return mapWire<AgentDiagnostics, AgentDiagnosticsResponse>(
    requireData(response.data, "Get agent diagnostics"),
  );
}

export async function runAgentSelfTest(
  clusterId: string,
  options: AgentRequestOptions = {},
): Promise<AgentSelfTestResponse> {
  const response = await postClusterAgentsByClusterIdSelfTest({
    path: { cluster_id: clusterId },
    signal: options.signal,
  });
  return mapWire<AgentSelfTest, AgentSelfTestResponse>(
    requireData(response.data, "Run agent self-test"),
  );
}

export async function downloadAgentDiagnosticsBundle(
  clusterId: string,
  options: AgentRequestOptions = {},
): Promise<Blob> {
  const bundle = await getClusterAgentsByClusterIdDiagnosticsBundle({
    path: { cluster_id: clusterId },
    signal: options.signal,
  });
  return new Blob([JSON.stringify(bundle, null, 2)], {
    type: "application/json",
  });
}

export async function createAgentUpgradePlan(
  clusterId: string,
  data: AgentUpgradePlanRequest = {},
  options: AgentRequestOptions = {},
): Promise<AgentUpgradePlanResponse> {
  const response = await postClusterAgentsByClusterIdUpgradePlan({
    path: { cluster_id: clusterId },
    body: mapUpgradeRequest(data),
    signal: options.signal,
  });
  return mapWire<AgentUpgradePlan, AgentUpgradePlanResponse>(
    requireData(response.data, "Create agent upgrade plan"),
  );
}

export async function createAgentUpgradeOperation(
  clusterId: string,
  data: AgentUpgradePlanRequest = {},
  options: AgentRequestOptions = {},
): Promise<AgentUpgradeOperationResponse> {
  const response = await postClusterAgentsByClusterIdUpgrade({
    path: { cluster_id: clusterId },
    headerParams: idempotencyHeaderParams(),
    body: mapUpgradeRequest(data),
    signal: options.signal,
  });
  return mapWire<AgentUpgradeOperationWire, AgentUpgradeOperationResponse>(
    requireData(response.data, "Create agent upgrade operation"),
  );
}

export async function getAgentOperations(
  clusterId: string,
  params?: { limit?: number; offset?: number },
  options: AgentRequestOptions = {},
): Promise<AgentLifecycleOperationsResponse> {
  const response = await getClusterAgentsByClusterIdOperations({
    path: { cluster_id: clusterId },
    query: params,
    signal: options.signal,
  });
  return mapWire<
    AgentLifecycleOperationsWire,
    AgentLifecycleOperationsResponse
  >(requireData(response.data, "List agent operations"));
}

export async function registerCluster(
  clusterId: string,
  options: AgentRequestOptions = {},
): Promise<ClusterRegistrationTokenReceiptView> {
  const response = await postClustersByIdRegister({
    path: { id: clusterId },
    signal: options.signal,
  });
  const receipt = requireData(response.data, "Register cluster");
  if (
    !receipt.id ||
    !receipt.cluster_id ||
    !receipt.token ||
    !receipt.expires_at
  ) {
    throw new Error("Register cluster response is incomplete");
  }
  return {
    id: receipt.id,
    clusterId: receipt.cluster_id,
    token: receipt.token,
    expiresAt: receipt.expires_at,
  };
}
