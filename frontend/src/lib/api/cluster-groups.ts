/**
 * Cluster groups API client (migration 066).
 *
 * Operator-defined folder hierarchy over clusters. Two levels of nesting
 * (root + 2) are supported; the depth cap is enforced server-side and
 * surfaces as a 400 with code "max_depth".
 *
 * All endpoints sit under /api/v1/cluster-groups/ and are gated by
 * clusters:update (group admin is a clusters-admin concept).
 */
import {
  deleteClusterGroupsById,
  getClusterGroups,
  getClusterGroupsById,
  getClusterGroupsByIdClusters,
  postClusterGroups,
  postClusterGroupsByIdMove,
  putClusterGroupsById,
} from "@/lib/api/generated/client";
import type {
  ClusterGroupResponse,
  CreateClusterGroupRequest,
} from "@/types/openapi.generated";

export interface ClusterGroup {
  id: string;
  name: string;
  slug: string;
  description: string;
  parentId?: string;
  color: string;
  icon: string;
  enabled: boolean;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
  clusterCount: number;
  clusterCountTree: number;
}

export interface ClusterGroupTreeNode extends ClusterGroup {
  depth: number;
}

export type ClusterGroupWriteRequest = CreateClusterGroupRequest;

export interface MoveClustersResult {
  moved: number;
  skipped: string[];
}

export interface ClusterGroupRequestOptions {
  signal?: AbortSignal;
}

function mapClusterGroup(wire: ClusterGroupResponse): ClusterGroup {
  return {
    id: wire.id,
    name: wire.name,
    slug: wire.slug,
    description: wire.description,
    parentId: wire.parent_id,
    color: wire.color,
    icon: wire.icon,
    enabled: wire.enabled,
    createdBy: wire.created_by,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
    clusterCount: wire.cluster_count,
    clusterCountTree: wire.cluster_count_tree,
  };
}

function requireClusterGroup(
  wire: ClusterGroupResponse | undefined,
): ClusterGroup {
  if (!wire) throw new Error("Cluster group response omitted data");
  return mapClusterGroup(wire);
}

export async function listClusterGroups(
  options: ClusterGroupRequestOptions = {},
): Promise<ClusterGroupTreeNode[]> {
  const response = await getClusterGroups({ signal: options.signal });
  return (response.data ?? []).map((wire) => ({
    ...mapClusterGroup(wire),
    depth: wire.depth ?? 0,
  }));
}

export async function getClusterGroup(
  id: string,
  options: ClusterGroupRequestOptions = {},
): Promise<ClusterGroup> {
  const response = await getClusterGroupsById({
    path: { id },
    signal: options.signal,
  });
  return requireClusterGroup(response.data);
}

export async function createClusterGroup(
  body: ClusterGroupWriteRequest,
  options: ClusterGroupRequestOptions = {},
): Promise<ClusterGroup> {
  const response = await postClusterGroups({
    body,
    signal: options.signal,
  });
  return requireClusterGroup(response.data);
}

export async function updateClusterGroup(
  id: string,
  body: ClusterGroupWriteRequest,
  options: ClusterGroupRequestOptions = {},
): Promise<ClusterGroup> {
  const response = await putClusterGroupsById({
    path: { id },
    body,
    signal: options.signal,
  });
  return requireClusterGroup(response.data);
}

export async function deleteClusterGroup(
  id: string,
  options: ClusterGroupRequestOptions = {},
): Promise<void> {
  await deleteClusterGroupsById({ path: { id }, signal: options.signal });
}

export async function listClustersInGroup(
  id: string,
  options: ClusterGroupRequestOptions = {},
): Promise<{ id: string; name: string }[]> {
  const response = await getClusterGroupsByIdClusters({
    path: { id },
    signal: options.signal,
  });
  return (response.data ?? []).map((cluster) => {
    if (!cluster.id || !cluster.name) {
      throw new Error("Cluster group member response is incomplete");
    }
    return { id: cluster.id, name: cluster.name };
  });
}

export async function moveClustersToGroup(
  id: string,
  clusterIds: string[],
  options: ClusterGroupRequestOptions = {},
): Promise<MoveClustersResult> {
  const response = await postClusterGroupsByIdMove({
    path: { id },
    body: { cluster_ids: clusterIds },
    signal: options.signal,
  });
  const result = response.data;
  if (result?.moved === undefined || !result.skipped) {
    throw new Error("Cluster group move response is incomplete");
  }
  return { moved: result.moved, skipped: result.skipped };
}

/**
 * Curated set of lucide-react icons the operator can pick from on the
 * group form. Kept short on purpose — a wall of icons is harder to
 * scan than a focused palette.
 */
export const CLUSTER_GROUP_ICONS = [
  "folder",
  "folder-tree",
  "layers",
  "globe",
  "server",
  "cloud",
  "database",
  "box",
  "shield",
  "flag",
  "star",
  "tag",
] as const;

export const CLUSTER_GROUP_COLORS = [
  "#6b7280",
  "#ef4444",
  "#f97316",
  "#eab308",
  "#22c55e",
  "#06b6d4",
  "#3b82f6",
  "#8b5cf6",
  "#ec4899",
] as const;
