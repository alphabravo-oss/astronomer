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
 * Generated responses retain snake_case; endpoint functions explicitly map
 * them into the camelCase view models below.
 */

import {
  createControlPlaneSnapshot as createControlPlaneSnapshotOperation,
  getControlPlaneSnapshot as getControlPlaneSnapshotOperation,
  getControlPlaneSnapshotRestoreGuidance as getControlPlaneSnapshotRestoreGuidanceOperation,
  listControlPlaneSnapshots as listControlPlaneSnapshotsOperation,
} from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import type { OpenAPIComponents } from "@/types/openapi.generated";

// ============================================================
// Types
// ============================================================

/**
 * Lifecycle of a control-plane snapshot. String-widened so a new backend
 * phase never renders as a blank cell — unknown values fall through to the
 * neutral pill.
 */
export type ControlPlaneSnapshotStatus =
  "pending" | "in_progress" | "completed" | "failed" | string;

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
  name?: string;
  location?: "local" | "s3";
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
  signal?: AbortSignal,
): Promise<ControlPlaneSnapshot[]> {
  const response = await listControlPlaneSnapshotsOperation({
    path: { cluster_id: clusterId },
    signal,
  });
  return (response.data?.items ?? []).map(snapshotFromWire);
}

export async function getControlPlaneSnapshot(
  clusterId: string,
  snapshotId: string,
  signal?: AbortSignal,
): Promise<ControlPlaneSnapshot> {
  const response = await getControlPlaneSnapshotOperation({
    path: { cluster_id: clusterId, id: snapshotId },
    signal,
  });
  return snapshotFromWire(response.data!);
}

export async function createControlPlaneSnapshot(
  clusterId: string,
  body: CreateControlPlaneSnapshotRequest = {},
  signal?: AbortSignal,
): Promise<ControlPlaneSnapshot> {
  const response = await createControlPlaneSnapshotOperation({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body,
    signal,
  });
  return snapshotFromWire(response.data!);
}

/**
 * Fetch the read-only restore runbook for a snapshot. This does NOT trigger a
 * restore — it returns guidance the operator follows manually.
 */
export async function getControlPlaneSnapshotRestoreGuidance(
  clusterId: string,
  snapshotId: string,
  signal?: AbortSignal,
): Promise<RestoreGuidance> {
  const response = await getControlPlaneSnapshotRestoreGuidanceOperation({
    path: { cluster_id: clusterId, id: snapshotId },
    signal,
  });
  const wire = response.data!;
  return {
    clusterId,
    snapshotId,
    distribution: wire.distribution,
    steps: wire.steps,
    guidance: [wire.summary, wire.warning, wire.docs_url].filter(Boolean).join("\n\n"),
  };
}

type ControlPlaneSnapshotWire = OpenAPIComponents["schemas"]["ControlPlaneSnapshotWire"];

function snapshotFromWire(wire: ControlPlaneSnapshotWire): ControlPlaneSnapshot {
  return {
    id: wire.id,
    clusterId: wire.cluster_id,
    name: wire.name,
    status: wire.status,
    sizeBytes: wire.size_bytes,
    storageLocation: wire.location,
    error: wire.error,
    createdBy: wire.requested_by_id,
    createdAt: wire.created_at,
    completedAt: wire.completed_at,
  };
}
