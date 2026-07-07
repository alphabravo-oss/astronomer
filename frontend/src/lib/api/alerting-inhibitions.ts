/**
 * Alertmanager-style inhibition rules API client (P-03).
 *
 * Backend (mirrors the control_plane_silences model):
 *   GET    /api/v1/admin/alerting/inhibitions/         -> list
 *   POST   /api/v1/admin/alerting/inhibitions/         (admin) -> create
 *   GET    /api/v1/admin/alerting/inhibitions/{id}/    -> get
 *   PUT    /api/v1/admin/alerting/inhibitions/{id}/    (admin) -> update
 *   DELETE /api/v1/admin/alerting/inhibitions/{id}/    (admin) -> delete
 *
 * Wire JSON: { id, name, source_matchers:[{label,value,is_regex}],
 * target_matchers:[{label,value,is_regex}], equal_labels:[string], enabled,
 * created_at, updated_at }. The shared axios interceptor camelizes reads
 * (`source_matchers` -> `sourceMatchers`, `is_regex` -> `isRegex`); writes
 * re-serialize the snake_case keys the handler expects.
 *
 * Re-exported from ../api.ts via `export * from './api/alerting-inhibitions'`.
 */
import api from '@/lib/api';
import type { AlertInhibition, InhibitionMatcher } from '@/types';

// Write payload — snake_case matchers to match the Go handler json tags.
export interface InhibitionWriteRequest {
  name: string;
  source_matchers: Array<{ label: string; value: string; is_regex: boolean }>;
  target_matchers: Array<{ label: string; value: string; is_regex: boolean }>;
  equal_labels: string[];
  enabled: boolean;
}

function toWireMatchers(
  matchers: InhibitionMatcher[],
): Array<{ label: string; value: string; is_regex: boolean }> {
  return matchers.map((m) => ({ label: m.label, value: m.value, is_regex: m.isRegex }));
}

export interface InhibitionFormValues {
  name: string;
  sourceMatchers: InhibitionMatcher[];
  targetMatchers: InhibitionMatcher[];
  equalLabels: string[];
  enabled: boolean;
}

export function toInhibitionWriteRequest(values: InhibitionFormValues): InhibitionWriteRequest {
  return {
    name: values.name,
    source_matchers: toWireMatchers(values.sourceMatchers),
    target_matchers: toWireMatchers(values.targetMatchers),
    equal_labels: values.equalLabels,
    enabled: values.enabled,
  };
}

export async function listInhibitions(): Promise<AlertInhibition[]> {
  const res = await api.get<{ data?: AlertInhibition[] }>('/admin/alerting/inhibitions/');
  return res.data.data ?? [];
}

export async function getInhibition(id: string): Promise<AlertInhibition> {
  const res = await api.get<{ data: AlertInhibition }>(`/admin/alerting/inhibitions/${id}/`);
  return res.data.data;
}

export async function createInhibition(body: InhibitionWriteRequest): Promise<AlertInhibition> {
  const res = await api.post<{ data: AlertInhibition }>('/admin/alerting/inhibitions/', body);
  return res.data.data;
}

export async function updateInhibition(
  id: string,
  body: InhibitionWriteRequest,
): Promise<AlertInhibition> {
  const res = await api.put<{ data: AlertInhibition }>(`/admin/alerting/inhibitions/${id}/`, body);
  return res.data.data;
}

export async function deleteInhibition(id: string): Promise<void> {
  await api.delete(`/admin/alerting/inhibitions/${id}/`);
}
