/**
 * Live-event SSE plumbing for the Astronomer dashboard.
 *
 * The backend exposes a single Server-Sent Events stream at
 * `/api/v1/events/stream/?ticket=<one-use-ticket>`. This module owns one EventSource
 * for the lifetime of the dashboard layout and fans incoming frames out
 * via a tiny EventTarget so any number of pages / hooks can subscribe
 * without each opening their own connection.
 *
 * Auth contract (also documented in
 * `internal/handler/events_stream.go`): EventSource cannot send custom
 * headers, so the frontend first requests a short-lived stream ticket using a
 * normal authenticated XHR, then passes only that one-use ticket in the stream
 * URL.
 */

'use client';

import { useEffect, useRef } from 'react';
import { useQueryClient, type QueryKey } from '@tanstack/react-query';
import { createStreamTicket } from '@/lib/api';

// --- Types ---

/**
 * Server-side event types the bus produces today. Anything not in this list
 * still works — the dispatcher is open to extension — but listing them here
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
  // Sprint 078 — cluster registration wizard live events.
  | 'cluster.registration.step'
  | 'cluster.registration.phase';

/** Wire shape produced by the backend's events.Bus. */
export interface LiveEvent<T = unknown> {
  id: number;
  type: LiveEventType | string;
  time: string;
  data?: T;
}

/** Payload of a `cluster.metrics` event. */
export interface ClusterMetricsPayload {
  cluster_id: string;
  cpu_percentage: number;
  memory_percentage: number;
  pod_count: number;
  timestamp: string;
}

/** Payload of a `cluster.heartbeat` event. */
export interface ClusterHeartbeatPayload {
  cluster_id: string;
  last_heartbeat: string;
  agent_version?: string;
  kubernetes_version?: string;
  node_count?: number;
  pod_count?: number;
  cpu_usage_percent?: number;
  memory_usage_percent?: number;
  distribution?: string;
}

/** Payload shared by status-change-style events. */
export interface ClusterStatusChangedPayload {
  cluster_id: string;
  old_status?: string;
  new_status?: string;
  timestamp?: string;
}

/** Payload of `cluster.created` / `cluster.updated`. */
export interface ClusterMutationPayload {
  cluster_id: string;
  name?: string;
  display_name?: string;
  status?: string;
}

// --- Singleton transport ---

interface ConnectionState {
  source: EventSource | null;
  /** EventTarget that re-emits each backend event by `event.type`. */
  target: EventTarget;
  /** Number of consecutive failed connect attempts (for backoff). */
  retryCount: number;
  /** Pending reconnect timer so we can cancel during teardown. */
  reconnectTimer: ReturnType<typeof setTimeout> | null;
  /** Ticket this connection was opened with. */
  openedWithTicket: string | null;
  /** Reference count: how many `useLiveEvents()` mounts hold the connection. */
  refCount: number;
  /** Most recent status the SSE client sees. */
  status: 'idle' | 'connecting' | 'open' | 'closed';
}

let conn: ConnectionState | null = null;

function ensureConnection(): ConnectionState {
  if (!conn) {
    conn = {
      source: null,
      target: new EventTarget(),
      retryCount: 0,
      reconnectTimer: null,
      openedWithTicket: null,
      refCount: 0,
      status: 'idle',
    };
  }
  return conn;
}

/** Build the stream URL with a one-use stream ticket. */
function streamURL(ticket: string): string {
  const base = process.env.NEXT_PUBLIC_API_URL || '/api/v1';
  // Trailing slash matches the backend route registration.
  const url = `${base}/events/stream/`;
  return `${url}?ticket=${encodeURIComponent(ticket)}`;
}

/**
 * Open (or re-open) the underlying EventSource. Reconnects on close with
 * an exponential backoff capped at 30s. Each incoming frame is re-emitted
 * on `state.target` keyed by event type so subscribers only see what they
 * asked for.
 */
