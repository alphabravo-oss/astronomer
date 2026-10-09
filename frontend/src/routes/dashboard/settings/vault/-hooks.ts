import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/query-keys";
import { extractApiErrorMessage } from "@/lib/api/errors";
import {
  listVaultConnections,
  createVaultConnection,
  deleteVaultConnection,
  testVaultConnection,
  type VaultConnectionWriteRequest,
} from "@/lib/api/vault";
export function useVaultConnections(setError: (message: string) => void) {
  const qc = useQueryClient();
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: queryKeys.vault.connections });
  };
  const vaultConnectionsQuery = useQuery({
    queryKey: queryKeys.vault.connections,
    queryFn: ({ signal }) => listVaultConnections({ signal }),
  });
  const createMu = useMutation({
    mutationFn: (body: VaultConnectionWriteRequest) =>
      createVaultConnection(body),
    onSuccess: invalidate,
    onError: (err: unknown) =>
      setError(extractApiErrorMessage(err) ?? String(err)),
  });
  const delMu = useMutation({
    mutationFn: (id: string) => deleteVaultConnection(id),
    onSuccess: invalidate,
  });
  const testMu = useMutation({
    mutationFn: (id: string) => testVaultConnection(id),
    onSuccess: invalidate,
  });
  return { vaultConnectionsQuery, createMu, delMu, testMu };
}
