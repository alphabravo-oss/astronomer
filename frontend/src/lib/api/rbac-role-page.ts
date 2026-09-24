import {
  getRbacGlobalRoles,
  getRbacClusterRoles,
  getRbacProjectRoles,
} from "./generated/client";
import { mapRole, type RoleScope } from "./rbac";
import { mapPage } from "./pagination";
import {
  getRbacGlobalRolesById,
  getRbacClusterRolesById,
  getRbacProjectRolesById,
} from "./generated/client";

/** Canonical role lists have offset pagination, not server-side search. */
export async function getRolePage(
  scope: RoleScope,
  offset: number,
  signal?: AbortSignal,
) {
  const list =
    scope === "global"
      ? getRbacGlobalRoles
      : scope === "cluster"
        ? getRbacClusterRoles
        : getRbacProjectRoles;
  const response = await list({ query: { limit: 25, offset }, signal });
  return mapPage(
    { data: response.data ?? [], pagination: response.pagination },
    mapRole,
  );
}

export async function getRoleDetail(
  scope: RoleScope,
  id: string,
  signal?: AbortSignal,
) {
  const get =
    scope === "global"
      ? getRbacGlobalRolesById
      : scope === "cluster"
        ? getRbacClusterRolesById
        : getRbacProjectRolesById;
  const response = await get({ path: { id }, signal });
  if (!response.data) throw new Error("Role detail returned no data");
  return mapRole(response.data);
}
