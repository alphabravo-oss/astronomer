/**
 * SCIM provisioning-token admin API client (F-05).
 *
 * Backend: /api/v1/admin/scim-tokens/* (mint / list / revoke). Superuser-gated
 * server-side. The plaintext token is returned ONLY in the create response
 * under `token`; list rows carry metadata only.
 *
 * Re-exported from ../api.ts via `export * from './api/scim-tokens'`.
 */
import api from '@/lib/api';
import type { SCIMToken, SCIMTokenCreated } from '@/types';

export async function listSCIMTokens(): Promise<SCIMToken[]> {
  const res = await api.get<{ data?: { tokens?: SCIMToken[] } }>('/admin/scim-tokens/');
  return res.data.data?.tokens ?? [];
}

export async function createSCIMToken(name: string): Promise<SCIMTokenCreated> {
  const res = await api.post<{ data: SCIMTokenCreated }>('/admin/scim-tokens/', { name });
  return res.data.data;
}

export async function deleteSCIMToken(id: string): Promise<void> {
  await api.delete(`/admin/scim-tokens/${id}/`);
}
