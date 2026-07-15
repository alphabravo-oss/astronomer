/**
 * React Query hooks for Gatekeeper / OPA constraint authoring (P-04).
 *
 * Co-located with the page to avoid touching the shared `lib/hooks.ts` this
 * wave. Validate is a plain mutation (no cache); apply + delete invalidate the
 * per-cluster constraint list.
 */
'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api';
import { queryKeys } from '@/lib/query-keys';

export function useGatekeeperConstraints(clusterId: string) {
  return useQuery({
    queryKey: queryKeys.gatekeeperConstraints(clusterId),
    queryFn: () => api.listGatekeeperConstraints(clusterId),
    enabled: !!clusterId,
  });
}

export function useValidateConstraint(clusterId: string) {
  return useMutation({
    mutationFn: (yaml: string) => api.validateGatekeeperConstraint(clusterId, yaml),
    onError: (err: Error) => toastApiError('Validation request failed', err),
  });
}

export function useApplyConstraint(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (yaml: string) => api.applyGatekeeperConstraint(clusterId, yaml),
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: queryKeys.gatekeeperConstraints(clusterId) });
      if (result.applied) {
        toastSuccess(`Applied ${result.kind} "${result.name}"`);
      }
    },
    onError: (err: Error) => toastApiError('Failed to apply constraint', err),
  });
}

export function useDeleteConstraint(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.deleteGatekeeperConstraint(clusterId, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.gatekeeperConstraints(clusterId) });
      toastSuccess('Constraint deleted');
    },
    onError: (err: Error) => toastApiError('Failed to delete constraint', err),
  });
}
