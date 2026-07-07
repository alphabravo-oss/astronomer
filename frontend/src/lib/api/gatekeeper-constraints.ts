/**
 * Gatekeeper / OPA constraint authoring API client (P-04).
 *
 * Backend (per-cluster, applies through the agent tunnel; create/delete are
 * RBAC-gated + audited server-side):
 *   GET    /api/v1/clusters/{id}/gatekeeper/constraints/          -> list bundle + custom
 *   POST   /api/v1/clusters/{id}/gatekeeper/constraints/validate/ -> validate YAML only
 *   POST   /api/v1/clusters/{id}/gatekeeper/constraints/          -> validate + apply + persist
 *   DELETE /api/v1/clusters/{id}/gatekeeper/constraints/{name}/   -> remove authored constraint
 *
 * Validate/create body: { yaml }. Validate/create response:
 * { valid, errors, applied, name, kind }.
 *
 * Re-exported from ../api.ts via `export * from './api/gatekeeper-constraints'`.
 */
import api from '@/lib/api';
import type { GatekeeperConstraint, ConstraintValidateResult } from '@/types';

// Defensive unwrap: the validate/apply endpoints may respond either
// `{ data: {...} }` (RespondJSON) or the bare object (RespondJSONUnwrapped).
function unwrap<T>(payload: T | { data: T }): T {
  if (payload && typeof payload === 'object' && 'data' in (payload as { data?: T })) {
    return (payload as { data: T }).data;
  }
  return payload as T;
}

export async function listGatekeeperConstraints(
  clusterId: string,
): Promise<GatekeeperConstraint[]> {
  const res = await api.get<{ data?: { items?: GatekeeperConstraint[] } }>(
    `/clusters/${clusterId}/gatekeeper/constraints/`,
  );
  return res.data.data?.items ?? [];
}

export async function validateGatekeeperConstraint(
  clusterId: string,
  yaml: string,
): Promise<ConstraintValidateResult> {
  const res = await api.post<ConstraintValidateResult | { data: ConstraintValidateResult }>(
    `/clusters/${clusterId}/gatekeeper/constraints/validate/`,
    { yaml },
  );
  return unwrap(res.data);
}

export async function applyGatekeeperConstraint(
  clusterId: string,
  yaml: string,
): Promise<ConstraintValidateResult> {
  const res = await api.post<ConstraintValidateResult | { data: ConstraintValidateResult }>(
    `/clusters/${clusterId}/gatekeeper/constraints/`,
    { yaml },
  );
  return unwrap(res.data);
}

export async function deleteGatekeeperConstraint(
  clusterId: string,
  name: string,
): Promise<void> {
  await api.delete(`/clusters/${clusterId}/gatekeeper/constraints/${encodeURIComponent(name)}/`);
}
