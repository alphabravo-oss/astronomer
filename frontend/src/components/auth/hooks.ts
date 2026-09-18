/**
 * Phase B4 — Dex react-query hooks.
 *
 * These live next to the auth UI rather than the global `lib/hooks.ts` so the
 * Dex feature owns its query keys and invalidation rules without touching the
 * shared hooks module. Each hook follows the same shape as the existing
 * Astronomer hooks (toast on success/error, queryClient invalidation on
 * mutate) so the auth pages feel identical to the rest of the dashboard.
 */
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import {
  getDexConnectorTypes,
  getDexConnectors,
  getDexConnector,
  createDexConnector,
  updateDexConnector,
  deleteDexConnector,
  getDexSettings,
  updateDexSettings,
  applyDexConfig,
  registerDexAsSSO,
} from "@/lib/api/dex";
import type {
  DexConnectorWriteRequest,
  DexSettingsWriteRequest,
  DexRegisterAsSSORequest,
} from "@/types";

export function isDexRuntimeApplied(result: {
  applied?: boolean;
  verified?: boolean;
}) {
  return (
    result.applied === true &&
    (result.verified === undefined || result.verified === true)
  );
}

export const dexQueryKeys = {
  connectorTypes: ["auth", "dex", "connector-types"] as const,
  connectors: ["auth", "dex", "connectors"] as const,
  connector: (id: string) => ["auth", "dex", "connectors", id] as const,
  settings: ["auth", "dex", "settings"] as const,
};

export function useDexConnectorTypes() {
  return useQuery({
    queryKey: dexQueryKeys.connectorTypes,
    queryFn: () => getDexConnectorTypes(),
    // The registry is process-static on the backend; cache aggressively.
    staleTime: 5 * 60 * 1000,
  });
}

export function useDexConnectors() {
  return useQuery({
    queryKey: dexQueryKeys.connectors,
    queryFn: () => getDexConnectors(),
  });
}

export function useDexConnector(id: string | undefined) {
  return useQuery({
    queryKey: dexQueryKeys.connector(id || ""),
    queryFn: () => getDexConnector(id as string),
    enabled: !!id,
  });
}

export function useCreateDexConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: DexConnectorWriteRequest) => createDexConnector(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.connectors });
      toastSuccess("Connector created");
    },
    onError: (err: Error) => {
      toastApiError("Failed to create connector", err);
    },
  });
}

export function useUpdateDexConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: string;
      data: Partial<DexConnectorWriteRequest>;
    }) => updateDexConnector(id, data),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.connectors });
      qc.invalidateQueries({ queryKey: dexQueryKeys.connector(vars.id) });
      toastSuccess("Connector updated");
    },
    onError: (err: Error) => {
      toastApiError("Failed to update connector", err);
    },
  });
}

export function useDeleteDexConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteDexConnector(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.connectors });
      toastSuccess("Connector deleted");
    },
    onError: (err: Error) => {
      toastApiError("Failed to delete connector", err);
    },
  });
}

export function useDexSettings() {
  return useQuery({
    queryKey: dexQueryKeys.settings,
    queryFn: () => getDexSettings(),
  });
}

export function useUpdateDexSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: DexSettingsWriteRequest) => updateDexSettings(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.settings });
      toastSuccess("Dex settings saved");
    },
    onError: (err: Error) => {
      toastApiError("Failed to save settings", err);
    },
  });
}

export function useApplyDexConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => applyDexConfig(),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.settings });
      if (isDexRuntimeApplied(data))
        toastSuccess(`Applied ${data.connectorCount} connector(s) to Dex`);
    },
    onError: (err: Error) => {
      toastApiError("Apply failed", err);
    },
  });
}

export function useRegisterDexAsSSO() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: DexRegisterAsSSORequest) => registerDexAsSSO(data),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: dexQueryKeys.settings });
      if (isDexRuntimeApplied(data))
        toastSuccess("Dex registered as SSO provider");
    },
    onError: (err: Error) => {
      toastApiError("Failed to register SSO", err);
    },
  });
}
