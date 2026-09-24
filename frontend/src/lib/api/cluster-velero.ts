/** Workload backup and restore APIs backed by Velero. */

import * as generated from "@/lib/api/generated/client";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import type { OpenAPIComponents } from "@/types/openapi.generated";

// ============================================================
// Snapshots (Velero)
// ============================================================

export type SnapshotPhase =
  | "New"
  | "InProgress"
  | "Completed"
  | "PartiallyFailed"
  | "Failed"
  | "FailedValidation"
  | "Deleting"
  | string;

export interface SnapshotSpec {
  includedNamespaces?: string[];
  excludedNamespaces?: string[];
  includedResources?: string[];
  excludedResources?: string[];
  snapshotVolumes?: boolean;
  ttl?: string;
  storageLocation?: string;
  labelSelector?: string;
  volumeSnapshotLocations?: string[];
}

export interface Snapshot {
  id: string;
  name: string;
  source: "adhoc" | "schedule";
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

export interface VeleroBSLSummary {
  name: string;
  provider: string;
  default: boolean;
  phase: string;
  bucket: string;
}

export interface VeleroStatus {
  installed: boolean;
  namespace?: string;
  storageReady: boolean;
  storageLocations?: VeleroBSLSummary[];
  reason?: string;
}

type SnapshotWire = OpenAPIComponents["schemas"]["SnapshotResponse"];
type SnapshotScheduleWire =
  OpenAPIComponents["schemas"]["SnapshotScheduleResponse"];
type SnapshotRestoreWire =
  OpenAPIComponents["schemas"]["SnapshotRestoreResponse"];
type VeleroStatusWire = OpenAPIComponents["schemas"]["VeleroStatusResponse"];

function mapSnapshotSpec(
  wire: OpenAPIComponents["schemas"]["SnapshotSpec"] | undefined,
): SnapshotSpec {
  return {
    includedNamespaces: wire?.includedNamespaces,
    excludedNamespaces: wire?.excludedNamespaces,
    includedResources: wire?.includedResources,
    excludedResources: wire?.excludedResources,
    snapshotVolumes: wire?.snapshotVolumes ?? undefined,
    ttl: wire?.ttl,
    storageLocation: wire?.storageLocation,
    labelSelector: wire?.labelSelector,
    volumeSnapshotLocations: wire?.volumeSnapshotLocations,
  };
}

function mapSnapshot(wire: SnapshotWire): Snapshot {
  if (!wire.id || !wire.velero_name || !wire.phase || !wire.created_at) {
    throw new Error("Snapshot response is missing required identity fields");
  }
  return {
    id: wire.id,
    name: wire.velero_name,
    source: wire.source === "schedule" ? "schedule" : "adhoc",
    phase: wire.phase,
    spec: mapSnapshotSpec(wire.spec),
    startTimestamp: wire.start_time ?? undefined,
    completionTimestamp: wire.completion_time ?? undefined,
    expiration: wire.expires_at ?? undefined,
    warnings: wire.warnings_count,
    errors: wire.errors_count,
    createdAt: wire.created_at,
  };
}

function mapSnapshotSchedule(wire: SnapshotScheduleWire): SnapshotSchedule {
  if (
    !wire.id ||
    !wire.name ||
    !wire.cron_schedule ||
    wire.enabled === undefined ||
    !wire.created_at ||
    !wire.updated_at
  ) {
    throw new Error("Snapshot schedule response is incomplete");
  }
  return {
    id: wire.id,
    name: wire.name,
    cron: wire.cron_schedule,
    enabled: wire.enabled,
    spec: mapSnapshotSpec(wire.spec),
    lastRun: wire.last_run_at ?? undefined,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function mapSnapshotRestore(wire: SnapshotRestoreWire): SnapshotRestore {
  if (
    !wire.id ||
    !wire.snapshot_id ||
    !wire.target_cluster_id ||
    !wire.velero_name ||
    !wire.phase
  ) {
    throw new Error("Snapshot restore response is incomplete");
  }
  return {
    id: wire.id,
    name: wire.velero_name,
    snapshotId: wire.snapshot_id,
    targetClusterId: wire.target_cluster_id,
    phase: wire.phase,
    startTimestamp: wire.start_time ?? undefined,
    completionTimestamp: wire.completion_time ?? undefined,
    errors: wire.errors_count,
    warnings: wire.warnings_count,
  };
}

function mapVeleroStatus(wire: VeleroStatusWire): VeleroStatus {
  return {
    installed: wire.installed ?? false,
    namespace: wire.namespace,
    storageReady: wire.storage_ready ?? false,
    storageLocations: wire.storage_locations?.map((location) => ({
      name: location.name ?? "",
      provider: location.provider ?? "",
      default: location.default ?? false,
      phase: location.phase ?? "",
      bucket: location.bucket ?? "",
    })),
    reason: wire.reason,
  };
}

export async function getVeleroStatus(
  clusterId: string,
  signal?: AbortSignal,
): Promise<VeleroStatus> {
  const wire = await generated.getClustersByClusterIdVeleroStatus({
    path: { cluster_id: clusterId },
    signal,
  });
  return mapVeleroStatus(requireEnvelopeData(wire, "Velero status"));
}

export async function listSnapshots(
  clusterId: string,
  signal?: AbortSignal,
): Promise<Snapshot[]> {
  const wire = await generated.getClustersByClusterIdSnapshots({
    path: { cluster_id: clusterId },
    signal,
  });
  return (requireEnvelopeData(wire, "Snapshot list").items ?? []).map(
    mapSnapshot,
  );
}

export async function createSnapshot(
  clusterId: string,
  body: { spec: SnapshotSpec },
  signal?: AbortSignal,
): Promise<Snapshot> {
  const wire = await generated.postClustersByClusterIdSnapshots({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body: body.spec,
    signal,
  });
  return mapSnapshot(requireEnvelopeData(wire, "Snapshot create"));
}

export async function deleteSnapshot(
  clusterId: string,
  snapshotId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdSnapshotsById({
    path: { cluster_id: clusterId, id: snapshotId },
    signal,
  });
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
  signal?: AbortSignal,
): Promise<SnapshotRestore> {
  const wire = await generated.postClustersByClusterIdSnapshotsByIdRestore({
    path: { cluster_id: clusterId, id: snapshotId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body,
    signal,
  });
  return mapSnapshotRestore(requireEnvelopeData(wire, "Snapshot restore"));
}

export async function listSnapshotSchedules(
  clusterId: string,
  signal?: AbortSignal,
): Promise<SnapshotSchedule[]> {
  const wire = await generated.getClustersByClusterIdSnapshotSchedules({
    path: { cluster_id: clusterId },
    signal,
  });
  return (requireEnvelopeData(wire, "Snapshot schedule list").items ?? []).map(
    mapSnapshotSchedule,
  );
}

export async function createSnapshotSchedule(
  clusterId: string,
  body: { name: string; cron: string; enabled?: boolean; spec: SnapshotSpec },
  signal?: AbortSignal,
): Promise<SnapshotSchedule> {
  const wire = await generated.postClustersByClusterIdSnapshotSchedules({
    path: { cluster_id: clusterId },
    body: {
      name: body.name,
      cron_schedule: body.cron,
      enabled: body.enabled,
      spec: body.spec,
    },
    signal,
  });
  return mapSnapshotSchedule(
    requireEnvelopeData(wire, "Snapshot schedule create"),
  );
}

export async function updateSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
  body: Partial<{
    name: string;
    cron: string;
    enabled: boolean;
    spec: SnapshotSpec;
  }>,
  signal?: AbortSignal,
): Promise<SnapshotSchedule> {
  const wire = await generated.putClustersByClusterIdSnapshotSchedulesById({
    path: { cluster_id: clusterId, id: scheduleId },
    body: {
      name: body.name,
      cron_schedule: body.cron,
      enabled: body.enabled,
      spec: body.spec,
    },
    signal,
  });
  return mapSnapshotSchedule(
    requireEnvelopeData(wire, "Snapshot schedule update"),
  );
}

export async function deleteSnapshotSchedule(
  clusterId: string,
  scheduleId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdSnapshotSchedulesById({
    path: { cluster_id: clusterId, id: scheduleId },
    signal,
  });
}

export async function getSnapshotRestore(
  clusterId: string,
  id: string,
  signal?: AbortSignal,
) {
  const wire = await generated.getClustersByClusterIdSnapshotRestoresById({
    path: { cluster_id: clusterId, id },
    signal,
  });
  return wire.data;
}
export async function getSnapshotRestores(
  clusterId: string,
  offset = 0,
  signal?: AbortSignal,
) {
  return generated.getClustersByClusterIdSnapshotRestores({
    path: { cluster_id: clusterId },
    query: { limit: 50, offset },
    signal,
  });
}
