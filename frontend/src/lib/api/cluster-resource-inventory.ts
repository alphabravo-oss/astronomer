/** Mirrored cluster network, quota, and ingress resource inventory. */

import * as generated from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";

// ============================================================
// CRD-mirror read-only views
// ============================================================
//
// Backed by the mirrored_* tables; the per-cluster agent streams
// observe events into Postgres so these reads never round-trip
// through kubectl. The is_default / is_managed / accepted_status
// fields are pre-resolved server-side so the UI doesn't have to
// re-parse annotations or condition arrays per render.

export type MirroredIngressClass = Omit<
  OpenAPIComponents["schemas"]["MirroredIngressClass"],
  "is_default" | "last_seen_at" | "created_at" | "updated_at"
> & {
  isDefault: boolean;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredGatewayClass = Omit<
  OpenAPIComponents["schemas"]["MirroredGatewayClass"],
  | "controller_name"
  | "accepted_status"
  | "last_seen_at"
  | "created_at"
  | "updated_at"
> & {
  controllerName: string;
  // "True" | "False" | "Unknown" | "" (when the Accepted condition is unset).
  acceptedStatus: string;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredNetworkPolicy = Omit<
  OpenAPIComponents["schemas"]["MirroredNetworkPolicy"],
  | "pod_selector"
  | "policy_types"
  | "ingress_rules"
  | "egress_rules"
  | "is_managed"
  | "last_seen_at"
  | "created_at"
  | "updated_at"
> & {
  podSelector: unknown;
  policyTypes: string[];
  ingressRules: unknown[];
  egressRules: unknown[];
  // True when app.kubernetes.io/managed-by=astronomer on the policy's
  // labels at ingest time. The UI surfaces this as a "managed by
  // astronomer" badge so operators can tell at a glance which
  // policies are owned by sprint-068's NetworkPolicy writer.
  isManaged: boolean;
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredResourceQuota = Omit<
  OpenAPIComponents["schemas"]["MirroredResourceQuota"],
  "hard" | "used" | "scopes" | "last_seen_at" | "created_at" | "updated_at"
> & {
  // Free-form maps so future-proofed for whatever quota keys
  // upstream Kubernetes carries. Typed as `unknown` so the dashboard
  // can render any shape (`cpu`, `requests.memory`,
  // `count/configmaps`, …) without a per-key DTO bump.
  hard: Record<string, string> | null;
  used: Record<string, string> | null;
  scopes: string[];
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

export type MirroredLimitRange = Omit<
  OpenAPIComponents["schemas"]["MirroredLimitRange"],
  "limits" | "last_seen_at" | "created_at" | "updated_at"
> & {
  limits: unknown[];
  lastSeenAt: string;
  createdAt: string;
  updatedAt: string;
};

type IngressClassWire = OpenAPIComponents["schemas"]["MirroredIngressClass"];
type GatewayClassWire = OpenAPIComponents["schemas"]["MirroredGatewayClass"];
type NetworkPolicyWire = OpenAPIComponents["schemas"]["MirroredNetworkPolicy"];
type ResourceQuotaWire = OpenAPIComponents["schemas"]["MirroredResourceQuota"];
type LimitRangeWire = OpenAPIComponents["schemas"]["MirroredLimitRange"];

const mapIngressClass = (wire: IngressClassWire): MirroredIngressClass => ({
  name: wire.name,
  controller: wire.controller,
  parameters: wire.parameters,
  labels: wire.labels,
  annotations: wire.annotations,
  isDefault: wire.is_default,
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapGatewayClass = (wire: GatewayClassWire): MirroredGatewayClass => ({
  name: wire.name,
  description: wire.description,
  parameters: wire.parameters,
  labels: wire.labels,
  annotations: wire.annotations,
  controllerName: wire.controller_name,
  acceptedStatus: wire.accepted_status,
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapNetworkPolicy = (wire: NetworkPolicyWire): MirroredNetworkPolicy => ({
  namespace: wire.namespace,
  name: wire.name,
  labels: wire.labels,
  annotations: wire.annotations,
  podSelector: wire.pod_selector,
  policyTypes: Array.isArray(wire.policy_types)
    ? (wire.policy_types as string[])
    : [],
  ingressRules: Array.isArray(wire.ingress_rules) ? wire.ingress_rules : [],
  egressRules: Array.isArray(wire.egress_rules) ? wire.egress_rules : [],
  isManaged: wire.is_managed,
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapResourceQuota = (wire: ResourceQuotaWire): MirroredResourceQuota => ({
  namespace: wire.namespace,
  name: wire.name,
  labels: wire.labels,
  annotations: wire.annotations,
  hard:
    wire.hard && typeof wire.hard === "object"
      ? (wire.hard as Record<string, string>)
      : null,
  used:
    wire.used && typeof wire.used === "object"
      ? (wire.used as Record<string, string>)
      : null,
  scopes: Array.isArray(wire.scopes) ? (wire.scopes as string[]) : [],
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

const mapLimitRange = (wire: LimitRangeWire): MirroredLimitRange => ({
  namespace: wire.namespace,
  name: wire.name,
  labels: wire.labels,
  annotations: wire.annotations,
  limits: Array.isArray(wire.limits) ? wire.limits : [],
  lastSeenAt: wire.last_seen_at,
  createdAt: wire.created_at,
  updatedAt: wire.updated_at,
});

export async function listMirroredIngressClasses(
  clusterId: string,
  signal?: AbortSignal,
): Promise<MirroredIngressClass[]> {
  const wire = await generated.getClustersByClusterIdIngressClasses({
    path: { cluster_id: clusterId },
    signal,
  });
  return wire.data.map(mapIngressClass);
}

export async function listMirroredGatewayClasses(
  clusterId: string,
  signal?: AbortSignal,
): Promise<MirroredGatewayClass[]> {
  const wire = await generated.getClustersByClusterIdGatewayClasses({
    path: { cluster_id: clusterId },
    signal,
  });
  return wire.data.map(mapGatewayClass);
}

export async function listMirroredNetworkPolicies(
  clusterId: string,
  namespace?: string,
  signal?: AbortSignal,
): Promise<MirroredNetworkPolicy[]> {
  const wire = await generated.getClustersByClusterIdNetworkPolicies({
    path: { cluster_id: clusterId },
    query: { namespace },
    signal,
  });
  return wire.data.map(mapNetworkPolicy);
}

export async function listMirroredResourceQuotas(
  clusterId: string,
  namespace?: string,
  signal?: AbortSignal,
): Promise<MirroredResourceQuota[]> {
  const wire = await generated.getClustersByClusterIdResourceQuotas({
    path: { cluster_id: clusterId },
    query: { namespace },
    signal,
  });
  return wire.data.map(mapResourceQuota);
}

export async function listMirroredLimitRanges(
  clusterId: string,
  namespace?: string,
  signal?: AbortSignal,
): Promise<MirroredLimitRange[]> {
  const wire = await generated.getClustersByClusterIdLimitRanges({
    path: { cluster_id: clusterId },
    query: { namespace },
    signal,
  });
  return wire.data.map(mapLimitRange);
}
