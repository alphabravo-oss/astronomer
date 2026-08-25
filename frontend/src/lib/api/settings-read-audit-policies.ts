import {
  adminReadAuditPoliciesList,
  adminReadAuditPolicyCreate,
  adminReadAuditPolicyDelete,
  adminReadAuditPolicyGet,
  adminReadAuditPolicyUpdate,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type ReadAuditPolicyWire =
  OpenAPIComponents["schemas"]["ReadAuditPolicyResponse"];

export type ReadAuditPolicyCreateBody =
  OpenAPIComponents["schemas"]["ReadAuditPolicyCreateRequest"];
export type ReadAuditPolicyUpdateBody =
  OpenAPIComponents["schemas"]["ReadAuditPolicyUpdateRequest"];

export interface ReadAuditPolicyView {
  id: string;
  name: string;
  description: string;
  path_pattern: string;
  verbs: string;
  sample_rate: number;
  enabled: boolean;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface ReadAuditPolicyRequestOptions {
  signal?: AbortSignal;
}

function requireData<T>(value: T | undefined, operation: string): T {
  if (value === undefined) {
    throw new Error(`${operation} returned no data`);
  }
  return value;
}

function readAuditPolicyFromWire(
  wire: ReadAuditPolicyWire,
): ReadAuditPolicyView {
  return {
    id: wire.id,
    name: wire.name,
    description: wire.description,
    path_pattern: wire.path_pattern,
    verbs: wire.verbs,
    sample_rate: wire.sample_rate,
    enabled: wire.enabled,
    ...(wire.created_by === undefined ? {} : { created_by: wire.created_by }),
    created_at: wire.created_at,
    updated_at: wire.updated_at,
  };
}

export async function listReadAuditPolicies(
  options: ReadAuditPolicyRequestOptions = {},
): Promise<ReadAuditPolicyView[]> {
  const response = await adminReadAuditPoliciesList({ signal: options.signal });
  return requireData(response.data, "Read-audit policy list").items.map(
    readAuditPolicyFromWire,
  );
}

export async function getReadAuditPolicy(
  id: string,
  options: ReadAuditPolicyRequestOptions = {},
): Promise<ReadAuditPolicyView> {
  const response = await adminReadAuditPolicyGet({
    path: { id },
    signal: options.signal,
  });
  return readAuditPolicyFromWire(
    requireData(response.data, "Read-audit policy detail"),
  );
}

export async function createReadAuditPolicy(
  body: ReadAuditPolicyCreateBody,
  options: ReadAuditPolicyRequestOptions = {},
): Promise<ReadAuditPolicyView> {
  const response = await adminReadAuditPolicyCreate({
    body,
    signal: options.signal,
  });
  return readAuditPolicyFromWire(
    requireData(response.data, "Read-audit policy create"),
  );
}

export async function updateReadAuditPolicy(
  id: string,
  body: ReadAuditPolicyUpdateBody,
  options: ReadAuditPolicyRequestOptions = {},
): Promise<ReadAuditPolicyView> {
  const response = await adminReadAuditPolicyUpdate({
    path: { id },
    body,
    signal: options.signal,
  });
  return readAuditPolicyFromWire(
    requireData(response.data, "Read-audit policy update"),
  );
}

export async function deleteReadAuditPolicy(
  id: string,
  options: ReadAuditPolicyRequestOptions = {},
): Promise<void> {
  await adminReadAuditPolicyDelete({
    path: { id },
    signal: options.signal,
  });
}
