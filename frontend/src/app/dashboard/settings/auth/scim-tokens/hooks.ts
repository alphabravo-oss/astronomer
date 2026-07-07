/**
 * React Query hooks for SCIM provisioning tokens (F-05).
 *
 * Co-located with the page to avoid touching the shared `lib/hooks.ts` this
 * wave. The plaintext token surfaces only in the create mutation's result and
 * is never cached.
 */
'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api';
import { queryKeys } from '@/lib/query-keys';

export function useSCIMTokens() {
  return useQuery({
    queryKey: queryKeys.scimTokens,
    queryFn: () => api.listSCIMTokens(),
  });
}

export function useCreateSCIMToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.createSCIMToken(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.scimTokens });
      toastSuccess('SCIM token created');
    },
    onError: (err: Error) => toastApiError('Failed to create SCIM token', err),
  });
}

export function useRevokeSCIMToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteSCIMToken(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.scimTokens });
      toastSuccess('SCIM token revoked');
    },
    onError: (err: Error) => toastApiError('Failed to revoke SCIM token', err),
  });
}
