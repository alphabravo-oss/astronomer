import {
  deleteClustersById as deleteClusterOperation,
  getClusters as listClustersOperation,
  getClustersById as getClusterOperation,
  patchClustersById as updateClusterOperation,
  postClusters as createClusterOperation,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { Cluster, ClusterRegistration, PaginatedResponse } from "@/types";

type ClusterWire = OpenAPIComponents["schemas"]["Cluster"];
export type UpdateClusterInput =
  OpenAPIComponents["schemas"]["UpdateClusterRequest"];

function requireData<T>(value: { data?: T } | undefined, operation: string): T {
  if (!value?.data) throw new Error(`${operation} returned no data payload`);
  return value.data;
}

/** Deliberate wire-to-view mapping; no global casing transform is involved. */
export function mapCluster(wire: ClusterWire): Cluster {
  return {
    id: wire.id,
    name: wire.name,
    displayName: wire.display_name,
    description: wire.description,
    status: wire.status,
    apiServerUrl: wire.api_server_url,
    caCertificate: wire.ca_certificate,
    environment: wire.environment,
    region: wire.region,
    provider: wire.provider,
    labels: wire.labels,
    annotations: wire.annotations,
    distribution: wire.distribution as Cluster["distribution"],
    agentVersion: wire.agent_version,
    lastHeartbeat: wire.last_heartbeat,
    kubernetesVersion: wire.kubernetes_version,
    nodeCount: wire.node_count,
    createdById: wire.created_by_id,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
    isLocal: wire.is_local,
    decommissionedAt: wire.decommissioned_at,
    clusterUid: wire.cluster_uid,
    groupId: wire.group_id,
    registrationPhase: wire.registration_phase,
    registrationStartedAt: wire.registration_started_at,
    registrationCompletedAt: wire.registration_completed_at,
    installBaseline: wire.install_baseline,
    managedBy: wire.managed_by,
    externalRefApiVersion: wire.external_ref_api_version,
    externalRefKind: wire.external_ref_kind,
    externalRefNamespace: wire.external_ref_namespace,
    externalRefName: wire.external_ref_name,
    observedGeneration: wire.observed_generation,
    cpuPercentage: wire.cpu_percentage,
    memoryPercentage: wire.memory_percentage,
    podCount: wire.pod_count,
    metricsServerPresent: wire.metrics_server_present,
    decommissioning: wire.decommissioning,
    agentPrivilegeProfile: wire.agent_privilege_profile,
    downstreamImpersonation: wire.downstream_impersonation,
  };
}

function createBody(
  input: ClusterRegistration,
): OpenAPIComponents["schemas"]["CreateClusterRequest"] {
  return {
    name: input.name,
    display_name: input.displayName,
    description: input.description,
    environment: input.environment,
    region: input.region,
    provider: input.provider,
    distribution: input.distribution,
    labels: input.labels,
    annotations: input.annotations,
    api_server_url: input.apiServerUrl,
    ca_certificate: input.caCertificate,
  };
}

export interface ClusterListParameters {
  status?: string;
  provider?: string;
  environment?: string;
  search?: string;
  page?: number;
  pageSize?: number;
}

export async function getClusters(
  params?: ClusterListParameters,
): Promise<PaginatedResponse<Cluster>> {
  const pageSize = Math.max(1, Math.min(200, params?.pageSize ?? 20));
  const page = Math.max(1, params?.page ?? 1);
  const response = await listClustersOperation({
    query: {
      status: params?.status,
      provider: params?.provider,
      environment: params?.environment,
      search: params?.search,
      limit: pageSize,
      offset: (page - 1) * pageSize,
    },
  });
  const count = response.count;
  return {
    data: response.data.map(mapCluster),
    total: count,
    count,
    next: response.next,
    previous: response.previous,
    page,
    pageSize,
    totalPages: Math.max(1, Math.ceil(count / pageSize)),
  };
}

export async function getCluster(id: string): Promise<Cluster> {
  return mapCluster(
    requireData(await getClusterOperation({ path: { id } }), "getCluster"),
  );
}

export async function createCluster(
  input: ClusterRegistration,
): Promise<Cluster> {
  return mapCluster(
    requireData(
      await createClusterOperation({ body: createBody(input) }),
      "createCluster",
    ),
  );
}

export async function updateCluster(
  id: string,
  input: UpdateClusterInput,
): Promise<Cluster> {
  return mapCluster(
    requireData(
      await updateClusterOperation({ path: { id }, body: input }),
      "updateCluster",
    ),
  );
}

export async function deleteCluster(
  id: string,
  options?: { force?: boolean },
): Promise<void> {
  await deleteClusterOperation({
    path: { id },
    query: { force: options?.force },
  });
}
