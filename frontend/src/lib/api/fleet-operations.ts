/**
 * Fleet operations API client (DIR-01).
 *
 * Bulk fleet-operations let an operator drive an upgrade / install /
 * uninstall / template-apply / agent-token-rotation across a whole
 * cluster fleet from one dashboard. The backend is fully built,
 * RBAC-gated (`fleet_operations`), audited, idempotent and orchestrated
 * by a durable worker — this client is the frontend consumer.
 *
 * All endpoints sit under /api/v1/fleet-operations/. The list + targets
 * endpoints return the standard paginated envelope ({ data, count, next,
 * previous }); get/create/lifecycle return the wrapped { data } envelope.
 *
 * Wire shapes are snake_case here because they mirror the handler structs
 * (internal/handler/fleet_operations.go) verbatim — do NOT camelCase them.
 *
 * Re-exported from ../api.ts via `export * from './api/fleet-operations'`.
 */
import api from '@/lib/api';
import type { APIResponse } from '@/types';

// ─────────────────────────────────────────────────────────────────────
// Enums
// ─────────────────────────────────────────────────────────────────────

/** Operation types the orchestrator actually dispatches today. */
export type FleetOperationType =
  | 'tool_upgrade'
  | 'tool_install'
  | 'tool_uninstall'
  | 'apply_template'
  | 'rotate_agent_token';

/** Parent-operation lifecycle status. */
export type FleetOperationStatus =
  | 'pending'
  | 'running'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'aborted';

export type FleetStrategy = 'parallel' | 'sequential';
export type FleetOnError = 'abort' | 'continue';
export type FleetSelectorOperator = 'In' | 'NotIn' | 'Exists' | 'DoesNotExist';

// ─────────────────────────────────────────────────────────────────────
// Selector
// ─────────────────────────────────────────────────────────────────────

export interface FleetSelectorExpression {
  key: string;
  operator: FleetSelectorOperator;
  values?: string[];
}

/**
 * Kubernetes-style label selector. Mirrors internal/worker/tasks/
 * fleet_selector.go. An empty selector matches NO clusters — that's a
 * load-bearing safety property the UI must enforce client-side too.
 */
export interface FleetSelector {
  matchLabels?: Record<string, string>;
  matchExpressions?: FleetSelectorExpression[];
  matchGroupIDs?: string[];
}

// ─────────────────────────────────────────────────────────────────────
// Wire shapes
// ─────────────────────────────────────────────────────────────────────

