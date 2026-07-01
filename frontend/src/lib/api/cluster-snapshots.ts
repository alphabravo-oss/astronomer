/**
 * Control-plane (etcd) snapshot API client — pairs with the be-etcd handler
 * mounted under `/api/v1/clusters/{cluster_id}/control-plane-snapshots/…`.
 *
 * Unlike the Velero workload snapshots in ./cluster-detail.ts, these capture
 * the cluster's etcd/control-plane state. Restore is deliberately NOT
 * automated: the API returns human-readable runbook guidance and the operator
 * performs the restore out-of-band, so there is no "restore now" mutation here.
 *
 * Only available for self-managed control planes (k3s / rke2 / k8s / openshift).
 * Managed distributions (eks / aks / gke) expose no etcd and the backend
 * returns an empty list / 4xx there — the page gates on distribution before
 * ever calling these.
 *
 * Response bodies are camelized by the shared axios interceptor in ../api.ts,
 * so every field below is camelCase.
 */

import api from '../api';
import type { APIResponse } from '@/types';

// ============================================================
// Types
// ============================================================

/**
 * Lifecycle of a control-plane snapshot. String-widened so a new backend
 * phase never renders as a blank cell — unknown values fall through to the
 * neutral pill.
 */
export type ControlPlaneSnapshotStatus =
  | 'pending'
  | 'in_progress'
  | 'completed'
  | 'failed'
  | string;

export interface ControlPlaneSnapshot {
  id: string;
  clusterId: string;
  /** Snapshot file/object name as written by the agent (e.g. `snapshot-2026-06-30.db`). */
  name: string;
  status: ControlPlaneSnapshotStatus;
  /** etcd store revision captured at snapshot time, when known. */
  etcdRevision?: number;
  /** On-disk / object-store size of the snapshot artifact in bytes. */
  sizeBytes?: number;
  /** Where the artifact was persisted (node path or object-store URI). */
  storageLocation?: string;
  /** Set when status === 'failed'. */
  error?: string;
  /** Free-form note supplied by the operator when the snapshot was taken. */
  note?: string;
  /** Subject that initiated the snapshot (user email / "schedule"). */
  createdBy?: string;
  createdAt: string;
  completedAt?: string;
}

export interface CreateControlPlaneSnapshotRequest {
  /** Optional operator note stored alongside the snapshot. */
  note?: string;
}

/**
 * Read-only restore runbook returned by the backend. Deliberately opaque
 * guidance text (plus optional ordered steps) rather than an executable plan —
 * this surface never performs a restore, it only tells the operator how.
 */
export interface RestoreGuidance {
  clusterId: string;
  snapshotId: string;
  /** Human-readable runbook, typically markdown/plain text. */
  guidance: string;
  /** Optional ordered checklist rendered above/below the guidance body. */
  steps?: string[];
  /** Distribution the guidance was tailored for (k3s / rke2 / …). */
  distribution?: string;
  /** When the guidance snapshot was generated. */
  generatedAt?: string;
}

// ============================================================
// Endpoints
// ============================================================

export async function listControlPlaneSnapshots(
  clusterId: string,
): Promise<ControlPlaneSnapshot[]> {
  const res = await api.get<APIResponse<ControlPlaneSnapshot[]>>(
    `/clusters/${clusterId}/control-plane-snapshots/`,
  );
  return res.data.data ?? [];
}

export async function getControlPlaneSnapshot(
  clusterId: string,
  snapshotId: string,
): Promise<ControlPlaneSnapshot> {
  const res = await api.get<APIResponse<ControlPlaneSnapshot>>(
    `/clusters/${clusterId}/control-plane-snapshots/${snapshotId}/`,
  );
  return res.data.data;
}

export async function createControlPlaneSnapshot(
  clusterId: string,
  body: CreateControlPlaneSnapshotRequest = {},
): Promise<ControlPlaneSnapshot> {
  const res = await api.post<APIResponse<ControlPlaneSnapshot>>(
    `/clusters/${clusterId}/control-plane-snapshots/`,
    body,
  );
  return res.data.data;
}

/**
 * Fetch the read-only restore runbook for a snapshot. This does NOT trigger a
 * restore — it returns guidance the operator follows manually.
 */
export async function getControlPlaneSnapshotRestoreGuidance(
  clusterId: string,
  snapshotId: string,
): Promise<RestoreGuidance> {
  const res = await api.get<APIResponse<RestoreGuidance>>(
    `/clusters/${clusterId}/control-plane-snapshots/${snapshotId}/restore-guidance/`,
  );
  return res.data.data;
}
