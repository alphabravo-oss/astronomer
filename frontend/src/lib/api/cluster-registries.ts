/** Private image-registry credentials scoped to one adopted cluster. */

import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";

// ============================================================
// Registry credentials
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
  statusCode?: number;
  message?: string;
  /** Retained for view compatibility; this endpoint currently reports statusCode instead. */
  latencyMs?: number;
}

function mapClusterRegistry(wire: Record<string, unknown>): ClusterRegistry {
  return {
    id: String(wire.id ?? ""),
    registryUrl: String(wire.private_registry_url ?? ""),
    username: String(wire.registry_username ?? ""),
    namespaces: Array.isArray(wire.namespaces)
      ? (wire.namespaces as string[])
      : [],
    secretName: String(wire.secret_name ?? ""),
    injectDefaultSa: Boolean(wire.inject_default_sa),
    lastAppliedAt: wire.last_applied_at
      ? String(wire.last_applied_at)
      : undefined,
    lastApplyError: wire.last_apply_error
      ? String(wire.last_apply_error)
      : undefined,
    createdAt: String(wire.created_at ?? ""),
    updatedAt: String(wire.updated_at ?? ""),
  };
}

export async function listClusterRegistries(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ClusterRegistry[]> {
  const wire = await generated.getClustersByClusterIdRegistries({
    path: { cluster_id: clusterId },
    signal,
  });
  return (wire.data?.items ?? []).map(mapClusterRegistry);
}

export async function createClusterRegistry(
  clusterId: string,
  body: CreateRegistryRequest,
  signal?: AbortSignal,
): Promise<ClusterRegistry> {
  const wire = await generated.postClustersByClusterIdRegistries({
    path: { cluster_id: clusterId },
    body: {
      private_registry_url: body.registry_url,
      registry_username: body.username,
      registry_password: body.password,
      namespaces: body.namespaces,
      secret_name: body.secret_name,
      inject_default_sa: body.inject_default_sa,
    },
    signal,
  });
  return mapClusterRegistry(requireEnvelopeData(wire, "Registry create"));
}

export async function updateClusterRegistry(
  clusterId: string,
  registryId: string,
  body: UpdateRegistryRequest,
  signal?: AbortSignal,
): Promise<ClusterRegistry> {
  const wire = await generated.putClustersByClusterIdRegistriesById({
    path: { cluster_id: clusterId, id: registryId },
    body: {
      private_registry_url: body.registry_url,
      registry_username: body.username,
      registry_password: body.password,
      namespaces: body.namespaces,
      secret_name: body.secret_name,
      inject_default_sa: body.inject_default_sa,
    },
    signal,
  });
  return mapClusterRegistry(requireEnvelopeData(wire, "Registry update"));
}

export async function deleteClusterRegistry(
  clusterId: string,
  registryId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdRegistriesById({
    path: { cluster_id: clusterId, id: registryId },
    signal,
  });
}

export async function testClusterRegistry(
  clusterId: string,
  registryId: string,
  signal?: AbortSignal,
): Promise<RegistryTestResult> {
  const wire = await generated.postClustersByClusterIdRegistriesByIdTest({
    path: { cluster_id: clusterId, id: registryId },
    signal,
  });
  const data = requireEnvelopeData(wire, "Registry test");
  return {
    ok: Boolean(data.ok),
    statusCode: data.status_code == null ? undefined : Number(data.status_code),
    message: data.message == null ? undefined : String(data.message),
  };
}
