/**
 * React Query hooks for the Phase B2 Velero-backed backup endpoints.
 *
 * These live alongside the page components rather than in the global
 * `lib/hooks.ts` because the file ownership rules for this phase forbid
 * touching that file. They follow the same conventions: stable query-key
 * factories, mutations that invalidate the relevant lists, and `toast`
 * calls for user-visible side-effects.
 */

'use client';

import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api';
import type {
  BackupRestore,
  BackupRun,
  BackupScheduleRow,
  BackupStorageLocation,
  CreateBackupStorageRequest,
  CreateRestoreRequestB2,
  CreateScheduleRequestB2,
  TestStorageResult,
} from '@/types';

/** Stable query keys for cache invalidation. */
export const b2Keys = {
  all: ['b2-backups'] as const,
  storage: (params?: Record<string, unknown>) =>
    ['b2-backups', 'storage', params] as const,
  schedules: (params?: Record<string, unknown>) =>
    ['b2-backups', 'schedules', params] as const,
  runs: (params?: Record<string, unknown>) =>
    ['b2-backups', 'runs', params] as const,
  runDetail: (id: string) => ['b2-backups', 'runs', 'detail', id] as const,
  restores: (params?: Record<string, unknown>) =>
    ['b2-backups', 'restores', params] as const,
  restoreDetail: (id: string) =>
    ['b2-backups', 'restores', 'detail', id] as const,
};

// --- Storage Locations ---

export function useB2StorageLocations(params?: { cluster_id?: string }) {
  return useQuery({
    queryKey: b2Keys.storage(params),
    queryFn: () => api.b2ListStorageLocations({ ...params, page_size: 100 }),
  });
}

export function useB2CreateStorageLocation() {
  const qc = useQueryClient();
  return useMutation<BackupStorageLocation, Error, CreateBackupStorageRequest>({
    mutationFn: (body) => api.b2CreateStorageLocation(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: b2Keys.all });
      toastSuccess('Storage location created');
    },
    onError: (e) => toastApiError('Failed to create storage', e),
  });
}

export function useB2DeleteStorageLocation() {
  const qc = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => api.b2DeleteStorageLocation(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: b2Keys.all });
      toastSuccess('Storage location deleted');
    },
    onError: (e) => toastApiError('Failed to delete storage', e),
  });
}

export function useB2TestStorageLocation() {
  return useMutation<TestStorageResult, Error, string>({
    mutationFn: (id) => api.b2TestStorageLocation(id),
    // Caller renders inline result; no toast here so the wizard can show a
    // structured success/failure card without being shouted at twice.
  });
}

// --- Schedules ---

export function useB2Schedules(params?: { cluster_id?: string }) {
  return useQuery({
    queryKey: b2Keys.schedules(params),
    queryFn: () => api.b2ListSchedules({ ...params, page_size: 100 }),
  });
}

export function useB2CreateSchedule() {
  const qc = useQueryClient();
  return useMutation<BackupScheduleRow, Error, CreateScheduleRequestB2>({
    mutationFn: (body) => api.b2CreateSchedule(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: b2Keys.all });
      toastSuccess('Schedule created');
    },
    onError: (e) => toastApiError('Failed to create schedule', e),
  });
}

export function useB2UpdateSchedule() {
  const qc = useQueryClient();
  return useMutation<
    BackupScheduleRow,
    Error,
    { id: string; data: Partial<CreateScheduleRequestB2> }
  >({
    mutationFn: ({ id, data }) => api.b2UpdateSchedule(id, data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: b2Keys.all });
    },
    onError: (e) => toastApiError('Failed to update schedule', e),
  });
}

export function useB2DeleteSchedule() {
  const qc = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => api.b2DeleteSchedule(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: b2Keys.all });
      toastSuccess('Schedule deleted');
    },
    onError: (e) => toastApiError('Failed to delete schedule', e),
  });
}

export function useB2TriggerScheduleNow() {
  const qc = useQueryClient();
  return useMutation<BackupRun, Error, string>({
    mutationFn: (id) => api.b2TriggerScheduleNow(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: b2Keys.all });
      toastSuccess('One-off backup triggered');
    },
    onError: (e) => toastApiError('Failed to trigger backup', e),
  });
}

// --- Runs ---

export function useB2Runs(params?: { cluster_id?: string }) {
  return useQuery({
    queryKey: b2Keys.runs(params),
    queryFn: () => api.b2ListRuns({ ...params, page_size: 100 }),
    refetchInterval: 15000,
  });
}

export function useB2Run(id: string) {
  return useQuery({
    queryKey: b2Keys.runDetail(id),
    queryFn: () => api.b2GetRun(id),
    enabled: !!id,
    refetchInterval: 10000,
  });
}

// --- Restores ---

export function useB2Restore(id: string) {
  return useQuery({
    queryKey: b2Keys.restoreDetail(id),
    queryFn: () => api.b2GetRestore(id),
    enabled: !!id,
    refetchInterval: 10000,
  });
}

export function useB2CreateRestore() {
  const qc = useQueryClient();
  return useMutation<BackupRestore, Error, CreateRestoreRequestB2>({
    mutationFn: (body) => api.b2CreateRestore(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: b2Keys.all });
      toastSuccess('Restore initiated');
    },
    onError: (e) => toastApiError('Failed to start restore', e),
  });
}
