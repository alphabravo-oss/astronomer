/**
 * Vault connections API client (migration 067).
 *
 * The admin endpoints are superuser-gated server-side; the UI hides
 * the settings link behind `useIsSuperuser()` so non-admins never see
 * the page. Project-default endpoints follow the standard project
 * RBAC rules (projects:read / projects:update).
 *
 * Convention:
 *   - Reads retain exact generated wire casing and are mapped locally.
 *   - Writes use generated request keys matching the Go handler's json tags.
 *   - Auth blobs are typed per method; on GET secret fields arrive as
 *     the sentinel "<encrypted>" — a PUT echoing that sentinel
 *     preserves the stored value.
 */
import {
  adminVaultConnectionDelete,
  adminVaultConnectionGet,
  adminVaultConnectionHealth,
  adminVaultConnectionsCreate,
  adminVaultConnectionsList,
  adminVaultConnectionTest,
  adminVaultConnectionUpdate,
  getProjectsByIdDefaultVaultConnection,
  putProjectsByIdDefaultVaultConnection,
} from "@/lib/api/generated/client";
import type {
  VaultConnection as VaultConnectionWire,
  VaultConnectionRequest,
  VaultTestResult as VaultTestResultWire,
} from "@/types/openapi.generated";

export type VaultAuthMethod = "token" | "approle" | "kubernetes";

/** Sentinel value the server emits in place of redacted auth fields. */
export const VAULT_AUTH_SENTINEL = "<encrypted>";

export interface VaultConnectionView {
  id: string;
  name: string;
  description: string;
  addr: string;
  authMethod: VaultAuthMethod;
  auth: Record<string, string>;
  namespace: string;
  tlsSkipVerify: boolean;
  caCertPem: string;
  defaultMount: string;
  enabled: boolean;
  lastHealthAt?: string;
  lastHealthOk: boolean;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export type VaultConnectionWriteRequest = Omit<
  VaultConnectionRequest,
  "auth"
> & {
  auth: Record<string, string>;
};

export interface VaultTestResultView {
  ok: boolean;
  reachable: boolean;
  authOk: boolean;
  latencyMs: number;
  message: string;
  probePath?: string;
}

export interface VaultHealthResult {
  ok: boolean;
  latencyMs: number;
  message: string;
}

export interface VaultRequestOptions {
  signal?: AbortSignal;
}

function requireVaultConnection(
  wire: VaultConnectionWire | undefined,
): VaultConnectionView {
  if (
    !wire?.id ||
    !wire.name ||
    !wire.addr ||
    !wire.auth_method ||
    !wire.created_at ||
    !wire.updated_at
  ) {
    throw new Error("Vault connection response is incomplete");
  }
  return {
    id: wire.id,
    name: wire.name,
    description: wire.description ?? "",
    addr: wire.addr,
    authMethod: wire.auth_method,
    auth: wire.auth ?? {},
    namespace: wire.namespace ?? "",
    tlsSkipVerify: wire.tls_skip_verify ?? false,
    caCertPem: wire.ca_cert_pem ?? "",
    defaultMount: wire.default_mount ?? "",
    enabled: wire.enabled ?? false,
    lastHealthAt: wire.last_health_at,
    lastHealthOk: wire.last_health_ok ?? false,
    lastError: wire.last_error,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function mapVaultTestResult(wire: VaultTestResultWire): VaultTestResultView {
  return {
    ok: wire.ok ?? false,
    reachable: wire.reachable ?? false,
    authOk: wire.auth_ok ?? false,
    latencyMs: wire.latency_ms ?? 0,
    message: wire.message ?? "",
    probePath: wire.probe_path,
  };
}

export async function listVaultConnections(
  options: VaultRequestOptions = {},
): Promise<VaultConnectionView[]> {
  const response = await adminVaultConnectionsList({ signal: options.signal });
  return (response.data?.items ?? []).map(requireVaultConnection);
}

export async function getVaultConnection(
  id: string,
  options: VaultRequestOptions = {},
): Promise<VaultConnectionView> {
  const response = await adminVaultConnectionGet({
    path: { id },
    signal: options.signal,
  });
  return requireVaultConnection(response.data);
}

export async function createVaultConnection(
  body: VaultConnectionWriteRequest,
  options: VaultRequestOptions = {},
): Promise<VaultConnectionView> {
  const response = await adminVaultConnectionsCreate({
    body,
    signal: options.signal,
  });
  return requireVaultConnection(response.data);
}

export async function updateVaultConnection(
  id: string,
  body: VaultConnectionWriteRequest,
  options: VaultRequestOptions = {},
): Promise<VaultConnectionView> {
  const response = await adminVaultConnectionUpdate({
    path: { id },
    body,
    signal: options.signal,
  });
  return requireVaultConnection(response.data);
}

export async function deleteVaultConnection(
  id: string,
  options: VaultRequestOptions = {},
): Promise<void> {
  await adminVaultConnectionDelete({ path: { id }, signal: options.signal });
}

export async function testVaultConnection(
  id: string,
  probePath?: string,
  options: VaultRequestOptions = {},
): Promise<VaultTestResultView> {
  const response = await adminVaultConnectionTest({
    path: { id },
    body: { probe_path: probePath ?? "" },
    signal: options.signal,
  });
  return mapVaultTestResult(response.data ?? {});
}

export async function healthCheckVaultConnection(
  id: string,
  options: VaultRequestOptions = {},
): Promise<VaultHealthResult> {
  const response = await adminVaultConnectionHealth({
    path: { id },
    signal: options.signal,
  });
  return {
    ok: response.data?.ok ?? false,
    latencyMs: response.data?.latency_ms ?? 0,
    message: response.data?.message ?? "",
  };
}

/** Project default ----------------------------------------------------- */

export interface ProjectDefaultVaultConnection {
  connectionId: string | null;
  connection: VaultConnectionView | null;
}

export async function getProjectDefaultVault(
  projectId: string,
  options: VaultRequestOptions = {},
): Promise<ProjectDefaultVaultConnection> {
  const response = await getProjectsByIdDefaultVaultConnection({
    path: { id: projectId },
    signal: options.signal,
  });
  return {
    connectionId: response.data?.connection_id ?? null,
    connection: response.data?.connection
      ? requireVaultConnection(response.data.connection)
      : null,
  };
}

export async function setProjectDefaultVault(
  projectId: string,
  connectionId: string | null,
  options: VaultRequestOptions = {},
): Promise<{ connectionId: string | null }> {
  const response = await putProjectsByIdDefaultVaultConnection({
    path: { id: projectId },
    body: { connection_id: connectionId },
    signal: options.signal,
  });
  return { connectionId: response.data?.connection_id ?? null };
}
