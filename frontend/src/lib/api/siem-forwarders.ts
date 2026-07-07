/**
 * SIEM forwarder admin API client (F-05).
 *
 * Backend: /api/v1/admin/siem-forwarders/* (List/Create/Get/Update/Delete +
 * test + status). All endpoints are superuser-gated server-side.
 *
 * Conventions mirror the rest of `lib/api.ts`:
 *   - Reads come back camelCased by the shared axios response interceptor, so
 *     the `SIEMForwarder` / `SIEMForwarderStatus` types are camelCase even
 *     though the wire format is snake_case.
 *   - Writes send the snake_case keys the Go `siemForwarderRequest` declares.
 *   - List responds `{ data: { items, total } }`; single objects respond
 *     `{ data: {...} }`; test + status respond unwrapped (camelized).
 *
 * Re-exported from ../api.ts via `export * from './api/siem-forwarders'`.
 */
import api from '@/lib/api';
import type { SIEMForwarder, SIEMForwarderStatus } from '@/types';

// The GET path returns this sentinel instead of the auth ciphertext; echoing
// it back on PUT means "keep the existing auth blob unchanged".
export const SIEM_AUTH_SENTINEL = '<encrypted>';

// Write payload — snake_case to match the Go handler's json tags. Every field
// is optional so PUT can do partial updates; Create validates required fields.
export interface SIEMForwarderWriteRequest {
  name?: string;
  transport?: string;
  endpoint?: string;
  auth?: string;
  event_filters?: string[];
  format?: string;
  tls_skip_verify?: boolean;
  ca_cert_pem?: string;
  batch_size?: number;
  flush_interval_ms?: number;
  timeout_seconds?: number;
  enabled?: boolean;
}

export async function listSIEMForwarders(): Promise<SIEMForwarder[]> {
  const res = await api.get<{ data?: { items?: SIEMForwarder[] } }>('/admin/siem-forwarders/');
  return res.data.data?.items ?? [];
}

export async function getSIEMForwarder(id: string): Promise<SIEMForwarder> {
  const res = await api.get<{ data: SIEMForwarder }>(`/admin/siem-forwarders/${id}/`);
  return res.data.data;
}

export async function createSIEMForwarder(
  body: SIEMForwarderWriteRequest,
): Promise<SIEMForwarder> {
  const res = await api.post<{ data: SIEMForwarder }>('/admin/siem-forwarders/', body);
  return res.data.data;
}

export async function updateSIEMForwarder(
  id: string,
  body: SIEMForwarderWriteRequest,
): Promise<SIEMForwarder> {
  const res = await api.put<{ data: SIEMForwarder }>(`/admin/siem-forwarders/${id}/`, body);
  return res.data.data;
}

export async function deleteSIEMForwarder(id: string): Promise<void> {
  await api.delete(`/admin/siem-forwarders/${id}/`);
}

export interface SIEMTestResult {
  queueId: string;
  forwarderId: string;
  queuedAt: string;
  message: string;
}

export async function testSIEMForwarder(id: string): Promise<SIEMTestResult> {
  // Unwrapped body (RespondJSONUnwrapped); interceptor camelizes keys.
  const res = await api.post<SIEMTestResult>(`/admin/siem-forwarders/${id}/test/`);
  return res.data;
}

export async function getSIEMForwarderStatus(id: string): Promise<SIEMForwarderStatus> {
  const res = await api.get<SIEMForwarderStatus>(`/admin/siem-forwarders/${id}/status/`);
  return res.data;
}
