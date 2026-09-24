import { useQueries } from "@tanstack/react-query";
import { getRoleDetail } from "@/lib/api/rbac-role-page";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { queryKeys } from "@/lib/query-keys";
import { useEntityNames } from "@/lib/hooks/entity-names";
import type { AccessBinding } from "@/types";
import type { RoleScope } from "@/lib/api/rbac";

export function useBindingNames(bindings: AccessBinding[], scope: RoleScope) {
  const names = useEntityNames({
    userIds: bindings.map((row) => row.userId),
    clusterIds: bindings.map((row) => row.clusterId),
    projectIds: bindings.map((row) => row.projectId),
  });
  const rolesRead = usePermissionDecision("rbac", "read").allowed;
  const roles = useQueries({
    queries: [
      ...new Set(bindings.map((row) => row.roleId).filter(Boolean)),
    ].map((id) => ({
      queryKey: queryKeys.rbac.roleDetail(scope, id),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        getRoleDetail(scope, id, signal),
      enabled: rolesRead,
      throwOnError: false,
      retry: false,
    })),
  });
  return {
    ...names,
    roles: rolesRead
      ? roles.flatMap((query) =>
          !query.isError && query.data ? [query.data] : [],
        )
      : [],
  };
}
