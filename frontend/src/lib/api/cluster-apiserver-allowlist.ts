/** API-server source allow-list posture and reconciliation controls. */

import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import { createIdempotencyKey } from "@/lib/api/idempotency";
import { mapPage } from "@/lib/api/pagination";
import type { PaginatedResponse } from "@/types";

// ============================================================
// Apiserver allow-list (migration 070)
// ============================================================

export type ApiserverAllowlistMode = "monitor" | "enforce" | "disabled";
export type ApiserverAllowlistSyncStatus =
  "synced" | "drifting" | "pending" | "failed";
export type ApiserverAllowlistProvider =
  "eks" | "gke" | "aks" | "doks" | "self_managed" | "unknown";

export interface ApiserverAllowlistCapability {
  provider: ApiserverAllowlistProvider;
  canMonitor: boolean;
  canEnforce: boolean;
  reason?: string;
  requiredMetadata: string[];
}

export interface ApiserverAllowlistResponse {
  clusterId: string;
  operatorCidrs: string[];
  astronomerEgress: string[];
  emergency: string[];
  desired: string[];
  effective: string[];
  mode: ApiserverAllowlistMode;
  detectedProvider: ApiserverAllowlistProvider;
  syncStatus: ApiserverAllowlistSyncStatus;
  lastError?: string;
  lastReconciledAt?: string;
  drift: boolean;
  capability: ApiserverAllowlistCapability;
}

export interface ApiserverAllowlistUpdateRequest {
  cidrs: string[];
  mode: ApiserverAllowlistMode;
  forceApply?: boolean;
}

export interface ApiserverAllowlistSnapshot {
  id: number;
  clusterId: string;
  capturedAt: string;
  effectiveCidrs: string[];
  desiredCidrs: string[];
  drift: boolean;
}

function mapAllowlist(
  raw: Record<string, unknown>,
): ApiserverAllowlistResponse {
  const capability = (raw.capability ?? {}) as Record<string, unknown>;
  return {
    clusterId: String(raw.cluster_id ?? ""),
    operatorCidrs: Array.isArray(raw.operator_cidrs)
      ? (raw.operator_cidrs as string[])
      : [],
    astronomerEgress: Array.isArray(raw.astronomer_egress)
      ? (raw.astronomer_egress as string[])
      : [],
    emergency: Array.isArray(raw.emergency) ? (raw.emergency as string[]) : [],
    desired: Array.isArray(raw.desired) ? (raw.desired as string[]) : [],
    effective: Array.isArray(raw.effective) ? (raw.effective as string[]) : [],
    mode: String(raw.mode ?? "disabled") as ApiserverAllowlistMode,
    detectedProvider: String(
      raw.detected_provider ?? "unknown",
    ) as ApiserverAllowlistProvider,
    syncStatus: String(
      raw.sync_status ?? "pending",
    ) as ApiserverAllowlistSyncStatus,
    lastError: raw.last_error ? String(raw.last_error) : undefined,
    lastReconciledAt: raw.last_reconciled_at
      ? String(raw.last_reconciled_at)
      : undefined,
    drift: Boolean(raw.drift),
    capability: {
      provider: String(
        capability.provider ?? raw.detected_provider ?? "unknown",
      ) as ApiserverAllowlistProvider,
      canMonitor: Boolean(capability.can_monitor),
      canEnforce: Boolean(capability.can_enforce),
      reason: capability.reason ? String(capability.reason) : undefined,
      requiredMetadata: Array.isArray(capability.required_metadata)
        ? capability.required_metadata.map(String)
        : [],
    },
  };
}

function mapAllowlistSnapshot(input: unknown): ApiserverAllowlistSnapshot {
  const raw = input as Record<string, unknown>;
  return {
    id: Number(raw.id ?? 0),
    clusterId: String(raw.cluster_id ?? ""),
    capturedAt: String(raw.captured_at ?? ""),
    effectiveCidrs: Array.isArray(raw.effective_cidrs)
      ? (raw.effective_cidrs as string[])
      : [],
    desiredCidrs: Array.isArray(raw.desired_cidrs)
      ? (raw.desired_cidrs as string[])
      : [],
    drift: Boolean(raw.drift),
  };
}

export async function getApiserverAllowlist(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ApiserverAllowlistResponse> {
  const wire = await generated.getClustersByClusterIdApiserverAllowlist({
    path: { cluster_id: clusterId },
    signal,
  });
  return mapAllowlist(requireEnvelopeData(wire, "API server allowlist"));
}

export async function previewApiserverAllowlist(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ApiserverAllowlistResponse> {
  const wire = await generated.getClustersByClusterIdApiserverAllowlistPreview({
    path: { cluster_id: clusterId },
    signal,
  });
  return mapAllowlist(
    requireEnvelopeData(wire, "API server allowlist preview"),
  );
}

export async function updateApiserverAllowlist(
  clusterId: string,
  body: ApiserverAllowlistUpdateRequest,
  signal?: AbortSignal,
): Promise<ApiserverAllowlistResponse> {
  const wire = await generated.putClustersByClusterIdApiserverAllowlist({
    path: { cluster_id: clusterId },
    body: { cidrs: body.cidrs, mode: body.mode, force_apply: body.forceApply },
    signal,
  });
  return mapAllowlist(requireEnvelopeData(wire, "API server allowlist update"));
}

export async function reconcileApiserverAllowlist(
  clusterId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.postClustersByClusterIdApiserverAllowlistReconcile({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    signal,
  });
}

export async function listApiserverAllowlistSnapshots(
  clusterId: string,
  opts?: { limit?: number; offset?: number },
  signal?: AbortSignal,
): Promise<PaginatedResponse<ApiserverAllowlistSnapshot>> {
  const wire =
    await generated.getClustersByClusterIdApiserverAllowlistSnapshots({
      path: { cluster_id: clusterId },
      query: opts,
      signal,
    });
  return mapPage(wire, mapAllowlistSnapshot);
}
