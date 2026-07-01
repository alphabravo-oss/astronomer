/**
 * Native per-CRD RBAC rules API client — pairs with the backend handler mounted
 * under `/api/v1/native-rbac-rules/…`.
 *
 * Native rules are an ADDITIVE allow layer: a rule GRANTS access on an exact
 * (apiGroup, resource, verb) tuple even when the coarse `custom_resources`
 * permission wouldn't, letting operators scope access per-CRD (e.g. "read
 * cert-manager Certificates but not other CRDs"). They can never widen
 * privilege-escalation api groups (rbac.authorization.k8s.io,
 * admissionregistration.k8s.io, apiregistration.k8s.io, apiextensions.k8s.io)
 * and can never grant `exec`/`logs` — the backend rejects those with a 400.
 *
 * The feature is gated server-side behind `native_rbac_enabled`; when off the
 * API 404s. Callers should degrade gracefully (DataTable `isError` state)
 * rather than crash.
 *
 * Response bodies are camelized by the shared axios interceptor in ../api.ts,
 * so every field below is camelCase.
 */

import api from '../api';
import type { APIResponse } from '@/types';

// ============================================================
// Types
// ============================================================

/**
 * Verb vocabulary allowed on a native rule. `*` grants all listed verbs. The
 * backend rejects `exec` and `logs`, so they are deliberately absent here.
 */
export type NativeRuleVerb =
  | 'read'
  | 'list'
  | 'watch'
  | 'create'
  | 'update'
  | 'delete'
  | '*';

export interface NativeRule {
  id: string;
  userId: string;
  /** Omitted / empty grants across all clusters. */
  clusterId?: string;
  /** Empty grants across all namespaces. */
  namespace: string;
  /** Empty targets the core API group. */
  apiGroup: string;
  /** Plural resource name (e.g. `certificates`) or `*` for all resources. */
  resource: string;
  verbs: string[];
  createdAt: string;
  createdBy?: string;
}

export interface CreateNativeRuleRequest {
  /** Subject the rule grants access to (required). */
  userId: string;
  /** Omit to grant across all clusters. */
  clusterId?: string;
  /** Omit / empty to grant across all namespaces. */
  namespace?: string;
  /** Empty targets the core API group. */
  apiGroup?: string;
  /** Plural resource name or `*` (required). */
  resource: string;
  verbs: string[];
}

// ============================================================
// Endpoints
// ============================================================

export async function listNativeRules(userId?: string): Promise<NativeRule[]> {
  const res = await api.get<APIResponse<NativeRule[]>>('/native-rbac-rules/', {
    params: userId ? { userId } : undefined,
  });
  return res.data.data ?? [];
}

export async function createNativeRule(
  body: CreateNativeRuleRequest,
): Promise<NativeRule> {
  const res = await api.post<APIResponse<NativeRule>>('/native-rbac-rules/', body);
  return res.data.data;
}

export async function deleteNativeRule(id: string): Promise<void> {
  await api.delete(`/native-rbac-rules/${id}/`);
}
