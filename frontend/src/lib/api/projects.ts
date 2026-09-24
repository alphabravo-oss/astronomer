import {
  deleteProjectsById,
  getClustersByClusterIdProjects,
  getProjects as getProjectsOperation,
  getProjectsById,
  postProjects,
  postProjectsByIdOwnershipTakeover,
  putProjectsById,
} from "@/lib/api/generated/client";
import { mapPage } from "@/lib/api/pagination";
import type { OwnershipTransferResult } from "@/lib/api/ownership";
import type { PaginatedResponse, Project } from "@/types";
import type {
  CreateProjectRequest,
  OwnershipTransferResponse,
  Project as ProjectWire,
  UpdateProjectRequest,
} from "@/types/openapi.generated";

export interface ProjectRequestOptions {
  signal?: AbortSignal;
}

export interface ProjectListParams {
  page?: number;
  pageSize?: number;
  search?: string;
}

function requireData<T>(data: T | undefined, operation: string): T {
  if (data === undefined) throw new Error(`${operation} response omitted data`);
  return data;
}

function mapProject(wire: ProjectWire): Project {
  const storageLimit = wire.resource_quota["requests.storage"];
  return {
    id: wire.id,
    name: wire.name,
    displayName: wire.display_name,
    description: wire.description || undefined,
    clusterId: wire.cluster_id,
    clusterIds: wire.cluster_ids ?? [wire.cluster_id],
    namespaceScopes: wire.namespace_scopes?.map((scope) => ({
      clusterId: scope.cluster_id,
      namespaces: scope.namespaces,
    })),
    namespaces: wire.namespaces,
    resourceQuota: {
      cpuLimit: wire.resource_quota_cpu_limit,
      memoryLimit: wire.resource_quota_memory_limit,
      podLimit: wire.resource_quota_pod_count,
      storageLimit: typeof storageLimit === "string" ? storageLimit : "",
    },
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function projectClusterId(project: Partial<Project>): string | undefined {
  return project.clusterId ?? project.clusterIds?.[0];
}

function mapCreateProject(data: Partial<Project>): CreateProjectRequest {
  const clusterId = projectClusterId(data);
  if (!data.name || !clusterId) {
    throw new Error("Project name and cluster are required");
  }
  return {
    name: data.name,
    display_name: data.displayName,
    cluster_id: clusterId,
    description: data.description,
    namespaces: data.namespaces,
    ...(data.resourceQuota
      ? {
          resource_quota_cpu_limit: data.resourceQuota.cpuLimit,
          resource_quota_memory_limit: data.resourceQuota.memoryLimit,
          resource_quota_pod_count: data.resourceQuota.podLimit,
        }
      : {}),
  };
}

function mapUpdateProject(data: Partial<Project>): UpdateProjectRequest {
  return {
    display_name: data.displayName,
    description: data.description,
    namespaces: data.namespaces,
    ...(data.resourceQuota
      ? {
          resource_quota_cpu_limit: data.resourceQuota.cpuLimit,
          resource_quota_memory_limit: data.resourceQuota.memoryLimit,
          resource_quota_pod_count: data.resourceQuota.podLimit,
        }
      : {}),
  };
}

function mapOwnershipTransfer(
  wire: OwnershipTransferResponse,
): OwnershipTransferResult {
  return {
    id: wire.id,
    managedBy: wire.managed_by,
    transferred: wire.transferred,
  };
}

export async function getProjects(
  params?: ProjectListParams,
  options: ProjectRequestOptions = {},
): Promise<PaginatedResponse<Project>> {
  const pageSize = params?.pageSize ?? 20;
  const page = Math.max(1, params?.page ?? 1);
  const search = params?.search?.trim();
  const response = await getProjectsOperation({
    query: {
      limit: pageSize,
      offset: (page - 1) * pageSize,
      ...(search ? { search } : {}),
    },
    signal: options.signal,
  });
  return mapPage(
    { data: response.data ?? [], pagination: response.pagination },
    mapProject,
  );
}

export async function getClusterProjects(
  clusterId: string,
  params?: ProjectListParams,
  options: ProjectRequestOptions = {},
): Promise<PaginatedResponse<Project>> {
  const pageSize = params?.pageSize ?? 20;
  const page = Math.max(1, params?.page ?? 1);
  const search = params?.search?.trim();
  const response = await getClustersByClusterIdProjects({
    path: { cluster_id: clusterId },
    query: {
      limit: pageSize,
      offset: (page - 1) * pageSize,
      ...(search ? { search } : {}),
    },
    signal: options.signal,
  });
  return mapPage(
    { data: response.data ?? [], pagination: response.pagination },
    mapProject,
  );
}

export async function getProject(
  id: string,
  options: ProjectRequestOptions = {},
): Promise<Project> {
  const response = await getProjectsById({
    path: { id },
    signal: options.signal,
  });
  return mapProject(requireData(response.data, "Get project"));
}

export async function createProject(
  data: Partial<Project>,
  options: ProjectRequestOptions = {},
): Promise<Project> {
  const response = await postProjects({
    body: mapCreateProject(data),
    signal: options.signal,
  });
  return mapProject(requireData(response.data, "Create project"));
}

export async function updateProject(
  id: string,
  data: Partial<Project>,
  options: ProjectRequestOptions = {},
): Promise<Project> {
  const response = await putProjectsById({
    path: { id },
    body: mapUpdateProject(data),
    signal: options.signal,
  });
  return mapProject(requireData(response.data, "Update project"));
}

export async function takeoverProjectOwnership(
  id: string,
  options: ProjectRequestOptions = {},
): Promise<OwnershipTransferResult> {
  const response = await postProjectsByIdOwnershipTakeover({
    path: { id },
    signal: options.signal,
  });
  return mapOwnershipTransfer(
    requireData(response.data, "Take over project ownership"),
  );
}

export async function deleteProject(
  id: string,
  options: ProjectRequestOptions = {},
): Promise<void> {
  await deleteProjectsById({ path: { id }, signal: options.signal });
}