export interface FleetOperation {
  id: string;
  name: string;
  description: string;
  operation_type: string;
  operation_spec: Record<string, unknown> | null;
  selector: FleetSelector | null;
  strategy: FleetStrategy;
  max_concurrent: number;
  on_error: FleetOnError;
  respect_maintenance_windows: boolean;
  status: FleetOperationStatus;
  total_clusters: number;
  completed_clusters: number;
  failed_clusters: number;
  skipped_clusters: number;
  started_at?: string;
  completed_at?: string;
  last_error?: string;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface FleetOperationTarget {
  id: string;
  operation_id: string;
  cluster_id: string;
  status: string;
  sub_operation_id?: string;
  sub_operation_type?: string;
  started_at?: string;
  completed_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateFleetOperationRequest {
  name: string;
  description?: string;
  operation_type: FleetOperationType;
  operation_spec?: Record<string, unknown>;
  selector: FleetSelector;
  strategy?: FleetStrategy;
  max_concurrent?: number;
  on_error?: FleetOnError;
  respect_maintenance_windows?: boolean;
}

/** Standard paginated envelope returned by RespondPaginated. */
export interface FleetPaginated<T> {
  data: T[];
  count: number;
  next: string | null;
  previous: string | null;
}

export interface FleetListParams {
  status?: FleetOperationStatus;
  limit?: number;
  offset?: number;
}

// ─────────────────────────────────────────────────────────────────────
// Client functions
// ─────────────────────────────────────────────────────────────────────

export async function getFleetOperations(
  params?: FleetListParams,
): Promise<FleetPaginated<FleetOperation>> {
  const res = await api.get<FleetPaginated<FleetOperation>>('/fleet-operations/', { params });
  return res.data;
}

export async function getFleetOperation(id: string): Promise<FleetOperation> {
  const res = await api.get<APIResponse<FleetOperation>>(`/fleet-operations/${id}/`);
  return res.data.data;
}

export async function createFleetOperation(
  body: CreateFleetOperationRequest,
): Promise<FleetOperation> {
  const res = await api.post<APIResponse<FleetOperation>>('/fleet-operations/', body);
  return res.data.data;
}

export async function getFleetTargets(
  id: string,
  params?: { limit?: number; offset?: number },
): Promise<FleetPaginated<FleetOperationTarget>> {
  const res = await api.get<FleetPaginated<FleetOperationTarget>>(
    `/fleet-operations/${id}/targets/`,
    { params },
  );
  return res.data;
}

export async function pauseFleetOperation(id: string): Promise<FleetOperation> {
  const res = await api.post<APIResponse<FleetOperation>>(`/fleet-operations/${id}/pause/`);
  return res.data.data;
}

export async function resumeFleetOperation(id: string): Promise<FleetOperation> {
  const res = await api.post<APIResponse<FleetOperation>>(`/fleet-operations/${id}/resume/`);
  return res.data.data;
}

export async function abortFleetOperation(id: string): Promise<FleetOperation> {
  const res = await api.post<APIResponse<FleetOperation>>(`/fleet-operations/${id}/abort/`);
  return res.data.data;
}

export async function retryFailedFleetOperation(id: string): Promise<FleetOperation> {
  const res = await api.post<APIResponse<FleetOperation>>(`/fleet-operations/${id}/retry-failed/`);
  return res.data.data;
}

// ─────────────────────────────────────────────────────────────────────
// Client-side selector predicate — mirrors matchesSelector in
// internal/worker/tasks/fleet_selector.go so the UI can preview a match
// count before create. This is a stopgap (there is no backend dry-run
// endpoint); keep it byte-for-byte faithful to the Go truth table.
// ─────────────────────────────────────────────────────────────────────

export interface SelectorCandidate {
  labels: Record<string, string>;
  groupIds?: string[];
}

/** True when the selector has no matchLabels / matchExpressions / matchGroupIDs. */
export function selectorIsEmpty(sel: FleetSelector | null | undefined): boolean {
  if (!sel) return true;
  return (
    Object.keys(sel.matchLabels ?? {}).length === 0 &&
    (sel.matchExpressions ?? []).length === 0 &&
    (sel.matchGroupIDs ?? []).length === 0
  );
}

function intersects(a: string[], b: string[]): boolean {
  return a.some((x) => b.includes(x));
}

function matchesExpression(
  expr: FleetSelectorExpression,
  labels: Record<string, string>,
): boolean {
  const present = Object.prototype.hasOwnProperty.call(labels, expr.key);
  const val = labels[expr.key];
  const values = expr.values ?? [];
  switch (expr.operator) {
    case 'In':
      if (!present) return false;
      return values.includes(val);
    case 'NotIn':
      // k8s semantics: NotIn matches when the key is absent.
      if (!present) return true;
      return !values.includes(val);
    case 'Exists':
      return present;
    case 'DoesNotExist':
      return !present;
    default:
      // Unknown operators match nothing (defensive — same as Go).
      return false;
  }
}

/** Per-cluster predicate. AND of every matchLabel, matchExpression, and the group branch. */
export function matchesFleetSelector(sel: FleetSelector, c: SelectorCandidate): boolean {
  for (const [k, v] of Object.entries(sel.matchLabels ?? {})) {
    if (c.labels[k] !== v) return false;
  }
  for (const expr of sel.matchExpressions ?? []) {
    if (!matchesExpression(expr, c.labels)) return false;
  }
  const groupIds = sel.matchGroupIDs ?? [];
  if (groupIds.length > 0 && !intersects(groupIds, c.groupIds ?? [])) return false;
  return true;
}

/** Returns the candidates the selector matches. An empty selector matches NONE. */
export function evaluateFleetSelector(
  sel: FleetSelector,
  candidates: SelectorCandidate[],
): SelectorCandidate[] {
  if (selectorIsEmpty(sel)) return [];
  return candidates.filter((c) => matchesFleetSelector(sel, c));
}

// ─────────────────────────────────────────────────────────────────────
// UI helpers
// ─────────────────────────────────────────────────────────────────────

/** Operation types offered in the create form (implemented only). */
export const FLEET_OPERATION_TYPES: { value: FleetOperationType; label: string; hint: string }[] = [
  { value: 'tool_upgrade', label: 'Upgrade tool', hint: 'Upgrade an installed cluster tool across the fleet' },
  { value: 'tool_install', label: 'Install tool', hint: 'Install a cluster tool across the fleet' },
  { value: 'tool_uninstall', label: 'Uninstall tool', hint: 'Remove a cluster tool across the fleet' },
  { value: 'apply_template', label: 'Apply template', hint: 'Apply an onboarding template across the fleet' },
  { value: 'rotate_agent_token', label: 'Rotate agent token', hint: 'Rotate the agent token on every matched cluster' },
];

/** Operation types the backend reserves but rejects (400) today. */
export const FLEET_OPERATION_TYPES_RESERVED: FleetOperationType[] | string[] = [
  'drain_namespaces',
  'custom_helm',
];

export function fleetOpTypeLabel(type: string): string {
  return FLEET_OPERATION_TYPES.find((t) => t.value === type)?.label ?? type;
}

/** Terminal statuses stop polling. */
export function isTerminalFleetStatus(status: string): boolean {
  return status === 'completed' || status === 'failed' || status === 'aborted';
}
