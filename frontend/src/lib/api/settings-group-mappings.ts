import {
  deleteAdminGroupMappingsById,
  getAdminGroupMappings,
  postAdminGroupMappings,
  postAdminUsersByIdResyncGroups,
} from "@/lib/api/generated/client";
import type {
  GroupMapping as GroupMappingWire,
  GroupMappingWriteRequest,
} from "@/types/openapi.generated";

// ============================================================
// Types — Group Mappings
// ============================================================

export type GroupScope = GroupMappingWriteRequest["scope"];
export type { GroupMappingWriteRequest };

/** Camel-cased presentation model derived from the generated wire contract. */
export interface GroupMappingView {
  id: string;
  /** Empty / "any" matches any connector. */
  connector: string;
  groupName: string;
  scope: GroupScope;
  role: string;
  /** Cluster UUID when scope=cluster; project name when scope=project. */
  target?: string;
  targetDisplay?: string;
  createdAt: string;
}

function mapGroupMapping(wire: GroupMappingWire): GroupMappingView {
  return {
    id: wire.id,
    connector: wire.connector_id,
    groupName: wire.group_name,
    scope: wire.scope,
    role: wire.role_id,
    target:
      wire.scope === "cluster"
        ? wire.cluster_id
        : wire.scope === "project"
          ? wire.project_id
          : undefined,
    createdAt: wire.created_at,
  };
}
// ============================================================
// Group Mappings — API funcs
// ============================================================

export async function listGroupMappings(): Promise<GroupMappingView[]> {
  const response = await getAdminGroupMappings();
  return response.data.map(mapGroupMapping);
}

export async function createGroupMapping(
  body: GroupMappingWriteRequest,
): Promise<GroupMappingView> {
  const response = await postAdminGroupMappings({ body });
  return mapGroupMapping(response.data);
}

export async function deleteGroupMapping(id: string): Promise<void> {
  await deleteAdminGroupMappingsById({ path: { id } });
}

export async function resyncUserGroups(
  userId: string,
): Promise<{ synced: number }> {
  const response = await postAdminUsersByIdResyncGroups({
    path: { id: userId },
  });
  return { synced: response.added_count + response.removed_count };
}