function openSource(state: ConnectionState): void {
  if (state.source) {
    if (state.status !== 'closed') return;
    try {
      state.source.close();
    } catch {
      /* ignore */
    }
    state.source = null;
  }
  if (state.reconnectTimer) {
    clearTimeout(state.reconnectTimer);
    state.reconnectTimer = null;
  }
  state.status = 'connecting';
  createStreamTicket('events')
    .then(({ ticket }) => {
      if (state.refCount === 0 || state.source) return;
      state.openedWithTicket = ticket;
      let es: EventSource;
      try {
        es = new EventSource(streamURL(ticket), { withCredentials: false });
      } catch {
        scheduleReconnect(state);
        return;
      }
      state.source = es;

      es.onopen = () => {
        state.status = 'open';
        state.retryCount = 0;
      };

      es.onmessage = (ev) => dispatch(state, 'message', ev);

      for (const t of KNOWN_EVENT_TYPES) {
        es.addEventListener(t, (ev) => dispatch(state, t, ev as MessageEvent));
      }

      es.onerror = () => {
        state.status = 'closed';
        try {
          es.close();
        } catch {
          /* ignore */
        }
        state.source = null;
        scheduleReconnect(state);
      };
    })
    .catch(() => {
      state.status = 'closed';
      scheduleReconnect(state);
    });
}

const KNOWN_EVENT_TYPES: LiveEventType[] = [
  'cluster.connected',
  'cluster.disconnected',
  'cluster.heartbeat',
  'cluster.metrics',
  'cluster.status_changed',
  'cluster.created',
  'cluster.updated',
  'cluster.deleted',
  'cluster.k8s_changed',
  'agent.reconnecting',
  'agent.failed',
];

function scheduleReconnect(state: ConnectionState): void {
  if (state.refCount === 0) return; // nothing's listening
  state.retryCount += 1;
  // Exponential backoff: 1s, 2s, 4s, 8s, ... capped at 30s.
  const delay = Math.min(30000, 1000 * 2 ** Math.min(state.retryCount - 1, 5));
  state.reconnectTimer = setTimeout(() => {
    state.reconnectTimer = null;
    openSource(state);
  }, delay);
}

function dispatch(state: ConnectionState, type: string, ev: MessageEvent): void {
  let payload: unknown = undefined;
  try {
    payload = ev.data ? JSON.parse(ev.data as string) : undefined;
  } catch {
    payload = ev.data;
  }
  // The wire frame is `{ id, type, time, data }`; un-nest one level so
  // listeners get a consistent LiveEvent.
  const detail: LiveEvent = (typeof payload === 'object' && payload !== null && 'id' in (payload as object))
    ? (payload as LiveEvent)
    : { id: 0, type, time: new Date().toISOString(), data: payload };
  state.target.dispatchEvent(new CustomEvent(type, { detail }));
  // Also fire a wildcard so "subscribe to everything" pages don't have to
  // enumerate every event type.
  state.target.dispatchEvent(new CustomEvent('*', { detail }));
}

// --- Public hook surface ---

/**
 * Subscriber returned to consumers — call it to unsubscribe.
 */
export type Unsubscribe = () => void;

/**
 * Public live-events surface. Pages call `subscribe(type, handler)` to
 * react to specific events; the connection itself is held open by mounting
 * `useLiveEvents()` once at the dashboard root.
 */
export interface LiveEventsAPI {
  subscribe<T = unknown>(
    types: LiveEventType | LiveEventType[] | '*',
    handler: (ev: LiveEvent<T>) => void,
  ): Unsubscribe;
  /** Connection liveness for diagnostics / status pills. */
  status(): ConnectionState['status'];
}

/**
 * Mount this hook once near the top of the authenticated tree (the dashboard
 * layout) to keep one EventSource open for the whole session. Subsequent
 * calls cheaply share the same connection via refcount.
 */
export function useLiveEvents(): LiveEventsAPI {
  const stateRef = useRef<ConnectionState | null>(null);

  useEffect(() => {
    const state = ensureConnection();
    stateRef.current = state;
    state.refCount += 1;
    if (!state.source || state.status === 'closed') {
      openSource(state);
    }

    // Re-open whenever the tab returns to focus — handles laptop-sleep
    // scenarios where the underlying TCP connection was reset by the OS.
    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        if (state.status === 'closed') {
          openSource(state);
        }
      }
    };
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      state.refCount -= 1;
      if (state.refCount <= 0) {
        if (state.reconnectTimer) {
          clearTimeout(state.reconnectTimer);
          state.reconnectTimer = null;
        }
        try {
          state.source?.close();
        } catch {
          /* ignore */
        }
        state.source = null;
        state.status = 'closed';
        state.refCount = 0;
      }
    };
  }, []);

  const api: LiveEventsAPI = {
    subscribe<T = unknown>(
      types: LiveEventType | LiveEventType[] | '*',
      handler: (ev: LiveEvent<T>) => void,
    ) {
      const state = stateRef.current ?? ensureConnection();
      const list = Array.isArray(types) ? types : [types];
      const wrap = (e: Event) => {
        const detail = (e as CustomEvent<LiveEvent<T>>).detail;
        // The CustomEvent wrapper preserves the caller's generic via the cast
        // above; we just forward it.
        handler(detail);
      };
      for (const t of list) {
        state.target.addEventListener(t, wrap as EventListener);
      }
      return () => {
        for (const t of list) {
          state.target.removeEventListener(t, wrap as EventListener);
        }
      };
    },
    status() {
      return stateRef.current?.status ?? 'idle';
    },
  };
  return api;
}

