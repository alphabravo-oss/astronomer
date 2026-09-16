/**
 * React Query hooks for SCIM provisioning tokens (F-05).
 *
 * Co-located with the page to avoid touching the shared `lib/hooks.ts` this
 * wave. The plaintext token surfaces only in the create mutation's result and
 * is never cached.
 */
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import {
  listSCIMTokens,
  createSCIMToken,
  deleteSCIMToken,
} from "@/lib/api/scim-tokens";
import { queryKeys } from "@/lib/query-keys";

export function useSCIMTokens() {
  return useQuery({
    queryKey: queryKeys.scimTokens,
    queryFn: () => listSCIMTokens(),
  });
}

export function useCreateSCIMToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => createSCIMToken(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.scimTokens });
      toastSuccess("SCIM token created");
    },
    onError: (err: Error) => toastApiError("Failed to create SCIM token", err),
  });
}

export function useRevokeSCIMToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteSCIMToken(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.scimTokens });
      toastSuccess("SCIM token revoked");
    },
    onError: (err: Error) => toastApiError("Failed to revoke SCIM token", err),
  });
}
