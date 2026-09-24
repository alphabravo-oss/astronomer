import {
  getRbacGlobalRoleBindings,
  getRbacClusterRoleBindings,
  getRbacProjectRoleBindings,
} from "./generated/client";
import { mapAccessBinding, type RoleScope } from "./rbac";
import { mapPage } from "./pagination";

export async function getBindingPage(
  scope: RoleScope,
  offset: number,
  signal?: AbortSignal,
  projectId?: string,
) {
  const list =
    scope === "global"
      ? getRbacGlobalRoleBindings
      : scope === "cluster"
        ? getRbacClusterRoleBindings
        : getRbacProjectRoleBindings;
  const response = await list({
    query: {
      limit: 25,
      offset,
      ...(scope === "project" && projectId ? { project_id: projectId } : {}),
    },
    signal,
  });
  return mapPage(
    { data: response.data ?? [], pagination: response.pagination },
    (wire) => mapAccessBinding(scope, wire),
  );
}
