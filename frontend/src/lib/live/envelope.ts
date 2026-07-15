/**
 * Live-event wire envelope: frame parsing + central camelization.
 *
 * The backend emits default-framed SSE messages whose `data:` line is the
 * JSON envelope `{ id, type, time, data }` (see
 * `internal/handler/events_stream.go`). `data` is snake_case on the wire;
 * this module camelizes it once so ALL downstream live code (dispatcher,
 * hooks, mergers) sees camelCase only — resolving the snake/camel split in
 * exactly one place.
 */

import { camelizeKeys } from '@/lib/camelize';

/**
 * Server-side event types the bus produces today. Purely advisory — the
 * transport dispatches on whatever `type` the envelope carries, so new
 * backend types work without touching this union; listing them here only
 * gives subscribers compile-time autocomplete.
 */
export type LiveEventType =
  | 'cluster.connected'
  | 'cluster.disconnected'
  | 'cluster.heartbeat'
  | 'cluster.metrics'
  | 'cluster.status_changed'
  | 'cluster.created'
  | 'cluster.updated'
  | 'cluster.deleted'
  | 'cluster.k8s_changed'
  | 'agent.reconnecting'
  | 'agent.failed'
  | 'cluster.registration.step'
  | 'cluster.registration.phase'
  | 'sys.ping';

/** Parsed live event delivered to subscribers (data already camelCase). */
export interface LiveEvent<T = unknown> {
  id: number;
  type: LiveEventType | string;
  time: string;
  data?: T;
}

/** Payload of a `cluster.metrics` event (camelized). */
export interface ClusterMetricsPayload {
  clusterId: string;
  cpuPercentage: number;
  memoryPercentage: number;
  podCount: number;
  timestamp: string;
}

/** Payload of a `cluster.heartbeat` event (camelized). */
export interface ClusterHeartbeatPayload {
  clusterId: string;
  lastHeartbeat: string;
  agentVersion?: string;
  heartbeatSchemaVersion?: number;
  kubernetesVersion?: string;
  nodeCount?: number;
  podCount?: number;
  cpuUsagePercent?: number;
  memoryUsagePercent?: number;
  distribution?: string;
}

/** Payload shared by status-change-style events (camelized). */
export interface ClusterStatusChangedPayload {
  clusterId: string;
  oldStatus?: string;
  newStatus?: string;
  timestamp?: string;
}

/** Payload of `cluster.created` / `cluster.updated` (camelized). */
export interface ClusterMutationPayload {
  clusterId: string;
  name?: string;
  displayName?: string;
  status?: string;
}

/** Subscriber return — call it to unsubscribe. */
export type Unsubscribe = () => void;

/**
 * Parse one SSE `data:` payload into a LiveEvent, camelizing `data`.
 * Returns null for frames that are not a typed JSON envelope (nothing can
 * be dispatched without a `type`). `sys.ping` frames carry no `id`; it
 * defaults to 0.
 */
export function parseFrame(raw: unknown): LiveEvent | null {
  if (typeof raw !== 'string' || raw === '') return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== 'object') return null;
  const frame = parsed as { id?: unknown; type?: unknown; time?: unknown; data?: unknown };
  if (typeof frame.type !== 'string' || frame.type === '') return null;
  return {
    id: typeof frame.id === 'number' ? frame.id : 0,
    type: frame.type,
    time: typeof frame.time === 'string' ? frame.time : new Date().toISOString(),
    data: frame.data === undefined ? undefined : camelizeKeys(frame.data),
  };
}
