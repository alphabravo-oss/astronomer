import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastError, toastSuccess } from "@/lib/toast";
import { queryKeys } from "@/lib/query-keys";
import {
  deleteClusterRegistry,
  testClusterRegistry,
} from "@/lib/api/cluster-registries";
import type { RegistryTestState } from "./-registries-table";

export function useRegistryActions(clusterId: string, onDeleted: () => void) {
  const queryClient = useQueryClient();
  const [testStatus, setTestStatus] = useState<
    Record<string, RegistryTestState>
  >({});

  const deleteMutation = useMutation({
    mutationFn: (registryId: string) =>
      deleteClusterRegistry(clusterId, registryId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.registries(clusterId),
      });
      toastSuccess("Registry removed");
      onDeleted();
    },
    onError: (e: Error) => toastApiError("Delete failed", e),
  });

  const testMutation = useMutation({
    mutationFn: (registryId: string) =>
      testClusterRegistry(clusterId, registryId),
    onMutate: (registryId: string) => {
      setTestStatus((s) => ({ ...s, [registryId]: "pending" }));
    },
    onSuccess: (res, registryId) => {
      setTestStatus((s) => ({ ...s, [registryId]: res.ok ? "ok" : "fail" }));
      if (res.ok) {
        toastSuccess(
          `Registry reachable${res.latencyMs ? ` (${res.latencyMs}ms)` : ""}`,
        );
      } else {
        toastError(res.message || "Registry test failed");
      }
    },
    onError: (e: Error, registryId) => {
      setTestStatus((s) => ({ ...s, [registryId]: "fail" }));
      toastApiError("Test failed", e);
    },
  });

  return { testStatus, deleteMutation, testMutation };
}
