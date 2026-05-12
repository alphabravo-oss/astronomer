/**
 * Vault connections API client (migration 067).
 *
 * The admin endpoints are superuser-gated server-side; the UI hides
 * the settings link behind `useIsSuperuser()` so non-admins never see
 * the page. Project-default endpoints follow the standard project
 * RBAC rules (projects:read / projects:update).
 *
 * Convention:
 *   - Reads come back camelCased (axios interceptor).
 *   - Writes send snake_case keys matching the Go handler's json tags.
 *   - Auth blobs are typed per method; on GET secret fields arrive as
 *     the sentinel "<encrypted>" — a PUT echoing that sentinel
 *     preserves the stored value.
 */
import api from '@/lib/api';
import type { APIResponse } from '@/types';

export type VaultAuthMethod = 'token' | 'approle' | 'kubernetes';

/** Sentinel value the server emits in place of redacted auth fields. */
export const VAULT_AUTH_SENTINEL = '<encrypted>';

export interface VaultConnection {
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

export interface VaultConnectionWriteRequest {
  name?: string;
  description?: string;
  addr: string;
  auth_method: VaultAuthMethod;
  auth: Record<string, string>;
  namespace?: string;
  tls_skip_verify?: boolean;
  ca_cert_pem?: string;
  default_mount?: string;
  enabled?: boolean;
}

export interface VaultTestResult {
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

export async function listVaultConnections(): Promise<VaultConnection[]> {
  const res = await api.get<APIResponse<{ items: VaultConnection[] }>>('/admin/vault-connections/');
  const wrapped = res.data.data ?? (res.data as unknown as { items: VaultConnection[] });
  return wrapped.items ?? [];
}

export async function getVaultConnection(id: string): Promise<VaultConnection> {
  const res = await api.get<APIResponse<VaultConnection>>(`/admin/vault-connections/${id}/`);
  return res.data.data ?? (res.data as unknown as VaultConnection);
}

export async function createVaultConnection(body: VaultConnectionWriteRequest): Promise<VaultConnection> {
  const res = await api.post<APIResponse<VaultConnection>>('/admin/vault-connections/', body);
  return res.data.data ?? (res.data as unknown as VaultConnection);
}

export async function updateVaultConnection(
  id: string,
  body: Partial<VaultConnectionWriteRequest>,
): Promise<VaultConnection> {
  const res = await api.put<APIResponse<VaultConnection>>(`/admin/vault-connections/${id}/`, body);
  return res.data.data ?? (res.data as unknown as VaultConnection);
}

export async function deleteVaultConnection(id: string): Promise<void> {
  await api.delete(`/admin/vault-connections/${id}/`);
}

export async function testVaultConnection(id: string, probePath?: string): Promise<VaultTestResult> {
  const res = await api.post<APIResponse<VaultTestResult>>(
    `/admin/vault-connections/${id}/test/`,
    { probe_path: probePath ?? '' },
  );
  return res.data.data ?? (res.data as unknown as VaultTestResult);
}

export async function healthCheckVaultConnection(id: string): Promise<VaultHealthResult> {
  const res = await api.post<APIResponse<VaultHealthResult>>(`/admin/vault-connections/${id}/health/`);
  return res.data.data ?? (res.data as unknown as VaultHealthResult);
}

/** Project default ----------------------------------------------------- */

export interface ProjectDefaultVaultConnection {
  connectionId: string | null;
  connection: VaultConnection | null;
}

export async function getProjectDefaultVault(projectId: string): Promise<ProjectDefaultVaultConnection> {
  const res = await api.get<APIResponse<ProjectDefaultVaultConnection>>(
    `/projects/${projectId}/default-vault-connection/`,
  );
  return res.data.data ?? (res.data as unknown as ProjectDefaultVaultConnection);
}

export async function setProjectDefaultVault(
  projectId: string,
  connectionId: string | null,
): Promise<{ connectionId: string | null }> {
  const res = await api.put<APIResponse<{ connectionId: string | null }>>(
    `/projects/${projectId}/default-vault-connection/`,
    { connection_id: connectionId },
  );
  return res.data.data ?? (res.data as unknown as { connectionId: string | null });
}
