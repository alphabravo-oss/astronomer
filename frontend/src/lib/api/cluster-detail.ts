/**
 * Cluster-detail API client — Velero snapshots/schedules, private registries,
 * and cluster-template binding. Backend endpoints live under
 * `/api/v1/clusters/{cluster_id}/…` and are camelized by the shared axios
 * interceptor in ../api.ts, so all response types use camelCase keys.
 *
 * Re-exported from ../api.ts via `export * from './api/cluster-detail'`.
 */

import api from '../api';
import type { APIResponse } from '@/types';

// ============================================================
// Snapshots (Velero)
// ============================================================

export type SnapshotPhase =
  | 'New'
  | 'InProgress'
  | 'Completed'
  | 'PartiallyFailed'
  | 'Failed'
  | 'FailedValidation'
  | 'Deleting'
  | string;

export interface SnapshotSpec {
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  includedResources?: string[];
  excludedResources?: string[];
  snapshotVolumes?: boolean;
  ttl?: string;
  storageLocation?: string;
  labelSelector?: Record<string, string>;
}

export interface Snapshot {
  id: string;
  name: string;
  source: 'adhoc' | 'schedule';
  scheduleId?: string;
  scheduleName?: string;
  phase: SnapshotPhase;
  spec: SnapshotSpec;
  startTimestamp?: string;
  completionTimestamp?: string;
  expiration?: string;
  warnings?: number;
  errors?: number;
  validationErrors?: string[];
  createdAt: string;
}

export interface SnapshotSchedule {
  id: string;
  name: string;
  cron: string;
  enabled: boolean;
  spec: SnapshotSpec;
  lastRun?: string;
  nextRun?: string;
  createdAt: string;
  updatedAt: string;
}

export interface VeleroStatus {
  installed: boolean;
  bslReady: boolean;
  vslReady?: boolean;
  version?: string;
  storageLocation?: string;
  message?: string;
}

export async function getVeleroStatus(clusterId: string): Promise<VeleroStatus> {
  const res = await api.get<APIResponse<VeleroStatus>>(`/clusters/${clusterId}/velero-status`);
  return res.data.data;
}

export async function listSnapshots(clusterId: string): Promise<Snapshot[]> {
  const res = await api.get<APIResponse<Snapshot[]>>(`/clusters/${clusterId}/snapshots`);
  return res.data.data ?? [];
}

export async function createSnapshot(
  clusterId: string,
  body: { spec: SnapshotSpec },
): Promise<Snapshot> {
  const res = await api.post<APIResponse<Snapshot>>(`/clusters/${clusterId}/snapshots`, body);
  return res.data.data;
}

export async function deleteSnapshot(clusterId: string, snapshotId: string): Promise<void> {
  await api.delete(`/clusters/${clusterId}/snapshots/${snapshotId}`);
}

export interface RestoreSnapshotRequest {
  target_cluster_id?: string;
  spec?: {
    includedNamespaces?: string[];
    excludedNamespaces?: string[];
    namespaceMapping?: Record<string, string>;
    restorePVs?: boolean;
  };
}

export interface SnapshotRestore {
  id: string;
  name: string;
  snapshotId: string;
  targetClusterId: string;
  phase: string;
  startTimestamp?: string;
  completionTimestamp?: string;
  errors?: number;
  warnings?: number;
}

export async function restoreSnapshot(
  clusterId: string,
  snapshotId: string,
  body: RestoreSnapshotRequest,
): Promise<SnapshotRestore> {
  const res = await api.post<APIResponse<SnapshotRestore>>(
    `/clusters/${clusterId}/snapshots/${snapshotId}/restore`,
    body,
  );
  return res.data.data;
}

export async function listSnapshotSchedules(clusterId: string): Promise<SnapshotSchedule[]> {
  const res = await api.get<APIResponse<SnapshotSchedule[]>>(
    `/clusters/${clusterId}/snapshot-schedules`,
  );
  return res.data.data ?? [];
}

export async function createSnapshotSchedule(
  clusterId: string,
  body: { name: string; cron: string; enabled?: boolean; spec: SnapshotSpec },
): Promise<SnapshotSchedule> {
  const res = await api.post<APIResponse<SnapshotSchedule>>(
    `/clusters/${clusterId}/snapshot-schedules`,
    body,
  );
  return res.data.data;
}

