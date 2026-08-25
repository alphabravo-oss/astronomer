import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createAPIToken,
  createSSOProvider,
  createUser,
  deleteAPIToken,
  deleteUser,
  getAPITokens,
  getGeneralSettings,
  getSSOProviders,
  getUsers,
  resetUserPassword,
  saveGeneralSettings,
  updateUser,
  type CreateAPITokenInput,
  type CreateSSOProviderInput,
  type CreateUserInput,
  type GeneralSettings,
  type UpdateUserInput,
  type UserListParameters,
} from "@/lib/api/user-settings";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";

export function useUsers(params?: UserListParameters) {
  return useQuery({
    queryKey: queryKeys.users.list(params ? { ...params } : undefined),
    queryFn: () => getUsers(params),
  });
}

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateUserInput) => createUser(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users.all });
      toastSuccess("User created");
    },
    onError: (error: Error) => toastApiError("Failed to create user", error),
  });
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateUserInput }) =>
      updateUser(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users.all });
      toastSuccess("User updated");
    },
    onError: (error: Error) => toastApiError("Failed to update user", error),
  });
}

export function useDeleteUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteUser(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.users.all });
      toastSuccess("User deleted");
    },
    onError: (error: Error) => toastApiError("Failed to delete user", error),
  });
}

export function useResetUserPassword() {
  return useMutation({
    mutationFn: (id: string) => resetUserPassword(id),
    onSuccess: () => toastSuccess("Temporary password generated"),
    onError: (error: Error) => toastApiError("Failed to reset password", error),
  });
}

export function useGeneralSettings() {
  return useQuery({
    queryKey: queryKeys.settings.general,
    queryFn: getGeneralSettings,
  });
}

export function useSaveGeneralSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: GeneralSettings) => saveGeneralSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.general });
      toastSuccess("Settings saved successfully");
    },
    onError: (error: Error) => toastApiError("Failed to save settings", error),
  });
}

export function useSSOProviders() {
  return useQuery({
    queryKey: queryKeys.settings.sso,
    queryFn: getSSOProviders,
  });
}

export function useCreateSSOProvider() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateSSOProviderInput) => createSSOProvider(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.sso });
      toastSuccess("SSO provider created successfully");
    },
    onError: (error: Error) =>
      toastApiError("Failed to create SSO provider", error),
  });
}

export function useAPITokens() {
  return useQuery({
    queryKey: queryKeys.settings.tokens,
    queryFn: getAPITokens,
  });
}

export function useCreateAPIToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateAPITokenInput) => createAPIToken(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.tokens });
      toastSuccess("API token created");
    },
    onError: (error: Error) => toastApiError("Failed to create token", error),
  });
}

export function useDeleteAPIToken() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteAPIToken(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.settings.tokens });
      toastSuccess("API token deleted");
    },
    onError: (error: Error) => toastApiError("Failed to delete token", error),
  });
}
