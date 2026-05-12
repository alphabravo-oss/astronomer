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
import api from '@/lib/api';
import type { APIResponse } from '@/types';

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

export interface ClusterGroupWriteRequest {
  name: string;
  slug?: string;
  description?: string;
  parent_id?: string;
  color?: string;
  icon?: string;
}

export interface MoveClustersResult {
  moved: number;
  skipped: string[];
}

export async function listClusterGroups(): Promise<ClusterGroupTreeNode[]> {
  const res = await api.get<APIResponse<ClusterGroupTreeNode[]>>('/cluster-groups/');
  return res.data.data ?? [];
}

export async function getClusterGroup(id: string): Promise<ClusterGroup> {
  const res = await api.get<APIResponse<ClusterGroup>>(`/cluster-groups/${id}/`);
  return res.data.data;
}

export async function createClusterGroup(body: ClusterGroupWriteRequest): Promise<ClusterGroup> {
  const res = await api.post<APIResponse<ClusterGroup>>('/cluster-groups/', body);
  return res.data.data;
}

export async function updateClusterGroup(
  id: string,
  body: ClusterGroupWriteRequest,
): Promise<ClusterGroup> {
  const res = await api.put<APIResponse<ClusterGroup>>(`/cluster-groups/${id}/`, body);
  return res.data.data;
}

export async function deleteClusterGroup(id: string): Promise<void> {
  await api.delete(`/cluster-groups/${id}/`);
}

export async function listClustersInGroup(
  id: string,
): Promise<{ id: string; name: string }[]> {
  const res = await api.get<APIResponse<{ id: string; name: string }[]>>(
    `/cluster-groups/${id}/clusters/`,
  );
  return res.data.data ?? [];
}

export async function moveClustersToGroup(
  id: string,
  clusterIds: string[],
): Promise<MoveClustersResult> {
  const res = await api.post<APIResponse<MoveClustersResult>>(`/cluster-groups/${id}/move/`, {
    cluster_ids: clusterIds,
  });
  return res.data.data;
}

/**
 * Curated set of lucide-react icons the operator can pick from on the
 * group form. Kept short on purpose — a wall of icons is harder to
 * scan than a focused palette.
 */
export const CLUSTER_GROUP_ICONS = [
  'folder',
  'folder-tree',
  'layers',
  'globe',
  'server',
  'cloud',
  'database',
  'box',
  'shield',
  'flag',
  'star',
  'tag',
] as const;

export const CLUSTER_GROUP_COLORS = [
  '#6b7280',
  '#ef4444',
  '#f97316',
  '#eab308',
  '#22c55e',
  '#06b6d4',
  '#3b82f6',
  '#8b5cf6',
  '#ec4899',
] as const;