/**
 * Subscribe to a set of event types and invalidate the listed React Query
 * keys whenever any of them fire. This is the standard "make this page
 * live" hook — drop it into any page component alongside its data hooks.
 *
 * Call sites only need a stable reference to the keys they want refreshed;
 * we deep-compare against the previous keys array so callers can pass an
 * inline literal without remounting on every render.
 */
export function useLiveQueryInvalidation(
  eventTypes: LiveEventType | LiveEventType[],
  queryKeys: QueryKey | QueryKey[],
): void {
  const queryClient = useQueryClient();
  // Hash the inputs into a stable string so the effect only re-runs when the
  // caller actually passes a different set. Using JSON.stringify keeps the
  // implementation tiny — query keys are small arrays of strings/objects so
  // serialisation cost is negligible.
  const typesKey = JSON.stringify(eventTypes);
  const keysKey = JSON.stringify(queryKeys);

  useEffect(() => {
    const state = ensureConnection();
    state.refCount += 1;
    if (!state.source || state.status === 'closed') {
      openSource(state);
    }

    const types = Array.isArray(eventTypes) ? eventTypes : [eventTypes];
    const allKeys: QueryKey[] = isQueryKeyArray(queryKeys)
      ? (queryKeys as QueryKey[])
      : [queryKeys as QueryKey];

    const handler = () => {
      for (const key of allKeys) {
        // Prefix-match invalidation: passing ['clusters'] also nukes
        // ['clusters', 'list', ...] so callers don't need every variant.
        queryClient.invalidateQueries({ queryKey: key });
      }
    };
    const wrap = () => handler();
    for (const t of types) {
      state.target.addEventListener(t, wrap as EventListener);
    }

    return () => {
      for (const t of types) {
        state.target.removeEventListener(t, wrap as EventListener);
      }
      state.refCount -= 1;
      if (state.refCount <= 0) {
        try {
          state.source?.close();
        } catch {
          /* ignore */
        }
        state.source = null;
        state.status = 'closed';
        state.refCount = 0;
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [typesKey, keysKey, queryClient]);
}

/** Accept either a single QueryKey or an array of them. */
function isQueryKeyArray(v: QueryKey | QueryKey[]): boolean {
  // A QueryKey is itself a readonly array; distinguish "array of keys" from
  // "a single key" by checking that the first element is itself array-like.
  if (!Array.isArray(v)) return false;
  if (v.length === 0) return false;
  const first = v[0];
  return Array.isArray(first);
}

/**
 * Convenience: subscribe to `cluster.metrics` and merge each tick into the
 * `clusters` list query cache. Used by widgets that want continuous
 * percentage updates without paying a re-fetch round-trip on every tick.
 *
 * The optimistic cache update is bounded — if the cluster row isn't found
 * (e.g. the cluster was just deleted), the tick is silently dropped.
 */
export function useLiveClusterMetricsMerger(): void {
  const queryClient = useQueryClient();

  useEffect(() => {
    const state = ensureConnection();
    state.refCount += 1;
    if (!state.source || state.status === 'closed') {
      openSource(state);
    }

    const onMetrics = (e: Event) => {
      const detail = (e as CustomEvent<LiveEvent<ClusterMetricsPayload>>).detail;
      const payload = detail?.data;
      if (!payload?.cluster_id) return;

      // Walk every cached `clusters list` query (across param variants) and
      // patch the affected row in place. queryKeys.clusters.list((params))
      // produces ['clusters', 'list', params] — the prefix match below
      // catches all of them at once.
      queryClient.setQueriesData(
        { queryKey: ['clusters', 'list'] },
        (old: unknown) => mergeClusterMetricsIntoListResponse(old, payload),
      );

      // Detail page cache: ['clusters', 'detail', id]
      queryClient.setQueryData(['clusters', 'detail', payload.cluster_id], (old: unknown) =>
        mergeClusterMetricsIntoDetail(old, payload),
      );
    };

    const onStatus = (e: Event) => {
      const detail = (e as CustomEvent<LiveEvent<ClusterStatusChangedPayload>>).detail;
      const payload = detail?.data;
      if (!payload?.cluster_id || !payload?.new_status) return;
      // Patch any cached list / detail responses to flip the status field
      // without waiting for the next refetch.
      queryClient.setQueriesData(
        { queryKey: ['clusters', 'list'] },
        (old: unknown) => mergeClusterStatusIntoListResponse(old, payload.cluster_id, payload.new_status!),
      );
      queryClient.setQueryData(['clusters', 'detail', payload.cluster_id], (old: unknown) =>
        mergeClusterStatusIntoDetail(old, payload.new_status!),
      );
      // Also kick a hard refresh so any dependent caches catch up.
      queryClient.invalidateQueries({ queryKey: ['clusters', 'detail', payload.cluster_id] });
    };

    state.target.addEventListener('cluster.metrics', onMetrics as EventListener);
    state.target.addEventListener('cluster.status_changed', onStatus as EventListener);

    return () => {
      state.target.removeEventListener('cluster.metrics', onMetrics as EventListener);
      state.target.removeEventListener('cluster.status_changed', onStatus as EventListener);
      state.refCount -= 1;
      if (state.refCount <= 0) {
        try {
          state.source?.close();
        } catch {
          /* ignore */
        }
        state.source = null;
        state.status = 'closed';
        state.refCount = 0;
      }
    };
  }, [queryClient]);
}

// --- Cache merge helpers ---
//
// We patch React Query caches in-place rather than firing invalidations on
// every metrics tick; invalidation would queue a network refetch every
// 10 seconds across every cluster, which defeats the point of the bus.

interface ClusterListShape {
  data?: Array<{
    id: string;
    cpuPercentage?: number;
    memoryPercentage?: number;
    podCount?: number;
    status?: string;
  }>;
  total?: number;
  page?: number;
  pageSize?: number;
  totalPages?: number;
}

interface ClusterDetailShape {
  id: string;
  cpuPercentage?: number;
  memoryPercentage?: number;
  podCount?: number;
  status?: string;
}

function mergeClusterMetricsIntoListResponse(
  old: unknown,
  m: ClusterMetricsPayload,
): unknown {
  if (!old || typeof old !== 'object') return old;
  const list = old as ClusterListShape;
  if (!Array.isArray(list.data)) return old;
  const idx = list.data.findIndex((c) => c.id === m.cluster_id);
  if (idx < 0) return old;
  const next = list.data.slice();
  next[idx] = {
    ...next[idx],
    cpuPercentage: m.cpu_percentage,
    memoryPercentage: m.memory_percentage,
    podCount: m.pod_count,
    // If a metrics tick arrives for a cluster the list still shows as
    // `disconnected`, the cluster is clearly active — flip the status
    // optimistically (the cluster.status_changed sweep will confirm
    // shortly).
    status: next[idx].status === 'disconnected' ? 'active' : next[idx].status,
  };
  return { ...list, data: next };
}

function mergeClusterMetricsIntoDetail(old: unknown, m: ClusterMetricsPayload): unknown {
  if (!old || typeof old !== 'object') return old;
  const c = old as ClusterDetailShape;
  if (c.id !== m.cluster_id) return old;
  return {
    ...c,
    cpuPercentage: m.cpu_percentage,
    memoryPercentage: m.memory_percentage,
    podCount: m.pod_count,
    status: c.status === 'disconnected' ? 'active' : c.status,
  };
}

function mergeClusterStatusIntoListResponse(
  old: unknown,
  clusterId: string,
  newStatus: string,
): unknown {
  if (!old || typeof old !== 'object') return old;
  const list = old as ClusterListShape;
  if (!Array.isArray(list.data)) return old;
  const idx = list.data.findIndex((c) => c.id === clusterId);
  if (idx < 0) return old;
  const next = list.data.slice();
  next[idx] = { ...next[idx], status: newStatus };
  return { ...list, data: next };
}

function mergeClusterStatusIntoDetail(old: unknown, newStatus: string): unknown {
  if (!old || typeof old !== 'object') return old;
  const c = old as ClusterDetailShape;
  return { ...c, status: newStatus };
}