export async function updateSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
  body: Partial<{ name: string; cron: string; enabled: boolean; spec: SnapshotSpec }>,
): Promise<SnapshotSchedule> {
  const res = await api.put<APIResponse<SnapshotSchedule>>(
    `/clusters/${clusterId}/snapshot-schedules/${scheduleId}`,
    body,
  );
  return res.data.data;
}

export async function deleteSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
): Promise<void> {
  await api.delete(`/clusters/${clusterId}/snapshot-schedules/${scheduleId}`);
}

// ============================================================
// Cluster registries (private image-pull credentials)
// ============================================================

export interface ClusterRegistry {
  id: string;
  registryUrl: string;
  username: string;
  namespaces: string[];
  secretName: string;
  injectDefaultSa: boolean;
  lastAppliedAt?: string;
  lastApplyError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateRegistryRequest {
  registry_url: string;
  username: string;
  password: string;
  namespaces?: string[];
  secret_name?: string;
  inject_default_sa?: boolean;
}

export interface UpdateRegistryRequest {
  registry_url?: string;
  username?: string;
  /** Omit to preserve the existing password. */
  password?: string;
  namespaces?: string[];
  secret_name?: string;
  inject_default_sa?: boolean;
}

export interface RegistryTestResult {
  ok: boolean;
  message?: string;
  latencyMs?: number;
}

export async function listClusterRegistries(clusterId: string): Promise<ClusterRegistry[]> {
  const res = await api.get<APIResponse<ClusterRegistry[]>>(
    `/clusters/${clusterId}/registries`,
  );
  return res.data.data ?? [];
}

export async function createClusterRegistry(
  clusterId: string,
  body: CreateRegistryRequest,
): Promise<ClusterRegistry> {
  const res = await api.post<APIResponse<ClusterRegistry>>(
    `/clusters/${clusterId}/registries`,
    body,
  );
  return res.data.data;
}

export async function updateClusterRegistry(
  clusterId: string,
  registryId: string,
  body: UpdateRegistryRequest,
): Promise<ClusterRegistry> {
  const res = await api.put<APIResponse<ClusterRegistry>>(
    `/clusters/${clusterId}/registries/${registryId}`,
    body,
  );
  return res.data.data;
}

export async function deleteClusterRegistry(
  clusterId: string,
  registryId: string,
): Promise<void> {
  await api.delete(`/clusters/${clusterId}/registries/${registryId}`);
}

export async function testClusterRegistry(
  clusterId: string,
  registryId: string,
): Promise<RegistryTestResult> {
  const res = await api.post<APIResponse<RegistryTestResult>>(
    `/clusters/${clusterId}/registries/${registryId}/test`,
  );
  return res.data.data;
}

// ============================================================
// Cluster template binding
// ============================================================

export type ClusterTemplateStatus = 'pending' | 'applying' | 'applied' | 'failed' | string;

/**
 * Shape returned by `GET /clusters/{id}/template/` — the *binding* between a
 * cluster and the template it has applied. The template type itself (with
 * the editable spec) lives in ./project-detail.ts; importing it here would
 * create a circular re-export, so callers that need both pull each from its
 * source module.
 */
export interface ClusterTemplateBinding {
  templateId: string;
  templateName: string;
  templateDisplayName: string;
  status: ClusterTemplateStatus;
  appliedAt?: string;
  lastError?: string;
  spec: unknown;
}

export async function getClusterTemplateBinding(
  clusterId: string,
): Promise<ClusterTemplateBinding | null> {
  try {
    const res = await api.get<APIResponse<ClusterTemplateBinding>>(
      `/clusters/${clusterId}/template`,
    );
    return res.data.data ?? null;
  } catch (err) {
    // 404 — no template bound. Anything else surfaces to the caller.
    const status = (err as { response?: { status?: number } })?.response?.status;
    if (status === 404) return null;
    throw err;
  }
}

export async function bindClusterTemplate(
  clusterId: string,
  body: { template_id: string },
): Promise<ClusterTemplateBinding> {
  const res = await api.post<APIResponse<ClusterTemplateBinding>>(
    `/clusters/${clusterId}/template`,
    body,
  );
  return res.data.data;
}

export async function reapplyClusterTemplate(
  clusterId: string,
): Promise<ClusterTemplateBinding> {
  const res = await api.post<APIResponse<ClusterTemplateBinding>>(
    `/clusters/${clusterId}/template/reapply`,
  );
  return res.data.data;
}

export async function detachClusterTemplate(clusterId: string): Promise<void> {
  await api.delete(`/clusters/${clusterId}/template`);
}
