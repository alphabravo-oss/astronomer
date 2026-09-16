/** Service-mesh discovery, mTLS posture, inventory, and validation APIs. */

import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import type { OpenAPIComponents } from "@/types/openapi.generated";

// ============================================================
// Service-mesh posture
// ============================================================

export type ServiceMeshKind =
  "istio" | "linkerd" | "kuma" | "cilium" | "none" | "unknown";

export interface ServiceMeshDetection {
  clusterId: string;
  detectedMesh: ServiceMeshKind;
  detectedVersion: string;
  controlPlaneNamespace: string;
  gatewayCount: number;
  virtualServiceCount: number;
  destinationRuleCount: number;
  peerAuthenticationCount: number;
  serviceProfileCount: number;
  serverAuthCount: number;
  mtlsCoveragePct: number;
  lastDetectedAt?: string;
  lastError?: string;
}

export interface MTLSBreakdownRow {
  namespace: string;
  mode: string;
  rules: number;
}

export interface MTLSBreakdown {
  clusterId: string;
  mesh: ServiceMeshKind;
  mtlsCoveragePct: number;
  totalCount: number;
  rows: MTLSBreakdownRow[];
  notice?: string;
}

export interface ServiceMeshInventoryItem {
  name: string;
  namespace?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  managedBy?: string;
  readOnly: boolean;
  reason?: string;
  createdAt?: string;
}

export interface ServiceMeshInventoryResource {
  kind: string;
  apiVersion: string;
  plural: string;
  count: number;
  items: ServiceMeshInventoryItem[];
  notice?: string;
}

export interface ServiceMeshInventory {
  clusterId: string;
  mesh: ServiceMeshKind;
  resources: ServiceMeshInventoryResource[];
  totalCount: number;
  notice?: string;
}

export interface ServiceMeshPolicyValidationFinding {
  field?: string;
  severity: "warning" | "error" | string;
  message: string;
}

export interface ServiceMeshPolicyValidation {
  clusterId: string;
  valid: boolean;
  apiVersion?: string;
  kind?: string;
  name?: string;
  namespace?: string;
  managedBy?: string;
  readOnly: boolean;
  applyAllowed: boolean;
  warnings: ServiceMeshPolicyValidationFinding[];
  errors: ServiceMeshPolicyValidationFinding[];
}

function mapServiceMeshDetection(
  raw: Record<string, unknown>,
): ServiceMeshDetection {
  return {
    clusterId: String(raw.cluster_id ?? ""),
    detectedMesh: String(raw.detected_mesh ?? "unknown") as ServiceMeshKind,
    detectedVersion: String(raw.detected_version ?? ""),
    controlPlaneNamespace: String(raw.control_plane_namespace ?? ""),
    gatewayCount: Number(raw.gateway_count ?? 0),
    virtualServiceCount: Number(raw.virtual_service_count ?? 0),
    destinationRuleCount: Number(raw.destination_rule_count ?? 0),
    peerAuthenticationCount: Number(raw.peer_authentication_count ?? 0),
    serviceProfileCount: Number(raw.service_profile_count ?? 0),
    serverAuthCount: Number(raw.server_auth_count ?? 0),
    mtlsCoveragePct: Number(raw.mtls_coverage_pct ?? 0),
    lastDetectedAt: raw.last_detected_at
      ? String(raw.last_detected_at)
      : undefined,
    lastError: raw.last_error ? String(raw.last_error) : undefined,
  };
}

function mapServiceMeshInventory(
  raw: OpenAPIComponents["schemas"]["ServiceMeshInventory"],
): ServiceMeshInventory {
  return {
    clusterId: raw.cluster_id ?? "",
    mesh: (raw.mesh ?? "unknown") as ServiceMeshKind,
    totalCount: raw.total_count ?? 0,
    notice: raw.notice,
    resources: (raw.resources ?? []).map((resource) => ({
      kind: resource.kind ?? "",
      apiVersion: resource.api_version ?? "",
      plural: resource.plural ?? "",
      count: resource.count ?? 0,
      notice: resource.notice,
      items: (resource.items ?? []).map((item) => ({
        name: item.name ?? "",
        namespace: item.namespace,
        managedBy: item.managed_by,
        readOnly: item.read_only ?? false,
        reason: item.reason,
      })),
    })),
  };
}

function mapValidationFinding(
  raw: Record<string, unknown>,
): ServiceMeshPolicyValidationFinding {
  return {
    field: raw.field ? String(raw.field) : undefined,
    severity: String(raw.severity ?? "warning"),
    message: String(raw.message ?? ""),
  };
}

export async function getServiceMeshDetection(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ServiceMeshDetection> {
  const wire = await generated.getClustersByClusterIdServiceMesh({
    path: { cluster_id: clusterId },
    signal,
  });
  return mapServiceMeshDetection(
    requireEnvelopeData(wire, "Service mesh detection"),
  );
}

export async function reDetectServiceMesh(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ServiceMeshDetection> {
  const wire = await generated.postClustersByClusterIdServiceMeshDetect({
    path: { cluster_id: clusterId },
    signal,
  });
  return mapServiceMeshDetection(
    requireEnvelopeData(wire, "Service mesh detection"),
  );
}

export async function getServiceMeshMTLS(
  clusterId: string,
  signal?: AbortSignal,
): Promise<MTLSBreakdown> {
  const wire = await generated.getClustersByClusterIdServiceMeshMtls({
    path: { cluster_id: clusterId },
    signal,
  });
  const raw = requireEnvelopeData(wire, "Service mesh mTLS") as Record<
    string,
    unknown
  >;
  return {
    clusterId: String(raw.cluster_id ?? ""),
    mesh: String(raw.mesh ?? "unknown") as ServiceMeshKind,
    mtlsCoveragePct: Number(raw.mtls_coverage_pct ?? 0),
    totalCount: Number(raw.total_count ?? 0),
    notice: raw.notice ? String(raw.notice) : undefined,
    rows: (Array.isArray(raw.rows) ? raw.rows : []).map((item) => {
      const row = item as Record<string, unknown>;
      return {
        namespace: String(row.namespace ?? ""),
        mode: String(row.mode ?? ""),
        rules: Number(row.rules ?? 0),
      };
    }),
  };
}

export async function getServiceMeshInventory(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ServiceMeshInventory> {
  const wire = await generated.getClustersByClusterIdServiceMeshInventory({
    path: { cluster_id: clusterId },
    signal,
  });
  return mapServiceMeshInventory(
    requireEnvelopeData(wire, "Service mesh inventory"),
  );
}

export async function validateServiceMeshPolicy(
  clusterId: string,
  body: { yaml?: string; object?: unknown },
  signal?: AbortSignal,
): Promise<ServiceMeshPolicyValidation> {
  const requestBody =
    body.yaml !== undefined
      ? { yaml: body.yaml }
      : { object: body.object as Record<string, unknown> | undefined };
  const wire = await generated.postClustersByClusterIdServiceMeshValidate({
    path: { cluster_id: clusterId },
    body: requestBody,
    signal,
  });
  const raw = requireEnvelopeData(wire, "Service mesh policy validation");
  return {
    clusterId: raw.cluster_id ?? "",
    valid: raw.valid ?? false,
    apiVersion: raw.api_version,
    kind: raw.kind,
    name: raw.name,
    namespace: raw.namespace,
    managedBy: raw.managed_by,
    readOnly: raw.read_only ?? false,
    applyAllowed: raw.apply_allowed ?? false,
    warnings: (raw.warnings ?? []).map(mapValidationFinding),
    errors: (raw.errors ?? []).map(mapValidationFinding),
  };
}
