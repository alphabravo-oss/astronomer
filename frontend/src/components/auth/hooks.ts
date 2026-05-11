/**
 * Phase B4 — Dex react-query hooks.
 *
 * These live next to the auth UI rather than the global `lib/hooks.ts` so the
 * Dex feature owns its query keys and invalidation rules without touching the
 * shared hooks module. Each hook follows the same shape as the existing
 * Astronomer hooks (toast on success/error, queryClient invalidation on
 * mutate) so the auth pages feel identical to the rest of the dashboard.
 */
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import * as apiClient from '@/lib/api';
import type {
  DexConnectorWriteRequest,
  DexSettingsWriteRequest,
  DexRegisterAsSSORequest,
} from '@/types';

export const dexQueryKeys = {
  connectorTypes: ['auth', 'dex', 'connector-types'] as const,
  connectors: ['auth', 'dex', 'connectors'] as const,
  connector: (id: string) => ['auth', 'dex', 'connectors', id] as const,
  settings: ['auth', 'dex', 'settings'] as const,
};

export function useDexConnectorTypes() {
  return useQuery({
    queryKey: dexQueryKeys.connectorTypes,
    queryFn: () => apiClient.getDexConnectorTypes(),
    // The registry is process-static on the backend; cache aggressively.
    staleTime: 5 * 60 * 1000,
  });
}

export function useDexConnectors() {
  return useQuery({
    queryKey: dexQueryKeys.connectors,
    queryFn: () => apiClient.getDexConnectors(),
  });
}

export function useDexConnector(id: string | undefined) {
  return useQuery({
    queryKey: dexQueryKeys.connector(id || ''),
    queryFn: () => apiClient.getDexConnector(id as string),
    enabled: !!id,
  });
}

export function useCreateDexConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: DexConnectorWriteRequest) => apiClient.createDexConnector(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.connectors });
      toast.success('Connector created');
    },
    onError: (err: Error) => {
      toast.error(`Failed to create connector: ${err.message}`);
    },
  });
}

export function useUpdateDexConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<DexConnectorWriteRequest> }) =>
      apiClient.updateDexConnector(id, data),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.connectors });
      qc.invalidateQueries({ queryKey: dexQueryKeys.connector(vars.id) });
      toast.success('Connector updated');
    },
    onError: (err: Error) => {
      toast.error(`Failed to update connector: ${err.message}`);
    },
  });
}

export function useDeleteDexConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.deleteDexConnector(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.connectors });
      toast.success('Connector deleted');
    },
    onError: (err: Error) => {
      toast.error(`Failed to delete connector: ${err.message}`);
    },
  });
}

export function useDexSettings() {
  return useQuery({
    queryKey: dexQueryKeys.settings,
    queryFn: () => apiClient.getDexSettings(),
  });
}

export function useUpdateDexSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: DexSettingsWriteRequest) => apiClient.updateDexSettings(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.settings });
      toast.success('Dex settings saved');
    },
    onError: (err: Error) => {
      toast.error(`Failed to save settings: ${err.message}`);
    },
  });
}

export function useApplyDexConfig() {
  return useMutation({
    mutationFn: () => apiClient.applyDexConfig(),
    onSuccess: (data) => {
      toast.success(`Applied ${data.connectorCount} connector(s) to Dex`);
    },
    onError: (err: Error) => {
      toast.error(`Apply failed: ${err.message}`);
    },
  });
}

export function useRegisterDexAsSSO() {
  return useMutation({
    mutationFn: (data: DexRegisterAsSSORequest) => apiClient.registerDexAsSSO(data),
    onSuccess: () => {
      toast.success('Dex registered as SSO provider');
    },
    onError: (err: Error) => {
      toast.error(`Failed to register SSO: ${err.message}`);
    },
  });
}
