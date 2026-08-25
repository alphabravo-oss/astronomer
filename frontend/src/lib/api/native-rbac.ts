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
 * This handler already emits camelCase fields; the generated client preserves
 * that wire contract without transport-level key rewriting.
 */

import {
  deleteNativeRbacRulesById,
  getNativeRbacRules,
  postNativeRbacRules,
} from "@/lib/api/generated/client";

// ============================================================
// Types
// ============================================================

/**
 * Verb vocabulary allowed on a native rule. `*` grants all listed verbs. The
 * backend rejects `exec` and `logs`, so they are deliberately absent here.
 */
export type NativeRuleVerb =
  "read" | "list" | "watch" | "create" | "update" | "delete" | "*";

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
  verbs: NativeRuleVerb[];
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
  verbs: NativeRuleVerb[];
}

// ============================================================
// Endpoints
// ============================================================

export async function listNativeRules(
  userId?: string,
  signal?: AbortSignal,
): Promise<NativeRule[]> {
  const response = await getNativeRbacRules({
    query: userId ? { userId } : undefined,
    signal,
  });
  return response.data ?? [];
}

export async function createNativeRule(
  body: CreateNativeRuleRequest,
  signal?: AbortSignal,
): Promise<NativeRule> {
  const response = await postNativeRbacRules({ body, signal });
  return response.data;
}

export async function deleteNativeRule(id: string, signal?: AbortSignal): Promise<void> {
  await deleteNativeRbacRulesById({ path: { id }, signal });
}
