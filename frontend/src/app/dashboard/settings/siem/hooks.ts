/**
 * React Query hooks for the SIEM forwarders settings surface (F-05).
 *
 * Co-located with the page rather than in the global `lib/hooks.ts` so this
 * wave doesn't touch the shared hooks module. Conventions match the settings
 * hub hooks: stable query keys, mutations invalidate the list, toasts on
 * user-visible side-effects.
 */
'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api';
import type { SIEMForwarderWriteRequest } from '@/lib/api/siem-forwarders';
import { queryKeys } from '@/lib/query-keys';

export function useSIEMForwarders() {
  return useQuery({
    queryKey: queryKeys.siemForwarders.list,
    queryFn: () => api.listSIEMForwarders(),
  });
}

export function useSIEMForwarderStatus(id: string | undefined, enabled = true) {
  return useQuery({
    queryKey: queryKeys.siemForwarders.status(id ?? ''),
    queryFn: () => api.getSIEMForwarderStatus(id as string),
    enabled: !!id && enabled,
    // Status (queue depth / dropped counts) is live-ish; refresh periodically
    // while the drawer is open.
    refetchInterval: 10_000,
  });
}

export function useCreateSIEMForwarder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: SIEMForwarderWriteRequest) => api.createSIEMForwarder(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.siemForwarders.all });
      toastSuccess('SIEM forwarder created');
    },
    onError: (err: Error) => toastApiError('Failed to create SIEM forwarder', err),
  });
}

export function useUpdateSIEMForwarder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: SIEMForwarderWriteRequest }) =>
      api.updateSIEMForwarder(id, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.siemForwarders.all });
      toastSuccess('SIEM forwarder updated');
    },
    onError: (err: Error) => toastApiError('Failed to update SIEM forwarder', err),
  });
}

export function useDeleteSIEMForwarder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteSIEMForwarder(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.siemForwarders.all });
      toastSuccess('SIEM forwarder deleted');
    },
    onError: (err: Error) => toastApiError('Failed to delete SIEM forwarder', err),
  });
}

export function useTestSIEMForwarder() {
  return useMutation({
    mutationFn: (id: string) => api.testSIEMForwarder(id),
    onSuccess: () => toastSuccess('Test event queued — it ships on the next dispatch tick'),
    onError: (err: Error) => toastApiError('Failed to queue test event', err),
  });
}
