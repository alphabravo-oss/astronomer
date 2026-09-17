import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  applyProjectRoleTemplate,
  createClusterRole,
  createClusterRoleBinding,
  createGlobalRole,
  createGlobalRoleBinding,
  createProjectRole,
  createProjectRoleBinding,
  deleteRole,
  deleteClusterRoleBinding,
  deleteGlobalRoleBinding,
  deleteProjectRoleBinding,
  getClusterRoles,
  getEffectivePermissionsForUser,
  getGlobalRoles,
  getMyEffectivePermissions,
  getProjectRoles,
  listClusterRoleBindings,
  listGlobalRoleBindings,
  listProjectRoleBindings,
  listRoleTemplates,
  materializePrincipal,
  searchPrincipals,
  updateRole,
  type EffectivePermissionParams,
  type RoleInput,
  type RoleScope,
} from "@/lib/api/rbac";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import type { PolicyRule } from "@/types";

export function useGlobalRoles() {
  return useQuery({
    queryKey: queryKeys.rbac.globalRoles,
    queryFn: ({ signal }) => getGlobalRoles(signal),
  });
}

export function useClusterRoles() {
  return useQuery({
    queryKey: queryKeys.rbac.clusterRoles(),
    queryFn: ({ signal }) => getClusterRoles(signal),
  });
}

export function useProjectRoles() {
  return useQuery({
    queryKey: queryKeys.rbac.projectRoles(),
    queryFn: ({ signal }) => getProjectRoles(signal),
  });
}

export function useMyEffectivePermissions(params?: EffectivePermissionParams) {
  return useQuery({
    queryKey: queryKeys.rbac.myPermissions(params),
    queryFn: ({ signal }) => getMyEffectivePermissions(params, signal),
  });
}

export function useEffectivePermissions(
  userId: string | undefined,
  params?: EffectivePermissionParams,
) {
  const self = !userId;
  return useQuery({
    queryKey: queryKeys.rbac.effectivePermissions(userId || "me", params, self),
    queryFn: ({ signal }) =>
      self
        ? getMyEffectivePermissions(params, signal)
        : getEffectivePermissionsForUser(userId, params, signal),
  });
}

export function useCreateRole() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      scope: "global" | "cluster" | "project";
      name: string;
      displayName: string;
      description?: string;
      rules: Array<PolicyRule | Record<string, unknown>>;
    }) => {
      const { scope, ...roleData } = data;
      switch (scope) {
        case "global":
          return createGlobalRole(roleData);
        case "cluster":
          return createClusterRole(roleData);
        case "project":
          return createProjectRole(roleData);
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess("Role created successfully");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create role", error);
    },
  });
}

export function useUpdateRole() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { scope: RoleScope; id: string; role: RoleInput }) =>
      updateRole(data.scope, data.id, data.role),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess("Role updated successfully");
    },
    onError: (error: Error) => toastApiError("Failed to update role", error),
  });
}

export function useDeleteRole() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: { scope: RoleScope; id: string }) =>
      deleteRole(data.scope, data.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess("Role deleted");
    },
    onError: (error: Error) => toastApiError("Failed to delete role", error),
  });
}

export function useRoleTemplates() {
  return useQuery({
    queryKey: queryKeys.rbac.templates,
    queryFn: ({ signal }) => listRoleTemplates(signal),
  });
}

export function usePrincipalSearch(query: string) {
  const normalized = query.trim();
  return useQuery({
    queryKey: queryKeys.rbac.principals(normalized),
    queryFn: ({ signal }) => searchPrincipals(normalized, signal),
    enabled: normalized.length >= 3 && normalized.length <= 128,
    staleTime: 30_000,
  });
}

export function useMaterializePrincipal() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: materializePrincipal,
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.rbac.principalsAll,
      });
    },
    onError: (error: Error) =>
      toastApiError("Failed to verify external identity", error),
  });
}

export function useApplyProjectRoleTemplate() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: applyProjectRoleTemplate,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess("Template applied");
    },
    onError: (error: Error) => toastApiError("Failed to apply template", error),
  });
}

export function useClusterRoleBindings(params?: { cluster_id?: string }) {
  return useQuery({
    queryKey: queryKeys.rbac.clusterRoleBindings(params),
    queryFn: ({ signal }) => listClusterRoleBindings(params, signal),
  });
}

export function useGlobalRoleBindings() {
  return useQuery({
    queryKey: queryKeys.rbac.globalRoleBindings,
    queryFn: ({ signal }) => listGlobalRoleBindings(signal),
  });
}

export function useProjectRoleBindings(params?: { project_id?: string }) {
  return useQuery({
    queryKey: queryKeys.rbac.projectRoleBindings(params),
    queryFn: ({ signal }) => listProjectRoleBindings(params, signal),
  });
}

export function useCreateAccessBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (data: {
      scope: "global" | "cluster" | "project";
      user_id: string;
      role_id: string;
      cluster_id?: string;
      project_id?: string;
      namespace?: string;
    }) => {
      switch (data.scope) {
        case "global":
          return createGlobalRoleBinding({
            user_id: data.user_id,
            role_id: data.role_id,
          });
        case "project":
          return createProjectRoleBinding({
            user_id: data.user_id,
            role_id: data.role_id,
            project_id: data.project_id || "",
          });
        default:
          return createClusterRoleBinding({
            user_id: data.user_id,
            role_id: data.role_id,
            cluster_id: data.cluster_id || "",
            namespace: data.namespace,
          });
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess("Binding created");
    },
    onError: (error: Error) => {
      toastApiError("Failed to create binding", error);
    },
  });
}

export function useDeleteAccessBinding() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (binding: {
      scope: "global" | "cluster" | "project";
      id: string;
    }) => {
      switch (binding.scope) {
        case "global":
          return deleteGlobalRoleBinding(binding.id);
        case "project":
          return deleteProjectRoleBinding(binding.id);
        default:
          return deleteClusterRoleBinding(binding.id);
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.rbac.all });
      toastSuccess("Binding revoked");
    },
    onError: (error: Error) => {
      toastApiError("Failed to revoke binding", error);
    },
  });
}
