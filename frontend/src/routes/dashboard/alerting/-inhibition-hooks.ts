/**
 * React Query hooks for Alertmanager-style inhibition rules (P-03).
 *
 * Co-located with the alerting page to avoid touching the shared
 * `lib/hooks.ts` this wave. Mirrors the silence hooks' conventions.
 */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api';
import type { InhibitionWriteRequest } from '@/lib/api/alerting-inhibitions';
import { queryKeys } from '@/lib/query-keys';

export function useInhibitions() {
  return useQuery({
    queryKey: queryKeys.alerting.inhibitions,
    queryFn: () => api.listInhibitions(),
  });
}

export function useCreateInhibition() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: InhibitionWriteRequest) => api.createInhibition(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.alerting.inhibitions });
      toastSuccess('Inhibition rule created');
    },
    onError: (err: Error) => toastApiError('Failed to create inhibition rule', err),
  });
}

export function useUpdateInhibition() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: InhibitionWriteRequest }) =>
      api.updateInhibition(id, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.alerting.inhibitions });
      toastSuccess('Inhibition rule updated');
    },
    onError: (err: Error) => toastApiError('Failed to update inhibition rule', err),
  });
}

export function useDeleteInhibition() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteInhibition(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.alerting.inhibitions });
      toastSuccess('Inhibition rule deleted');
    },
    onError: (err: Error) => toastApiError('Failed to delete inhibition rule', err),
  });
}
